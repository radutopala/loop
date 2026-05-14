package container

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/types"
)

// mcpConfig represents the MCP config structure written to .loop/mcp.json.
type mcpConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// mcpServerEntry represents a single MCP server in the config.
type mcpServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// claudeResponse represents a stream-json event from claude --output-format stream-json.
// The final event has Type "result" and contains the response.
type claudeResponse struct {
	Type       string `json:"type"`
	Result     string `json:"result"`
	SessionID  string `json:"session_id"`
	IsError    bool   `json:"is_error"`
	DurationMs int    `json:"duration_ms"`
	NumTurns   int    `json:"num_turns"`
	StopReason string `json:"stop_reason"`
	Model      string `json:"-"` // set by scanStreamJSON from assistant events
}

// assistantContentBlock is a single content block within an assistant message.
// A block is one of: "text" (Text), "thinking" (Thinking), or "tool_use"
// (ID + Name + Input). Other fields are zero on a given block.
type assistantContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`     // text blocks
	Thinking string          `json:"thinking"` // thinking blocks
	ID       string          `json:"id"`       // tool_use id
	Name     string          `json:"name"`     // tool_use name
	Input    json.RawMessage `json:"input"`    // tool_use input
}

// assistantMessage represents an "assistant" event from Claude's stream-json output.
// Each assistant turn contains a message with content blocks.
type assistantMessage struct {
	Type    string `json:"type"`
	Message struct {
		Model   string                  `json:"model"`
		Content []assistantContentBlock `json:"content"`
	} `json:"message"`
}

// systemEvent represents a "system" event from Claude's stream-json output.
type systemEvent struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// extractText joins all text content blocks from an assistant message.
func (m *assistantMessage) extractText() string {
	var texts []string
	for _, c := range m.Message.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// extractThinking joins all thinking content blocks from an assistant message.
func (m *assistantMessage) extractThinking() string {
	var parts []string
	for _, c := range m.Message.Content {
		if c.Type == "thinking" && c.Thinking != "" {
			parts = append(parts, c.Thinking)
		}
	}
	return strings.Join(parts, "\n")
}

// ToolUse represents a tool invocation extracted from an assistant message.
type ToolUse struct {
	ID    string // per-block tool_use id, pairs with the matching tool_result
	Name  string
	Input string // short summary of the input
}

// extractToolUses returns tool_use content blocks from an assistant message.
func (m *assistantMessage) extractToolUses() []ToolUse {
	var tools []ToolUse
	for _, c := range m.Message.Content {
		if c.Type == "tool_use" && c.Name != "" {
			summary := summarizeToolInput(c.Name, c.Input)
			tools = append(tools, ToolUse{ID: c.ID, Name: c.Name, Input: summary})
		}
	}
	return tools
}

// summarizeToolInput extracts a short description from tool input JSON.
func summarizeToolInput(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	switch name {
	case "Bash":
		if cmd, ok := m["command"].(string); ok {
			if len(cmd) > 120 {
				cmd = cmd[:120] + "..."
			}
			return cmd
		}
	case "Read":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Edit":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Write":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Glob":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Agent":
		if desc, ok := m["description"].(string); ok {
			return desc
		}
	case "AskUserQuestion", "ExitPlanMode", "TodoWrite":
		return string(raw)
	}
	// For other tools, try common keys.
	for _, key := range []string{"description", "query", "prompt", "path", "url"} {
		if v, ok := m[key].(string); ok {
			if len(v) > 120 {
				v = v[:120] + "..."
			}
			return v
		}
	}
	return ""
}

// ContainerConfig holds settings for creating a container.
type ContainerConfig struct {
	Image       string
	MemoryMB    int64
	CPUs        float64
	Env         []string
	Cmd         []string
	Binds       []string
	WorkingDir  string
	GroupAdd    []string
	Labels      map[string]string
	NetworkName string // Docker network to attach to
	Hostname    string // container hostname on the network
	SecurityOpt []string
	CapAdd      []string
}

// WaitResponse represents the result of waiting for a container to finish.
type WaitResponse struct {
	StatusCode int64
	Error      error
}

// DockerClient abstracts the Docker SDK container lifecycle methods used by
// DockerRunner. It handles creating, starting, stopping, and removing containers.
//
// This is distinct from terminal.ExecClient, which handles exec-ing into
// already-running containers for interactive PTY sessions (docker exec).
type DockerClient interface {
	ContainerCreate(ctx context.Context, cfg *ContainerConfig, name string) (string, error)
	ContainerStart(ctx context.Context, containerID string) error
	ContainerLogs(ctx context.Context, containerID string) (io.Reader, error)
	ContainerLogsFollow(ctx context.Context, containerID string) (io.ReadCloser, error)
	ContainerWait(ctx context.Context, containerID string) (<-chan WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, containerID string) error
	ContainerStop(ctx context.Context, containerID string) error
	ImageList(ctx context.Context, image string) ([]string, error)
	ImagePull(ctx context.Context, image string) error
	ImageBuild(ctx context.Context, contextDir, tag string) error
	ImageBuildFile(ctx context.Context, contextDir, dockerfile, tag string) error
	RemoveImageAndContainers(ctx context.Context, imageName string) error
	ImageInspectLabels(ctx context.Context, imageName string) (map[string]string, error)
	ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error)
	ListContainerInfos(ctx context.Context) ([]*ContainerInfo, error)
	CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error
	NetworkEnsure(ctx context.Context, name string) error
	SetLoopVersion(v string)
	LatestClaudeVersion() string
}

// Runner executes agent requests inside containers.
type Runner interface {
	Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error)
	Cleanup(ctx context.Context) error
}

// DockerRunner implements Runner using Docker containers.
// runnerSystem abstracts OS operations needed by DockerRunner.
type runnerSystem interface {
	Stat(name string) (os.FileInfo, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	MkdirAll(path string, perm os.FileMode) error
	Readlink(name string) (string, error)
	UserHomeDir() (string, error)
	Getenv(key string) string
	Getuid() int
	Getgid() int
	ExecCommandOutput(name string, args ...string) ([]byte, error)
}

type DockerRunner struct {
	client                    DockerClient
	cfg                       atomic.Pointer[config.Config]
	configLoad                func() (*config.Config, error)
	sys                       runnerSystem
	loadProjectConfig         func(string, *config.Config) (*config.Config, error)
	loadWorktreeProjectConfig func(string, string, *config.Config) (*config.Config, error)
	osRandRead                func([]byte) (int, error)
	osTimeLocalName           func() string
	registry                  ContainerRegistry
	instanceID                string // unique per daemon, used to scope Cleanup

	// Docker HTTP proxy (stage 2 of agentgate). When cfg.Gates.DockerProxy.Enabled,
	// the runner writes proxy-policy.json into policyDir/<channel>/ and mounts it
	// read-only into the container; loop-dockerproxy inside the container
	// reads the file, binds /var/run/docker.sock (tmpfs) and reverse-proxies
	// to /var/run/docker.sock.host (the real daemon, bind-mounted read-only).
	// Approvals are round-tripped back to loop-server via HTTP (see
	// POST /api/gate/container-approval).
	hostDockerSock string // "" -> "/var/run/docker.sock"

	// Gate approval wiring (stage 2 of agentgate). When both fields below are
	// set, the runner constructs a per-container agentgate.Manager, registers
	// it in gateResolver under the real container ID with a per-container
	// bearer token, and the in-container dockerproxy/syscallwrap processes
	// call back through the HTTP approval endpoint authenticated by that
	// token. Clicks arriving via any bot are routed to the right Manager by
	// gateResolver.
	gateResolver   *agentgate.MultiManagerResolver
	gateBotRouter  agentgate.BotRouter
	gateRateLimits types.RateLimits

	// Gate seccomp wiring (stage 1 of agentgate). When cfg.Gates.Agentgate.Enabled, the
	// runner writes gate-policy.json into policyDir/<channel>/ and mounts it
	// read-only into the container; loop-syscallwrap parent (running as root
	// in-container) reads the file, installs the filter in its child (dropped
	// to the agent user), and runs agentgate.Server in-process against the
	// notify fd. No notify fd crosses the container boundary.
	gatePolicy *agentgate.Policy

	// policyDir is the shared host dir where per-container policy JSON files
	// (proxy-policy.json, gate-policy.json) are written; the container mounts
	// them read-only under /etc/loop/. Defaults to "" which disables policy-
	// file writes (tests that don't need the files can skip the filesystem
	// setup).
	policyDir string
}

// NewDockerRunner creates a new DockerRunner with the given Docker client and config.
func NewDockerRunner(client DockerClient, cfg *config.Config, configLoad func() (*config.Config, error)) *DockerRunner {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	r := &DockerRunner{
		client:                    client,
		configLoad:                configLoad,
		sys:                       osutil.RealSystem{},
		loadProjectConfig:         config.LoadProjectConfig,
		loadWorktreeProjectConfig: config.LoadWorktreeProjectConfig,
		osRandRead:                rand.Read,
		osTimeLocalName:           func() string { return time.Now().Location().String() },
		instanceID:                hex.EncodeToString(b),
	}
	r.cfg.Store(cfg)
	return r
}

// SetDockerProxyDeps wires the host dir for per-container policy files and
// the host-side docker daemon socket path. hostSock defaults to
// "/var/run/docker.sock" when empty — the runner bind-mounts this into the
// container at /var/run/docker.sock.host (read-only) so loop-dockerproxy can
// reverse-proxy to it. policyDir is where proxy-policy.json is written;
// empty falls back to no file writes (used by tests that disable the proxy).
func (r *DockerRunner) SetDockerProxyDeps(policyDir, hostSock string) {
	r.policyDir = policyDir
	r.hostDockerSock = hostSock
}

// PolicyDir returns the host dir where per-container policy files are
// written. Exposed for integration tests that verify serve.go plumbs
// ~/.loop/run all the way through.
func (r *DockerRunner) PolicyDir() string {
	return r.policyDir
}

// AuditDir returns the host dir that backs the in-container
// /var/log/loop-gate bind for the given channel. Returns "" when policyDir
// is unset (gate + proxy both disabled). The directory may not exist yet —
// it is created lazily on first container spawn. Read-only resolver used
// by the API server to list / read jsonl audit files.
func (r *DockerRunner) AuditDir(channelID string) string {
	if r.policyDir == "" {
		return ""
	}
	return filepath.Join(r.policyDir, policyChannelKey(channelID), "audit")
}

// SetGateDeps wires the per-daemon approval resolver and the bot router used
// to render prompts. When both resolver and botRouter are non-nil, each
// container gets a fresh agentgate.Manager that acts as the HTTP approval
// target for the in-container proxy + seccomp gate; clicks on any surface
// (Discord / Slack / Local) are routed to the right Manager by the resolver.
// Zero-valued limits disable the respective rate-limit caps.
func (r *DockerRunner) SetGateDeps(resolver *agentgate.MultiManagerResolver, botRouter agentgate.BotRouter, limits types.RateLimits) {
	r.gateResolver = resolver
	r.gateBotRouter = botRouter
	r.gateRateLimits = limits
}

// SetGatePolicy wires the shared seccomp policy used by the in-container
// gate (loop-syscallwrap). The runner writes the policy as JSON into
// {policyDir}/<channel>/gate-policy.json and bind-mounts it read-only into
// the container. Nil policy means the gate does not spawn even when
// cfg.Gates.Agentgate.Enabled is true.
func (r *DockerRunner) SetGatePolicy(policy *agentgate.Policy, policyDir string) {
	r.gatePolicy = policy
	if policyDir != "" {
		r.policyDir = policyDir
	}
}

// ContainerRemove deregisters the per-container agentgate.Manager (freeing the
// bearer token on the resolver so stale requests get 401) and delegates to the
// underlying DockerClient. Implements the containerRemover interface consumed
// by the ContainerRegistry so scheduled-remove paths clean up the same way as
// synchronous removal. Policy files under {policyDir}/<channel>/ are intentionally
// left on disk — they're tiny, survive a daemon restart, and are overwritten
// on next spawn for the same channel (idempotent — derived from global config).
func (r *DockerRunner) ContainerRemove(ctx context.Context, containerID string) error {
	if r.gateResolver != nil {
		r.gateResolver.Remove(containerID)
	}
	return r.client.ContainerRemove(ctx, containerID)
}

// currentConfig returns a fresh config by calling configLoad, falling back
// to the last-known-good config on error or when configLoad is nil.
func (r *DockerRunner) currentConfig() *config.Config {
	if r.configLoad == nil {
		return r.cfg.Load()
	}
	fresh, err := r.configLoad()
	if err != nil {
		return r.cfg.Load()
	}
	r.cfg.Store(fresh)
	return fresh
}

// SetContainerRegistry configures the container registry for lifecycle tracking.
func (r *DockerRunner) SetContainerRegistry(reg ContainerRegistry) {
	r.registry = reg
}

// InstanceID returns the unique identifier for this daemon instance,
// used to scope container cleanup via the loop-instance Docker label.
func (r *DockerRunner) InstanceID() string {
	return r.instanceID
}

const (
	scannerBufInit = 64 * 1024 // initial reader buffer capacity
)

var nonAlphanumRegexp = regexp.MustCompile(`[^a-z0-9]+`)

// localTimezone returns the IANA timezone name (e.g. "Europe/Bucharest").
func (r *DockerRunner) localTimezone() string {
	if tz := r.sys.Getenv("TZ"); tz != "" && tz != "Local" {
		return tz
	}
	if loc := r.osTimeLocalName(); loc != "Local" {
		return loc
	}
	// Linux: /etc/timezone contains the IANA name directly.
	if data, err := r.sys.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(data)); tz != "" {
			return tz
		}
	}
	// macOS/Linux: /etc/localtime is a symlink into the zoneinfo directory.
	if target, err := r.sys.Readlink("/etc/localtime"); err == nil {
		if _, after, ok := strings.Cut(target, "zoneinfo/"); ok {
			return after
		}
	}
	return "UTC"
}

// SanitizeName lowercases the input, replaces non-alphanumeric chars with
// hyphens, collapses consecutive hyphens, trims leading/trailing hyphens,
// and truncates to 40 characters. Used for Docker container names and hostnames.
func SanitizeName(name string) string {
	s := strings.ToLower(name)
	s = nonAlphanumRegexp.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// containerName generates a Docker container name in the format
// "loop-{base}-{6-hex-chars}". When dirPath is set, the base is derived
// from filepath.Base(dirPath); otherwise channelID is used.
func (r *DockerRunner) containerName(channelID, dirPath string) string {
	base := channelID
	if dirPath != "" {
		base = filepath.Base(dirPath)
	}
	sanitized := SanitizeName(base)
	b := make([]byte, 3)
	_, _ = r.osRandRead(b)
	return "loop-" + sanitized + "-" + hex.EncodeToString(b)
}

// Run executes an agent request in a Docker container.
// If a session ID is set and the run fails, it retries with --resume
// using only the original prompt (no full message history rebuild).
func (r *DockerRunner) Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	resp, err := r.runOnce(ctx, req)
	if err == nil || req.SessionID == "" {
		return resp, err
	}

	// When a session is too long, compact it and retry.
	if resp != nil && resp.SessionID != "" && strings.Contains(err.Error(), "Prompt is too long") {
		compactReq := &agent.AgentRequest{
			SessionID: resp.SessionID,
			ChannelID: req.ChannelID,
			DirPath:   req.DirPath,
			Prompt:    "/compact",
		}
		compactResp, compactErr := r.runOnce(ctx, compactReq)
		if compactErr != nil {
			return nil, fmt.Errorf("compacting session: %w", compactErr)
		}
		retryReq := *req
		retryReq.SessionID = compactResp.SessionID
		retryReq.ForkSession = false
		if retryReq.Prompt == "" && len(retryReq.Messages) > 0 {
			retryReq.Prompt = retryReq.Messages[len(retryReq.Messages)-1].Content
		}
		return r.runOnce(ctx, &retryReq)
	}

	retryReq := *req
	if retryReq.Prompt == "" && len(retryReq.Messages) > 0 {
		retryReq.Prompt = retryReq.Messages[len(retryReq.Messages)-1].Content
	}
	retryResp, retryErr := r.runOnce(ctx, &retryReq)
	if retryErr != nil {
		return nil, err
	}
	return retryResp, nil
}

// RunBash executes a shell script in a Docker container and returns stdout.
// Uses the same container configuration (mounts, env) as agent runs.
func (r *DockerRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	containerID, ctrName, mcpConfigPath, keepMCP, err := r.createAndStartContainer(ctx, channelID, dirPath, "", "", "",
		ContainerTypeAgent,
		func(_ *config.Config, _ string) []string {
			return []string{"/bin/sh", "-c", script}
		},
	)
	if mcpConfigPath != "" && !keepMCP {
		defer func() { _ = r.sys.Remove(mcpConfigPath) }()
	}
	if containerID != "" {
		if r.registry != nil {
			r.registry.Register(&ContainerInfo{
				ContainerID:   containerID,
				ChannelID:     channelID,
				Type:          ContainerTypeAgent,
				ContainerName: ctrName,
			})
		}
		defer func() {
			if r.registry != nil {
				r.registry.ScheduleRemove(containerID, r.cfg.Load().ContainerKeepAlive)
			}
		}()
	}
	if err != nil {
		return "", err
	}

	// Wait for the container to finish.
	waitCh, errCh := r.client.ContainerWait(ctx, containerID)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errCh:
		return "", fmt.Errorf("waiting for container: %w", err)
	case wr := <-waitCh:
		if wr.Error != nil {
			return "", fmt.Errorf("container error: %w", wr.Error)
		}
		// Read raw stdout.
		reader, logErr := r.client.ContainerLogs(ctx, containerID)
		if logErr != nil {
			return "", fmt.Errorf("reading container logs: %w", logErr)
		}
		data, _ := io.ReadAll(reader)
		output := string(data)
		if wr.StatusCode != 0 {
			return output, fmt.Errorf("script exited with status %d", wr.StatusCode)
		}
		return output, nil
	}
}

// buildMCPConfig creates the merged MCP config with the built-in loop
// and any user-defined servers from the config. The built-in loop always
// takes precedence over a user-defined server with the same name.
func buildMCPConfig(channelID, apiURL, workDir, authorID, agentID string, memoryEnabled, browserEnabled bool, userServers map[string]config.MCPServerConfig) mcpConfig {
	servers := make(map[string]mcpServerEntry, len(userServers)+1)
	for name, srv := range userServers {
		servers[name] = mcpServerEntry{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}
	}
	// Add built-in loop only if the user hasn't defined one.
	if _, exists := userServers["loop"]; !exists {
		args := []string{"mcp", "--channel-id", channelID, "--api-url", apiURL, "--log", filepath.Join(workDir, ".loop", "mcp.log")}
		if authorID != "" {
			args = append(args, "--author-id", authorID)
		}
		if memoryEnabled {
			args = append(args, "--memory")
		}
		if agentID != "" {
			args = append(args, "--agent-id", agentID)
		}
		servers["loop"] = mcpServerEntry{
			Command: "/usr/local/bin/loop",
			Args:    args,
		}
	}
	// Add built-in browser MCP server that proxies actions through the host API.
	if browserEnabled {
		if _, exists := userServers["loop-browser"]; !exists {
			browserArgs := []string{"mcp-browser", "--log", filepath.Join(workDir, ".loop", "mcp-browser.log"), "--api-url", apiURL, "--channel-id", channelID}
			servers["loop-browser"] = mcpServerEntry{
				Command: "/usr/local/bin/loop",
				Args:    browserArgs,
			}
		}
	}
	return mcpConfig{MCPServers: servers}
}

// expandPath expands ~ in paths to the user's home directory.
func (r *DockerRunner) expandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := r.sys.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[2:]), nil
}

// mountSpec represents a parsed mount specification (host:container[:mode]).
type mountSpec struct {
	Host      string
	Container string
	Mode      string // optional, e.g. "ro"
}

// parseMountSpec splits a mount string into its components.
func parseMountSpec(mount string) (mountSpec, error) {
	parts := strings.Split(mount, ":")
	if len(parts) < 2 {
		return mountSpec{}, fmt.Errorf("invalid mount format: %s", mount)
	}
	ms := mountSpec{Host: parts[0], Container: parts[1]}
	if len(parts) > 2 {
		ms.Mode = parts[2]
	}
	return ms, nil
}

// String returns the mount spec as a bind string (host:container[:mode]).
func (m mountSpec) String() string {
	s := m.Host + ":" + m.Container
	if m.Mode != "" {
		s += ":" + m.Mode
	}
	return s
}

// filterProxySockConflicts removes binds whose container target is
// /var/run/docker.sock. The in-container docker proxy needs that path free
// so it can listen there; a user-configured mount there would shadow the
// listener with a bind to the host socket. Each skipped entry is logged to
// stderr so the user can remove it from their config.
func filterProxySockConflicts(binds []string) []string {
	out := make([]string, 0, len(binds))
	for _, b := range binds {
		if ms, err := parseMountSpec(b); err == nil && ms.Container == "/var/run/docker.sock" {
			fmt.Fprintf(os.Stderr,
				"Warning: dropping mount %q: docker proxy listens on /var/run/docker.sock; remove this entry from your config.mounts\n",
				b,
			)
			continue
		}
		out = append(out, b)
	}
	return out
}

// processMount processes a single mount specification and returns the expanded bind string.
// Returns empty string if the mount should be skipped.
func (r *DockerRunner) processMount(mount string) (string, error) {
	ms, err := parseMountSpec(mount)
	if err != nil {
		return "", err
	}

	// Docker named volumes (e.g. "gomodcache:/go/pkg/mod") are passed through
	// without host path expansion or existence checks — Docker manages them.
	// The container path still needs ~ expansion since Docker requires absolute paths.
	if config.IsNamedVolume(ms.Host) {
		containerPath, err := r.expandPath(ms.Container)
		if err != nil {
			return "", fmt.Errorf("expanding container path %s: %w", ms.Container, err)
		}
		ms.Container = containerPath
		return ms.String(), nil
	}

	expanded, err := r.expandPath(ms.Host)
	if err != nil {
		return "", fmt.Errorf("expanding path %s: %w", ms.Host, err)
	}

	// Check if path exists
	if _, err := r.sys.Stat(expanded); os.IsNotExist(err) {
		// Skip non-existent paths silently
		return "", nil
	}

	// Expand ~ in the container path too — container HOME matches host HOME.
	containerPath, err := r.expandPath(ms.Container)
	if err != nil {
		return "", fmt.Errorf("expanding container path %s: %w", ms.Container, err)
	}
	ms.Host = expanded
	ms.Container = containerPath
	return ms.String(), nil
}

// gitExcludesMount detects the host git core.excludesFile and returns a bind
// mount string so the file is available inside the container at the path git
// will look for it. Returns "" if unconfigured or the file doesn't exist.
func (r *DockerRunner) gitExcludesMount() string {
	out, err := r.sys.ExecCommandOutput("git", "config", "--global", "--get", "core.excludesFile")
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return ""
	}

	// Expand ~ for the host path (source)
	hostPath := raw
	if strings.HasPrefix(hostPath, "~/") {
		home, err := r.sys.UserHomeDir()
		if err != nil {
			return ""
		}
		hostPath = filepath.Join(home, hostPath[2:])
	}

	// Check if the file exists on the host
	if _, err := r.sys.Stat(hostPath); err != nil {
		return ""
	}

	// Container HOME matches host HOME, so the expanded host path works in both.
	return hostPath + ":" + hostPath + ":ro"
}

// runOnce executes a single container run.
func (r *DockerRunner) runOnce(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	containerID, ctrName, mcpConfigPath, keepMCP, err := r.createAndStartContainer(ctx, req.ChannelID, req.DirPath, req.AuthorID, req.ParentDirPath, req.AgentID,
		ContainerTypeAgent,
		func(cfg *config.Config, mcpConfigPath string) []string {
			return buildClaudeCmd(cfg, mcpConfigPath, req)
		},
	)
	if mcpConfigPath != "" && !keepMCP {
		defer func() { _ = r.sys.Remove(mcpConfigPath) }()
	}
	if containerID != "" {
		if r.registry != nil {
			r.registry.Register(&ContainerInfo{
				ContainerID:   containerID,
				ChannelID:     req.ChannelID,
				Type:          ContainerTypeAgent,
				ContainerName: ctrName,
			})
		}
		defer func() {
			if r.registry != nil {
				r.registry.ScheduleRemove(containerID, r.cfg.Load().ContainerKeepAlive)
			}
		}()
	}
	if err != nil {
		return nil, err
	}

	claudeResp, err := r.collectOutput(ctx, containerID, streamCallbacks{
		onTurn:       req.OnTurn,
		onToolUse:    req.OnToolUse,
		onActivity:   req.OnActivity,
		onThinking:   req.OnThinking,
		onToolResult: req.OnToolResult,
	})
	if err != nil {
		return nil, err
	}

	if claudeResp.IsError {
		return &agent.AgentResponse{
			SessionID: claudeResp.SessionID,
			Error:     claudeResp.Result,
		}, fmt.Errorf("claude returned error: %s", claudeResp.Result)
	}

	return &agent.AgentResponse{
		Response:   claudeResp.Result,
		SessionID:  claudeResp.SessionID,
		DurationMs: claudeResp.DurationMs,
		NumTurns:   claudeResp.NumTurns,
		StopReason: claudeResp.StopReason,
		Model:      claudeResp.Model,
	}, nil
}

// buildContainerEnv assembles environment variables for the container,
// including auth credentials, proxy settings, timezone, and custom envs.
func (r *DockerRunner) buildContainerEnv(cfg *config.Config, channelID, apiURL string) ([]string, error) {
	hostHome, err := r.sys.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	env := []string{
		"CHANNEL_ID=" + channelID,
		"API_URL=" + apiURL,
		"HOME=" + hostHome,
		"HOST_USER=" + r.sys.Getenv("USER"),
		fmt.Sprintf("HOST_UID=%d", r.sys.Getuid()),
		fmt.Sprintf("HOST_GID=%d", r.sys.Getgid()),
		"TZ=" + r.localTimezone(),
		"PATH=" + hostHome + "/.local/bin:" + hostHome + "/bin:" + hostHome + "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	env = addAuthEnv(env, cfg)
	env = r.addProxyEnv(env)

	for k, v := range cfg.Envs {
		expanded, err := r.expandPath(v)
		if err != nil {
			return nil, fmt.Errorf("expanding env %s value: %w", k, err)
		}
		env = append(env, k+"="+expanded)
	}

	return env, nil
}

// addAuthEnv appends authentication environment variables to env.
// Prefers OAuth token over API key.
func addAuthEnv(env []string, cfg *config.Config) []string {
	if cfg.ClaudeCodeOAuthToken != "" {
		return append(env, "CLAUDE_CODE_OAUTH_TOKEN="+cfg.ClaudeCodeOAuthToken)
	}
	if cfg.AnthropicAPIKey != "" {
		return append(env, "ANTHROPIC_API_KEY="+cfg.AnthropicAPIKey)
	}
	return env
}

// addProxyEnv forwards host proxy environment variables into env,
// rewriting localhost addresses to host.docker.internal.
// extraNoProxyHosts are added to NO_PROXY (e.g. Chrome container hostname).
func (r *DockerRunner) addProxyEnv(env []string, extraNoProxyHosts ...string) []string {
	hasProxy := false
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if v := r.sys.Getenv(key); v != "" {
			env = append(env, key+"="+localhostToDockerHost(v))
			if key != "NO_PROXY" && key != "no_proxy" {
				hasProxy = true
			}
		}
	}
	if hasProxy {
		env = ensureNoProxy(env, extraNoProxyHosts...)
	}
	return env
}

// writeMCPConfig creates host directories and writes the per-channel MCP
// config file. Returns the config file path.
func (r *DockerRunner) writeMCPConfig(workDir, channelID, apiURL, authorID, agentID string, cfg *config.Config) (string, error) {
	for _, dir := range []string{workDir, filepath.Join(workDir, ".loop")} {
		if err := r.sys.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating host directory %s: %w", dir, err)
		}
	}

	mcpConfigPath := mcpConfigPathForAgent(workDir, channelID, agentID)
	mcpCfg := buildMCPConfig(channelID, apiURL, workDir, authorID, agentID, cfg.Memory.Enabled, cfg.Browser.Enabled, cfg.MCPServers)
	mcpJSON, _ := json.MarshalIndent(mcpCfg, "", "  ")
	if err := r.sys.WriteFile(mcpConfigPath, mcpJSON, 0o644); err != nil {
		return "", fmt.Errorf("writing mcp config: %w", err)
	}
	return mcpConfigPath, nil
}

// buildContainerMounts processes config mounts and adds the workDir bind.
// If parentDirPath is set and workDir is inside it (worktree), also mounts
// the parent so the container sees the main .git directory.
// Returns the bind strings and any named-volume container paths that need chown.
func (r *DockerRunner) buildContainerMounts(mounts []string, workDir, parentDirPath string, extraDirs []string) (binds, chownPaths []string) {
	for _, mount := range mounts {
		if ms, err := parseMountSpec(mount); err == nil && config.IsNamedVolume(ms.Host) {
			expanded, _ := r.expandPath(ms.Container)
			if expanded != "" {
				chownPaths = append(chownPaths, expanded)
			}
		}
		bind, err := r.processMount(mount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping mount %s: %v\n", mount, err)
			continue
		}
		if bind != "" {
			binds = append(binds, bind)
		}
	}

	if excludesBind := r.gitExcludesMount(); excludesBind != "" {
		binds = append(binds, excludesBind)
	}

	// Worktree threads: mount the parent project dir (which includes the
	// worktree subdir) so the container sees the main .git directory.
	// Otherwise mount just the workDir.
	if parentDirPath != "" && strings.HasPrefix(workDir, parentDirPath+"/") {
		// Worktree is inside the parent — mounting parent covers both.
		binds = append(binds, parentDirPath+":"+parentDirPath)
	} else {
		binds = append(binds, workDir+":"+workDir)
		// External worktree (outside parent dir): mount parent separately for .git access.
		// Skip when parent == workDir (e.g. a worktree task whose thread was
		// deleted mid-flight) — Docker rejects duplicate mount targets.
		if parentDirPath != "" && parentDirPath != workDir {
			binds = append(binds, parentDirPath+":"+parentDirPath)
		}
	}

	// Mount extra directories for multi-dir workspaces.
	// Build a set of already-mounted container paths to avoid duplicates.
	mounted := make(map[string]bool, len(binds))
	for _, b := range binds {
		if parts := strings.SplitN(b, ":", 3); len(parts) >= 2 {
			mounted[parts[1]] = true
		}
	}
	for _, dir := range extraDirs {
		expanded, err := r.expandPath(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping extra dir %s: %v\n", dir, err)
			continue
		}
		if expanded == workDir || expanded == parentDirPath || mounted[expanded] {
			continue
		}
		if parentDirPath != "" && strings.HasPrefix(expanded, parentDirPath+"/") {
			continue
		}
		binds = append(binds, expanded+":"+expanded)
	}

	return binds, chownPaths
}

// buildBaseClaudeCmd returns the common Claude CLI flags shared by both
// batch and interactive modes.
func buildBaseClaudeCmd(cfg *config.Config, mcpConfigPath, sessionID, agentID string, forkSession bool, extraDirs []string) []string {
	cmd := []string{cfg.ClaudeBinPath, "--mcp-config", mcpConfigPath}
	if cfg.ClaudeModel != "" {
		cmd = append(cmd, "--model", cfg.ClaudeModel)
	}
	cmd = append(cmd, "--dangerously-skip-permissions")
	if sessionID != "" {
		cmd = append(cmd, "--resume", sessionID)
		if forkSession {
			cmd = append(cmd, "--fork-session")
		}
	}
	// Enable MCP Channels when agent tools are configured, so the agent
	// can receive push notifications from other agents. Anthropic ships
	// `--dangerously-load-development-channels` as a development-only flag,
	// so it's opt-in via config (global → project → worktree).
	if agentID != "" && cfg.ClaudeDangerouslyLoadDevelopmentChannels {
		cmd = append(cmd, "--dangerously-load-development-channels", "server:loop")
	}
	for _, dir := range extraDirs {
		cmd = append(cmd, "--add-dir", dir)
	}
	return cmd
}

// planModePromptPrefix is prepended to the user's prompt when req.PlanMode is
// true. Calling EnterPlanMode flips the session's permission context to
// "plan", which causes Claude Code's per-turn attachment loop to inject the
// full pair-planning prompt (with a computed planFilePath and read-only
// restrictions) from the next turn onward.
const planModePromptPrefix = "Call the EnterPlanMode tool before doing anything else, then follow the plan-mode instructions that follow.\n\n"

// buildClaudeCmd assembles the Claude CLI command with all flags for batch mode.
func buildClaudeCmd(cfg *config.Config, mcpConfigPath string, req *agent.AgentRequest) []string {
	cmd := buildBaseClaudeCmd(cfg, mcpConfigPath, req.SessionID, req.AgentID, req.ForkSession, cfg.ExtraDirs)
	cmd = append(cmd, "--print", "--verbose", "--output-format", "stream-json")
	if req.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", req.SystemPrompt)
	}
	prompt := req.BuildPrompt()
	if req.PlanMode {
		prompt = planModePromptPrefix + prompt
	}
	return append(cmd, prompt)
}

// mcpConfigPathForAgent returns the MCP config file path. When agentID is set,
// a per-agent config is used so each agent gets its own --agent-id flag.
func mcpConfigPathForAgent(workDir, channelID, agentID string) string {
	if agentID != "" {
		return filepath.Join(workDir, ".loop", "mcp-"+channelID+"-"+agentID+".json")
	}
	return filepath.Join(workDir, ".loop", "mcp-"+channelID+".json")
}

// BuildInteractiveClaudeCmd assembles the Claude CLI shell command for interactive
// terminal sessions (no --print, --verbose, --output-format flags).
//
// When the seccomp gate is enabled, the command is wrapped in
// `loop syscallwrap --` so the interactive claude runs under the same filter
// the agent-mode (stream) path gets via entrypoint.sh. docker-exec'ing into the
// running shell container does NOT inherit the shell's seccomp state (setns(2)
// is per-namespace, but seccomp is per-process), so without this wrapper a
// user typing `claude` at the terminal would bypass the gate entirely.
func BuildInteractiveClaudeCmd(cfg *config.Config, channelID, workDir, sessionID, agentID string, forkSession bool) string {
	mcpConfigPath := mcpConfigPathForAgent(workDir, channelID, agentID)
	cmd := buildBaseClaudeCmd(cfg, mcpConfigPath, sessionID, agentID, forkSession, cfg.ExtraDirs)
	if cfg.Gates.Agentgate.Enabled {
		cmd = append([]string{"loop", "syscallwrap", "--"}, cmd...)
	}
	return "CLAUDE_CODE_NO_FLICKER=1 " + strings.Join(cmd, " ")
}

// ClaudeCmdBuilder builds the interactive Claude command for terminal sessions.
// It implements api.InteractiveCmdBuilder.
type ClaudeCmdBuilder struct {
	cfg                       atomic.Pointer[config.Config]
	configLoad                func() (*config.Config, error)
	loadProjectConfig         func(string, *config.Config) (*config.Config, error)
	loadWorktreeProjectConfig func(string, string, *config.Config) (*config.Config, error)
	writeFile                 func(string, []byte, os.FileMode) error
	mkdirAll                  func(string, os.FileMode) error
}

// NewClaudeCmdBuilder creates a builder that uses the given config.
func NewClaudeCmdBuilder(cfg *config.Config, configLoad func() (*config.Config, error)) *ClaudeCmdBuilder {
	b := &ClaudeCmdBuilder{
		configLoad:                configLoad,
		loadProjectConfig:         config.LoadProjectConfig,
		loadWorktreeProjectConfig: config.LoadWorktreeProjectConfig,
		writeFile:                 os.WriteFile,
		mkdirAll:                  os.MkdirAll,
	}
	b.cfg.Store(cfg)
	return b
}

// currentConfig returns a fresh config by calling configLoad, falling back
// to the last-known-good config on error or when configLoad is nil.
func (b *ClaudeCmdBuilder) currentConfig() *config.Config {
	if b.configLoad == nil {
		return b.cfg.Load()
	}
	fresh, err := b.configLoad()
	if err != nil {
		return b.cfg.Load()
	}
	b.cfg.Store(fresh)
	return fresh
}

// BuildInteractiveCmd returns the interactive Claude shell command for the given channel.
// It loads the project-specific config (if any) to apply per-project overrides
// such as claude_model before building the command.
// When agentID is set, a per-agent MCP config is written with --agent-id so
// the agent can identify itself via the MCP tools.
func (b *ClaudeCmdBuilder) BuildInteractiveCmd(channelID, dirPath, parentDirPath, sessionID, agentID string, forkSession bool) string {
	baseCfg := b.currentConfig()
	workDir := dirPath
	if workDir == "" {
		workDir = filepath.Join(baseCfg.LoopDir, channelID, "work")
	}
	cfg := baseCfg
	if parentDirPath != "" {
		if merged, err := b.loadWorktreeProjectConfig(workDir, parentDirPath, baseCfg); err == nil {
			cfg = merged
		}
	} else if merged, err := b.loadProjectConfig(workDir, baseCfg); err == nil {
		cfg = merged
	}
	if agentID != "" {
		b.writeAgentMCPConfig(cfg, workDir, channelID, agentID)
	}
	return BuildInteractiveClaudeCmd(cfg, channelID, workDir, sessionID, agentID, forkSession)
}

// writeAgentMCPConfig writes a per-agent MCP config file with --agent-id.
func (b *ClaudeCmdBuilder) writeAgentMCPConfig(cfg *config.Config, workDir, channelID, agentID string) {
	apiURL := "http://host.docker.internal" + cfg.APIAddr
	loopDir := filepath.Join(workDir, ".loop")
	_ = b.mkdirAll(loopDir, 0o755)
	mcpCfg := buildMCPConfig(channelID, apiURL, workDir, "", agentID, cfg.Memory.Enabled, cfg.Browser.Enabled, cfg.MCPServers)
	data, _ := json.MarshalIndent(mcpCfg, "", "  ")
	configPath := mcpConfigPathForAgent(workDir, channelID, agentID)
	_ = b.writeFile(configPath, data, 0o644)
}

// filterMountedCopyFiles removes entries from copyFiles whose expanded paths
// are already bind-mounted into the container, avoiding "device or resource busy"
// errors from CopyToContainer.
func (r *DockerRunner) filterMountedCopyFiles(copyFiles, binds []string) []string {
	// Build set of bind-mounted container paths.
	mounted := make(map[string]struct{}, len(binds))
	for _, b := range binds {
		if ms, err := parseMountSpec(b); err == nil {
			mounted[ms.Container] = struct{}{}
		}
	}

	var filtered []string
	for _, f := range copyFiles {
		expanded, err := r.expandPath(f)
		if err != nil {
			filtered = append(filtered, f) // keep on error; copyFiles handles it
			continue
		}
		if _, ok := mounted[expanded]; ok {
			continue // skip — already bind-mounted
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// copyFiles copies the given host files into the container so that each
// container gets its own copy instead of sharing a Docker volume.
// Files that don't exist are silently skipped.
func (r *DockerRunner) copyFiles(ctx context.Context, containerID string, files []string) error {
	for _, f := range files {
		expanded, err := r.expandPath(f)
		if err != nil {
			return fmt.Errorf("expanding path %s: %w", f, err)
		}

		data, err := r.sys.ReadFile(expanded)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", f, err)
		}

		dir := filepath.Dir(expanded)
		name := filepath.Base(expanded)

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(data)),
		})
		_, _ = tw.Write(data)
		_ = tw.Close()

		if err := r.client.CopyToContainer(ctx, containerID, dir, &buf); err != nil {
			return err
		}
	}
	return nil
}

// createAndStartContainer resolves the work directory, loads project config,
// builds environment and mounts, writes MCP config, creates the Docker
// container with the command returned by buildCmd, and starts it.
// The mcpConfigPath is returned so the caller can clean it up if needed.
// keepMCPConfig reflects the merged config's KeepMCPConfigs flag.
func (r *DockerRunner) createAndStartContainer(
	ctx context.Context,
	channelID, dirPath, authorID, parentDirPath, agentID string,
	cType ContainerType,
	buildCmd func(cfg *config.Config, mcpConfigPath string) []string,
) (containerID, containerName, mcpConfigPath string, keepMCPConfig bool, err error) {
	workDir := filepath.Join(r.currentConfig().LoopDir, channelID, "work")
	if dirPath != "" {
		workDir = dirPath
	}

	baseCfg := r.currentConfig()
	var cfg *config.Config
	if parentDirPath != "" {
		cfg, err = r.loadWorktreeProjectConfig(workDir, parentDirPath, baseCfg)
	} else {
		cfg, err = r.loadProjectConfig(workDir, baseCfg)
	}
	if err != nil {
		return "", "", "", false, fmt.Errorf("loading project config: %w", err)
	}
	keepMCPConfig = cfg.KeepMCPConfigs

	apiURL := "http://host.docker.internal" + cfg.APIAddr

	env, err := r.buildContainerEnv(cfg, channelID, apiURL)
	if err != nil {
		return "", "", "", false, err
	}

	binds, chownPaths := r.buildContainerMounts(cfg.Mounts, workDir, parentDirPath, cfg.ExtraDirs)
	for _, f := range r.filterMountedCopyFiles(cfg.CopyFiles, binds) {
		if expanded, err := r.expandPath(f); err == nil {
			chownPaths = append(chownPaths, expanded)
		}
	}

	// Bind-mount the screenshot directory so the MCP server can read files written by the host.
	screenshotDir := filepath.Join(baseCfg.LoopDir, "screenshots")
	binds = append(binds, screenshotDir+":"+screenshotDir+":ro")

	// Bind-mount the playground directory so agents can read/write playground files.
	playgroundDir := filepath.Join(baseCfg.LoopDir, "playground")
	binds = append(binds, playgroundDir+":"+playgroundDir)

	containerName = r.containerName(channelID, dirPath)

	// Per-container bearer token authenticates HTTP callbacks from the
	// in-container docker proxy + seccomp-gate parent into loop-server.
	// Generated once per spawn; shared by both layers via env var.
	gateToken, err := r.newGateToken()
	if err != nil {
		return "", "", "", false, fmt.Errorf("generating gate token: %w", err)
	}

	// Build the per-container agentgate.Manager that will receive HTTP
	// approval requests. Registered with the resolver (keyed by the real
	// container ID + token) once ContainerCreate returns a cid.
	var gateMgr *agentgate.Manager
	if (cfg.Gates.DockerProxy.Enabled || cfg.Gates.Agentgate.Enabled) && r.gateResolver != nil && r.gateBotRouter != nil {
		gateMgr = agentgate.NewManager(r.gateBotRouter, r.gateRateLimits)
	}

	// Write the per-container docker-proxy policy file. loop-dockerproxy
	// reads it inside the container; the bind-mount below mounts it read-
	// only under /etc/loop/proxy-policy.json.
	proxyPolicyHostPath, err := r.writeProxyPolicyFile(cfg, channelID, workDir, parentDirPath)
	if err != nil {
		return "", "", "", false, err
	}
	if proxyPolicyHostPath != "" {
		hostSock := r.hostDockerSock
		if hostSock == "" {
			hostSock = "/var/run/docker.sock"
		}
		// The proxy listens on /var/run/docker.sock inside the container; any
		// user mount targeting that path collides with the listener and the
		// socket file can't be unlinked since it's a bind. Strip the conflict
		// and warn so the user removes it from their config.
		binds = filterProxySockConflicts(binds)
		binds = append(binds,
			hostSock+":/var/run/docker.sock.host:ro",
			proxyPolicyHostPath+":/etc/loop/proxy-policy.json:ro",
		)
		env = append(env,
			"LOOP_DOCKERPROXY_ENABLED=1",
			"LOOP_DOCKERPROXY_POLICY_FILE=/etc/loop/proxy-policy.json",
			"LOOP_DOCKERPROXY_UPSTREAM=/var/run/docker.sock.host",
		)
	}

	// Write the per-container seccomp-gate policy file. loop-syscallwrap
	// parent reads it inside the container; mounted read-only at
	// /etc/loop/gate-policy.json.
	gatePolicyHostPath, err := r.writeGatePolicyFile(cfg, channelID, workDir, parentDirPath)
	if err != nil {
		return "", "", "", false, err
	}
	if gatePolicyHostPath != "" {
		binds = append(binds, gatePolicyHostPath+":/etc/loop/gate-policy.json:ro")
		env = append(env,
			"LOOP_GATE_ENABLED=1",
			"LOOP_GATE_POLICY_FILE=/etc/loop/gate-policy.json",
		)
		// Per-channel audit sink: rotating jsonl written by the
		// in-container gate parent (root). Bound rw; retention is
		// applied by FileAuditor on each rotation. Keyed by channel/
		// thread so the same dir is reused across container restarts.
		auditHostPath, err := r.ensureGateAuditDir(channelID, dirPath)
		if err != nil {
			return "", "", "", false, err
		}
		if auditHostPath != "" {
			binds = append(binds, auditHostPath+":/var/log/loop-gate:rw")
			env = append(env,
				"LOOP_GATE_AUDIT_DIR=/var/log/loop-gate",
				fmt.Sprintf("LOOP_GATE_AUDIT_RETENTION_DAYS=%d", cfg.Gates.Audit.RetentionDays),
			)
			if cfg.Gates.Audit.Verbose {
				env = append(env, "LOOP_GATE_AUDIT_VERBOSE=1")
			}
		}
	}

	// Both the proxy and the gate authenticate HTTP callbacks with the same
	// per-container bearer token. The channel id is how loop-server knows
	// which chat surface to prompt when a trap fires. LOOP_CONTAINER_ID is
	// required by loop-dockerproxy's Server constructor — the docker daemon
	// only hands us the real cid after ContainerCreate, but the container
	// name is stable and unique so we use it as the CID the proxy stamps on
	// approval events.
	if proxyPolicyHostPath != "" || gatePolicyHostPath != "" {
		env = append(env,
			"LOOP_CHANNEL_ID="+channelID,
			"LOOP_GATE_TOKEN="+gateToken,
			"LOOP_CONTAINER_ID="+containerName,
		)
	}

	if len(chownPaths) > 0 {
		env = append(env, "CHOWN_PATHS="+strings.Join(chownPaths, ":"))
	}

	// Ensure workDir exists on host (it's bind-mounted into the container).
	if err := r.sys.MkdirAll(workDir, 0o755); err != nil {
		return "", "", "", false, fmt.Errorf("creating work dir: %w", err)
	}

	// Initialize git in auto-created work directories so the agent can use version control.
	if dirPath == "" {
		_, _ = r.sys.ExecCommandOutput("git", "init", workDir)
	}

	mcpConfigPath, err = r.writeMCPConfig(workDir, channelID, apiURL, authorID, agentID, cfg)
	if err != nil {
		return "", "", "", false, err
	}

	cmd := buildCmd(cfg, mcpConfigPath)

	// The seccomp gate needs two things Docker's default sandbox denies:
	//   - seccomp=unconfined: the gate calls seccomp(2) with NEW_LISTENER,
	//     which the default outer profile gates behind CAP_SYS_ADMIN. Drop
	//     the outer profile so the install can run; the inner gate is the
	//     real defense once it's up.
	//   - CAP_SYS_PTRACE: the gate-server (root) reads the traced child's
	//     memory via process_vm_readv to inspect argv / paths before making
	//     a decision. Default caps lack SYS_PTRACE, and the child's dumpable
	//     bit is cleared by the setuid drop, so without this cap every trap
	//     fails closed with EPERM — which bubbles back out of execve.
	var securityOpt, capAdd []string
	if cfg.Gates.Agentgate.Enabled {
		securityOpt = []string{"seccomp=unconfined"}
		capAdd = []string{"SYS_PTRACE"}
	}

	containerCfg := &ContainerConfig{
		Image:       cfg.ContainerImage,
		MemoryMB:    cfg.ContainerMemoryMB,
		CPUs:        cfg.ContainerCPUs,
		Env:         env,
		Cmd:         cmd,
		Binds:       binds,
		WorkingDir:  workDir,
		Labels:      map[string]string{ChannelLabelKey: channelID, ContainerTypeKey: string(cType), InstanceLabelKey: r.instanceID},
		SecurityOpt: securityOpt,
		CapAdd:      capAdd,
	}

	containerID, err = r.client.ContainerCreate(ctx, containerCfg, containerName)
	if err != nil {
		return "", "", mcpConfigPath, keepMCPConfig, fmt.Errorf("creating container: %w", err)
	}
	if gateMgr != nil && r.gateResolver != nil {
		r.gateResolver.AddWithToken(containerID, gateToken, gateMgr, channelID)
	}

	if err := r.copyFiles(ctx, containerID, r.filterMountedCopyFiles(cfg.CopyFiles, binds)); err != nil {
		return containerID, containerName, mcpConfigPath, keepMCPConfig, fmt.Errorf("copying files: %w", err)
	}

	if err := r.client.ContainerStart(ctx, containerID); err != nil {
		return containerID, containerName, mcpConfigPath, keepMCPConfig, fmt.Errorf("starting container: %w", err)
	}

	return containerID, containerName, mcpConfigPath, keepMCPConfig, nil
}

// newGateToken returns 32 bytes of crypto/rand hex. Per-container bearer
// token used to authenticate HTTP callbacks from in-container loop-
// dockerproxy + loop-syscallwrap parent. Uses osRandRead so tests can
// inject a deterministic value.
func (r *DockerRunner) newGateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := r.osRandRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// proxyPolicyJSON mirrors the subset of config.DockerProxyConfig that
// loop-dockerproxy reads. Keeping it explicit (rather than serialising the
// whole DockerProxyConfig) avoids leaking the Enabled flag into the container
// and lets us splice in per-container body rules without mutating cfg.
type proxyPolicyJSON struct {
	DefaultDecision types.Decision          `json:"default_decision"`
	HTTPRules       []types.HTTPServiceRule `json:"http_rules"`
	BodyRules       []types.BodyRule        `json:"body_rules"`
}

// writeProxyPolicyFile serialises cfg.DockerProxy to JSON at
// {policyDir}/<channel>/proxy-policy.json and returns the host path. Returns
// ("", nil) when the proxy is disabled OR when policyDir is unset (tests).
// loop-dockerproxy reads the same JSON shape.
//
// Keyed by channel (not cid) so all per-channel artifacts — policy files and
// audit logs — share one tree. Concurrent same-channel spawns are safe: the
// payload is derived from global config plus the channel's stable workDir,
// so overwrites are idempotent.
//
// workDir/parentDirPath come from the per-channel mount setup — the workspace
// bind-approval rule is injected here (not in the static defaults) because
// the real workspace path is the host bind-mount path, not a fixed /work.
func (r *DockerRunner) writeProxyPolicyFile(cfg *config.Config, channelID, workDir, parentDirPath string) (string, error) {
	if !cfg.Gates.DockerProxy.Enabled {
		return "", nil
	}
	if r.policyDir == "" {
		return "", nil
	}
	dir := filepath.Join(r.policyDir, policyChannelKey(channelID))
	if err := r.sys.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating policy dir: %w", err)
	}
	payload := proxyPolicyJSON{
		DefaultDecision: cfg.Gates.DockerProxy.DefaultDecision,
		HTTPRules:       cfg.Gates.DockerProxy.HTTPRules,
		BodyRules:       injectWorkspaceBindRule(cfg.Gates.DockerProxy.BodyRules, workDir, parentDirPath),
	}
	raw, _ := json.Marshal(payload)
	path := filepath.Join(dir, "proxy-policy.json")
	if err := r.sys.WriteFile(path, raw, 0o640); err != nil {
		return "", fmt.Errorf("writing proxy policy: %w", err)
	}
	return path, nil
}

// gatePolicyJSON mirrors the subset of config.AgentgateConfig that
// loop-syscallwrap parent reads. Keeping it here (rather than
// serialising the whole AgentgateConfig) makes the wire format
// explicit and avoids leaking the Enabled flag into the container.
type gatePolicyJSON struct {
	DefaultDecision types.Decision      `json:"default_decision"`
	PathRules       []types.PathRule    `json:"path_rules"`
	CommandRules    []types.CommandRule `json:"command_rules"`
	FileRules       []types.FileRule    `json:"file_rules"`
}

// writeGatePolicyFile serialises the subset of cfg.Gates.Agentgate that the
// in-container seccomp gate needs and writes it to
// {policyDir}/<channel>/gate-policy.json. Returns ("", nil) when the gate is
// disabled OR when policyDir is unset.
//
// Keyed by channel (shared with proxy policy + audit) so all per-channel
// artifacts live under one tree. Concurrent same-channel spawns are safe:
// the payload derives from global config and the channel's stable workDir,
// so overwrites are idempotent.
//
// workDir/parentDirPath come from the per-channel mount setup — the workspace
// allow rule is injected here (not in the static defaults) because the real
// workspace path is the host bind-mount path, not a fixed /work.
func (r *DockerRunner) writeGatePolicyFile(cfg *config.Config, channelID, workDir, parentDirPath string) (string, error) {
	if !cfg.Gates.Agentgate.Enabled {
		return "", nil
	}
	if r.policyDir == "" {
		return "", nil
	}
	dir := filepath.Join(r.policyDir, policyChannelKey(channelID))
	if err := r.sys.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating policy dir: %w", err)
	}
	payload := gatePolicyJSON{
		DefaultDecision: cfg.Gates.Agentgate.DefaultDecision,
		PathRules:       cfg.Gates.Agentgate.PathRules,
		CommandRules:    injectWorkspaceRmRfRule(cfg.Gates.Agentgate.CommandRules, workDir, parentDirPath),
		FileRules:       injectWorkspaceRule(cfg.Gates.Agentgate.FileRules, workDir, parentDirPath),
	}
	raw, _ := json.Marshal(payload)
	path := filepath.Join(dir, "gate-policy.json")
	if err := r.sys.WriteFile(path, raw, 0o640); err != nil {
		return "", fmt.Errorf("writing gate policy: %w", err)
	}
	return path, nil
}

// policyChannelKey returns the sanitized channel-directory name used as the
// per-channel root under policyDir. Falls back to "unkeyed" when channelID
// is empty (ad-hoc one-shot runs) so the path is always well-formed.
func policyChannelKey(channelID string) string {
	if channelID == "" {
		return "unkeyed"
	}
	return SanitizeName(channelID)
}

// ensureGateAuditDir creates the host dir that backs the in-container
// /var/log/loop-gate bind. Returns ("", nil) when policyDir is unset (tests).
//
// Keyed by channel/thread, NOT by container: every restart of the same
// channel reuses the same dir, so the rotating jsonl files accumulate one
// ongoing history instead of fragmenting across thousands of per-spawn
// dirs. Path: {policyDir}/<sanitized-channel>/audit/ — channel is the
// primary key so all per-channel artifacts (policy file, audit log, future
// per-channel state) live under one tree. Falls back to the dirPath
// basename when channelID is empty (ad-hoc one-shot runs).
//
// The dir is created 0o770 so the in-container parent (uid 0) can write
// into it; host owner is whoever runs the loop daemon.
func (r *DockerRunner) ensureGateAuditDir(channelID, dirPath string) (string, error) {
	if r.policyDir == "" {
		return "", nil
	}
	base := channelID
	if base == "" {
		base = filepath.Base(dirPath)
	}
	if base == "" || base == "." || base == "/" {
		base = ""
	}
	dir := filepath.Join(r.policyDir, policyChannelKey(base), "audit")
	if err := r.sys.MkdirAll(dir, 0o770); err != nil {
		return "", fmt.Errorf("creating audit dir: %w", err)
	}
	return dir, nil
}

// injectWorkspaceBindRule prepends an Allow body rule that fires when the
// agent submits a HostConfig.Binds[*] entry whose source is the channel's real
// workspace path (workDir, optionally also parentDirPath). The rule must come
// before the static deny on POST /containers/create so workspaces under
// generically-denied prefixes (e.g. /home/<user>/projects on Linux) aren't
// blanket-denied — any mount inside the project's own tree is the agent's
// own dev-loop work and is allowed directly without a prompt.
//
// On macOS Docker Desktop, agent-submitted bind sources may carry a
// /host_mnt prefix (the daemon's view of host paths through the Linux VM),
// so each pattern is emitted in both bare and /host_mnt-prefixed form.
//
// Returns rules unchanged when workDir is empty (ad-hoc one-shot runs).
func injectWorkspaceBindRule(rules []types.BodyRule, workDir, parentDirPath string) []types.BodyRule {
	if workDir == "" {
		return rules
	}
	paths := []string{workDir}
	if parentDirPath != "" && parentDirPath != workDir {
		paths = append(paths, parentDirPath)
	}
	values := make([]string, 0, len(paths)*2)
	for _, p := range paths {
		quoted := regexp.QuoteMeta(p)
		values = append(values,
			"^"+quoted+"($|/)",
			"^/host_mnt"+quoted+"($|/)",
		)
	}
	ws := types.BodyRule{
		AppliesTo:    "POST ^/containers/create$",
		ContentTypes: []string{"application/json"},
		MaxBodyBytes: 1048576,
		JSONChecks: []types.JSONCheck{
			{
				Path:   "HostConfig.Binds[*]",
				Op:     "source_path_in",
				Values: values,
			},
		},
		Decision: types.DecisionAllow,
		Message:  "workspace bind-mount fast-path",
	}
	out := make([]types.BodyRule, 0, len(rules)+1)
	out = append(out, ws)
	out = append(out, rules...)
	return out
}

// injectWorkspaceRmRfRule inserts an Allow command rule for `rm` whose
// ArgsPattern accepts any combination of flags followed by paths under the
// channel's workspace (workDir, optionally also parentDirPath). The rule is
// prepended so it fires before the static `rm -rf on absolute path` deny —
// agents routinely `rm -rf` build outputs inside their own project tree, and
// without this whitelist the deny would block legitimate cleanup.
//
// Mirrors the /tmp allow's "every positional arg must be a workspace path"
// shape: a mixed `rm -rf /workspace/build /etc/passwd` won't match (the /etc
// path falls outside the alternation) and falls through to the deny.
//
// Returns rules unchanged when workDir is empty (ad-hoc one-shot runs).
func injectWorkspaceRmRfRule(rules []types.CommandRule, workDir, parentDirPath string) []types.CommandRule {
	if workDir == "" {
		return rules
	}
	prefixes := []string{regexp.QuoteMeta(workDir)}
	if parentDirPath != "" && parentDirPath != workDir {
		prefixes = append(prefixes, regexp.QuoteMeta(parentDirPath))
	}
	alt := "(?:" + strings.Join(prefixes, "|") + ")"
	pattern := `^(-[a-zA-Z]+\s+)*` + alt + `(/\S*)?(\s+` + alt + `(/\S*)?)*$`
	ws := types.CommandRule{
		Commands:     []string{"rm"},
		ArgsPatterns: []string{pattern},
		Decision:     types.DecisionAllow,
		Message:      "rm under workspace",
	}
	out := make([]types.CommandRule, 0, len(rules)+1)
	out = append(out, ws)
	out = append(out, rules...)
	return out
}

// injectWorkspaceRule inserts a workspace fast-path Allow rule into the file
// rules list at the first position following any Deny/Approve rules. This
// keeps generic denies (**/.ssh/**, etc.) and Approve markers (approve-me*)
// matching first, while granting blanket access to the real workspace path.
func injectWorkspaceRule(rules []types.FileRule, workDir, parentDirPath string) []types.FileRule {
	if workDir == "" {
		return rules
	}
	paths := []string{workDir + "/**"}
	if parentDirPath != "" && parentDirPath != workDir {
		paths = append(paths, parentDirPath+"/**")
	}
	ws := types.FileRule{
		Paths:      paths,
		Operations: []string{"read", "write", "create", "delete", "stat", "list", "chmod", "chown", "link"},
		Decision:   types.DecisionAllow,
		Message:    "workspace fast-path",
	}
	insertAt := len(rules)
	for i, r := range rules {
		if r.Decision == types.DecisionAllow {
			insertAt = i
			break
		}
	}
	out := make([]types.FileRule, 0, len(rules)+1)
	out = append(out, rules[:insertAt]...)
	out = append(out, ws)
	out = append(out, rules[insertAt:]...)
	return out
}

// collectOutput reads container logs (streaming or batch) and waits for exit.
// Returns the parsed Claude response or an error.
func (r *DockerRunner) collectOutput(ctx context.Context, containerID string, cb streamCallbacks) (*claudeResponse, error) {
	if cb.onTurn != nil || cb.onThinking != nil || cb.onToolResult != nil {
		return r.collectStreamingOutput(ctx, containerID, cb)
	}
	return r.collectBatchOutput(ctx, containerID)
}

// collectStreamingOutput follows container logs in real-time, then waits for exit.
func (r *DockerRunner) collectStreamingOutput(ctx context.Context, containerID string, cb streamCallbacks) (*claudeResponse, error) {
	logsReader, err := r.client.ContainerLogsFollow(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("following container logs: %w", err)
	}

	claudeResp, parseErr := scanStreamJSON(logsReader, cb)
	logsReader.Close()

	exitCode, err := r.waitForExit(ctx, containerID)
	if err != nil {
		return nil, err
	}

	if parseErr != nil {
		if exitCode != 0 {
			return nil, fmt.Errorf("container exited with code %d: %w", exitCode, parseErr)
		}
		return nil, parseErr
	}
	return claudeResp, nil
}

// collectBatchOutput waits for the container to exit, then reads all logs.
func (r *DockerRunner) collectBatchOutput(ctx context.Context, containerID string) (*claudeResponse, error) {
	exitCode, err := r.waitForExit(ctx, containerID)
	if err != nil {
		return nil, err
	}

	reader, err := r.client.ContainerLogs(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("reading container logs: %w", err)
	}

	claudeResp, parseErr := scanStreamJSON(reader, streamCallbacks{})
	if parseErr != nil {
		if exitCode != 0 {
			return nil, fmt.Errorf("container exited with code %d: %w", exitCode, parseErr)
		}
		return nil, parseErr
	}
	return claudeResp, nil
}

// waitForExit blocks until the container exits and returns the exit code.
func (r *DockerRunner) waitForExit(ctx context.Context, containerID string) (int64, error) {
	waitCh, errCh := r.client.ContainerWait(ctx, containerID)
	select {
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = r.client.ContainerStop(stopCtx, containerID)
		return 0, fmt.Errorf("container execution timed out: %w", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return 0, fmt.Errorf("waiting for container: %w", err)
		}
		return 0, nil
	case wr := <-waitCh:
		if wr.Error != nil {
			return 0, fmt.Errorf("container exited with error: %w", wr.Error)
		}
		return wr.StatusCode, nil
	}
}

// ensureNoProxy ensures host.docker.internal (and any extra hosts) are in
// NO_PROXY and no_proxy so the container's API calls bypass the proxy.
func ensureNoProxy(env []string, extraHosts ...string) []string {
	hosts := append([]string{"host.docker.internal", "localhost", "127.0.0.1", "::1"}, extraHosts...)
	found := false
	for i, e := range env {
		for _, key := range []string{"NO_PROXY=", "no_proxy="} {
			if strings.HasPrefix(e, key) {
				found = true
				val := strings.TrimPrefix(e, key)
				for _, host := range hosts {
					if !strings.Contains(val, host) {
						if val != "" {
							val += ","
						}
						val += host
					}
				}
				env[i] = key + val
			}
		}
	}
	if !found {
		all := strings.Join(hosts, ",")
		env = append(env, "NO_PROXY="+all, "no_proxy="+all)
	}
	return env
}

// localhostToDockerHost rewrites localhost proxy addresses so they resolve
// inside the container. E.g. ":3128" → "http://host.docker.internal:3128",
// "http://127.0.0.1:3128" → "http://host.docker.internal:3128".
func localhostToDockerHost(v string) string {
	// Bare port like ":3128"
	if strings.HasPrefix(v, ":") {
		return "http://host.docker.internal" + v
	}

	r := strings.NewReplacer(
		"://localhost:", "://host.docker.internal:",
		"://localhost/", "://host.docker.internal/",
		"://127.0.0.1:", "://host.docker.internal:",
		"://127.0.0.1/", "://host.docker.internal/",
	)
	result := r.Replace(v)
	// Handle trailing-slash-less variants like "http://localhost"
	for _, suffix := range []string{"://localhost", "://127.0.0.1"} {
		if before, ok := strings.CutSuffix(result, suffix); ok {
			result = before + "://host.docker.internal"
		}
	}
	return result
}

// streamCallbacks holds optional callbacks for scanStreamJSON.
type streamCallbacks struct {
	onTurn       func(string)
	onToolUse    func(toolUseID, name, input string)
	onActivity   func(activity, detail string)
	onThinking   func(text string)
	onToolResult func(toolUseID, output string, isError bool)
}

// userEventMaxBytes caps the size of "user" stream-json lines we will fully
// read. Above this we drain the line (without buffering it) — this protects
// against multi-MB screenshot tool_results. Below it, we parse the line so
// non-image tool_result blocks (Read/Bash/Grep output) reach the chat.
const userEventMaxBytes = 256 * 1024

// toolResultMaxInline caps the live tool_result output we forward over SSE
// and persist via OnToolResult. Anything above is truncated at this boundary;
// /timeline re-applies the same cap defensively when serving the row.
const toolResultMaxInline = 8 * 1024

// userMessage represents a "user" event from Claude's stream-json output, used
// only to surface tool_result blocks live. The Message.Content is polymorphic:
// each block's Content is either a plain string OR an array of {type, text|image}
// blocks; both shapes are handled by parseToolResultContent.
type userMessage struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
}

// parseToolResultContent extracts the textual body of a tool_result content
// field. The field is polymorphic: a plain string OR an array of {type, text}
// or {type, image} blocks. Image blocks are dropped; text blocks are joined.
func parseToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// truncateInline trims s to maxBytes and reports whether it was truncated.
func truncateInline(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	return s[:maxBytes], true
}

// readLineOrSkip reads a line from the buffered reader. For "user" events
// (tool results), it caps the buffered bytes at userEventMaxBytes — over the
// cap, the rest of the line is drained without buffering and the function
// returns nil (typically a screenshot). Under the cap, the full line is
// returned so callers can dispatch tool_result blocks live. Non-user lines
// are returned in full.
func readLineOrSkip(br *bufio.Reader) ([]byte, error) {
	// Peek at the first bytes to detect the event type without reading
	// the full line. Tool results (screenshots) can be several MB.
	peek, peekErr := br.Peek(30)
	if len(peek) == 0 && peekErr != nil {
		return nil, peekErr // EOF or real error
	}
	isUser := strings.Contains(string(peek), `"type":"user"`)

	var (
		buf  []byte
		over bool
	)
	for {
		chunk, err := br.ReadSlice('\n')
		if isUser && !over && len(buf)+len(chunk) > userEventMaxBytes {
			// Over cap — stop buffering and start draining.
			over = true
			buf = nil
		}
		if !over {
			buf = append(buf, chunk...)
		}
		switch {
		case err == nil:
			if over {
				return nil, nil
			}
			return bytes.TrimSpace(buf), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if over {
				return nil, nil
			}
			return bytes.TrimSpace(buf), nil
		default:
			return nil, err
		}
	}
}

// scanStreamJSON scans newline-delimited JSON events from Claude's stream-json output.
// It dispatches "assistant" text to onTurn, tool_use blocks to onToolUse,
// model/system events to onActivity, and returns the final "result" event.
func scanStreamJSON(r io.Reader, cb streamCallbacks) (*claudeResponse, error) {
	br := bufio.NewReaderSize(r, scannerBufInit)
	var result *claudeResponse
	var lastModel string
	for {
		// Peek at the first bytes to detect the event type without reading
		// the entire line. Tool results (screenshots) can be several MB —
		// we only need to fully read "assistant", "system", and "result" events.
		line, err := readLineOrSkip(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			return result, fmt.Errorf("reading container output: %w", err)
		}
		if len(line) == 0 {
			continue
		}

		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &typeCheck); err != nil {
			continue // skip non-JSON lines (e.g. ANSI noise)
		}

		switch typeCheck.Type {
		case "assistant":
			var msg assistantMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if msg.Message.Model != "" && msg.Message.Model != lastModel {
				lastModel = msg.Message.Model
				if cb.onActivity != nil {
					cb.onActivity("model", lastModel)
				}
			}
			if cb.onTurn != nil {
				if text := msg.extractText(); text != "" {
					cb.onTurn(text)
				}
			}
			if cb.onThinking != nil {
				if text := msg.extractThinking(); text != "" {
					cb.onThinking(text)
				}
			}
			if cb.onToolUse != nil {
				for _, tu := range msg.extractToolUses() {
					cb.onToolUse(tu.ID, tu.Name, tu.Input)
				}
			}
		case "user":
			if cb.onToolResult == nil {
				continue
			}
			var um userMessage
			if err := json.Unmarshal(line, &um); err != nil {
				continue
			}
			for _, blk := range um.Message.Content {
				if blk.Type != "tool_result" {
					continue
				}
				body := parseToolResultContent(blk.Content)
				out, _ := truncateInline(body, toolResultMaxInline)
				cb.onToolResult(blk.ToolUseID, out, blk.IsError)
			}
		case "system":
			if cb.onActivity != nil {
				var evt systemEvent
				if err := json.Unmarshal(line, &evt); err != nil {
					continue
				}
				switch evt.Subtype {
				case "task_started":
					cb.onActivity("subagent_started", evt.Description)
				case "task_progress":
					cb.onActivity("subagent_progress", evt.Description)
				case "status":
					cb.onActivity(evt.Status, evt.Description)
				}
			}
		case "result":
			var evt claudeResponse
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			result = &evt
		}
	}
	if result == nil {
		return nil, fmt.Errorf("parsing claude response: no result event found")
	}
	if lastModel != "" {
		result.Model = lastModel
	}
	return result, nil
}

// CreateShellContainer creates a long-lived shell container for terminal access.
// Unlike Run, the container runs "sleep infinity" instead of Claude CLI and is
// not auto-removed — it persists until explicitly stopped.
func (r *DockerRunner) CreateShellContainer(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error) {
	containerID, ctrName, _, _, err := r.createAndStartContainer(ctx, channelID, dirPath, "", parentDirPath, "",
		ContainerTypeShell,
		func(*config.Config, string) []string {
			return []string{"sleep", "infinity"}
		})
	if err == nil && containerID != "" && r.registry != nil {
		r.registry.Register(&ContainerInfo{
			ContainerID:   containerID,
			ChannelID:     channelID,
			Type:          ContainerTypeShell,
			ContainerName: ctrName,
		})
	}
	return containerID, err
}

// Cleanup removes containers created by this daemon instance.
// Each daemon stamps its containers with a unique loop-instance label,
// so cleanup only affects containers this process created — not containers
// managed by other daemons sharing the same Docker socket.
func (r *DockerRunner) Cleanup(ctx context.Context) error {
	containers, err := r.client.ContainerList(ctx, InstanceLabelKey, r.instanceID)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	var lastErr error
	for _, id := range containers {
		if r.registry != nil {
			if err := r.registry.RemoveContainer(ctx, id); err != nil {
				lastErr = fmt.Errorf("removing container %s: %w", id, err)
			}
		} else {
			if err := r.client.ContainerRemove(ctx, id); err != nil {
				lastErr = fmt.Errorf("removing container %s: %w", id, err)
			}
		}
	}
	return lastErr
}
