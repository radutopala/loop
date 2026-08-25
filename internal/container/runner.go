package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
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
	ImageBuildFileLabels(ctx context.Context, contextDir, dockerfile, tag string, labels map[string]string) error
	PruneBuildCache(ctx context.Context, unusedFor time.Duration) (uint64, error)
	PruneDanglingImages(ctx context.Context) (uint64, error)
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
	// sleep waits for d or until ctx is cancelled, returning ctx.Err() if
	// cancelled. Injectable so retry-backoff tests don't sleep in real time.
	sleep      func(ctx context.Context, d time.Duration) error
	registry   ContainerRegistry
	instanceID string // unique per daemon, used to scope Cleanup
	// logger is optional; nil means the runner stays silent. Set via
	// SetLogger from serve.go, mirroring Registry.SetLogger.
	logger *slog.Logger
	// transcriptMissing reports whether Claude Code has no transcript on
	// disk for a (workDir, sessionID) pair. Injectable so tests can assert
	// both branches without staging ~/.claude/projects trees.
	transcriptMissing func(workDir, sessionID string) bool

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
		sleep:                     sleepCtx,
		instanceID:                hex.EncodeToString(b),
	}
	r.transcriptMissing = func(workDir, sessionID string) bool {
		return claudeTranscriptMissing(r.sys.Stat, r.sys.UserHomeDir, workDir, sessionID)
	}
	r.cfg.Store(cfg)
	return r
}

// SetLogger configures the runner logger. Optional — an unset logger just
// means the runner reports nothing.
func (r *DockerRunner) SetLogger(logger *slog.Logger) {
	r.logger = logger
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

// Run executes an agent request in a Docker container, retrying on transient
// API errors (rate limiting, overload, transient 5xx) with bounded exponential
// backoff. Terminal errors (usage/quota, auth, billing) are surfaced
// immediately. Each retry re-invokes with --resume so the session context is
// preserved; the backoff is interruptible via ctx (the Stop button), and
// progress is surfaced through req.OnActivity as a "rate_limited" notice.
func (r *DockerRunner) Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	retry := r.currentConfig().AgentRetry

	resp, err := r.runWithRecovery(ctx, req)
	for attempt := 0; err != nil &&
		attempt < retry.MaxAttempts &&
		isRetryableAgentError(err) &&
		ctx.Err() == nil; attempt++ {

		delay := backoffDelay(attempt, retry.BackoffBase, retry.BackoffMax)
		if req.OnActivity != nil {
			req.OnActivity("rate_limited", fmt.Sprintf("Rate limited — retrying in %s (%d/%d)", delay.Round(time.Second), attempt+1, retry.MaxAttempts))
		}
		if serr := r.sleep(ctx, delay); serr != nil {
			return resp, err // ctx cancelled during backoff — surface the last error
		}

		// Resume the session produced by the failed attempt when available, so
		// retries continue the same conversation rather than restarting it.
		retryReq := *req
		if resp != nil && resp.SessionID != "" {
			retryReq.SessionID = resp.SessionID
			retryReq.ForkSession = false
		}
		resp, err = r.runWithRecovery(ctx, &retryReq)
	}
	return resp, err
}

// runWithRecovery executes an agent request and, on failure with a live
// session, retries with --resume — compacting first when the session is too
// long. This is the per-attempt unit the backoff loop in Run calls.
func (r *DockerRunner) runWithRecovery(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
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
	// Any API limit/overload error is left for the layers above: transient ones
	// (rate limit, overload) are picked up by the backoff loop in Run; terminal
	// ones (weekly/session/usage limit) are surfaced to the orchestrator (which
	// may schedule a session-limit auto-continue). Blind-retrying any of them
	// here just hits the same wall and burns a second container — the bug where
	// a weekly-limit error appeared twice a minute apart. resp carries the
	// failed run's SessionID so the caller can resume.
	if isAPILimitError(err) {
		return resp, err
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

// runOnce executes a single container run.
func (r *DockerRunner) runOnce(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	// Resuming a transcript that Claude Code has already pruned fails the
	// run outright, and nothing rewrites the channel's session id on
	// failure — so the channel would stay wedged on every later turn too.
	// Start a fresh session instead of shipping a doomed --resume.
	if r.transcriptMissing(r.resolveWorkDir(req.ChannelID, req.DirPath), req.SessionID) {
		if r.logger != nil {
			r.logger.Warn("session transcript not found; starting a fresh session",
				"channel_id", req.ChannelID, "session_id", req.SessionID)
		}
		fresh := *req
		fresh.SessionID = ""
		fresh.ForkSession = false
		req = &fresh
	}
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
		onToolUseRaw: req.OnToolUseRaw,
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

// createAndStartContainer resolves the work directory, loads project config,
// builds environment and mounts, writes MCP config, creates the Docker
// container with the command returned by buildCmd, and starts it.
// The mcpConfigPath is returned so the caller can clean it up if needed.
// keepMCPConfig reflects the merged config's KeepMCPConfigs flag.
// resolveWorkDir returns the directory a container for this channel runs in:
// the channel's own dir when it has one, else the per-channel scratch dir
// under LoopDir. It is also the key Claude Code stores session transcripts
// under, so anything reasoning about --resume must use the same value.
func (r *DockerRunner) resolveWorkDir(channelID, dirPath string) string {
	if dirPath != "" {
		return dirPath
	}
	return filepath.Join(r.currentConfig().LoopDir, channelID, "work")
}

func (r *DockerRunner) createAndStartContainer(
	ctx context.Context,
	channelID, dirPath, authorID, parentDirPath, agentID string,
	cType ContainerType,
	buildCmd func(cfg *config.Config, mcpConfigPath string) []string,
) (containerID, containerName, mcpConfigPath string, keepMCPConfig bool, err error) {
	workDir := r.resolveWorkDir(channelID, dirPath)

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

	apiURL := agentAPIBase(cfg)

	env, err := r.buildContainerEnv(cfg, channelID, apiURL)
	if err != nil {
		return "", "", "", false, err
	}

	binds, chownPaths := r.buildContainerMounts(cfg.Mounts, workDir, parentDirPath, cfg.ExtraDirs)
	copyFilesList := withClaudeConfig(cfg.CopyFiles)
	for _, f := range r.filterMountedCopyFiles(copyFilesList, binds) {
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

	if err := r.copyFiles(ctx, containerID, r.filterMountedCopyFiles(copyFilesList, binds), workDir); err != nil {
		return containerID, containerName, mcpConfigPath, keepMCPConfig, fmt.Errorf("copying files: %w", err)
	}

	if err := r.client.ContainerStart(ctx, containerID); err != nil {
		return containerID, containerName, mcpConfigPath, keepMCPConfig, fmt.Errorf("starting container: %w", err)
	}

	return containerID, containerName, mcpConfigPath, keepMCPConfig, nil
}

// collectOutput reads container logs (streaming or batch) and waits for exit.
// Returns the parsed Claude response or an error.
func (r *DockerRunner) collectOutput(ctx context.Context, containerID string, cb streamCallbacks) (*claudeResponse, error) {
	if cb.any() {
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
