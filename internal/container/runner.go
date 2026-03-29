package container

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/osutil"
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

// assistantMessage represents an "assistant" event from Claude's stream-json output.
// Each assistant turn contains a message with content blocks.
type assistantMessage struct {
	Type    string `json:"type"`
	Message struct {
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
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

// ToolUse represents a tool invocation extracted from an assistant message.
type ToolUse struct {
	Name  string
	Input string // short summary of the input
}

// extractToolUses returns tool_use content blocks from an assistant message.
func (m *assistantMessage) extractToolUses() []ToolUse {
	var tools []ToolUse
	for _, c := range m.Message.Content {
		if c.Type == "tool_use" && c.Name != "" {
			summary := summarizeToolInput(c.Name, c.Input)
			tools = append(tools, ToolUse{Name: c.Name, Input: summary})
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
	case "AskUserQuestion", "ExitPlanMode":
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
	CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error
	RunningChannelIDs(ctx context.Context) (map[string]struct{}, error)
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
	ExecCommandOutput(name string, args ...string) ([]byte, error)
}

type DockerRunner struct {
	client                    DockerClient
	cfg                       *config.Config
	sys                       runnerSystem
	loadProjectConfig         func(string, *config.Config) (*config.Config, error)
	loadWorktreeProjectConfig func(string, string, *config.Config) (*config.Config, error)
	osTimeAfterFunc           func(time.Duration, func()) *time.Timer
	osRandRead                func([]byte) (int, error)
	osTimeLocalName           func() string
}

// NewDockerRunner creates a new DockerRunner with the given Docker client and config.
func NewDockerRunner(client DockerClient, cfg *config.Config) *DockerRunner {
	return &DockerRunner{
		client:                    client,
		cfg:                       cfg,
		sys:                       osutil.RealSystem{},
		loadProjectConfig:         config.LoadProjectConfig,
		loadWorktreeProjectConfig: config.LoadWorktreeProjectConfig,
		osTimeAfterFunc:           time.AfterFunc,
		osRandRead:                rand.Read,
		osTimeLocalName:           func() string { return time.Now().Location().String() },
	}
}

const (
	containerLabel = "loop-agent"
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

// sanitizeName lowercases the input, replaces non-alphanumeric chars with
// hyphens, collapses consecutive hyphens, trims leading/trailing hyphens,
// and truncates to 40 characters.
func sanitizeName(name string) string {
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
	sanitized := sanitizeName(base)
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
	containerID, mcpConfigPath, keepMCP, err := r.createAndStartContainer(ctx, req.ChannelID, req.DirPath, req.AuthorID, req.ParentDirPath, req.AgentID,
		func(cfg *config.Config, mcpConfigPath string) []string {
			return buildClaudeCmd(cfg, mcpConfigPath, req)
		},
	)
	if mcpConfigPath != "" && !keepMCP {
		defer func() { _ = r.sys.Remove(mcpConfigPath) }()
	}
	if containerID != "" {
		defer r.scheduleRemove(containerID)
	}
	if err != nil {
		return nil, err
	}

	claudeResp, err := r.collectOutput(ctx, containerID, streamCallbacks{
		onTurn:     req.OnTurn,
		onToolUse:  req.OnToolUse,
		onActivity: req.OnActivity,
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
		"TZ=" + r.localTimezone(),
		"PATH=" + hostHome + "/.local/bin:" + hostHome + "/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
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
		binds = append(binds, parentDirPath+":"+parentDirPath)
	} else {
		binds = append(binds, workDir+":"+workDir)
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
		if dir == workDir || dir == parentDirPath || mounted[dir] {
			continue
		}
		if parentDirPath != "" && strings.HasPrefix(dir, parentDirPath+"/") {
			continue
		}
		binds = append(binds, dir+":"+dir)
	}

	return binds, chownPaths
}

// buildBaseClaudeCmd returns the common Claude CLI flags shared by both
// batch and interactive modes.
func buildBaseClaudeCmd(cfg *config.Config, mcpConfigPath, sessionID, agentID string, forkSession, planMode bool, extraDirs []string) []string {
	cmd := []string{cfg.ClaudeBinPath, "--mcp-config", mcpConfigPath}
	if cfg.ClaudeModel != "" {
		cmd = append(cmd, "--model", cfg.ClaudeModel)
	}
	if planMode {
		cmd = append(cmd, "--permission-mode", "plan")
	} else {
		cmd = append(cmd, "--dangerously-skip-permissions")
	}
	if sessionID != "" {
		cmd = append(cmd, "--resume", sessionID)
		if forkSession {
			cmd = append(cmd, "--fork-session")
		}
	}
	// Enable MCP Channels when agent tools are configured, so the agent
	// can receive push notifications from other agents.
	if agentID != "" {
		cmd = append(cmd, "--dangerously-load-development-channels", "server:loop")
	}
	for _, dir := range extraDirs {
		cmd = append(cmd, "--add-dir", dir)
	}
	return cmd
}

// buildClaudeCmd assembles the Claude CLI command with all flags for batch mode.
func buildClaudeCmd(cfg *config.Config, mcpConfigPath string, req *agent.AgentRequest) []string {
	cmd := buildBaseClaudeCmd(cfg, mcpConfigPath, req.SessionID, req.AgentID, req.ForkSession, req.PlanMode, cfg.ExtraDirs)
	cmd = append(cmd, "--print", "--verbose", "--output-format", "stream-json")
	if req.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", req.SystemPrompt)
	}
	return append(cmd, req.BuildPrompt())
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
func BuildInteractiveClaudeCmd(cfg *config.Config, channelID, workDir, sessionID, agentID string, forkSession bool) string {
	mcpConfigPath := mcpConfigPathForAgent(workDir, channelID, agentID)
	return strings.Join(buildBaseClaudeCmd(cfg, mcpConfigPath, sessionID, agentID, forkSession, false, cfg.ExtraDirs), " ")
}

// ClaudeCmdBuilder builds the interactive Claude command for terminal sessions.
// It implements api.InteractiveCmdBuilder.
type ClaudeCmdBuilder struct {
	cfg               *config.Config
	loadProjectConfig func(string, *config.Config) (*config.Config, error)
	writeFile         func(string, []byte, os.FileMode) error
	mkdirAll          func(string, os.FileMode) error
}

// NewClaudeCmdBuilder creates a builder that uses the given config.
func NewClaudeCmdBuilder(cfg *config.Config) *ClaudeCmdBuilder {
	return &ClaudeCmdBuilder{
		cfg:               cfg,
		loadProjectConfig: config.LoadProjectConfig,
		writeFile:         os.WriteFile,
		mkdirAll:          os.MkdirAll,
	}
}

// BuildInteractiveCmd returns the interactive Claude shell command for the given channel.
// It loads the project-specific config (if any) to apply per-project overrides
// such as claude_model before building the command.
// When agentID is set, a per-agent MCP config is written with --agent-id so
// the agent can identify itself via the MCP tools.
func (b *ClaudeCmdBuilder) BuildInteractiveCmd(channelID, dirPath, sessionID, agentID string, forkSession bool) string {
	workDir := dirPath
	if workDir == "" {
		workDir = filepath.Join(b.cfg.LoopDir, channelID, "work")
	}
	cfg := b.cfg
	if merged, err := b.loadProjectConfig(workDir, b.cfg); err == nil {
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
	buildCmd func(cfg *config.Config, mcpConfigPath string) []string,
) (containerID, mcpConfigPath string, keepMCPConfig bool, err error) {
	workDir := filepath.Join(r.cfg.LoopDir, channelID, "work")
	if dirPath != "" {
		workDir = dirPath
	}

	var cfg *config.Config
	if parentDirPath != "" {
		cfg, err = r.loadWorktreeProjectConfig(workDir, parentDirPath, r.cfg)
	} else {
		cfg, err = r.loadProjectConfig(workDir, r.cfg)
	}
	if err != nil {
		return "", "", false, fmt.Errorf("loading project config: %w", err)
	}
	keepMCPConfig = cfg.KeepMCPConfigs

	apiURL := "http://host.docker.internal" + cfg.APIAddr

	env, err := r.buildContainerEnv(cfg, channelID, apiURL)
	if err != nil {
		return "", "", false, err
	}

	binds, chownPaths := r.buildContainerMounts(cfg.Mounts, workDir, parentDirPath, cfg.ExtraDirs)
	for _, f := range r.filterMountedCopyFiles(cfg.CopyFiles, binds) {
		if expanded, err := r.expandPath(f); err == nil {
			chownPaths = append(chownPaths, expanded)
		}
	}

	// Bind-mount the screenshot directory so the MCP server can read files written by the host.
	screenshotDir := filepath.Join(r.cfg.LoopDir, "screenshots")
	binds = append(binds, screenshotDir+":"+screenshotDir+":ro")

	// Bind-mount the playground directory so agents can read/write playground files.
	playgroundDir := filepath.Join(r.cfg.LoopDir, "playground")
	binds = append(binds, playgroundDir+":"+playgroundDir)

	if len(chownPaths) > 0 {
		env = append(env, "CHOWN_PATHS="+strings.Join(chownPaths, ":"))
	}

	// Ensure workDir exists on host (it's bind-mounted into the container).
	if err := r.sys.MkdirAll(workDir, 0o755); err != nil {
		return "", "", false, fmt.Errorf("creating work dir: %w", err)
	}

	// Initialize git in auto-created work directories so the agent can use version control.
	if dirPath == "" {
		_, _ = r.sys.ExecCommandOutput("git", "init", workDir)
	}

	mcpConfigPath, err = r.writeMCPConfig(workDir, channelID, apiURL, authorID, agentID, cfg)
	if err != nil {
		return "", "", false, err
	}

	cmd := buildCmd(cfg, mcpConfigPath)

	containerCfg := &ContainerConfig{
		Image:      cfg.ContainerImage,
		MemoryMB:   cfg.ContainerMemoryMB,
		CPUs:       cfg.ContainerCPUs,
		Env:        env,
		Cmd:        cmd,
		Binds:      binds,
		WorkingDir: workDir,
		Labels:     map[string]string{channelLabelKey: channelID},
	}

	name := r.containerName(channelID, dirPath)
	containerID, err = r.client.ContainerCreate(ctx, containerCfg, name)
	if err != nil {
		return "", mcpConfigPath, keepMCPConfig, fmt.Errorf("creating container: %w", err)
	}

	if err := r.copyFiles(ctx, containerID, r.filterMountedCopyFiles(cfg.CopyFiles, binds)); err != nil {
		return containerID, mcpConfigPath, keepMCPConfig, fmt.Errorf("copying files: %w", err)
	}

	if err := r.client.ContainerStart(ctx, containerID); err != nil {
		return containerID, mcpConfigPath, keepMCPConfig, fmt.Errorf("starting container: %w", err)
	}

	return containerID, mcpConfigPath, keepMCPConfig, nil
}

// collectOutput reads container logs (streaming or batch) and waits for exit.
// Returns the parsed Claude response or an error.
func (r *DockerRunner) collectOutput(ctx context.Context, containerID string, cb streamCallbacks) (*claudeResponse, error) {
	if cb.onTurn != nil {
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
	hosts := append([]string{"host.docker.internal"}, extraHosts...)
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
	onTurn     func(string)
	onToolUse  func(name, input string)
	onActivity func(activity, detail string)
}

// readLineOrSkip reads a line from the buffered reader. If the line starts
// with a "user" type JSON event (tool results, which can be several MB for
// screenshots), it skips the rest of the line without buffering it.
// Returns the full line for events we care about, or nil for skipped lines.
func readLineOrSkip(br *bufio.Reader) ([]byte, error) {
	// Peek at the first bytes to detect the event type without reading
	// the entire line. Tool results (screenshots) can be several MB.
	peek, peekErr := br.Peek(30)
	if len(peek) == 0 && peekErr != nil {
		return nil, peekErr // EOF or real error
	}

	// Check if this is a "user" event (tool results) — skip without reading fully.
	if strings.Contains(string(peek), `"type":"user"`) {
		// Discard the entire line without buffering it.
		_, _ = br.ReadBytes('\n')
		return nil, nil
	}

	// Read the full line for events we care about.
	// ReadBytes may return data with EOF (last line without \n) — that's fine.
	line, _ := br.ReadBytes('\n')
	return bytes.TrimSpace(line), nil
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
			if cb.onToolUse != nil {
				for _, tu := range msg.extractToolUses() {
					cb.onToolUse(tu.Name, tu.Input)
				}
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
func (r *DockerRunner) CreateShellContainer(ctx context.Context, channelID, dirPath string) (string, error) {
	containerID, _, _, err := r.createAndStartContainer(ctx, channelID, dirPath, "", "", "", func(*config.Config, string) []string {
		return []string{"sleep", "infinity"}
	})
	return containerID, err
}

// Cleanup removes any lingering containers with the loop-agent label.
func (r *DockerRunner) Cleanup(ctx context.Context) error {
	containers, err := r.client.ContainerList(ctx, "app", containerLabel)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	var lastErr error
	for _, id := range containers {
		if err := r.client.ContainerRemove(ctx, id); err != nil {
			lastErr = fmt.Errorf("removing container %s: %w", id, err)
		}
	}
	return lastErr
}

// scheduleRemove removes a container after a delay so that `docker logs`
// remains available for debugging shortly after the run completes.
func (r *DockerRunner) scheduleRemove(containerID string) {
	r.osTimeAfterFunc(r.cfg.ContainerKeepAlive, func() {
		_ = r.client.ContainerRemove(context.Background(), containerID)
	})
}
