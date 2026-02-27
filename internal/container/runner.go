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

// assistantMessage represents an "assistant" event from Claude's stream-json output.
// Each assistant turn contains a message with content blocks.
type assistantMessage struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
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
	ContainerLogsFollow(ctx context.Context, containerID string) (io.ReadCloser, error)
	ContainerWait(ctx context.Context, containerID string) (<-chan WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, containerID string) error
	ImageList(ctx context.Context, image string) ([]string, error)
	ImagePull(ctx context.Context, image string) error
	ImageBuild(ctx context.Context, contextDir, tag string) error
	ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error)
	CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error
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

const (
	containerLabel    = "loop-agent"
	scannerBufInit    = 64 * 1024   // initial scanner buffer capacity
	scannerBufMaxLine = 1024 * 1024 // max line size (1 MB)
)

var osMkdirAll = os.MkdirAll
var osGetenv = os.Getenv
var osWriteFile = os.WriteFile
var osUserHomeDir = os.UserHomeDir
var osStat = os.Stat
var osExecCommand = exec.Command
var osTimeAfterFunc = time.AfterFunc
var osRandRead = rand.Read
var osReadlink = os.Readlink
var osReadFile = os.ReadFile
var osTimeLocalName = func() string { return time.Now().Location().String() }

var nonAlphanumRegexp = regexp.MustCompile(`[^a-z0-9]+`)

// localTimezone returns the IANA timezone name (e.g. "Europe/Bucharest").
func localTimezone() string {
	if tz := osGetenv("TZ"); tz != "" && tz != "Local" {
		return tz
	}
	if loc := osTimeLocalName(); loc != "Local" {
		return loc
	}
	// Linux: /etc/timezone contains the IANA name directly.
	if data, err := osReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(data)); tz != "" {
			return tz
		}
	}
	// macOS/Linux: /etc/localtime is a symlink into the zoneinfo directory.
	if target, err := osReadlink("/etc/localtime"); err == nil {
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
func containerName(channelID, dirPath string) string {
	base := channelID
	if dirPath != "" {
		base = filepath.Base(dirPath)
	}
	sanitized := sanitizeName(base)
	b := make([]byte, 3)
	_, _ = osRandRead(b)
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
func buildMCPConfig(channelID, apiURL, workDir, authorID string, memoryEnabled bool, userServers map[string]config.MCPServerConfig) mcpConfig {
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
		servers["loop"] = mcpServerEntry{
			Command: "/usr/local/bin/loop",
			Args:    args,
		}
	}
	return mcpConfig{MCPServers: servers}
}

// expandPath expands ~ in paths to the user's home directory.
func expandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := osUserHomeDir()
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
	out, err := osExecCommand("git", "config", "--global", "--get", "core.excludesFile").Output()
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
		home, err := osUserHomeDir()
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
	workDir := filepath.Join(r.cfg.LoopDir, req.ChannelID, "work")
	if req.DirPath != "" {
		workDir = req.DirPath
	}

	cfg, err := config.LoadProjectConfig(workDir, r.cfg)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	apiURL := "http://host.docker.internal" + cfg.APIAddr

	env, err := r.buildContainerEnv(cfg, req.ChannelID, apiURL)
	if err != nil {
		return nil, err
	}

	mcpConfigPath, err := r.writeMCPConfig(workDir, req.ChannelID, apiURL, req.AuthorID, cfg)
	if err != nil {
		return nil, err
	}

	binds, chownPaths := r.buildContainerMounts(cfg.Mounts, workDir)
	// Include copied files for CopyToContainer ownership fix.
	// Skip files already bind-mounted (they won't be copied).
	for _, f := range filterMountedCopyFiles(cfg.CopyFiles, binds) {
		if expanded, err := expandPath(f); err == nil {
			chownPaths = append(chownPaths, expanded)
		}
	}
	if len(chownPaths) > 0 {
		env = append(env, "CHOWN_PATHS="+strings.Join(chownPaths, ":"))
	}

	cmd := buildClaudeCmd(cfg, mcpConfigPath, req)

	containerID, err := r.createAndStartContainer(ctx, cfg, env, cmd, binds, workDir, req.ChannelID, req.DirPath)
	if containerID != "" {
		defer r.scheduleRemove(containerID)
	}
	if err != nil {
		return nil, err
	}

	claudeResp, err := r.collectOutput(ctx, containerID, req.OnTurn)
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
		Response:  claudeResp.Result,
		SessionID: claudeResp.SessionID,
	}, nil
}

