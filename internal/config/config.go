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

// MCPServerConfig represents a single MCP server entry in the config.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// TaskTemplate represents a reusable task template with schedule and prompt.
type TaskTemplate struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Schedule      string `json:"schedule"`
	Type          string `json:"type"`
	Prompt        string `json:"prompt"`
	PromptPath    string `json:"prompt_path"`
	AutoDeleteSec int    `json:"auto_delete_sec"`
}

// ResolvePrompt returns the prompt text for the template.
// If Prompt is set, it is returned directly.
// If PromptPath is set, the file is read from {loopDir}/templates/{prompt_path}.
// Exactly one of Prompt or PromptPath must be set.
func (t *TaskTemplate) ResolvePrompt(loopDir string, readFile func(string) ([]byte, error)) (string, error) {
	if t.Prompt != "" && t.PromptPath != "" {
		return "", fmt.Errorf("template %q: prompt and prompt_path are mutually exclusive", t.Name)
	}
	if t.Prompt == "" && t.PromptPath == "" {
		return "", fmt.Errorf("template %q: one of prompt or prompt_path is required", t.Name)
	}
	if t.Prompt != "" {
		return t.Prompt, nil
	}
	path := filepath.Join(loopDir, "templates", t.PromptPath)
	data, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("reading prompt file for template %q: %w", t.Name, err)
	}
	return string(data), nil
}

// EmbeddingsConfig configures the embedding provider for semantic memory search.
type EmbeddingsConfig struct {
	Provider  string `json:"provider"`   // "ollama"
	Model     string `json:"model"`      // e.g. "nomic-embed-text"
	OllamaURL string `json:"ollama_url"` // default "http://localhost:11434"
}

// BrowserConfig groups all browser-related settings.
type BrowserConfig struct {
	Enabled     bool
	ChromeImage string
	Mode        string // "docker" (default) or "host"
	HostCDPPort int    // default 9222
}

// MemoryConfig groups all memory-related settings: enable flag, paths, and embeddings.
type MemoryConfig struct {
	Enabled            bool
	Paths              []string
	MaxChunkChars      int
	ReindexIntervalSec int
	Embeddings         EmbeddingsConfig
}

// Config holds all application configuration loaded from config.json.
type Config struct {
	Platforms            []types.Platform
	DiscordToken         string
	DiscordAppID         string
	SlackBotToken        string
	SlackAppToken        string
	ClaudeBinPath        string
	DBPath               string
	LogFile              string
	LogLevel             string
	LogFormat            string
	ContainerImage       string
	ContainerTimeout     time.Duration
	ContainerMemoryMB    int64
	ContainerCPUs        float64
	ContainerKeepAlive   time.Duration
	PollInterval         time.Duration
	APIAddr              string
	ClaudeCodeOAuthToken string
	AnthropicAPIKey      string
	DiscordGuildID       string
	LoopDir              string
	MCPServers           map[string]MCPServerConfig
	TaskTemplates        []TaskTemplate
	Mounts               []string
	CopyFiles            []string
	Envs                 map[string]string
	ClaudeModel          string
	StreamingEnabled     bool
	KeepMCPConfigs       bool
	Browser              BrowserConfig
	Memory               MemoryConfig
	Permissions          types.Permissions
	ExtraDirs            []string
}

// HasPlatform returns true if the given platform is enabled.
func (c *Config) HasPlatform(p types.Platform) bool {
	for _, plat := range c.Platforms {
		if plat == p {
			return true
		}
	}
	return false
}

// jsonConfig is an intermediate struct for JSON unmarshalling.
// Pointer types for numerics distinguish "missing" (nil) from "zero".
type jsonConfig struct {
	Platforms             []string               `json:"platforms"`
	DiscordToken          string                 `json:"discord_token"`
	DiscordAppID          string                 `json:"discord_app_id"`
	SlackBotToken         string                 `json:"slack_bot_token"`
	SlackAppToken         string                 `json:"slack_app_token"`
	ClaudeCodeOAuthToken  string                 `json:"claude_code_oauth_token"`
	AnthropicAPIKey       string                 `json:"anthropic_api_key"`
	DiscordGuildID        string                 `json:"discord_guild_id"`
	LogFile               string                 `json:"log_file"`
	LogLevel              string                 `json:"log_level"`
	LogFormat             string                 `json:"log_format"`
	DBPath                string                 `json:"db_path"`
	ContainerImage        string                 `json:"container_image"`
	ContainerTimeoutSec   *int                   `json:"container_timeout_sec"`
	ContainerMemoryMB     *int64                 `json:"container_memory_mb"`
	ContainerCPUs         *float64               `json:"container_cpus"`
	ContainerKeepAliveSec *int                   `json:"container_keep_alive_sec"`
	PollIntervalSec       *int                   `json:"poll_interval_sec"`
	APIAddr               string                 `json:"api_addr"`
	MCP                   *jsonMCPConfig         `json:"mcp"`
	TaskTemplates         []TaskTemplate         `json:"task_templates"`
	Mounts                []string               `json:"mounts"`
	CopyFiles             []string               `json:"copy_files"`
	Envs                  map[string]any         `json:"envs"`
	ClaudeModel           string                 `json:"claude_model"`
	ClaudeBinPath         string                 `json:"claude_bin_path"`
	StreamingEnabled      *bool                  `json:"streaming_enabled"`
	KeepMCPConfigs        *bool                  `json:"keep_mcp_configs"`
	Browser               *jsonBrowserConfig     `json:"browser"`
	Memory                *jsonMemoryConfig      `json:"memory"`
	Permissions           *jsonPermissionsConfig `json:"permissions"`
}

