package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

// mcpConfig represents the .mcp.json structure written to the container work directory.
type mcpConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// mcpServerEntry represents a single MCP server in .mcp.json.
type mcpServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// claudeResponse represents the JSON output from claude --output-format json.
type claudeResponse struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

var ansiRegexp = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z<>]|\][^\x07]*(?:\x07|\x1b\\)|\(B)|\r`)

// ContainerConfig holds settings for creating a container.
type ContainerConfig struct {
	Image    string
	MemoryMB int64
	CPUs     float64
	Env      []string
	Cmd      []string
	Binds    []string
}

// WaitResponse represents the result of waiting for a container to finish.
type WaitResponse struct {
	StatusCode int64
	Error      error
}

// DockerClient abstracts the Docker SDK methods used by DockerRunner.
type DockerClient interface {
	ContainerCreate(ctx context.Context, cfg *ContainerConfig, name string) (string, error)
	ContainerAttach(ctx context.Context, containerID string) (io.Reader, error)
	ContainerStart(ctx context.Context, containerID string) error
	ContainerWait(ctx context.Context, containerID string) (<-chan WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, containerID string) error
	ImageList(ctx context.Context, image string) ([]string, error)
	ImagePull(ctx context.Context, image string) error
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
const sessionVolume = "loop-sessions:/home/agent/.claude"

var mkdirAll = os.MkdirAll
var getenv = os.Getenv
var writeFile = os.WriteFile

// Run executes an agent request in a Docker container.
// If a session ID is set and the run fails, it retries without --resume
// (stale session IDs cause Claude to output non-JSON errors).
func (r *DockerRunner) Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	resp, err := r.runOnce(ctx, req)
	if err == nil || req.SessionID == "" {
		return resp, err
	}

	retryReq := *req
	retryReq.SessionID = ""
	retryResp, retryErr := r.runOnce(ctx, &retryReq)
	if retryErr != nil {
		return nil, err
	}
	return retryResp, nil
}

// buildMCPConfig creates the merged MCP config with the built-in loop-scheduler
// and any user-defined servers from the config. The built-in loop-scheduler always
// takes precedence over a user-defined server with the same name.
func buildMCPConfig(channelID, apiURL string, userServers map[string]config.MCPServerConfig) mcpConfig {
	servers := make(map[string]mcpServerEntry, len(userServers)+1)
	for name, srv := range userServers {
		servers[name] = mcpServerEntry{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
		}
	}
	// Built-in loop-scheduler always overrides any user-defined server with that name.
	servers["loop-scheduler"] = mcpServerEntry{
		Command: "/usr/local/bin/loop",
		Args:    []string{"mcp", "--channel-id", channelID, "--api-url", apiURL, "--log", "/mcp/mcp.log"},
	}
	return mcpConfig{MCPServers: servers}
}

// runOnce executes a single container run.
func (r *DockerRunner) runOnce(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	prompt := agent.BuildPrompt(req.Messages, req.SystemPrompt)

	apiURL := "http://host.docker.internal" + r.cfg.APIAddr
	env := []string{
		"CHANNEL_ID=" + req.ChannelID,
		"API_URL=" + apiURL,
	}
	if req.SessionID != "" {
		env = append(env, "SESSION_ID="+req.SessionID)
	}
	if r.cfg.ClaudeCodeOAuthToken != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+r.cfg.ClaudeCodeOAuthToken)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if v := getenv(key); v != "" {
			env = append(env, key+"="+localhostToDockerHost(v))
		}
	}

	workDir := filepath.Join(r.cfg.LoopDir, req.ChannelID, "work")
	if req.DirPath != "" {
		workDir = req.DirPath
	}
	mcpDir := filepath.Join(r.cfg.LoopDir, req.ChannelID, "mcp")
	for _, dir := range []string{workDir, mcpDir} {
		if err := mkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating host directory %s: %w", dir, err)
		}
	}

	mcpCfg := buildMCPConfig(req.ChannelID, apiURL, r.cfg.MCPServers)
	mcpJSON, _ := json.MarshalIndent(mcpCfg, "", "  ")
	if err := writeFile(filepath.Join(workDir, ".mcp.json"), mcpJSON, 0o644); err != nil {
		return nil, fmt.Errorf("writing mcp config: %w", err)
	}

	containerCfg := &ContainerConfig{
		Image:    r.cfg.ContainerImage,
		MemoryMB: r.cfg.ContainerMemoryMB,
		CPUs:     r.cfg.ContainerCPUs,
		Env:      env,
		Cmd:      []string{prompt},
		Binds:    []string{sessionVolume, workDir + ":/work", mcpDir + ":/mcp"},
	}

	containerID, err := r.client.ContainerCreate(ctx, containerCfg, "")
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}
	defer func() {
		_ = r.client.ContainerRemove(ctx, containerID)
	}()

	reader, err := r.client.ContainerAttach(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("attaching to container: %w", err)
	}

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

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return nil, fmt.Errorf("reading container output: %w", err)
	}

	output := stripANSI(buf.String())

	var claudeResp claudeResponse
	if err := json.Unmarshal([]byte(output), &claudeResp); err != nil {
		if exitCode != 0 {
			return nil, fmt.Errorf("container exited with code %d: %w", exitCode, err)
		}
		return nil, fmt.Errorf("parsing claude response: %w", err)
	}

	if claudeResp.IsError {
		return nil, fmt.Errorf("claude returned error: %s", claudeResp.Result)
	}

	return &agent.AgentResponse{
		Response:  claudeResp.Result,
		SessionID: claudeResp.SessionID,
	}, nil
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

// stripANSI removes ANSI escape sequences from TTY output.
func stripANSI(s string) string {
	return strings.TrimSpace(ansiRegexp.ReplaceAllString(s, ""))
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
