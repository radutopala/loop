package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tailscale/hujson"

	"github.com/radutopala/loop/internal/types"
)

// jsonConfig is an intermediate struct for JSON unmarshalling.
// Pointer types for numerics distinguish "missing" (nil) from "zero".
type jsonConfig struct {
	Platforms                                []string               `json:"platforms"`
	DiscordToken                             string                 `json:"discord_token"`
	DiscordAppID                             string                 `json:"discord_app_id"`
	SlackBotToken                            string                 `json:"slack_bot_token"`
	SlackAppToken                            string                 `json:"slack_app_token"`
	ClaudeCodeOAuthToken                     string                 `json:"claude_code_oauth_token"`
	AnthropicAPIKey                          string                 `json:"anthropic_api_key"`
	DiscordGuildID                           string                 `json:"discord_guild_id"`
	LogFile                                  string                 `json:"log_file"`
	LogLevel                                 string                 `json:"log_level"`
	LogFormat                                string                 `json:"log_format"`
	DBPath                                   string                 `json:"db_path"`
	ContainerImage                           string                 `json:"container_image"`
	ContainerTimeoutSec                      *int                   `json:"container_timeout_sec"`
	ContainerMemoryMB                        *int64                 `json:"container_memory_mb"`
	ContainerCPUs                            *float64               `json:"container_cpus"`
	ContainerKeepAliveSec                    *int                   `json:"container_keep_alive_sec"`
	PollIntervalSec                          *int                   `json:"poll_interval_sec"`
	APIAddr                                  string                 `json:"api_addr"`
	APIAdvertiseURL                          string                 `json:"api_advertise_url"`
	MCP                                      *jsonMCPConfig         `json:"mcp"`
	TaskTemplates                            []TaskTemplate         `json:"task_templates"`
	Workflows                                []WorkflowDef          `json:"workflows"`
	WorkflowConcurrency                      *WorkflowConcurrency   `json:"workflow_concurrency"`
	PromptShortcuts                          []PromptShortcut       `json:"prompt_shortcuts"`
	BashShortcuts                            []BashShortcut         `json:"bash_shortcuts"`
	Mounts                                   []string               `json:"mounts"`
	CopyFiles                                []string               `json:"copy_files"`
	Envs                                     map[string]any         `json:"envs"`
	ClaudeModel                              string                 `json:"claude_model"`
	ClaudeBinPath                            string                 `json:"claude_bin_path"`
	ClaudeDangerouslyLoadDevelopmentChannels *bool                  `json:"claude_dangerously_load_development_channels"`
	ClaudeBatchDisallowedTools               []string               `json:"claude_batch_disallowed_tools"`
	ClaudeRetry                              *jsonAgentRetryConfig  `json:"claude_retry"`
	KeepMCPConfigs                           *bool                  `json:"keep_mcp_configs"`
	WorkflowBashLocal                        *bool                  `json:"workflow_bash_local"`
	Browser                                  *jsonBrowserConfig     `json:"browser"`
	Memory                                   *jsonMemoryConfig      `json:"memory"`
	Quality                                  *jsonQualityConfig     `json:"quality"`
	Permissions                              *jsonPermissionsConfig `json:"permissions"`
	Desktop                                  *DesktopConfig         `json:"desktop"`
	Gates                                    *jsonGatesConfig       `json:"gates"`
	GitHub                                   *GitHubConfig          `json:"github"`
	Review                                   *jsonReviewConfig      `json:"review"`
}

// jsonMemoryConfig is the JSON representation of the memory block.
type jsonMemoryConfig struct {
	Enabled            *bool             `json:"enabled"`
	Paths              []string          `json:"paths"`
	MaxChunkChars      int               `json:"max_chunk_chars"`
	ReindexIntervalSec int               `json:"reindex_interval_sec"`
	Embeddings         *EmbeddingsConfig `json:"embeddings"`
}

