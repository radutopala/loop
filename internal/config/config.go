package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/types"
	"github.com/tailscale/hujson"
)

// MCPServerConfig represents a single MCP server entry in the config.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// TaskTemplate represents a reusable task template with schedule and prompt.
type TaskTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Type        string `json:"type"`
	Prompt      string `json:"prompt"`
	PromptPath  string `json:"prompt_path"`
}

// ResolvePrompt returns the prompt text for the template.
// If Prompt is set, it is returned directly.
// If PromptPath is set, the file is read from {loopDir}/templates/{prompt_path}.
// Exactly one of Prompt or PromptPath must be set.
func (t *TaskTemplate) ResolvePrompt(loopDir string) (string, error) {
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

// Config holds all application configuration loaded from config.json.
type Config struct {
	PlatformType         types.Platform
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
	DiscordGuildID       string
	LoopDir              string
	MCPServers           map[string]MCPServerConfig
	TaskTemplates        []TaskTemplate
	Mounts               []string
	Envs                 map[string]string
	ClaudeModel          string
}

// Platform returns the configured chat platform.
func (c *Config) Platform() types.Platform {
	return c.PlatformType
}

// jsonConfig is an intermediate struct for JSON unmarshalling.
// Pointer types for numerics distinguish "missing" (nil) from "zero".
type jsonConfig struct {
	Platform              string         `json:"platform"`
	DiscordToken          string         `json:"discord_token"`
	DiscordAppID          string         `json:"discord_app_id"`
	SlackBotToken         string         `json:"slack_bot_token"`
	SlackAppToken         string         `json:"slack_app_token"`
	ClaudeCodeOAuthToken  string         `json:"claude_code_oauth_token"`
	DiscordGuildID        string         `json:"discord_guild_id"`
	LogFile               string         `json:"log_file"`
	LogLevel              string         `json:"log_level"`
	LogFormat             string         `json:"log_format"`
	DBPath                string         `json:"db_path"`
	ContainerImage        string         `json:"container_image"`
	ContainerTimeoutSec   *int           `json:"container_timeout_sec"`
	ContainerMemoryMB     *int64         `json:"container_memory_mb"`
	ContainerCPUs         *float64       `json:"container_cpus"`
	ContainerKeepAliveSec *int           `json:"container_keep_alive_sec"`
	PollIntervalSec       *int           `json:"poll_interval_sec"`
	APIAddr               string         `json:"api_addr"`
	MCP                   *jsonMCPConfig `json:"mcp"`
	TaskTemplates         []TaskTemplate `json:"task_templates"`
	Mounts                []string       `json:"mounts"`
	Envs                  map[string]any `json:"envs"`
	ClaudeModel           string         `json:"claude_model"`
	ClaudeBinPath         string         `json:"claude_bin_path"`
}

type jsonMCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

// userHomeDir is a package-level variable to allow overriding in tests.
var userHomeDir = os.UserHomeDir

// readFile is a package-level variable to allow overriding in tests.
var readFile = os.ReadFile

// TestSetReadFile is a test helper to override the readFile function.
// Returns the original function so it can be restored.
func TestSetReadFile(fn func(string) ([]byte, error)) func(string) ([]byte, error) {
	orig := readFile
	readFile = fn
	return orig
}

// Load reads configuration from ~/.loop/config.json and returns a Config.
func Load() (*Config, error) {
	home, err := userHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	loopDir := filepath.Join(home, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	data, err := readFile(configPath)
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
		PlatformType:         types.Platform(strings.ToLower(jc.Platform)),
		DiscordToken:         jc.DiscordToken,
		DiscordAppID:         jc.DiscordAppID,
		SlackBotToken:        jc.SlackBotToken,
		SlackAppToken:        jc.SlackAppToken,
		ClaudeBinPath:        stringDefault(jc.ClaudeBinPath, "claude"),
		ClaudeCodeOAuthToken: jc.ClaudeCodeOAuthToken,
		DiscordGuildID:       jc.DiscordGuildID,
		LogFile:              stringDefault(jc.LogFile, filepath.Join(loopDir, "loop.log")),
		LogLevel:             stringDefault(jc.LogLevel, "info"),
		LogFormat:            stringDefault(jc.LogFormat, "text"),
		DBPath:               stringDefault(jc.DBPath, filepath.Join(loopDir, "loop.db")),
		ContainerImage:       stringDefault(jc.ContainerImage, "loop-agent:latest"),
		ContainerTimeout:     time.Duration(intPtrDefault(jc.ContainerTimeoutSec, 3600)) * time.Second,
		ContainerMemoryMB:    int64PtrDefault(jc.ContainerMemoryMB, 512),
		ContainerCPUs:        floatPtrDefault(jc.ContainerCPUs, 1.0),
		ContainerKeepAlive:   time.Duration(intPtrDefault(jc.ContainerKeepAliveSec, 300)) * time.Second,
		PollInterval:         time.Duration(intPtrDefault(jc.PollIntervalSec, 30)) * time.Second,
		APIAddr:              stringDefault(jc.APIAddr, ":8222"),
		LoopDir:              loopDir,
		ClaudeModel:          jc.ClaudeModel,
	}

	if jc.MCP != nil && len(jc.MCP.Servers) > 0 {
		cfg.MCPServers = jc.MCP.Servers
	}

	cfg.TaskTemplates = jc.TaskTemplates
	cfg.Mounts = jc.Mounts
	cfg.Envs = stringifyEnvs(jc.Envs)

	switch cfg.PlatformType {
	case types.PlatformDiscord:
		if cfg.DiscordToken == "" || cfg.DiscordAppID == "" {
			return nil, fmt.Errorf("platform \"discord\" requires discord_token and discord_app_id")
		}
	case types.PlatformSlack:
		if cfg.SlackBotToken == "" || cfg.SlackAppToken == "" {
			return nil, fmt.Errorf("platform \"slack\" requires slack_bot_token and slack_app_token")
		}
	case "":
		return nil, fmt.Errorf("missing required config: \"platform\" must be set to \"discord\" or \"slack\"")
	default:
		return nil, fmt.Errorf("unsupported platform %q: must be \"discord\" or \"slack\"", cfg.PlatformType)
	}

	return cfg, nil
}

func stringDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

func intPtrDefault(val *int, def int) int {
	if val != nil {
		return *val
	}
	return def
}

func int64PtrDefault(val *int64, def int64) int64 {
	if val != nil {
		return *val
	}
	return def
}

func floatPtrDefault(val *float64, def float64) float64 {
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
	Mounts            []string       `json:"mounts"`
	Envs              map[string]any `json:"envs"`
	MCP               *jsonMCPConfig `json:"mcp"`
	ClaudeModel       string         `json:"claude_model"`
	ClaudeBinPath     string         `json:"claude_bin_path"`
	ContainerImage    string         `json:"container_image"`
	ContainerMemoryMB *int64         `json:"container_memory_mb"`
	ContainerCPUs     *float64       `json:"container_cpus"`
	TaskTemplates     []TaskTemplate `json:"task_templates"`
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
	projectConfigPath := filepath.Join(workDir, ".loop", "config.json")

	data, err := readFile(projectConfigPath)
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

	if pc.ContainerImage != "" {
		merged.ContainerImage = pc.ContainerImage
	}
	if pc.ContainerMemoryMB != nil {
		merged.ContainerMemoryMB = *pc.ContainerMemoryMB
	}
	if pc.ContainerCPUs != nil {
		merged.ContainerCPUs = *pc.ContainerCPUs
	}

	// Merge envs: project takes precedence over global
	if len(pc.Envs) > 0 {
		mergedEnvs := make(map[string]string)
		maps.Copy(mergedEnvs, mainConfig.Envs)
		maps.Copy(mergedEnvs, stringifyEnvs(pc.Envs))
		merged.Envs = mergedEnvs
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

	return &merged, nil
}