// jsonMemoryConfig is the JSON representation of the memory block.
type jsonMemoryConfig struct {
	Enabled            *bool             `json:"enabled"`
	Paths              []string          `json:"paths"`
	MaxChunkChars      int               `json:"max_chunk_chars"`
	ReindexIntervalSec int               `json:"reindex_interval_sec"`
	Embeddings         *EmbeddingsConfig `json:"embeddings"`
}

// jsonBrowserConfig is the JSON representation of the browser block.
type jsonBrowserConfig struct {
	Enabled     *bool  `json:"enabled"`
	ChromeImage string `json:"chrome_image"`
	Mode        string `json:"mode"`
	HostCDPPort *int   `json:"host_cdp_port"`
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

func (l *Loader) load() (*Config, error) {
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
		DiscordToken:         jc.DiscordToken,
		DiscordAppID:         jc.DiscordAppID,
		SlackBotToken:        jc.SlackBotToken,
		SlackAppToken:        jc.SlackAppToken,
		ClaudeBinPath:        stringDefault(jc.ClaudeBinPath, "claude"),
		ClaudeCodeOAuthToken: jc.ClaudeCodeOAuthToken,
		AnthropicAPIKey:      jc.AnthropicAPIKey,
		DiscordGuildID:       jc.DiscordGuildID,
		LogFile:              stringDefault(jc.LogFile, filepath.Join(loopDir, "loop.log")),
		LogLevel:             stringDefault(jc.LogLevel, "info"),
		LogFormat:            stringDefault(jc.LogFormat, "text"),
		DBPath:               stringDefault(jc.DBPath, filepath.Join(loopDir, "loop.db")),
		ContainerImage:       stringDefault(jc.ContainerImage, "loop-agent:latest"),
		ContainerTimeout:     time.Duration(ptrDefault(jc.ContainerTimeoutSec, 43200)) * time.Second,
		ContainerMemoryMB:    ptrDefault(jc.ContainerMemoryMB, 1024),
		ContainerCPUs:        ptrDefault(jc.ContainerCPUs, 1.0),
		ContainerKeepAlive:   time.Duration(ptrDefault(jc.ContainerKeepAliveSec, 300)) * time.Second,
		PollInterval:         time.Duration(ptrDefault(jc.PollIntervalSec, 30)) * time.Second,
		APIAddr:              stringDefault(jc.APIAddr, ":8222"),
		LoopDir:              loopDir,
		ClaudeModel:          jc.ClaudeModel,
		StreamingEnabled:     ptrDefault(jc.StreamingEnabled, true),
		KeepMCPConfigs:       ptrDefault(jc.KeepMCPConfigs, false),
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

	if jc.MCP != nil && len(jc.MCP.Servers) > 0 {
		cfg.MCPServers = jc.MCP.Servers
	}

	cfg.TaskTemplates = jc.TaskTemplates
	cfg.Mounts = jc.Mounts
	cfg.CopyFiles = sliceDefault(jc.CopyFiles, []string{"~/.claude.json"})
	cfg.Envs = stringifyEnvs(jc.Envs)

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

	// Build the platforms list.
	for _, p := range jc.Platforms {
		cfg.Platforms = append(cfg.Platforms, types.Platform(strings.ToLower(p)))
	}

	if len(cfg.Platforms) == 0 {
		return nil, fmt.Errorf("missing required config: \"platforms\" must be set")
	}

	// Validate credentials for each listed platform.
	for _, p := range cfg.Platforms {
		switch p {
		case types.PlatformDiscord:
			if cfg.DiscordToken == "" || cfg.DiscordAppID == "" {
				return nil, fmt.Errorf("platform \"discord\" requires discord_token and discord_app_id")
			}
		case types.PlatformSlack:
			if cfg.SlackBotToken == "" || cfg.SlackAppToken == "" {
				return nil, fmt.Errorf("platform \"slack\" requires slack_bot_token and slack_app_token")
			}
		case types.PlatformLocal:
			// Local platform requires no external credentials.
		default:
			return nil, fmt.Errorf("unsupported platform %q: must be \"discord\", \"slack\", or \"local\"", p)
		}
	}

	return cfg, nil
}

func stringDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
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

// projectConfig is the structure for project-specific .loop/config.json files.
type projectConfig struct {
	Mounts               []string               `json:"mounts"`
	CopyFiles            []string               `json:"copy_files"`
	Envs                 map[string]any         `json:"envs"`
	MCP                  *jsonMCPConfig         `json:"mcp"`
	ClaudeModel          string                 `json:"claude_model"`
	ClaudeBinPath        string                 `json:"claude_bin_path"`
	ClaudeCodeOAuthToken string                 `json:"claude_code_oauth_token"`
	AnthropicAPIKey      string                 `json:"anthropic_api_key"`
	ContainerImage       string                 `json:"container_image"`
	ContainerMemoryMB    *int64                 `json:"container_memory_mb"`
	ContainerCPUs        *float64               `json:"container_cpus"`
	KeepMCPConfigs       *bool                  `json:"keep_mcp_configs"`
	Browser              *jsonBrowserConfig     `json:"browser"`
	TaskTemplates        []TaskTemplate         `json:"task_templates"`
	Memory               *jsonMemoryConfig      `json:"memory"`
	Permissions          *jsonPermissionsConfig `json:"permissions"`
	ExtraDirs            []string               `json:"extra_dirs"`
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
	_, err := l.readFile(filepath.Join(worktreeDir, ".loop", "config.json"))
	if os.IsNotExist(err) && parentDir != "" {
		return l.loadProjectConfig(parentDir, mainConfig)
	}
	return l.loadProjectConfig(worktreeDir, mainConfig)
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

	// ExtraDirs: project replaces global when set.
	if len(pc.ExtraDirs) > 0 {
		merged.ExtraDirs = pc.ExtraDirs
	}

	return &merged, nil
}
