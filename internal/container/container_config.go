// container_config.go holds env, mount, MCP-config, and policy-file building
// helpers used by DockerRunner when preparing a container for launch.
package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/radutopala/loop/internal/config"
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
		// Default Claude to the no-flicker (alternate-screen) renderer for any
		// claude launched in the container — including a manual `claude` in a
		// Docker Shell — so its TUI stays pinned instead of scrolling away. A
		// user-set envs.CLAUDE_CODE_NO_FLICKER (appended below) overrides this.
		"CLAUDE_CODE_NO_FLICKER=1",
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

// agentAPIBase returns the base URL agent containers use to reach the loop API.
// Defaults to host.docker.internal (correct when the daemon runs on the Docker
// host), but honors cfg.APIAdvertiseURL when set — needed when the daemon ITSELF
// runs in a container, so agents reach it over the Docker network rather than
// the host (where host.docker.internal:<port> would hit a different daemon).
func agentAPIBase(cfg *config.Config) string {
	if cfg.APIAdvertiseURL != "" {
		return cfg.APIAdvertiseURL
	}
	return "http://host.docker.internal" + cfg.APIAddr
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
// Files that don't exist are silently skipped. For ~/.claude.json the consent
// flags are merged in (see mergeClaudeFlags), keyed on workDir, so the
// interactive agent TUI skips the onboarding / bypass / trust dialogs it can't
// answer — without disturbing the user's auth or other files.
func (r *DockerRunner) copyFiles(ctx context.Context, containerID string, files []string, workDir string) error {
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
		if name == ".claude.json" {
			data = mergeClaudeFlags(data, workDir)
		}

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

// mergeClaudeFlags returns ~/.claude.json content with the consent flags Loop's
// sandboxed agents need, merged into `existing` (which may be empty/nil/invalid).
// Every existing key is preserved — notably oauthAccount auth — and only these
// are added/overwritten:
//   - global: hasCompletedOnboarding, bypassPermissionsModeAccepted
//   - projects[workDir]: hasTrustDialogAccepted, hasCompletedProjectOnboarding
//
// Without them the interactive Claude TUI (Docker Agent terminals) stalls on the
// onboarding, "Bypass Permissions mode" consent, and "trust this folder" dialogs
// — which an automated agent can't answer. Invalid JSON is treated as empty.
func mergeClaudeFlags(existing []byte, workDir string) []byte {
	m := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &m); err != nil || m == nil {
			m = map[string]any{}
		}
	}
	m["hasCompletedOnboarding"] = true
	m["bypassPermissionsModeAccepted"] = true

	projects, ok := m["projects"].(map[string]any)
	if !ok || projects == nil {
		projects = map[string]any{}
	}
	entry, ok := projects[workDir].(map[string]any)
	if !ok || entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true

	// Inherit project-scoped MCP servers from the nearest ancestor project.
	// Claude Code keys `projects[<cwd>].mcpServers` by the exact working dir,
	// but a git worktree agent runs at the worktree path — not the repo root
	// where the user configured them. Without this, worktree
	// (and nested-worktree task) agents silently lose those MCP servers. Only
	// fill in when the worktree has none of its own so an explicit override wins.
	if _, has := entry["mcpServers"]; !has {
		if servers := nearestAncestorMCPServers(projects, workDir); servers != nil {
			entry["mcpServers"] = servers
		}
	}

	projects[workDir] = entry
	m["projects"] = projects

	out, _ := json.Marshal(m) // a map decoded from JSON always re-marshals
	return out
}

// nearestAncestorMCPServers returns the non-empty mcpServers value of the
// deepest project path in projects that is a filesystem ancestor of workDir
// (workDir itself is excluded). Returns nil when no ancestor defines any.
func nearestAncestorMCPServers(projects map[string]any, workDir string) any {
	best := ""
	var bestServers any
	for path, v := range projects {
		if !isAncestorDir(path, workDir) {
			continue
		}
		pm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		servers, ok := pm["mcpServers"]
		if !ok || servers == nil {
			continue
		}
		if sm, ok := servers.(map[string]any); ok && len(sm) == 0 {
			continue
		}
		if len(path) > len(best) {
			best, bestServers = path, servers
		}
	}
	return bestServers
}

// isAncestorDir reports whether ancestor is a strict parent directory of child.
func isAncestorDir(ancestor, child string) bool {
	if ancestor == "" {
		return false
	}
	return strings.HasPrefix(child, strings.TrimRight(ancestor, "/")+"/")
}

// withClaudeConfig ensures ~/.claude.json is the first copy_files entry so every
// agent container gets a flag-merged copy (see mergeClaudeFlags) regardless of
// the user's copy_files config — the merge happens in copyFiles, keyed on the
// basename. Deduped if the caller already listed it.
func withClaudeConfig(copyFiles []string) []string {
	const claudeJSON = "~/.claude.json"
	out := make([]string, 0, len(copyFiles)+1)
	out = append(out, claudeJSON)
	for _, f := range copyFiles {
		if f != claudeJSON {
			out = append(out, f)
		}
	}
	return out
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
