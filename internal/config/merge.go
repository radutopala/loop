// merge.go holds the project-config loading and merge logic that layers
// project-level .loop/config.json overrides onto the global Config.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tailscale/hujson"

	"github.com/radutopala/loop/internal/types"
)

// projectConfig is the structure for project-specific .loop/config.json files.
type projectConfig struct {
	Mounts                                   []string               `json:"mounts"`
	CopyFiles                                []string               `json:"copy_files"`
	Envs                                     map[string]any         `json:"envs"`
	MCP                                      *jsonMCPConfig         `json:"mcp"`
	ClaudeModel                              string                 `json:"claude_model"`
	ClaudeBinPath                            string                 `json:"claude_bin_path"`
	ClaudeDangerouslyLoadDevelopmentChannels *bool                  `json:"claude_dangerously_load_development_channels"`
	ClaudeBatchDisallowedTools               []string               `json:"claude_batch_disallowed_tools"`
	ClaudeRetry                              *jsonAgentRetryConfig  `json:"claude_retry"`
	ClaudeCodeOAuthToken                     string                 `json:"claude_code_oauth_token"`
	AnthropicAPIKey                          string                 `json:"anthropic_api_key"`
	ContainerImage                           string                 `json:"container_image"`
	ContainerMemoryMB                        *int64                 `json:"container_memory_mb"`
	ContainerCPUs                            *float64               `json:"container_cpus"`
	KeepMCPConfigs                           *bool                  `json:"keep_mcp_configs"`
	Browser                                  *jsonBrowserConfig     `json:"browser"`
	TaskTemplates                            []TaskTemplate         `json:"task_templates"`
	Workflows                                []WorkflowDef          `json:"workflows"`
	WorkflowConcurrency                      *WorkflowConcurrency   `json:"workflow_concurrency"`
	PromptShortcuts                          []PromptShortcut       `json:"prompt_shortcuts"`
	BashShortcuts                            []BashShortcut         `json:"bash_shortcuts"`
	Memory                                   *jsonMemoryConfig      `json:"memory"`
	Quality                                  *jsonQualityConfig     `json:"quality"`
	Permissions                              *jsonPermissionsConfig `json:"permissions"`
	ExtraDirs                                []string               `json:"extra_dirs"`
	Gates                                    *jsonGatesConfig       `json:"gates"`
	GitHub                                   *GitHubConfig          `json:"github"`
	Review                                   *jsonReviewConfig      `json:"review"`
}

// LoadProjectConfig loads project-specific config from {workDir}/.loop/config.json
// and merges it with the main config. Only mounts, mcp_servers, and claude_model
// are loaded from the project config for security reasons.
//
// Merge behavior:
// - Mounts: Project mounts replace global mounts entirely
// - MCP Servers: Merged with project servers taking precedence over main config
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
	_, err := l.readFile(filepath.Join(worktreeDir, ".loop", "config.json"))
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
	return worktreeMerged, nil
}

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

	standardJSON, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("parsing project config file: %w", err)
	}

	var pc projectConfig
	if err := json.Unmarshal(standardJSON, &pc); err != nil {
		return nil, fmt.Errorf("parsing project config file: %w", err)
	}

	// Create a copy of main config to avoid mutating it
	merged := *mainConfig

	// Merge mounts: project mounts replace global mounts entirely.
	// Resolve relative paths relative to workDir.
	if len(pc.Mounts) > 0 {
		resolvedMounts := make([]string, 0, len(pc.Mounts))
		for _, mount := range pc.Mounts {
			parts := strings.Split(mount, ":")
			if len(parts) < 2 {
				return nil, fmt.Errorf("invalid mount format in project config: %s", mount)
			}

			hostPath := parts[0]
			// Resolve relative paths relative to workDir, but skip named volumes
			// (e.g. "loop-npmcache:~/.npm") which contain no path separators.
			if !filepath.IsAbs(hostPath) && !strings.HasPrefix(hostPath, "~") && !IsNamedVolume(hostPath) {
				hostPath = filepath.Join(workDir, hostPath)
			}

			// Reconstruct mount with resolved path
			containerPath := parts[1]
			mode := ""
			if len(parts) > 2 {
				mode = ":" + parts[2]
			}
			resolvedMounts = append(resolvedMounts, hostPath+":"+containerPath+mode)
		}

		merged.Mounts = resolvedMounts
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

	if pc.ClaudeBinPath != "" {
		merged.ClaudeBinPath = pc.ClaudeBinPath
	}

	if pc.ClaudeDangerouslyLoadDevelopmentChannels != nil {
		merged.ClaudeDangerouslyLoadDevelopmentChannels = *pc.ClaudeDangerouslyLoadDevelopmentChannels
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
	}

	if pc.ClaudeCodeOAuthToken != "" {
		merged.ClaudeCodeOAuthToken = pc.ClaudeCodeOAuthToken
		merged.AnthropicAPIKey = "" // OAuth takes precedence
	} else if pc.AnthropicAPIKey != "" {
		merged.AnthropicAPIKey = pc.AnthropicAPIKey
		merged.ClaudeCodeOAuthToken = "" // Clear OAuth so API key is used
	}

	if pc.ContainerImage != "" {
		merged.ContainerImage = pc.ContainerImage
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

	// Gates: project config has the same rule-authoring surface as global —
	// it can prepend rules with any decision (allow/deny/approve) so projects
	// can punch surgical holes (e.g. allow a specific bind-mount) without
	// turning a whole layer off.
	//   - Enabled: project can disable, but cannot re-enable if global is off (kill-switch).
	//   - DefaultDecision: ignored (global wins).
	//   - Rules: prepended; first-match-wins so project rules apply before global.
	//   - RateLimits / Audit: ignored (they live at the Gates umbrella; global wins).
	if pc.Gates != nil {
		if ag := pc.Gates.Agentgate; ag != nil {
			if ag.Enabled != nil && !*ag.Enabled && merged.Gates.Agentgate.Enabled {
				merged.Gates.Agentgate.Enabled = false
				// Transitive disable: docker proxy only runs when agentgate is on.
				merged.Gates.DockerProxy.Enabled = false
			}
			if len(ag.PathRules) > 0 {
				merged.Gates.Agentgate.PathRules = append(append([]types.PathRule{}, ag.PathRules...), merged.Gates.Agentgate.PathRules...)
			}
			if len(ag.CommandRules) > 0 {
				merged.Gates.Agentgate.CommandRules = append(append([]types.CommandRule{}, ag.CommandRules...), merged.Gates.Agentgate.CommandRules...)
			}
			if len(ag.FileRules) > 0 {
				merged.Gates.Agentgate.FileRules = append(append([]types.FileRule{}, ag.FileRules...), merged.Gates.Agentgate.FileRules...)
			}
		}

		if dp := pc.Gates.DockerProxy; dp != nil {
			if dp.Enabled != nil && !*dp.Enabled && merged.Gates.DockerProxy.Enabled {
				merged.Gates.DockerProxy.Enabled = false
			}
			if len(dp.HTTPRules) > 0 {
				merged.Gates.DockerProxy.HTTPRules = append(append([]types.HTTPServiceRule{}, dp.HTTPRules...), merged.Gates.DockerProxy.HTTPRules...)
			}
			if len(dp.BodyRules) > 0 {
				merged.Gates.DockerProxy.BodyRules = append(append([]types.BodyRule{}, dp.BodyRules...), merged.Gates.DockerProxy.BodyRules...)
			}
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

	return &merged, nil
}