// buildContainerEnv assembles environment variables for the container,
// including auth credentials, proxy settings, timezone, and custom envs.
func (r *DockerRunner) buildContainerEnv(cfg *config.Config, channelID, apiURL string) ([]string, error) {
	hostHome, err := osUserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	env := []string{
		"CHANNEL_ID=" + channelID,
		"API_URL=" + apiURL,
		"HOME=" + hostHome,
		"HOST_USER=" + osGetenv("USER"),
		"TZ=" + localTimezone(),
	}
	env = addAuthEnv(env, cfg)
	env = addProxyEnv(env)

	for k, v := range cfg.Envs {
		expanded, err := expandPath(v)
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
func addProxyEnv(env []string) []string {
	hasProxy := false
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if v := osGetenv(key); v != "" {
			env = append(env, key+"="+localhostToDockerHost(v))
			if key != "NO_PROXY" && key != "no_proxy" {
				hasProxy = true
			}
		}
	}
	if hasProxy {
		env = ensureNoProxy(env)
	}
	return env
}

// writeMCPConfig creates host directories and writes the per-channel MCP
// config file. Returns the config file path.
func (r *DockerRunner) writeMCPConfig(workDir, channelID, apiURL, authorID string, cfg *config.Config) (string, error) {
	for _, dir := range []string{workDir, filepath.Join(workDir, ".loop")} {
		if err := osMkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating host directory %s: %w", dir, err)
		}
	}

	mcpConfigPath := filepath.Join(workDir, ".loop", "mcp-"+channelID+".json")
	mcpCfg := buildMCPConfig(channelID, apiURL, workDir, authorID, cfg.Memory.Enabled, cfg.MCPServers)
	mcpJSON, _ := json.MarshalIndent(mcpCfg, "", "  ")
	if err := osWriteFile(mcpConfigPath, mcpJSON, 0o644); err != nil {
		return "", fmt.Errorf("writing mcp config: %w", err)
	}
	return mcpConfigPath, nil
}

// buildContainerMounts processes config mounts and adds the workDir bind.
// Returns the bind strings and any named-volume container paths that need chown.
func (r *DockerRunner) buildContainerMounts(mounts []string, workDir string) (binds, chownPaths []string) {
	for _, mount := range mounts {
		parts := strings.Split(mount, ":")
		if len(parts) >= 2 && config.IsNamedVolume(parts[0]) {
			expanded, _ := expandPath(parts[1])
			if expanded != "" {
				chownPaths = append(chownPaths, expanded)
			}
		}
		bind, err := processMount(mount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping mount %s: %v\n", mount, err)
			continue
		}
		if bind != "" {
			binds = append(binds, bind)
		}
	}

	if excludesBind := gitExcludesMount(); excludesBind != "" {
		binds = append(binds, excludesBind)
	}

	binds = append(binds, workDir+":"+workDir)
	return binds, chownPaths
}

// buildClaudeCmd assembles the Claude CLI command with all flags.
func buildClaudeCmd(cfg *config.Config, mcpConfigPath string, req *agent.AgentRequest) []string {
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
	if req.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", req.SystemPrompt)
	}
	return append(cmd, req.BuildPrompt())
}