// jsonQualityConfig is the JSON representation of the quality block.
type jsonQualityConfig struct {
	MaxFiles     *int                             `json:"max_files"`
	ExcludePaths []string                         `json:"exclude_paths"`
	Rules        map[string]jsonQualityRuleConfig `json:"rules"`
	Complexity   *jsonQualityComplexityConfig     `json:"complexity"`
	Clones       *jsonQualityClonesConfig         `json:"clones"`
}

// jsonQualityComplexityConfig is the JSON representation of the
// complexity-thresholds block. Pointer fields disambiguate "absent" from
// "explicitly zero" — zero on any T means "use default".
type jsonQualityComplexityConfig struct {
	CyclomaticT *int `json:"cyclomatic_t"`
	CognitiveT  *int `json:"cognitive_t"`
	NestingT    *int `json:"nesting_t"`
	ParamsT     *int `json:"params_t"`
	LOCT        *int `json:"loc_t"`
}

// jsonQualityClonesConfig is the JSON representation of the
// clone-detector block. MaxDistance is a pointer so 0 (exact match)
// stays distinguishable from "not configured".
type jsonQualityClonesConfig struct {
	MinLOC      *int `json:"min_loc"`
	MaxDistance *int `json:"max_distance"`
}

// jsonQualityRuleConfig is the JSON representation of one rule's overrides.
// Enabled is a pointer so an absent field falls back to "rule enabled" while
// `"enabled": false` flips the rule off explicitly.
type jsonQualityRuleConfig struct {
	Enabled   *bool   `json:"enabled"`
	Threshold float64 `json:"threshold"`
}

// jsonBrowserConfig is the JSON representation of the browser block.
type jsonBrowserConfig struct {
	Enabled     *bool  `json:"enabled"`
	ChromeImage string `json:"chrome_image"`
	Mode        string `json:"mode"`
	HostCDPPort *int   `json:"host_cdp_port"`
}

// jsonAgentRetryConfig is the JSON representation of the claude_retry block.
type jsonAgentRetryConfig struct {
	MaxAttempts    *int `json:"max_attempts"`
	BackoffBaseSec *int `json:"backoff_base_sec"`
	BackoffMaxSec  *int `json:"backoff_max_sec"`
}

// jsonGatesConfig is the JSON representation of the gates block — the umbrella
// that groups agentgate + docker_proxy and the settings they share.
type jsonGatesConfig struct {
	RateLimits  *types.RateLimits      `json:"rate_limits"`
	Audit       *types.AuditConfig     `json:"audit"`
	Agentgate   *jsonAgentgateConfig   `json:"agentgate"`
	DockerProxy *jsonDockerProxyConfig `json:"docker_proxy"`
}

// jsonAgentgateConfig is the JSON representation of the agentgate block.
type jsonAgentgateConfig struct {
	Enabled         *bool               `json:"enabled"`
	DefaultDecision string              `json:"default_decision"`
	PathRules       []types.PathRule    `json:"path_rules"`
	CommandRules    []types.CommandRule `json:"command_rules"`
	FileRules       []types.FileRule    `json:"file_rules"`
}

// jsonDockerProxyConfig is the JSON representation of the docker_proxy block.
type jsonDockerProxyConfig struct {
	Enabled         *bool                   `json:"enabled"`
	DefaultDecision string                  `json:"default_decision"`
	HTTPRules       []types.HTTPServiceRule `json:"http_rules"`
	BodyRules       []types.BodyRule        `json:"body_rules"`
}

type jsonMCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

// jsonPermissionsConfig is the JSON representation of the permissions block.
type jsonPermissionsConfig struct {
	Owners *struct {
		Users []string `json:"users"`
		Roles []string `json:"roles"`
	} `json:"owners"`
	Members *struct {
		Users []string `json:"users"`
		Roles []string `json:"roles"`
	} `json:"members"`
}

// Loader holds injectable dependencies for loading config files.
type Loader struct {
	userHomeDir func() (string, error)
	readFile    func(string) ([]byte, error)
}

func newLoader() *Loader {
	return &Loader{
		userHomeDir: os.UserHomeDir,
		readFile:    os.ReadFile,
	}
}

