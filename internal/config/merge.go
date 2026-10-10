// merge.go holds the project-config loading and merge logic that layers
// project-level .loop/config.json overrides onto the global Config.
package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// projectConfig is the structure for project-specific .loop/config.json files.
type projectConfig struct {
	Mounts                     []string                   `json:"mounts"`
	InheritMounts              *bool                      `json:"inherit_mounts"`
	Proxies                    ProxiesConfig              `json:"proxies"`
	CopyFiles                  []string                   `json:"copy_files"`
	Envs                       map[string]any             `json:"envs"`
	MCP                        *jsonMCPConfig             `json:"mcp"`
	ClaudeModel                string                     `json:"claude_model"`
	ClaudeEffort               string                     `json:"claude_effort"`
	ClaudeBinPath              string                     `json:"claude_bin_path"`
	ClaudeBatchDisallowedTools []string                   `json:"claude_batch_disallowed_tools"`
	ClaudeRetry                *jsonAgentRetryConfig      `json:"claude_retry"`
	ClaudeCodeOAuthToken       string                     `json:"claude_code_oauth_token"`
	AnthropicAPIKey            string                     `json:"anthropic_api_key"`
	AnthropicBaseURL           string                     `json:"anthropic_base_url"`
	ContainerImage             string                     `json:"container_image"`
	ContainerImageAutobuild    *bool                      `json:"container_image_autobuild"`
	ContainerMemoryMB          *int64                     `json:"container_memory_mb"`
	ContainerCPUs              *float64                   `json:"container_cpus"`
	KeepMCPConfigs             *bool                      `json:"keep_mcp_configs"`
	Browser                    *jsonBrowserConfig         `json:"browser"`
	TaskTemplates              []TaskTemplate             `json:"task_templates"`
	Workflows                  []WorkflowDef              `json:"workflows"`
	WorkflowConcurrency        *WorkflowConcurrency       `json:"workflow_concurrency"`
	PromptShortcuts            []PromptShortcut           `json:"prompt_shortcuts"`
	BashShortcuts              []BashShortcut             `json:"bash_shortcuts"`
	ChatComponents             []ChatComponent            `json:"chat_components"`
	Memory                     *jsonMemoryConfig          `json:"memory"`
	Quality                    *jsonQualityConfig         `json:"quality"`
	Permissions                *jsonPermissionsConfig     `json:"permissions"`
	ExtraDirs                  []string                   `json:"extra_dirs"`
	Gates                      *jsonGatesConfig           `json:"gates"`
	GitHub                     *GitHubConfig              `json:"github"`
	Review                     *jsonReviewConfig          `json:"review"`
	PlaygroundShare            *jsonPlaygroundShareConfig `json:"playground_share"`
	Learn                      *jsonLearnConfig           `json:"learn"`
	Explain                    *jsonExplainConfig         `json:"explain"`
}

// LoadProjectConfig loads project-specific config from {workDir}/.loop/config.json
// and merges it with the main config. Only mounts, mcp_servers, and claude_model
// are loaded from the project config for security reasons.
//
// Merge behavior:
//   - Mounts: Project mounts are added to the global ones, a project mount
//     replacing a global one at the same container path; inherit_mounts: false
//     makes them replace the global list instead
//   - Proxies.HTTPProxy/HTTPSProxy: Project value replaces the global one when set
//   - Proxies.NoProxy: Project entries are appended to the global ones
//   - MCP Servers: Merged with project servers taking precedence over main config
//
// Relative paths in project mounts are resolved relative to workDir.
// If the project config file doesn't exist, returns the main config unchanged.
func LoadProjectConfig(workDir string, mainConfig *Config) (*Config, error) {
	return newLoader().loadProjectConfig(workDir, mainConfig)
}

// LoadWorktreeProjectConfig loads project config for a worktree channel.
// It first checks worktreeDir/.loop/config.json; if absent, falls back to parentDir.
// This ensures worktree threads inherit the parent project's config unless the
// worktree has its own overrides.
func LoadWorktreeProjectConfig(worktreeDir, parentDir string, mainConfig *Config) (*Config, error) {
	return newLoader().loadWorktreeProjectConfig(worktreeDir, parentDir, mainConfig)
}