// filterMountedCopyFiles removes entries from copyFiles whose expanded paths
// are already bind-mounted into the container, avoiding "device or resource busy"
// errors from CopyToContainer.
func filterMountedCopyFiles(copyFiles, binds []string) []string {
	// Build set of bind-mounted container paths.
	mounted := make(map[string]struct{}, len(binds))
	for _, b := range binds {
		parts := strings.Split(b, ":")
		if len(parts) >= 2 {
			mounted[parts[1]] = struct{}{}
		}
	}

	var filtered []string
	for _, f := range copyFiles {
		expanded, err := expandPath(f)
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
		expanded, err := expandPath(f)
		if err != nil {
			return fmt.Errorf("expanding path %s: %w", f, err)
		}

		data, err := osReadFile(expanded)
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

// createAndStartContainer creates a Docker container and starts it.
func (r *DockerRunner) createAndStartContainer(ctx context.Context, cfg *config.Config, env, cmd, binds []string, workDir, channelID, dirPath string) (string, error) {
	containerCfg := &ContainerConfig{
		Image:      cfg.ContainerImage,
		MemoryMB:   cfg.ContainerMemoryMB,
		CPUs:       cfg.ContainerCPUs,
		Env:        env,
		Cmd:        cmd,
		Binds:      binds,
		WorkingDir: workDir,
	}

	name := containerName(channelID, dirPath)
	containerID, err := r.client.ContainerCreate(ctx, containerCfg, name)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	if err := r.copyFiles(ctx, containerID, filterMountedCopyFiles(cfg.CopyFiles, binds)); err != nil {
		return containerID, fmt.Errorf("copying files: %w", err)
	}

	if err := r.client.ContainerStart(ctx, containerID); err != nil {
		return containerID, fmt.Errorf("starting container: %w", err)
	}

	return containerID, nil
}

// collectOutput reads container logs (streaming or batch) and waits for exit.
// Returns the parsed Claude response or an error.
func (r *DockerRunner) collectOutput(ctx context.Context, containerID string, onTurn func(string)) (*claudeResponse, error) {
	if onTurn != nil {
		return r.collectStreamingOutput(ctx, containerID, onTurn)
	}
	return r.collectBatchOutput(ctx, containerID)
}

// collectStreamingOutput follows container logs in real-time, then waits for exit.
func (r *DockerRunner) collectStreamingOutput(ctx context.Context, containerID string, onTurn func(string)) (*claudeResponse, error) {
	logsReader, err := r.client.ContainerLogsFollow(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("following container logs: %w", err)
	}

	claudeResp, parseErr := parseStreamingJSON(logsReader, onTurn)
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

	claudeResp, parseErr := parseStreamJSON(reader)
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
	return scanStreamJSON(r, nil)
}

// parseStreamingJSON scans newline-delimited JSON events from Claude's
// streaming output, calling onTurn for each assistant turn's text content.
// Returns the final "result" event.
func parseStreamingJSON(r io.Reader, onTurn func(string)) (*claudeResponse, error) {
	return scanStreamJSON(r, onTurn)
}

// scanStreamJSON is the shared scanner for Claude's stream-json output.
// It scans newline-delimited JSON events, dispatches "assistant" events
// to onTurn (when non-nil), and returns the final "result" event.
func scanStreamJSON(r io.Reader, onTurn func(string)) (*claudeResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scannerBufInit), scannerBufMaxLine)
	var result *claudeResponse
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &typeCheck); err != nil {
			continue // skip non-JSON lines (e.g. ANSI noise)
		}

		switch typeCheck.Type {
		case "assistant":
			if onTurn != nil {
				var msg assistantMessage
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					continue
				}
				if text := msg.extractText(); text != "" {
					onTurn(text)
				}
			}
		case "result":
			var evt claudeResponse
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}
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
	osTimeAfterFunc(r.cfg.ContainerKeepAlive, func() {
		_ = r.client.ContainerRemove(context.Background(), containerID)
	})
}