// Load reads configuration from ~/.loop/config.json and returns a Config.
func Load() (*Config, error) {
	return newLoader().load()
}

// Reload re-reads ~/.loop/config.json without validating platform credentials.
// Use this for hot-reloading config during runtime (e.g. before container launches)
// where platform tokens are not needed and should not block the reload.
func Reload() (*Config, error) {
	return newLoader().reload()
}

func (l *Loader) load() (*Config, error) {
	cfg, err := l.parse()
	if err != nil {
		return nil, err
	}
	if err := l.validatePlatforms(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (l *Loader) reload() (*Config, error) {
	return l.parse()
}

// parse reads and parses the config file into a Config struct.
// It does NOT validate platform credentials.
func (l *Loader) parse() (*Config, error) {
	home, err := l.userHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	loopDir := filepath.Join(home, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	data, err := l.readFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	standardJSON, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	var jc jsonConfig
	if err := json.Unmarshal(standardJSON, &jc); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	cfg := &Config{
		DiscordToken:                             jc.DiscordToken,
		DiscordAppID:                             jc.DiscordAppID,
		SlackBotToken:                            jc.SlackBotToken,
		SlackAppToken:                            jc.SlackAppToken,
		ClaudeBinPath:                            stringDefault(jc.ClaudeBinPath, "claude"),
		ClaudeCodeOAuthToken:                     jc.ClaudeCodeOAuthToken,
		AnthropicAPIKey:                          jc.AnthropicAPIKey,
		DiscordGuildID:                           jc.DiscordGuildID,
		LogFile:                                  stringDefault(jc.LogFile, filepath.Join(loopDir, "loop.log")),
		LogLevel:                                 stringDefault(jc.LogLevel, "info"),
		LogFormat:                                stringDefault(jc.LogFormat, "text"),
		DBPath:                                   stringDefault(jc.DBPath, filepath.Join(loopDir, "loop.db")),
		ContainerImage:                           stringDefault(jc.ContainerImage, "loop-agent:latest"),
		ContainerTimeout:                         time.Duration(ptrDefault(jc.ContainerTimeoutSec, 43200)) * time.Second,
		ContainerMemoryMB:                        ptrDefault(jc.ContainerMemoryMB, 1024),
		ContainerCPUs:                            ptrDefault(jc.ContainerCPUs, 1.0),
		ContainerKeepAlive:                       time.Duration(ptrDefault(jc.ContainerKeepAliveSec, 300)) * time.Second,
		PollInterval:                             time.Duration(ptrDefault(jc.PollIntervalSec, 30)) * time.Second,
		APIAddr:                                  stringDefault(jc.APIAddr, ":8222"),
		APIAdvertiseURL:                          jc.APIAdvertiseURL,
		LoopDir:                                  loopDir,
		ClaudeModel:                              stringDefault(jc.ClaudeModel, "claude-sonnet-4-6"),
		ClaudeDangerouslyLoadDevelopmentChannels: ptrDefault(jc.ClaudeDangerouslyLoadDevelopmentChannels, false),
		ClaudeBatchDisallowedTools:               sliceDefault(jc.ClaudeBatchDisallowedTools, DefaultBatchDisallowedTools()),
		KeepMCPConfigs:                           ptrDefault(jc.KeepMCPConfigs, false),
		WorkflowBashLocal:                        ptrDefault(jc.WorkflowBashLocal, false),
	}

	// Browser config: nested struct with defaults.
	cfg.Browser = BrowserConfig{
		Enabled:     true,
		ChromeImage: "loop-chrome:latest",
		Mode:        "docker",
		HostCDPPort: 9222,
	}
	if jc.Browser != nil {
		cfg.Browser.Enabled = ptrDefault(jc.Browser.Enabled, true)
		if jc.Browser.ChromeImage != "" {
			cfg.Browser.ChromeImage = jc.Browser.ChromeImage
		}
		if jc.Browser.Mode != "" {
			cfg.Browser.Mode = jc.Browser.Mode
		}
		cfg.Browser.HostCDPPort = ptrDefault(jc.Browser.HostCDPPort, 9222)
	}

	// Agent retry: backoff policy for transient API errors. Defaults applied
	// per-field so a partial block (e.g. only max_attempts) keeps the other
	// defaults.
	cfg.AgentRetry = DefaultAgentRetry()
	if jc.ClaudeRetry != nil {
		if jc.ClaudeRetry.MaxAttempts != nil {
			cfg.AgentRetry.MaxAttempts = *jc.ClaudeRetry.MaxAttempts
		}
		if jc.ClaudeRetry.BackoffBaseSec != nil {
			cfg.AgentRetry.BackoffBase = time.Duration(*jc.ClaudeRetry.BackoffBaseSec) * time.Second
		}
		if jc.ClaudeRetry.BackoffMaxSec != nil {
			cfg.AgentRetry.BackoffMax = time.Duration(*jc.ClaudeRetry.BackoffMaxSec) * time.Second
		}
	}

	// Gates umbrella: agentgate + docker_proxy share a Manager, bearer token,
	// and approval endpoint. Shared settings (RateLimits, Audit) live at the
	// umbrella level. Both layers default to on.
	cfg.Gates = GatesConfig{
		RateLimits: types.RateLimits{Pending: 30, PerMinute: 60, Total: 500},
		Audit:      types.AuditConfig{RetentionDays: 30},
		Agentgate: AgentgateConfig{
			Enabled:         true,
			DefaultDecision: types.DecisionAllow,
			PathRules:       DefaultGatePathRules(),
			CommandRules:    DefaultGateCommandRules(),
			FileRules:       DefaultGateFileRules(),
		},
		DockerProxy: DockerProxyConfig{
			Enabled: true,
			// Allow unmatched routes by default. The explicit Approve list in
			// DefaultDockerProxyHTTPRules covers the lateral-movement ops
			// (exec/attach-start, docker cp); the Deny list covers swarm/
			// secrets/plugins; body rules on create/update hard-block escape
			// primitives. Anything else (wait, resize, rename, attach, …) is
			// safe to pass silently.
			DefaultDecision: types.DecisionAllow,
			HTTPRules:       DefaultDockerProxyHTTPRules(),
			BodyRules:       DefaultDockerProxyBodyRules(),
		},
	}
	if jc.Gates != nil {
		if jc.Gates.RateLimits != nil {
			cfg.Gates.RateLimits = *jc.Gates.RateLimits
		}
		if jc.Gates.Audit != nil {
			cfg.Gates.Audit = *jc.Gates.Audit
		}
		if jc.Gates.Agentgate != nil {
			ag := jc.Gates.Agentgate
			cfg.Gates.Agentgate.Enabled = ptrDefault(ag.Enabled, true)
			if ag.DefaultDecision != "" {
				cfg.Gates.Agentgate.DefaultDecision = types.Decision(ag.DefaultDecision)
			}
			cfg.Gates.Agentgate.PathRules = sliceDefault(ag.PathRules, cfg.Gates.Agentgate.PathRules)
			cfg.Gates.Agentgate.CommandRules = sliceDefault(ag.CommandRules, cfg.Gates.Agentgate.CommandRules)
			cfg.Gates.Agentgate.FileRules = sliceDefault(ag.FileRules, cfg.Gates.Agentgate.FileRules)
		}
		// Docker proxy defaults to the agentgate's enabled state — so disabling
		// agentgate transitively disables the proxy unless project config is
		// explicit.
		cfg.Gates.DockerProxy.Enabled = cfg.Gates.Agentgate.Enabled
		if jc.Gates.DockerProxy != nil {
			dp := jc.Gates.DockerProxy
			cfg.Gates.DockerProxy.Enabled = ptrDefault(dp.Enabled, cfg.Gates.Agentgate.Enabled)
			if dp.DefaultDecision != "" {
				cfg.Gates.DockerProxy.DefaultDecision = types.Decision(dp.DefaultDecision)
			}
			cfg.Gates.DockerProxy.HTTPRules = sliceDefault(dp.HTTPRules, cfg.Gates.DockerProxy.HTTPRules)
			cfg.Gates.DockerProxy.BodyRules = sliceDefault(dp.BodyRules, cfg.Gates.DockerProxy.BodyRules)
		}
	}

	if jc.MCP != nil && len(jc.MCP.Servers) > 0 {
		cfg.MCPServers = jc.MCP.Servers
	}

	cfg.TaskTemplates = jc.TaskTemplates
	cfg.Workflows = jc.Workflows
	if jc.WorkflowConcurrency != nil {
		cfg.WorkflowConcurrency = *jc.WorkflowConcurrency
	}
	cfg.PromptShortcuts = jc.PromptShortcuts
	cfg.BashShortcuts = jc.BashShortcuts
	cfg.Mounts = jc.Mounts
	// ~/.claude.json is no longer defaulted here — the container runner always
	// prepends it (flag-merged) to copy_files so every agent gets it regardless.
	cfg.CopyFiles = jc.CopyFiles
	cfg.Envs = stringifyEnvs(jc.Envs)

	if jc.GitHub != nil {
		cfg.GitHub = *jc.GitHub
	}

	if jc.Review != nil {
		cfg.Review.Enabled = ptrDefault(jc.Review.Enabled, false)
		cfg.Review.Prompt = jc.Review.Prompt
		cfg.Review.PromptPath = jc.Review.PromptPath
	}

	// Memory config: enabled must be explicitly true.
	if jc.Memory != nil {
		cfg.Memory.Enabled = ptrDefault(jc.Memory.Enabled, false)
		cfg.Memory.Paths = jc.Memory.Paths
		cfg.Memory.MaxChunkChars = jc.Memory.MaxChunkChars
		cfg.Memory.ReindexIntervalSec = jc.Memory.ReindexIntervalSec
		if jc.Memory.Embeddings != nil {
			cfg.Memory.Embeddings = EmbeddingsConfig{
				Provider:  jc.Memory.Embeddings.Provider,
				Model:     jc.Memory.Embeddings.Model,
				OllamaURL: stringDefault(jc.Memory.Embeddings.OllamaURL, "http://localhost:11434"),
			}
		}
	}
	if len(cfg.Memory.Paths) == 0 {
		cfg.Memory.Paths = []string{"./memory"}
	}

	// Quality config: MaxFiles / ExcludePaths feed engine.Config;
	// Rules feeds rules.Config (per-rule enable + threshold overrides);
	// Complexity / Clones feed metrics.Config (per-metric thresholds).
	if jc.Quality != nil {
		cfg.Quality.MaxFiles = ptrDefault(jc.Quality.MaxFiles, 0)
		cfg.Quality.ExcludePaths = jc.Quality.ExcludePaths
		if len(jc.Quality.Rules) > 0 {
			cfg.Quality.Rules = make(map[string]QualityRuleConfig, len(jc.Quality.Rules))
			for name, jrc := range jc.Quality.Rules {
				cfg.Quality.Rules[name] = QualityRuleConfig{
					Enabled:   ptrDefault(jrc.Enabled, true),
					Threshold: jrc.Threshold,
				}
			}
		}
		if jc.Quality.Complexity != nil {
			cfg.Quality.Complexity = QualityComplexityConfig{
				CyclomaticT: ptrDefault(jc.Quality.Complexity.CyclomaticT, 0),
				CognitiveT:  ptrDefault(jc.Quality.Complexity.CognitiveT, 0),
				NestingT:    ptrDefault(jc.Quality.Complexity.NestingT, 0),
				ParamsT:     ptrDefault(jc.Quality.Complexity.ParamsT, 0),
				LOCT:        ptrDefault(jc.Quality.Complexity.LOCT, 0),
			}
		}
		if jc.Quality.Clones != nil {
			cfg.Quality.Clones = QualityClonesConfig{
				MinLOC:      ptrDefault(jc.Quality.Clones.MinLOC, 0),
				MaxDistance: ptrDefault(jc.Quality.Clones.MaxDistance, 0),
			}
		}
	}

	if jc.Permissions != nil {
		if jc.Permissions.Owners != nil {
			cfg.Permissions.Owners.Users = jc.Permissions.Owners.Users
			cfg.Permissions.Owners.Roles = jc.Permissions.Owners.Roles
		}
		if jc.Permissions.Members != nil {
			cfg.Permissions.Members.Users = jc.Permissions.Members.Users
			cfg.Permissions.Members.Roles = jc.Permissions.Members.Roles
		}
	}

	// Desktop config (Electron app preferences).
	cfg.Desktop = DesktopConfig{
		AutoSaveOnBlur: false,
		PreviewTabs:    true,
		Islands:        true,
	}
	if jc.Desktop != nil {
		cfg.Desktop = *jc.Desktop
	}

	// Build the platforms list.
	for _, p := range jc.Platforms {
		cfg.Platforms = append(cfg.Platforms, types.Platform(strings.ToLower(p)))
	}

	return cfg, nil
}

// validatePlatforms checks that required credentials are present for each listed platform.
func (l *Loader) validatePlatforms(cfg *Config) error {
	if len(cfg.Platforms) == 0 {
		return fmt.Errorf("missing required config: \"platforms\" must be set")
	}

	for _, p := range cfg.Platforms {
		switch p {
		case types.PlatformDiscord:
			if cfg.DiscordToken == "" || cfg.DiscordAppID == "" {
				return fmt.Errorf("platform \"discord\" requires discord_token and discord_app_id")
			}
		case types.PlatformSlack:
			if cfg.SlackBotToken == "" || cfg.SlackAppToken == "" {
				return fmt.Errorf("platform \"slack\" requires slack_bot_token and slack_app_token")
			}
		case types.PlatformLocal:
			// Local platform requires no external credentials.
		default:
			return fmt.Errorf("unsupported platform %q: must be \"discord\", \"slack\", or \"local\"", p)
		}
	}
	return nil
}

func stringDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

// DefaultBatchDisallowedTools lists Claude Code tools denied via
// `--disallowedTools` in batch (`--print`) agent runs by default. These tools
// rely on a persistent interactive harness that one-shot mode lacks: the
// container exits at end of turn, so they silently park work that never
// resumes. ScheduleWakeup and the Cron* tools schedule future re-invocations
// that never fire. Monitor arms a background watcher whose events are delivered
// across turns — once the agent's single turn ends, the container exits and the
// watch dies mid-stream, dropping the remaining events. Override via the
// `claude_batch_disallowed_tools` config key (global/project/worktree).
func DefaultBatchDisallowedTools() []string {
	return []string{"ScheduleWakeup", "CronCreate", "CronDelete", "CronList", "Monitor"}
}

// DefaultAgentRetry returns the default backoff-retry policy for batch agent
// runs that fail with a transient API error (rate limiting, overload, 5xx).
// Five additional attempts at 5s, 10s, 20s, 40s, 80s (capped at 120s) give a
// rate limit several minutes to clear before the run is surfaced as an error.
func DefaultAgentRetry() AgentRetryConfig {
	return AgentRetryConfig{
		MaxAttempts: 5,
		BackoffBase: 5 * time.Second,
		BackoffMax:  120 * time.Second,
	}
}

func sliceDefault[T any](v []T, def []T) []T {
	if len(v) > 0 {
		return v
	}
	return def
}

func ptrDefault[T comparable](val *T, def T) T {
	if val != nil {
		return *val
	}
	return def
}

// IsNamedVolume returns true if the source part of a mount looks like a Docker
// named volume rather than a host path (no slashes, doesn't start with ~ or .).
func IsNamedVolume(source string) bool {
	return !strings.HasPrefix(source, "/") &&
		!strings.HasPrefix(source, "~") &&
		!strings.HasPrefix(source, ".") &&
		!strings.Contains(source, "/")
}

// stringifyEnvs converts a map of any JSON values to strings.
// Numbers, booleans, etc. are formatted as their natural string representation.
func stringifyEnvs(raw map[string]any) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