// NewProjectLoader returns a Loader whose project config loads gate the
// trusted fields with trust; a nil trust applies them as written.
func NewProjectLoader(trust *TrustStore) *Loader {
	l := newLoader()
	l.trust = trust
	return l
}

// LoadProject is LoadProjectConfig with this loader's trust store.
func (l *Loader) LoadProject(workDir string, mainConfig *Config) (*Config, error) {
	return l.loadProjectConfig(workDir, mainConfig)
}

// LoadWorktreeProject is LoadWorktreeProjectConfig with this loader's trust
// store.
func (l *Loader) LoadWorktreeProject(worktreeDir, parentDir string, mainConfig *Config) (*Config, error) {
	return l.loadWorktreeProjectConfig(worktreeDir, parentDir, mainConfig)
}

func (l *Loader) loadWorktreeProjectConfig(worktreeDir, parentDir string, mainConfig *Config) (*Config, error) {
	// Always apply parent project config first (global → parent).
	parentMerged := mainConfig
	if parentDir != "" {
		var err error
		parentMerged, err = l.loadProjectConfig(parentDir, mainConfig)
		if err != nil {
			return nil, err
		}
	}
	// Then layer worktree-specific overrides on top (global → parent → worktree).
	data, err := l.readFile(filepath.Join(worktreeDir, ".loop", "config.json"))
	if os.IsNotExist(err) {
		return parentMerged, nil
	}
	worktreeMerged, err := l.loadProjectConfig(worktreeDir, parentMerged)
	if err != nil {
		return nil, err
	}
	// extra_dirs use replace semantics in loadProjectConfig, but a worktree's
	// seeded config sets extra_dirs to the parent project dir — which would
	// otherwise wipe the parent project's own extra_dirs. Union them so a
	// worktree container mounts the same extra dirs as the parent channel
	// (plus the parent dir for --add-dir access), not just the parent dir.
	worktreeMerged.ExtraDirs = unionExtraDirs(parentMerged.ExtraDirs, worktreeMerged.ExtraDirs)
	// The seeded parent dir is the project the worktree belongs to, so it
	// needs no trust: worktree configs seeded before trust existed, or
	// rewritten by an agent, keep it.
	if parentDir != "" && seedsParentDir(data, parentDir) {
		worktreeMerged.ExtraDirs = unionExtraDirs(worktreeMerged.ExtraDirs, []string{parentDir})
	}
	return worktreeMerged, nil
}

// seedsParentDir reports whether the worktree config data lists parentDir
// in extra_dirs.
func seedsParentDir(data []byte, parentDir string) bool {
	pc, err := parseProjectConfig(data)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(pc.ExtraDirs, func(d string) bool { return filepath.Clean(d) == filepath.Clean(parentDir) })
}

// layerRules puts project rules under the global deny rules and over the
// rest of the global rules, keeping each group's order. With no project
// rules the global list is returned as is.
//
// A trusted project's rules also go above the built-in denies marked
// overridable: the global denies that stay first are the ones the user wrote
// and the built-in ones that guard more than credentials. The overridable
// denies keep their place among the rest of the global rules, so a built-in
// allow written ahead of one (rm under /tmp) still comes first. An untrusted
// project's rules (the last approved version, while the file has changed
// since) stay under every global deny.
func layerRules[T any](global, project []T, decision func(T) types.Decision, overridable func(T) bool, trusted bool) []T {
	if len(project) == 0 {
		return global
	}
	first := func(r T) bool {
		return decision(r) == types.DecisionDeny && (!trusted || !overridable(r))
	}
	out := make([]T, 0, len(global)+len(project))
	for _, r := range global {
		if first(r) {
			out = append(out, r)
		}
	}
	out = append(out, project...)
	for _, r := range global {
		if !first(r) {
			out = append(out, r)
		}
	}
	return out
}

// notOverridable is layerRules' overridable for the rule kinds whose
// built-in denies all stay ahead of project rules.
func notOverridable[T any](T) bool { return false }

