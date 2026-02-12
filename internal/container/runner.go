package container

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
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
	Type      string `json:"type"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

// ContainerConfig holds settings for creating a container.
type ContainerConfig struct {
	Image      string
	MemoryMB   int64
	CPUs       float64
	Env        []string
	Cmd        []string
	Binds      []string
	WorkingDir string
	GroupAdd   []string
}

// WaitResponse represents the result of waiting for a container to finish.
type WaitResponse struct {
	StatusCode int64
	Error      error
}

// DockerClient abstracts the Docker SDK methods used by DockerRunner.
type DockerClient interface {
	ContainerCreate(ctx context.Context, cfg *ContainerConfig, name string) (string, error)
	ContainerStart(ctx context.Context, containerID string) error
	ContainerLogs(ctx context.Context, containerID string) (io.Reader, error)
	ContainerWait(ctx context.Context, containerID string) (<-chan WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, containerID string) error
	ImageList(ctx context.Context, image string) ([]string, error)
	ImagePull(ctx context.Context, image string) error
	ImageBuild(ctx context.Context, contextDir, tag string) error
	ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error)
}

// Runner executes agent requests inside containers.
type Runner interface {
	Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error)
	Cleanup(ctx context.Context) error
}

// DockerRunner implements Runner using Docker containers.
type DockerRunner struct {
	client DockerClient
	cfg    *config.Config
}

// NewDockerRunner creates a new DockerRunner with the given Docker client and config.
func NewDockerRunner(client DockerClient, cfg *config.Config) *DockerRunner {
	return &DockerRunner{
		client: client,
		cfg:    cfg,
	}
}

const containerLabel = "loop-agent"

var mkdirAll = os.MkdirAll
var getenv = os.Getenv
var writeFile = os.WriteFile
var userHomeDir = os.UserHomeDir
var osStat = os.Stat
var execCommand = exec.Command
var timeAfterFunc = time.AfterFunc
var randRead = rand.Read
var readlink = os.Readlink
var readFile = os.ReadFile
var timeLocalName = func() string { return time.Now().Location().String() }

var nonAlphanumRegexp = regexp.MustCompile(`[^a-z0-9]+`)

// localTimezone returns the IANA timezone name (e.g. "Europe/Bucharest").
func localTimezone() string {
	if tz := getenv("TZ"); tz != "" && tz != "Local" {
		return tz
	}
	if loc := timeLocalName(); loc != "Local" {
		return loc
	}
	// Linux: /etc/timezone contains the IANA name directly.
	if data, err := readFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(data)); tz != "" {
			return tz
		}
	}
	// macOS/Linux: /etc/localtime is a symlink into the zoneinfo directory.
	if target, err := readlink("/etc/localtime"); err == nil {
		if idx := strings.Index(target, "zoneinfo/"); idx != -1 {
			return target[idx+len("zoneinfo/"):]
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
func containerName(channelID, dirPath string) string {
	base := channelID
	if dirPath != "" {
		base = filepath.Base(dirPath)
	}
	sanitized := sanitizeName(base)
	b := make([]byte, 3)
	_, _ = randRead(b)
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

	// When a forked session is too long, compact it and retry.
	if req.ForkSession && resp != nil && resp.SessionID != "" && strings.Contains(err.Error(), "Prompt is too long") {
		compactReq := &agent.AgentRequest{
			SessionID: resp.SessionID,
			ChannelID: req.ChannelID,
			DirPath:   req.DirPath,
			Prompt:    "/compact",
		}
		compactResp, compactErr := r.runOnce(ctx, compactReq)
		if compactErr != nil {
			return nil, fmt.Errorf("compacting forked session: %w", compactErr)
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
func buildMCPConfig(channelID, apiURL, workDir string, userServers map[string]config.MCPServerConfig) mcpConfig {
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
		servers["loop"] = mcpServerEntry{
			Command: "/usr/local/bin/loop",
			Args:    []string{"mcp", "--channel-id", channelID, "--api-url", apiURL, "--log", filepath.Join(workDir, ".loop", "mcp.log")},
		}
	}
	return mcpConfig{MCPServers: servers}
}

// expandPath expands ~ in paths to the user's home directory.
func expandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[2:]), nil
}

// processMount processes a single mount specification and returns the expanded bind string.
// Returns empty string if the mount should be skipped.
func processMount(mount string) (string, error) {
	parts := strings.Split(mount, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid mount format: %s", mount)
	}

	hostPath := parts[0]

	// Docker named volumes (e.g. "gomodcache:/go/pkg/mod") are passed through
	// without host path expansion or existence checks — Docker manages them.
	// The container path still needs ~ expansion since Docker requires absolute paths.
	if config.IsNamedVolume(hostPath) {
		containerPath, err := expandPath(parts[1])
		if err != nil {
			return "", fmt.Errorf("expanding container path %s: %w", parts[1], err)
		}
		mode := ""
		if len(parts) > 2 {
			mode = ":" + parts[2]
		}
		return hostPath + ":" + containerPath + mode, nil
	}

	expanded, err := expandPath(hostPath)
	if err != nil {
		return "", fmt.Errorf("expanding path %s: %w", hostPath, err)
	}

	// Check if path exists
	if _, err := osStat(expanded); os.IsNotExist(err) {
		// Skip non-existent paths silently
		return "", nil
	}

	// Expand ~ in the container path too — container HOME matches host HOME.
	containerPath, err := expandPath(parts[1])
	if err != nil {
		return "", fmt.Errorf("expanding container path %s: %w", parts[1], err)
	}
	mode := ""
	if len(parts) > 2 {
		mode = ":" + parts[2]
	}
	return expanded + ":" + containerPath + mode, nil
}

// gitExcludesMount detects the host git core.excludesFile and returns a bind
// mount string so the file is available inside the container at the path git
// will look for it. Returns "" if unconfigured or the file doesn't exist.
func gitExcludesMount() string {
	out, err := execCommand("git", "config", "--global", "--get", "core.excludesFile").Output()
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
		home, err := userHomeDir()
		if err != nil {
			return ""
		}
		hostPath = filepath.Join(home, hostPath[2:])
	}

	// Check if the file exists on the host
	if _, err := osStat(hostPath); err != nil {
		return ""
	}

	// Container HOME matches host HOME, so the expanded host path works in both.
	return hostPath + ":" + hostPath + ":ro"
}

// runOnce executes a single container run.
func (r *DockerRunner) runOnce(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	// When resuming a session, Claude already has the conversation history —
	// only send the latest message to avoid a huge redundant prompt.
	// Prefer the explicit Prompt field (set by the orchestrator from the
	// triggering message) so that rapid successive messages don't race.
	var prompt string
	switch {
	case req.SessionID != "" && req.Prompt != "":
		prompt = req.Prompt
	case req.SessionID != "" && len(req.Messages) > 0:
		last := req.Messages[len(req.Messages)-1]
		prompt = last.Content
	default:
		prompt = agent.BuildPrompt(req.Messages, req.SystemPrompt)
	}

	workDir := filepath.Join(r.cfg.LoopDir, req.ChannelID, "work")
	if req.DirPath != "" {
		workDir = req.DirPath
	}

	// Load and merge project-specific config if it exists
	cfg, err := config.LoadProjectConfig(workDir, r.cfg)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	hostHome, err := userHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	apiURL := "http://host.docker.internal" + cfg.APIAddr
	env := []string{
		"CHANNEL_ID=" + req.ChannelID,
		"API_URL=" + apiURL,
		"HOME=" + hostHome,
		"HOST_USER=" + getenv("USER"),
		"TZ=" + localTimezone(),
	}
	if cfg.ClaudeCodeOAuthToken != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+cfg.ClaudeCodeOAuthToken)
	}
	hasProxy := false
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if v := getenv(key); v != "" {
			env = append(env, key+"="+localhostToDockerHost(v))
			if key != "NO_PROXY" && key != "no_proxy" {
				hasProxy = true
			}
		}
	}
	if hasProxy {
		env = ensureNoProxy(env)
	}
	for k, v := range cfg.Envs {
		expanded, err := expandPath(v)
		if err != nil {
			return nil, fmt.Errorf("expanding env %s value: %w", k, err)
		}
		env = append(env, k+"="+expanded)
	}

	for _, dir := range []string{workDir, filepath.Join(workDir, ".loop")} {
		if err := mkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating host directory %s: %w", dir, err)
		}
	}

	// Write .loop/mcp.json for Claude's --mcp-config (always regenerated from merged config).
	mcpConfigPath := filepath.Join(workDir, ".loop", "mcp.json")
	mcpCfg := buildMCPConfig(req.ChannelID, apiURL, workDir, cfg.MCPServers)
	mcpJSON, _ := json.MarshalIndent(mcpCfg, "", "  ")
	if err := writeFile(mcpConfigPath, mcpJSON, 0o644); err != nil {
		return nil, fmt.Errorf("writing mcp config: %w", err)
	}

	var binds []string
	var chownDirs []string

	// Add mounts from merged config (includes project-specific mounts)
	for _, mount := range cfg.Mounts {
		parts := strings.Split(mount, ":")
		if len(parts) >= 2 && config.IsNamedVolume(parts[0]) {
			// Track named volume container paths for chown in entrypoint
			expanded, _ := expandPath(parts[1])
			if expanded != "" {
				chownDirs = append(chownDirs, expanded)
			}
		}
		bind, err := processMount(mount)
		if err != nil {
			// Log warning but continue (don't fail the container)
			fmt.Fprintf(os.Stderr, "Warning: skipping mount %s: %v\n", mount, err)
			continue
		}
		if bind != "" {
			binds = append(binds, bind)
		}
	}
	if len(chownDirs) > 0 {
		env = append(env, "CHOWN_DIRS="+strings.Join(chownDirs, ":"))
	}

	// Auto-detect and mount git core.excludesFile if configured
	if excludesBind := gitExcludesMount(); excludesBind != "" {
		binds = append(binds, excludesBind)
	}

	// Mount workDir at same path in container
	binds = append(binds, workDir+":"+workDir)

	// Build full Claude CLI command — entrypoint runs whatever CMD is passed.
	// --mcp-config must come before --print to avoid Claude Code hanging.
	cmd := []string{cfg.ClaudeBinPath, "--mcp-config", mcpConfigPath}
	if cfg.ClaudeModel != "" {
		cmd = append(cmd, "--model", cfg.ClaudeModel)
	}
	cmd = append(cmd, "--print", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions")
	if req.SessionID != "" {
		cmd = append(cmd, "--resume", req.SessionID)
		if req.ForkSession {
			cmd = append(cmd, "--fork-session")
		}
	}
	cmd = append(cmd, prompt)

	containerCfg := &ContainerConfig{
		Image:      cfg.ContainerImage,
		MemoryMB:   cfg.ContainerMemoryMB,
		CPUs:       cfg.ContainerCPUs,
		Env:        env,
		Cmd:        cmd,
		Binds:      binds,
		WorkingDir: workDir,
	}

	name := containerName(req.ChannelID, req.DirPath)
	containerID, err := r.client.ContainerCreate(ctx, containerCfg, name)
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}
	defer r.scheduleRemove(containerID)

	if err := r.client.ContainerStart(ctx, containerID); err != nil {
		return nil, fmt.Errorf("starting container: %w", err)
	}

	waitCh, errCh := r.client.ContainerWait(ctx, containerID)

	var exitCode int64
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("container execution timed out: %w", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("waiting for container: %w", err)
		}
	case wr := <-waitCh:
		if wr.Error != nil {
			return nil, fmt.Errorf("container exited with error: %w", wr.Error)
		}
		exitCode = wr.StatusCode
	}

	reader, err := r.client.ContainerLogs(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("reading container logs: %w", err)
	}

	claudeResp, err := parseStreamJSON(reader)
	if err != nil {
		if exitCode != 0 {
			return nil, fmt.Errorf("container exited with code %d: %w", exitCode, err)
		}
		return nil, err
	}

	if claudeResp.IsError {
		return &agent.AgentResponse{
			SessionID: claudeResp.SessionID,
			Error:     claudeResp.Result,
		}, fmt.Errorf("claude returned error: %s", claudeResp.Result)
	}

	return &agent.AgentResponse{
		Response:  claudeResp.Result,
		SessionID: claudeResp.SessionID,
	}, nil
}

// ensureNoProxy ensures host.docker.internal is in NO_PROXY and no_proxy
// so the container's API calls bypass the proxy.
func ensureNoProxy(env []string) []string {
	const host = "host.docker.internal"
	found := false
	for i, e := range env {
		for _, key := range []string{"NO_PROXY=", "no_proxy="} {
			if strings.HasPrefix(e, key) {
				found = true
				val := strings.TrimPrefix(e, key)
				if !strings.Contains(val, host) {
					if val != "" {
						val += ","
					}
					val += host
					env[i] = key + val
				}
			}
		}
	}
	if !found {
		env = append(env, "NO_PROXY="+host, "no_proxy="+host)
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

// parseStreamJSON scans newline-delimited JSON events from Claude's
// --output-format stream-json and returns the final "result" event.
func parseStreamJSON(r io.Reader) (*claudeResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // allow lines up to 1MB
	var result *claudeResponse
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt claudeResponse
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue // skip non-JSON lines (e.g. ANSI noise)
		}
		if evt.Type == "result" {
			result = &evt
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading container output: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("parsing claude response: no result event found")
	}
	return result, nil
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
	timeAfterFunc(r.cfg.ContainerKeepAlive, func() {
		_ = r.client.ContainerRemove(context.Background(), containerID)
	})
}
