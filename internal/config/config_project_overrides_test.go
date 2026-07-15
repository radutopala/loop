package config

import (
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

func (s *ConfigSuite) TestLoadProjectConfigOverrides() {
	tests := []struct {
		name        string
		projectJSON string
		mainCfg     *Config
		assert      func(merged, main *Config)
	}{
		{
			name:        "ClaudeModel/Override",
			projectJSON: `{"claude_model": "claude-opus-4-6"}`,
			mainCfg:     &Config{ClaudeModel: "claude-sonnet-4-5-20250929"},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "claude-opus-4-6", merged.ClaudeModel)
			},
		},
		{
			name:        "ClaudeModel/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{ClaudeModel: "claude-sonnet-4-5-20250929"},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "claude-sonnet-4-5-20250929", merged.ClaudeModel)
			},
		},
		{
			name:        "ClaudeEffort/Override",
			projectJSON: `{"claude_effort": "high"}`,
			mainCfg:     &Config{ClaudeEffort: "low"},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "high", merged.ClaudeEffort)
			},
		},
		{
			name:        "ClaudeEffort/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{ClaudeEffort: "low"},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "low", merged.ClaudeEffort)
			},
		},
		{
			name:        "OAuthToken/Override",
			projectJSON: `{"claude_code_oauth_token": "sk-ant-project-oauth"}`,
			mainCfg:     &Config{AnthropicAPIKey: "sk-ant-global-api-key"},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), "sk-ant-project-oauth", merged.ClaudeCodeOAuthToken)
				require.Empty(s.T(), merged.AnthropicAPIKey)
				require.Equal(s.T(), "sk-ant-global-api-key", main.AnthropicAPIKey)
			},
		},
		{
			name:        "APIKey/Override",
			projectJSON: `{"anthropic_api_key": "sk-ant-project-api-key"}`,
			mainCfg:     &Config{ClaudeCodeOAuthToken: "sk-ant-global-oauth"},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), "sk-ant-project-api-key", merged.AnthropicAPIKey)
				require.Empty(s.T(), merged.ClaudeCodeOAuthToken)
				require.Equal(s.T(), "sk-ant-global-oauth", main.ClaudeCodeOAuthToken)
			},
		},
		{
			name:        "Auth/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				ClaudeCodeOAuthToken: "sk-ant-global-oauth",
				AnthropicAPIKey:      "sk-ant-global-api-key",
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "sk-ant-global-oauth", merged.ClaudeCodeOAuthToken)
				require.Equal(s.T(), "sk-ant-global-api-key", merged.AnthropicAPIKey)
			},
		},
		{
			name:        "ClaudeBinPath/Override",
			projectJSON: `{"claude_bin_path": "/custom/bin/claude"}`,
			mainCfg:     &Config{ClaudeBinPath: "claude"},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "/custom/bin/claude", merged.ClaudeBinPath)
			},
		},
		{
			name:        "ClaudeBinPath/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{ClaudeBinPath: "claude"},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "claude", merged.ClaudeBinPath)
			},
		},
		{
			name:        "DevChannels/EnableFromOff",
			projectJSON: `{"claude_dangerously_load_development_channels": true}`,
			mainCfg:     &Config{ClaudeDangerouslyLoadDevelopmentChannels: false},
			assert: func(merged, main *Config) {
				require.True(s.T(), merged.ClaudeDangerouslyLoadDevelopmentChannels)
				require.False(s.T(), main.ClaudeDangerouslyLoadDevelopmentChannels)
			},
		},
		{
			name:        "DevChannels/DisableFromOn",
			projectJSON: `{"claude_dangerously_load_development_channels": false}`,
			mainCfg:     &Config{ClaudeDangerouslyLoadDevelopmentChannels: true},
			assert: func(merged, main *Config) {
				require.False(s.T(), merged.ClaudeDangerouslyLoadDevelopmentChannels)
				require.True(s.T(), main.ClaudeDangerouslyLoadDevelopmentChannels)
			},
		},
		{
			name:        "DevChannels/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{ClaudeDangerouslyLoadDevelopmentChannels: true},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.ClaudeDangerouslyLoadDevelopmentChannels)
			},
		},
		{
			name:        "BatchDisallowedTools/Override",
			projectJSON: `{"claude_batch_disallowed_tools": ["ScheduleWakeup"]}`,
			mainCfg:     &Config{ClaudeBatchDisallowedTools: []string{"ScheduleWakeup", "CronCreate"}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), []string{"ScheduleWakeup"}, merged.ClaudeBatchDisallowedTools)
				require.Equal(s.T(), []string{"ScheduleWakeup", "CronCreate"}, main.ClaudeBatchDisallowedTools)
			},
		},
		{
			name:        "BatchDisallowedTools/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{ClaudeBatchDisallowedTools: []string{"ScheduleWakeup"}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), []string{"ScheduleWakeup"}, merged.ClaudeBatchDisallowedTools)
			},
		},
		{
			name:        "AgentRetry/PartialOverride",
			projectJSON: `{"claude_retry": {"max_attempts": 2}}`,
			mainCfg:     &Config{AgentRetry: AgentRetryConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second, BackoffMax: 120 * time.Second}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), 2, merged.AgentRetry.MaxAttempts)
				require.Equal(s.T(), 5*time.Second, merged.AgentRetry.BackoffBase) // unchanged
				require.Equal(s.T(), 5, main.AgentRetry.MaxAttempts)               // global untouched
			},
		},
		{
			name:        "AgentRetry/FullOverride",
			projectJSON: `{"claude_retry": {"max_attempts": 2, "backoff_base_sec": 3, "backoff_max_sec": 60, "session_limit_auto_continue": false}}`,
			mainCfg:     &Config{AgentRetry: AgentRetryConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second, BackoffMax: 120 * time.Second, SessionLimitAutoContinue: true}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 2, merged.AgentRetry.MaxAttempts)
				require.Equal(s.T(), 3*time.Second, merged.AgentRetry.BackoffBase)
				require.Equal(s.T(), 60*time.Second, merged.AgentRetry.BackoffMax)
				require.False(s.T(), merged.AgentRetry.SessionLimitAutoContinue)
			},
		},
		{
			name:        "AgentRetry/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{AgentRetry: AgentRetryConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second, BackoffMax: 120 * time.Second}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 5, merged.AgentRetry.MaxAttempts)
			},
		},
		{
			name: "Container/Override",
			projectJSON: `{
				"container_image": "custom-agent:v3",
				"browser": { "chrome_image": "custom-chrome:v2" },
				"container_memory_mb": 2048,
				"container_cpus": 4.0
			}`,
			mainCfg: &Config{
				ContainerImage:    "loop-agent:latest",
				Browser:           BrowserConfig{ChromeImage: "loop-chrome:latest"},
				ContainerMemoryMB: 512,
				ContainerCPUs:     1.0,
			},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), "custom-agent:v3", merged.ContainerImage)
				require.Equal(s.T(), "custom-chrome:v2", merged.Browser.ChromeImage)
				require.Equal(s.T(), int64(2048), merged.ContainerMemoryMB)
				require.Equal(s.T(), 4.0, merged.ContainerCPUs)
				require.Equal(s.T(), "loop-agent:latest", main.ContainerImage)
				require.Equal(s.T(), "loop-chrome:latest", main.Browser.ChromeImage)
				require.Equal(s.T(), int64(512), main.ContainerMemoryMB)
			},
		},
		{
			name:        "Container/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				ContainerImage:    "loop-agent:latest",
				ContainerMemoryMB: 512,
				ContainerCPUs:     1.0,
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "loop-agent:latest", merged.ContainerImage)
				require.Equal(s.T(), int64(512), merged.ContainerMemoryMB)
				require.Equal(s.T(), 1.0, merged.ContainerCPUs)
			},
		},
		{
			name: "MemoryEmbeddings/Override",
			projectJSON: `{
				"memory": {
					"embeddings": {
						"provider": "ollama",
						"model": "mxbai-embed-large",
						"ollama_url": "http://gpu-server:11434"
					}
				}
			}`,
			mainCfg: &Config{
				Memory: MemoryConfig{
					Enabled: true,
					Embeddings: EmbeddingsConfig{
						Provider:  "ollama",
						Model:     "nomic-embed-text",
						OllamaURL: "http://localhost:11434",
					},
				},
			},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), "ollama", merged.Memory.Embeddings.Provider)
				require.Equal(s.T(), "mxbai-embed-large", merged.Memory.Embeddings.Model)
				require.Equal(s.T(), "http://gpu-server:11434", merged.Memory.Embeddings.OllamaURL)
				require.Equal(s.T(), "nomic-embed-text", main.Memory.Embeddings.Model)
			},
		},
		{
			name:        "MemoryEmbeddings/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				Memory: MemoryConfig{
					Enabled: true,
					Embeddings: EmbeddingsConfig{
						Provider:  "ollama",
						Model:     "nomic-embed-text",
						OllamaURL: "http://localhost:11434",
					},
				},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "ollama", merged.Memory.Embeddings.Provider)
				require.Equal(s.T(), "nomic-embed-text", merged.Memory.Embeddings.Model)
			},
		},
		{
			name:        "QualityMaxFiles/Override",
			projectJSON: `{"quality": {"max_files": 12000}}`,
			mainCfg:     &Config{Quality: QualityConfig{MaxFiles: 5000}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), 12000, merged.Quality.MaxFiles)
				require.Equal(s.T(), 5000, main.Quality.MaxFiles)
			},
		},
		{
			name:        "QualityMaxFiles/AbsentKeepsGlobal",
			projectJSON: `{"quality": {}}`,
			mainCfg:     &Config{Quality: QualityConfig{MaxFiles: 5000}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 5000, merged.Quality.MaxFiles)
			},
		},
		{
			name:        "QualityExcludePaths/Override",
			projectJSON: `{"quality": {"exclude_paths": ["./generated/**"]}}`,
			mainCfg:     &Config{Quality: QualityConfig{ExcludePaths: []string{"./vendor/**"}}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), []string{"./generated/**"}, merged.Quality.ExcludePaths)
				require.Equal(s.T(), []string{"./vendor/**"}, main.Quality.ExcludePaths)
			},
		},
		{
			name:        "QualityExcludePaths/AbsentKeepsGlobal",
			projectJSON: `{"quality": {}}`,
			mainCfg:     &Config{Quality: QualityConfig{ExcludePaths: []string{"./vendor/**"}}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), []string{"./vendor/**"}, merged.Quality.ExcludePaths)
			},
		},
		{
			name:        "QualityRules/OverrideThreshold",
			projectJSON: `{"quality": {"rules": {"signal_floor": {"threshold": 6000}}}}`,
			mainCfg: &Config{Quality: QualityConfig{Rules: map[string]QualityRuleConfig{
				"signal_floor": {Enabled: true, Threshold: 5000},
			}}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), 6000.0, merged.Quality.Rules["signal_floor"].Threshold)
				require.True(s.T(), merged.Quality.Rules["signal_floor"].Enabled)
				require.Equal(s.T(), 5000.0, main.Quality.Rules["signal_floor"].Threshold)
			},
		},
		{
			name:        "QualityRules/DisableOverridesGlobalEnabled",
			projectJSON: `{"quality": {"rules": {"no_import_cycles": {"enabled": false}}}}`,
			mainCfg: &Config{Quality: QualityConfig{Rules: map[string]QualityRuleConfig{
				"no_import_cycles": {Enabled: true},
			}}},
			assert: func(merged, _ *Config) {
				require.False(s.T(), merged.Quality.Rules["no_import_cycles"].Enabled)
			},
		},
		{
			name:        "QualityRules/AbsentRuleKeepsGlobalEntry",
			projectJSON: `{"quality": {"rules": {"signal_floor": {"threshold": 7000}}}}`,
			mainCfg: &Config{Quality: QualityConfig{Rules: map[string]QualityRuleConfig{
				"signal_floor":     {Enabled: true, Threshold: 5000},
				"no_import_cycles": {Enabled: true},
			}}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 7000.0, merged.Quality.Rules["signal_floor"].Threshold)
				require.True(s.T(), merged.Quality.Rules["no_import_cycles"].Enabled)
			},
		},
		{
			name:        "QualityRules/NewRuleAddedToEmptyGlobal",
			projectJSON: `{"quality": {"rules": {"parse_fail": {"threshold": 0.005}}}}`,
			mainCfg:     &Config{Quality: QualityConfig{}},
			assert: func(merged, _ *Config) {
				require.NotNil(s.T(), merged.Quality.Rules)
				rc := merged.Quality.Rules["parse_fail"]
				require.Equal(s.T(), 0.005, rc.Threshold)
				require.True(s.T(), rc.Enabled)
			},
		},
		{
			name:        "QualityComplexity/SingleFieldOverride",
			projectJSON: `{"quality": {"complexity": {"cyclomatic_t": 12}}}`,
			mainCfg: &Config{Quality: QualityConfig{Complexity: QualityComplexityConfig{
				CyclomaticT: 8, CognitiveT: 15, NestingT: 3, ParamsT: 5, LOCT: 50,
			}}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), 12, merged.Quality.Complexity.CyclomaticT)
				// Other dimensions survive unchanged.
				require.Equal(s.T(), 15, merged.Quality.Complexity.CognitiveT)
				require.Equal(s.T(), 3, merged.Quality.Complexity.NestingT)
				require.Equal(s.T(), 8, main.Quality.Complexity.CyclomaticT)
			},
		},
		{
			name:        "QualityComplexity/AllFieldsOverride",
			projectJSON: `{"quality": {"complexity": {"cyclomatic_t": 12, "cognitive_t": 20, "nesting_t": 5, "params_t": 6, "loc_t": 80}}}`,
			mainCfg:     &Config{Quality: QualityConfig{}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), QualityComplexityConfig{
					CyclomaticT: 12, CognitiveT: 20, NestingT: 5, ParamsT: 6, LOCT: 80,
				}, merged.Quality.Complexity)
			},
		},
		{
			name:        "QualityComplexity/AbsentKeepsGlobal",
			projectJSON: `{"quality": {}}`,
			mainCfg: &Config{Quality: QualityConfig{Complexity: QualityComplexityConfig{
				CyclomaticT: 8, CognitiveT: 15,
			}}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 8, merged.Quality.Complexity.CyclomaticT)
				require.Equal(s.T(), 15, merged.Quality.Complexity.CognitiveT)
			},
		},
		{
			name:        "QualityClones/SingleFieldOverride",
			projectJSON: `{"quality": {"clones": {"max_distance": 1}}}`,
			mainCfg: &Config{Quality: QualityConfig{Clones: QualityClonesConfig{
				MinLOC: 10, MaxDistance: 3,
			}}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), 1, merged.Quality.Clones.MaxDistance)
				require.Equal(s.T(), 10, merged.Quality.Clones.MinLOC)
				require.Equal(s.T(), 3, main.Quality.Clones.MaxDistance)
			},
		},
		{
			name:        "QualityClones/BothFieldsOverride",
			projectJSON: `{"quality": {"clones": {"min_loc": 7, "max_distance": 2}}}`,
			mainCfg:     &Config{Quality: QualityConfig{}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), QualityClonesConfig{MinLOC: 7, MaxDistance: 2}, merged.Quality.Clones)
			},
		},
		{
			name:        "QualityClones/AbsentKeepsGlobal",
			projectJSON: `{"quality": {}}`,
			mainCfg: &Config{Quality: QualityConfig{Clones: QualityClonesConfig{
				MinLOC: 10, MaxDistance: 3,
			}}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), QualityClonesConfig{MinLOC: 10, MaxDistance: 3}, merged.Quality.Clones)
			},
		},
		{
			name:        "Envs/Merged",
			projectJSON: `{"envs": {"PROJECT_KEY": "proj-val", "SHARED": "proj"}}`,
			mainCfg: &Config{
				Envs: map[string]string{"GLOBAL_KEY": "global-val", "SHARED": "global"},
			},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), "global-val", merged.Envs["GLOBAL_KEY"])
				require.Equal(s.T(), "proj-val", merged.Envs["PROJECT_KEY"])
				require.Equal(s.T(), "proj", merged.Envs["SHARED"])
				require.Equal(s.T(), "global", main.Envs["SHARED"])
				require.Empty(s.T(), main.Envs["PROJECT_KEY"])
			},
		},
		{
			name:        "Envs/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				Envs: map[string]string{"GLOBAL_KEY": "global-val"},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "global-val", merged.Envs["GLOBAL_KEY"])
			},
		},
		{
			name: "Templates/Merge",
			projectJSON: `{
				"task_templates": [
					{
						"name": "daily-summary",
						"description": "Overridden daily summary",
						"schedule": "0 18 * * *",
						"type": "cron",
						"prompt": "New summary prompt"
					},
					{
						"name": "project-only",
						"description": "Project-specific template",
						"schedule": "*/10 * * * *",
						"type": "cron",
						"prompt": "Project task"
					}
				]
			}`,
			mainCfg: &Config{
				TaskTemplates: []TaskTemplate{
					{Name: "daily-summary", Description: "Daily summary", Schedule: "0 17 * * *", Type: "cron", Prompt: "Generate summary"},
					{Name: "global-only", Description: "Global template", Schedule: "0 9 * * *", Type: "cron", Prompt: "Global task"},
				},
			},
			assert: func(merged, main *Config) {
				require.Len(s.T(), merged.TaskTemplates, 3)
				require.Equal(s.T(), "daily-summary", merged.TaskTemplates[0].Name)
				require.Equal(s.T(), "Overridden daily summary", merged.TaskTemplates[0].Description)
				require.Equal(s.T(), "0 18 * * *", merged.TaskTemplates[0].Schedule)
				require.Equal(s.T(), "New summary prompt", merged.TaskTemplates[0].Prompt)
				require.Equal(s.T(), "global-only", merged.TaskTemplates[1].Name)
				require.Equal(s.T(), "Global task", merged.TaskTemplates[1].Prompt)
				require.Equal(s.T(), "project-only", merged.TaskTemplates[2].Name)
				require.Equal(s.T(), "Project task", merged.TaskTemplates[2].Prompt)
				require.Len(s.T(), main.TaskTemplates, 2)
				require.Equal(s.T(), "Generate summary", main.TaskTemplates[0].Prompt)
			},
		},
		{
			name:        "Templates/Empty",
			projectJSON: `{}`,
			mainCfg: &Config{
				TaskTemplates: []TaskTemplate{
					{Name: "global", Description: "Global", Schedule: "0 9 * * *", Type: "cron", Prompt: "Do global"},
				},
			},
			assert: func(merged, _ *Config) {
				require.Len(s.T(), merged.TaskTemplates, 1)
				require.Equal(s.T(), "global", merged.TaskTemplates[0].Name)
			},
		},
		{
			name: "MemoryPaths/Appended",
			projectJSON: `{
				"memory": {
					"paths": ["./docs/arch.md"]
				}
			}`,
			mainCfg: &Config{
				Memory: MemoryConfig{Paths: []string{"/global/knowledge"}},
			},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), []string{"/global/knowledge", "./docs/arch.md"}, merged.Memory.Paths)
				require.Len(s.T(), main.Memory.Paths, 1)
			},
		},
		{
			name:        "MemoryPaths/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				Memory: MemoryConfig{Paths: []string{"/global/knowledge"}},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), []string{"/global/knowledge"}, merged.Memory.Paths)
			},
		},
		{
			name: "MaxChunkChars/Override",
			projectJSON: `{
				"memory": {
					"max_chunk_chars": 12000
				}
			}`,
			mainCfg: &Config{
				Memory: MemoryConfig{MaxChunkChars: 6000},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 12000, merged.Memory.MaxChunkChars)
			},
		},
		{
			name:        "MaxChunkChars/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				Memory: MemoryConfig{MaxChunkChars: 6000},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 6000, merged.Memory.MaxChunkChars)
			},
		},
		{
			name: "Permissions/Override",
			projectJSON: `{
				"permissions": {
					"owners":  {"users": [], "roles": []},
					"members": {"users": [], "roles": []}
				}
			}`,
			mainCfg: &Config{
				Permissions: types.Permissions{
					Owners:  types.RoleGrant{Users: []string{"U1"}, Roles: []string{"R1"}},
					Members: types.RoleGrant{Users: []string{"U2"}},
				},
			},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.Permissions.IsEmpty())
			},
		},
		{
			name:        "Permissions/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				Permissions: types.Permissions{
					Owners: types.RoleGrant{Users: []string{"U1"}},
				},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), []string{"U1"}, merged.Permissions.Owners.Users)
			},
		},
		{
			name:        "Browser/Override",
			projectJSON: `{"browser": {"enabled": false, "mode": "host", "host_cdp_port": 9333}}`,
			mainCfg:     &Config{Browser: BrowserConfig{Enabled: true, Mode: "docker", HostCDPPort: 9222}},
			assert: func(merged, _ *Config) {
				require.False(s.T(), merged.Browser.Enabled)
				require.Equal(s.T(), "host", merged.Browser.Mode)
				require.Equal(s.T(), 9333, merged.Browser.HostCDPPort)
			},
		},
		{
			name:        "Browser/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{Browser: BrowserConfig{Enabled: true, Mode: "docker", HostCDPPort: 9222}},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.Browser.Enabled)
				require.Equal(s.T(), "docker", merged.Browser.Mode)
				require.Equal(s.T(), 9222, merged.Browser.HostCDPPort)
			},
		},
		{
			name:        "KeepMCPConfigs/Override",
			projectJSON: `{"keep_mcp_configs": true}`,
			mainCfg:     &Config{KeepMCPConfigs: false},
			assert: func(merged, main *Config) {
				require.True(s.T(), merged.KeepMCPConfigs)
				require.False(s.T(), main.KeepMCPConfigs)
			},
		},
		{
			name:        "KeepMCPConfigs/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{KeepMCPConfigs: true},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.KeepMCPConfigs)
			},
		},
		{
			name: "Workflows/Merge",
			projectJSON: `{
				"workflows": [
					{
						"name": "code-review",
						"description": "Overridden review",
						"nodes": [{"id": "diff", "type": "bash", "script": "git diff"}]
					},
					{
						"name": "project-only",
						"description": "Project workflow",
						"nodes": [{"id": "run", "type": "bash", "script": "make run"}]
					}
				]
			}`,
			mainCfg: &Config{
				Workflows: []WorkflowDef{
					{Name: "code-review", Description: "Global review", Nodes: []NodeDef{{ID: "diff", Type: NodeTypeBash, Script: "git diff main"}}},
					{Name: "global-only", Description: "Global workflow", Nodes: []NodeDef{{ID: "test", Type: NodeTypeBash, Script: "make test"}}},
				},
			},
			assert: func(merged, main *Config) {
				require.Len(s.T(), merged.Workflows, 3)
				require.Equal(s.T(), "code-review", merged.Workflows[0].Name)
				require.Equal(s.T(), "Overridden review", merged.Workflows[0].Description)
				require.Equal(s.T(), "git diff", merged.Workflows[0].Nodes[0].Script)
				require.Equal(s.T(), "global-only", merged.Workflows[1].Name)
				require.Equal(s.T(), "project-only", merged.Workflows[2].Name)
				require.Len(s.T(), main.Workflows, 2)
				require.Equal(s.T(), "git diff main", main.Workflows[0].Nodes[0].Script)
			},
		},
		{
			name:        "Workflows/Empty",
			projectJSON: `{}`,
			mainCfg: &Config{
				Workflows: []WorkflowDef{
					{Name: "global", Description: "Global", Nodes: []NodeDef{{ID: "test", Type: NodeTypeBash, Script: "make test"}}},
				},
			},
			assert: func(merged, _ *Config) {
				require.Len(s.T(), merged.Workflows, 1)
				require.Equal(s.T(), "global", merged.Workflows[0].Name)
			},
		},
		{
			name:        "WorkflowConcurrency/Override",
			projectJSON: `{"workflow_concurrency": {"max_concurrent_runs": 3, "max_concurrent_nodes": 8}}`,
			mainCfg: &Config{
				WorkflowConcurrency: WorkflowConcurrency{MaxConcurrentRuns: 5, MaxConcurrentNodes: 10},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 3, merged.WorkflowConcurrency.MaxConcurrentRuns)
				require.Equal(s.T(), 8, merged.WorkflowConcurrency.MaxConcurrentNodes)
			},
		},
		{
			name:        "WorkflowConcurrency/PartialOverride",
			projectJSON: `{"workflow_concurrency": {"max_concurrent_runs": 2}}`,
			mainCfg: &Config{
				WorkflowConcurrency: WorkflowConcurrency{MaxConcurrentRuns: 5, MaxConcurrentNodes: 10},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 2, merged.WorkflowConcurrency.MaxConcurrentRuns)
				require.Equal(s.T(), 10, merged.WorkflowConcurrency.MaxConcurrentNodes)
			},
		},
		{
			name:        "WorkflowConcurrency/NoOverride",
			projectJSON: `{}`,
			mainCfg: &Config{
				WorkflowConcurrency: WorkflowConcurrency{MaxConcurrentRuns: 5, MaxConcurrentNodes: 10},
			},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), 5, merged.WorkflowConcurrency.MaxConcurrentRuns)
				require.Equal(s.T(), 10, merged.WorkflowConcurrency.MaxConcurrentNodes)
			},
		},
		{
			name:        "GitHub/Override",
			projectJSON: `{"github": {"gh_user": "radutopala"}}`,
			mainCfg:     &Config{GitHub: GitHubConfig{GHUser: "radutopalama"}},
			assert: func(merged, main *Config) {
				require.Equal(s.T(), "radutopala", merged.GitHub.GHUser)
				require.Equal(s.T(), "radutopalama", main.GitHub.GHUser)
			},
		},
		{
			name:        "GitHub/EmptyKeepsGlobal",
			projectJSON: `{"github": {"gh_user": ""}}`,
			mainCfg:     &Config{GitHub: GitHubConfig{GHUser: "radutopalama"}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "radutopalama", merged.GitHub.GHUser)
			},
		},
		{
			name:        "GitHub/NoOverride",
			projectJSON: `{}`,
			mainCfg:     &Config{GitHub: GitHubConfig{GHUser: "radutopalama"}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "radutopalama", merged.GitHub.GHUser)
			},
		},
		{
			name:        "Review/EnableOverridesGlobal",
			projectJSON: `{"review": {"enabled": true}}`,
			mainCfg:     &Config{Review: ReviewConfig{Enabled: false}},
			assert: func(merged, main *Config) {
				require.True(s.T(), merged.Review.Enabled)
				require.False(s.T(), main.Review.Enabled)
			},
		},
		{
			name:        "Review/DisableOverridesGlobal",
			projectJSON: `{"review": {"enabled": false}}`,
			mainCfg:     &Config{Review: ReviewConfig{Enabled: true}},
			assert: func(merged, _ *Config) {
				require.False(s.T(), merged.Review.Enabled)
			},
		},
		{
			name:        "Review/UnsetKeepsGlobal",
			projectJSON: `{"review": {}}`,
			mainCfg:     &Config{Review: ReviewConfig{Enabled: true}},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.Review.Enabled)
			},
		},
		{
			name:        "Review/NoOverrideKeepsGlobal",
			projectJSON: `{}`,
			mainCfg:     &Config{Review: ReviewConfig{Enabled: true}},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.Review.Enabled)
			},
		},
		{
			name:        "PlaygroundShare/EnableOverridesGlobal",
			projectJSON: `{"playground_share": {"enabled": true}}`,
			mainCfg:     &Config{PlaygroundShare: PlaygroundShareConfig{Enabled: false}},
			assert: func(merged, main *Config) {
				require.True(s.T(), merged.PlaygroundShare.Enabled)
				require.False(s.T(), main.PlaygroundShare.Enabled)
			},
		},
		{
			name:        "PlaygroundShare/DisableOverridesGlobal",
			projectJSON: `{"playground_share": {"enabled": false}}`,
			mainCfg:     &Config{PlaygroundShare: PlaygroundShareConfig{Enabled: true}},
			assert: func(merged, _ *Config) {
				require.False(s.T(), merged.PlaygroundShare.Enabled)
			},
		},
		{
			name:        "PlaygroundShare/UnsetKeepsGlobal",
			projectJSON: `{"playground_share": {}}`,
			mainCfg:     &Config{PlaygroundShare: PlaygroundShareConfig{Enabled: true}},
			assert: func(merged, _ *Config) {
				require.True(s.T(), merged.PlaygroundShare.Enabled)
			},
		},
		{
			name:        "Review/PromptOverride",
			projectJSON: `{"review": {"prompt": "project prompt"}}`,
			mainCfg:     &Config{Review: ReviewConfig{Prompt: "global prompt"}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "project prompt", merged.Review.Prompt)
			},
		},
		{
			name:        "Review/EmptyPromptKeepsGlobal",
			projectJSON: `{"review": {"prompt": ""}}`,
			mainCfg:     &Config{Review: ReviewConfig{Prompt: "global prompt"}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "global prompt", merged.Review.Prompt)
			},
		},
		{
			name:        "Review/PromptPathOverride",
			projectJSON: `{"review": {"prompt_path": "project/path.md"}}`,
			mainCfg:     &Config{Review: ReviewConfig{PromptPath: "global/path.md"}},
			assert: func(merged, _ *Config) {
				require.Equal(s.T(), "project/path.md", merged.Review.PromptPath)
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.setupProjectReadFile(tt.projectJSON)
			merged, err := s.loader.loadProjectConfig("/project", tt.mainCfg)
			require.NoError(s.T(), err)
			tt.assert(merged, tt.mainCfg)
		})
	}
}