// unionExtraDirs returns the union of two extra_dirs slices, preserving order
// (a entries first, then b entries not already present) and removing duplicates.
func unionExtraDirs(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, dirs := range [][]string{a, b} {
		for _, d := range dirs {
			if seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func (l *Loader) loadProjectConfig(workDir string, mainConfig *Config) (*Config, error) {
	projectConfigPath := filepath.Join(workDir, ".loop", "config.json")

	data, err := l.readFile(projectConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No project config, use main config as-is
			return mainConfig, nil
		}
		return nil, fmt.Errorf("reading project config file: %w", err)
	}

	pc, err := parseProjectConfig(data)
	if err != nil {
		return nil, err
	}
	// Until the owner trusts them, the fields that reach past the container
	// keep their last trusted values. Gate rules go above the overridable
	// built-in denies only once the owner trusts the file as it is.
	trusted := false
	if l.trust != nil {
		var fields trustedFields
		fields, trusted = l.trust.resolve(workDir, pc)
		fields.apply(pc)
	}

	// Create a copy of main config to avoid mutating it
	merged := *mainConfig

	// Merge mounts: project mounts are added to the global ones, unless the
	// project opts out with inherit_mounts: false. Resolve relative paths
	// relative to workDir.
	resolvedMounts := make([]string, 0, len(pc.Mounts))
	for _, mount := range pc.Mounts {
		resolved, err := ResolveMount(mount, workDir)
		if err != nil {
			return nil, err
		}
		resolvedMounts = append(resolvedMounts, resolved)
	}
	if pc.InheritMounts != nil && !*pc.InheritMounts {
		merged.Mounts = resolvedMounts
	} else {
		merged.Mounts = mergeByName(mainConfig.Mounts, resolvedMounts, mountTarget)
	}

	// A project behind its own proxy overrides the global one outright —
	// unlike NoProxy below, two proxy URLs cannot be combined.
	if pc.Proxies.HTTPProxy != "" {
		merged.Proxies.HTTPProxy = pc.Proxies.HTTPProxy
	}
	if pc.Proxies.HTTPSProxy != "" {
		merged.Proxies.HTTPSProxy = pc.Proxies.HTTPSProxy
	}

	// Appended, not replaced: a project declares the sibling names its own
	// compose stack uses, on top of whatever the global config bypasses.
	if len(pc.Proxies.NoProxy) > 0 {
		merged.Proxies.NoProxy = append(merged.Proxies.NoProxy, pc.Proxies.NoProxy...)
	}

	// CopyFiles: project replaces global when set.
	if len(pc.CopyFiles) > 0 {
		merged.CopyFiles = pc.CopyFiles
	}

	// Merge MCP servers: project takes precedence
	if pc.MCP != nil && len(pc.MCP.Servers) > 0 {
		// Start with main config servers
		mergedServers := make(map[string]MCPServerConfig)
		maps.Copy(mergedServers, mainConfig.MCPServers)
		// Override with project servers
		maps.Copy(mergedServers, pc.MCP.Servers)
		merged.MCPServers = mergedServers
	}

	if pc.ClaudeModel != "" {
		merged.ClaudeModel = pc.ClaudeModel
	}

	if pc.ClaudeEffort != "" {
		merged.ClaudeEffort = pc.ClaudeEffort
	}

	if pc.ClaudeBinPath != "" {
		merged.ClaudeBinPath = pc.ClaudeBinPath
	}

	if len(pc.ClaudeBatchDisallowedTools) > 0 {
		merged.ClaudeBatchDisallowedTools = pc.ClaudeBatchDisallowedTools
	}

	if pc.ClaudeRetry != nil {
		if pc.ClaudeRetry.MaxAttempts != nil {
			merged.AgentRetry.MaxAttempts = *pc.ClaudeRetry.MaxAttempts
		}
		if pc.ClaudeRetry.BackoffBaseSec != nil {
			merged.AgentRetry.BackoffBase = time.Duration(*pc.ClaudeRetry.BackoffBaseSec) * time.Second
		}
		if pc.ClaudeRetry.BackoffMaxSec != nil {
			merged.AgentRetry.BackoffMax = time.Duration(*pc.ClaudeRetry.BackoffMaxSec) * time.Second
		}
		if pc.ClaudeRetry.SessionLimitAutoContinue != nil {
			merged.AgentRetry.SessionLimitAutoContinue = *pc.ClaudeRetry.SessionLimitAutoContinue
		}
	}

	if pc.ClaudeCodeOAuthToken != "" {
		merged.ClaudeCodeOAuthToken = pc.ClaudeCodeOAuthToken
		merged.AnthropicAPIKey = "" // OAuth takes precedence
	} else if pc.AnthropicAPIKey != "" {
		merged.AnthropicAPIKey = pc.AnthropicAPIKey
		merged.ClaudeCodeOAuthToken = "" // Clear OAuth so API key is used
	}
	if pc.AnthropicBaseURL != "" {
		merged.AnthropicBaseURL = pc.AnthropicBaseURL
	}

	if pc.ContainerImage != "" {
		merged.ContainerImage = pc.ContainerImage
	}
	if pc.ContainerImageAutobuild != nil {
		merged.ContainerImageAutobuild = *pc.ContainerImageAutobuild
	}
	if pc.ContainerMemoryMB != nil {
		merged.ContainerMemoryMB = *pc.ContainerMemoryMB
	}
	if pc.ContainerCPUs != nil {
		merged.ContainerCPUs = *pc.ContainerCPUs
	}
	if pc.KeepMCPConfigs != nil {
		merged.KeepMCPConfigs = *pc.KeepMCPConfigs
	}
	if pc.Browser != nil {
		if pc.Browser.Enabled != nil {
			merged.Browser.Enabled = *pc.Browser.Enabled
		}
		if pc.Browser.ChromeImage != "" {
			merged.Browser.ChromeImage = pc.Browser.ChromeImage
		}
		if pc.Browser.Mode != "" {
			merged.Browser.Mode = pc.Browser.Mode
		}
		if pc.Browser.HostCDPPort != nil {
			merged.Browser.HostCDPPort = *pc.Browser.HostCDPPort
		}
		if pc.Browser.PersistProfile != nil {
			merged.Browser.PersistProfile = *pc.Browser.PersistProfile
		}
		if pc.Browser.Extensions != nil {
			merged.Browser.Extensions = pc.Browser.Extensions
		}
		if pc.Browser.MemoryMB != nil {
			merged.Browser.MemoryMB = *pc.Browser.MemoryMB
		}
		if pc.Browser.CPUs != nil {
			merged.Browser.CPUs = *pc.Browser.CPUs
		}
		mergeCookieImport(&merged.Browser.CookieImport, pc.Browser.CookieImport)
	}

	// Quality config: project overrides global per-key. Rules merge by
	// name — project entries replace global entries with the same name;
	// global entries that aren't mentioned in the project block survive.
	// Complexity / Clones override per-field — only fields the project
	// explicitly sets replace the global value, so a project that wants
	// to tweak just one threshold doesn't have to restate the rest.
	if pc.Quality != nil {
		if pc.Quality.MaxFiles != nil {
			merged.Quality.MaxFiles = *pc.Quality.MaxFiles
		}
		if pc.Quality.ExcludePaths != nil {
			merged.Quality.ExcludePaths = pc.Quality.ExcludePaths
		}
		if len(pc.Quality.Rules) > 0 {
			cloned := make(map[string]QualityRuleConfig, len(merged.Quality.Rules)+len(pc.Quality.Rules))
			maps.Copy(cloned, merged.Quality.Rules)
			for name, jrc := range pc.Quality.Rules {
				rc, existed := cloned[name]
				if jrc.Enabled != nil {
					rc.Enabled = *jrc.Enabled
				} else if !existed {
					rc.Enabled = true
				}
				if jrc.Threshold > 0 {
					rc.Threshold = jrc.Threshold
				}
				cloned[name] = rc
			}
			merged.Quality.Rules = cloned
		}
		if pc.Quality.Complexity != nil {
			if pc.Quality.Complexity.CyclomaticT != nil {
				merged.Quality.Complexity.CyclomaticT = *pc.Quality.Complexity.CyclomaticT
			}
			if pc.Quality.Complexity.CognitiveT != nil {
				merged.Quality.Complexity.CognitiveT = *pc.Quality.Complexity.CognitiveT
			}
			if pc.Quality.Complexity.NestingT != nil {
				merged.Quality.Complexity.NestingT = *pc.Quality.Complexity.NestingT
			}
			if pc.Quality.Complexity.ParamsT != nil {
				merged.Quality.Complexity.ParamsT = *pc.Quality.Complexity.ParamsT
			}
			if pc.Quality.Complexity.LOCT != nil {
				merged.Quality.Complexity.LOCT = *pc.Quality.Complexity.LOCT
			}
		}
		if pc.Quality.Clones != nil {
			if pc.Quality.Clones.MinLOC != nil {
				merged.Quality.Clones.MinLOC = *pc.Quality.Clones.MinLOC
			}
			if pc.Quality.Clones.MaxDistance != nil {
				merged.Quality.Clones.MaxDistance = *pc.Quality.Clones.MaxDistance
			}
		}
	}

	// Merge memory config: project paths appended, project embeddings override
	if pc.Memory != nil {
		if len(pc.Memory.Paths) > 0 {
			merged.Memory.Paths = append(merged.Memory.Paths, pc.Memory.Paths...)
		}
		if pc.Memory.MaxChunkChars > 0 {
			merged.Memory.MaxChunkChars = pc.Memory.MaxChunkChars
		}
		if pc.Memory.Embeddings != nil {
			merged.Memory.Embeddings = EmbeddingsConfig{
				Provider:  pc.Memory.Embeddings.Provider,
				Model:     pc.Memory.Embeddings.Model,
				OllamaURL: stringDefault(pc.Memory.Embeddings.OllamaURL, "http://localhost:11434"),
			}
		}
	}

	// Merge envs: project takes precedence over global
	if len(pc.Envs) > 0 {
		mergedEnvs := make(map[string]string)
		maps.Copy(mergedEnvs, mainConfig.Envs)
		maps.Copy(mergedEnvs, stringifyEnvs(pc.Envs))
		merged.Envs = mergedEnvs
	}

	// Permissions: project config replaces global when set.
	if pc.Permissions != nil {
		merged.Permissions = types.Permissions{}
		if pc.Permissions.Owners != nil {
			merged.Permissions.Owners.Users = pc.Permissions.Owners.Users
			merged.Permissions.Owners.Roles = pc.Permissions.Owners.Roles
		}
		if pc.Permissions.Members != nil {
			merged.Permissions.Members.Users = pc.Permissions.Members.Users
			merged.Permissions.Members.Roles = pc.Permissions.Members.Roles
		}
	}

	// Gates: a project adds rules with any decision (allow/deny/approve), so
	// it can punch surgical holes (e.g. allow a specific bind-mount) without
	// turning a whole layer off. The global config stays the baseline:
	//   - Enabled: ignored. A project can't turn a gate off (the project
	//     config is in the agent's workspace), nor on when global is off.
	//   - DefaultDecision: ignored (global wins).
	//   - Rules: the global deny rules come first, then the project rules,
	//     then the rest of the global rules. First match wins, so project
	//     rules apply before the global allows and approves, never before a
	//     deny the user wrote globally. Once the owner trusts the project
	//     config as it is, its rules also go before the built-in credential
	//     denies (see layerRules); the hard pins the container adds after
	//     the merge stay ahead of everything.
	//   - RateLimits / Audit: ignored (they live at the Gates umbrella; global wins).
	if pc.Gates != nil {
		if ag := pc.Gates.Agentgate; ag != nil {
			// The one built-in path deny (the daemon's own socket) would bypass
			// the docker proxy, so no path rule is overridable.
			merged.Gates.Agentgate.PathRules = layerRules(merged.Gates.Agentgate.PathRules, ag.PathRules, func(r types.PathRule) types.Decision { return r.Decision }, notOverridable, trusted)
			merged.Gates.Agentgate.CommandRules = layerRules(merged.Gates.Agentgate.CommandRules, ag.CommandRules, func(r types.CommandRule) types.Decision { return r.Decision }, func(r types.CommandRule) bool { return r.Overridable }, trusted)
			merged.Gates.Agentgate.FileRules = layerRules(merged.Gates.Agentgate.FileRules, ag.FileRules, func(r types.FileRule) types.Decision { return r.Decision }, func(r types.FileRule) bool { return r.Overridable }, trusted)
		}
		// The built-in docker proxy denies each block a container escape or
		// the swarm secrets API, so none is overridable.
		if dp := pc.Gates.DockerProxy; dp != nil {
			merged.Gates.DockerProxy.HTTPRules = layerRules(merged.Gates.DockerProxy.HTTPRules, dp.HTTPRules, func(r types.HTTPServiceRule) types.Decision { return r.Decision }, notOverridable, trusted)
			merged.Gates.DockerProxy.BodyRules = layerRules(merged.Gates.DockerProxy.BodyRules, dp.BodyRules, func(r types.BodyRule) types.Decision { return r.Decision }, notOverridable, trusted)
		}
	}

	// Merge task templates: project templates override global by name
	if len(pc.TaskTemplates) > 0 {
		byName := make(map[string]int, len(merged.TaskTemplates))
		mergedTemplates := make([]TaskTemplate, len(merged.TaskTemplates))
		copy(mergedTemplates, merged.TaskTemplates)
		for i, t := range mergedTemplates {
			byName[t.Name] = i
		}
		for _, pt := range pc.TaskTemplates {
			if idx, ok := byName[pt.Name]; ok {
				mergedTemplates[idx] = pt
			} else {
				mergedTemplates = append(mergedTemplates, pt)
			}
		}
		merged.TaskTemplates = mergedTemplates
	}

	// Merge workflows: project workflows override global by name
	if len(pc.Workflows) > 0 {
		byName := make(map[string]int, len(merged.Workflows))
		mergedWorkflows := make([]WorkflowDef, len(merged.Workflows))
		copy(mergedWorkflows, merged.Workflows)
		for i, w := range mergedWorkflows {
			byName[w.Name] = i
		}
		for _, pw := range pc.Workflows {
			if idx, ok := byName[pw.Name]; ok {
				mergedWorkflows[idx] = pw
			} else {
				mergedWorkflows = append(mergedWorkflows, pw)
			}
		}
		merged.Workflows = mergedWorkflows
	}

	if pc.WorkflowConcurrency != nil {
		if pc.WorkflowConcurrency.MaxConcurrentRuns > 0 {
			merged.WorkflowConcurrency.MaxConcurrentRuns = pc.WorkflowConcurrency.MaxConcurrentRuns
		}
		if pc.WorkflowConcurrency.MaxConcurrentNodes > 0 {
			merged.WorkflowConcurrency.MaxConcurrentNodes = pc.WorkflowConcurrency.MaxConcurrentNodes
		}
	}

	// Merge prompt shortcuts: project shortcuts override global by name
	if len(pc.PromptShortcuts) > 0 {
		byName := make(map[string]int, len(merged.PromptShortcuts))
		mergedShortcuts := make([]PromptShortcut, len(merged.PromptShortcuts))
		copy(mergedShortcuts, merged.PromptShortcuts)
		for i, s := range mergedShortcuts {
			byName[s.Name] = i
		}
		for _, ps := range pc.PromptShortcuts {
			if idx, ok := byName[ps.Name]; ok {
				mergedShortcuts[idx] = ps
			} else {
				mergedShortcuts = append(mergedShortcuts, ps)
			}
		}
		merged.PromptShortcuts = mergedShortcuts
	}

	// Merge bash shortcuts: project shortcuts override global by name
	if len(pc.BashShortcuts) > 0 {
		byName := make(map[string]int, len(merged.BashShortcuts))
		mergedShortcuts := make([]BashShortcut, len(merged.BashShortcuts))
		copy(mergedShortcuts, merged.BashShortcuts)
		for i, s := range mergedShortcuts {
			byName[s.Name] = i
		}
		for _, ps := range pc.BashShortcuts {
			if idx, ok := byName[ps.Name]; ok {
				mergedShortcuts[idx] = ps
			} else {
				mergedShortcuts = append(mergedShortcuts, ps)
			}
		}
		merged.BashShortcuts = mergedShortcuts
	}

	// Chat components: project templates override global ones by name.
	merged.ChatComponents = mergeByName(merged.ChatComponents, pc.ChatComponents, func(c ChatComponent) string { return c.Name })

	// ExtraDirs: project replaces global when set.
	if len(pc.ExtraDirs) > 0 {
		merged.ExtraDirs = pc.ExtraDirs
	}

	// GitHub: project overrides global when gh_user is set.
	if pc.GitHub != nil && pc.GitHub.GHUser != "" {
		merged.GitHub.GHUser = pc.GitHub.GHUser
	}

	// Review: each field overrides global only when explicitly set in the
	// project layer. Enabled is *bool so we can distinguish "unset" from
	// "false"; prompt and prompt_path override only when non-empty so an
	// empty project block doesn't wipe the global prompt.
	if pc.Review != nil {
		if pc.Review.Enabled != nil {
			merged.Review.Enabled = *pc.Review.Enabled
		}
		if pc.Review.Prompt != "" {
			merged.Review.Prompt = pc.Review.Prompt
		}
		if pc.Review.PromptPath != "" {
			merged.Review.PromptPath = pc.Review.PromptPath
		}
	}

	// PlaygroundShare: Enabled overrides global only when explicitly set.
	if pc.PlaygroundShare != nil && pc.PlaygroundShare.Enabled != nil {
		merged.PlaygroundShare.Enabled = *pc.PlaygroundShare.Enabled
	}

	// Learn: like Review, each field overrides only when set in the project.
	if pc.Learn != nil {
		if pc.Learn.Enabled != nil {
			merged.Learn.Enabled = *pc.Learn.Enabled
		}
		if pc.Learn.MinTurns != nil {
			merged.Learn.MinTurns = *pc.Learn.MinTurns
		}
		if pc.Learn.Model != "" {
			merged.Learn.Model = pc.Learn.Model
		}
		if pc.Learn.Effort != "" {
			merged.Learn.Effort = pc.Learn.Effort
		}
		if pc.Learn.Prompt != "" {
			merged.Learn.Prompt = pc.Learn.Prompt
		}
	}

	// Explain: like Learn, each field overrides only when set in the project.
	if pc.Explain != nil {
		if pc.Explain.Enabled != nil {
			merged.Explain.Enabled = *pc.Explain.Enabled
		}
		if pc.Explain.Model != "" {
			merged.Explain.Model = pc.Explain.Model
		}
		if pc.Explain.Effort != "" {
			merged.Explain.Effort = pc.Explain.Effort
		}
		if pc.Explain.Prompt != "" {
			merged.Explain.Prompt = pc.Explain.Prompt
		}
	}

	return &merged, nil
}

// mergeCookieImport applies a project-level browser.cookie_import block over
// the global one, per key: a project that only sets "auto" keeps the global
// source and domain list.
func mergeCookieImport(merged *CookieImportConfig, pc *jsonCookieImportConfig) {
	if pc == nil {
		return
	}
	if pc.Source != "" {
		merged.Source = pc.Source
	}
	if pc.Domains != nil {
		merged.Domains = pc.Domains
	}
	if pc.Auto != nil {
		merged.Auto = *pc.Auto
	}
}

// mergeByName layers overlay onto base: an overlay entry replaces the base
// entry with the same name in place, and new names are appended. base is
// never mutated.
func mergeByName[T any](base, overlay []T, name func(T) string) []T {
	if len(overlay) == 0 {
		return base
	}
	out := slices.Clone(base)
	idx := make(map[string]int, len(out))
	for i, it := range out {
		idx[name(it)] = i
	}
	for _, it := range overlay {
		if i, ok := idx[name(it)]; ok {
			out[i] = it
			continue
		}
		idx[name(it)] = len(out)
		out = append(out, it)
	}
	return out
}

// mountTarget returns a mount's container path, without a trailing slash,
// so "~/.tools/" and "~/.tools" count as the same target. Merged by
// it, a project mount replaces the global mount at the same path.
func mountTarget(mount string) string {
	_, rest, _ := strings.Cut(mount, ":")
	target, _, _ := strings.Cut(rest, ":")
	return strings.TrimSuffix(target, "/")
}

// ResolveMount returns a project config mount with a relative host path
// resolved against workDir, the project dir, as a project config's mounts
// are merged. Absolute and ~ paths and named volumes (e.g.
// "loop-npmcache:~/.npm", no path separators) are kept as they are.
func ResolveMount(mount, workDir string) (string, error) {
	parts := strings.Split(mount, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid mount format in project config: %s", mount)
	}
	hostPath := parts[0]
	if !filepath.IsAbs(hostPath) && !strings.HasPrefix(hostPath, "~") && !IsNamedVolume(hostPath) {
		hostPath = filepath.Join(workDir, hostPath)
	}
	parts[0] = hostPath
	return strings.Join(parts, ":"), nil
}
