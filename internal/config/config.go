package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
}

// Config holds all application configuration loaded from config.json.
type Config struct {
	DiscordToken         string
	DiscordAppID         string
	ClaudeBinPath        string
	DBPath               string
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
	ClaudeModel          string
}

// jsonConfig is an intermediate struct for JSON unmarshalling.
// Pointer types for numerics distinguish "missing" (nil) from "zero".
type jsonConfig struct {
	DiscordToken          string         `json:"discord_token"`
	DiscordAppID          string         `json:"discord_app_id"`
	ClaudeCodeOAuthToken  string         `json:"claude_code_oauth_token"`
	DiscordGuildID        string         `json:"discord_guild_id"`
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
	ClaudeModel           string         `json:"claude_model"`
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
		DiscordToken:         jc.DiscordToken,
		DiscordAppID:         jc.DiscordAppID,
		ClaudeBinPath:        "claude",
		ClaudeCodeOAuthToken: jc.ClaudeCodeOAuthToken,
		DiscordGuildID:       jc.DiscordGuildID,
		LogLevel:             stringDefault(jc.LogLevel, "info"),
		LogFormat:            stringDefault(jc.LogFormat, "text"),
		DBPath:               stringDefault(jc.DBPath, filepath.Join(loopDir, "loop.db")),
		ContainerImage:       stringDefault(jc.ContainerImage, "loop-agent:latest"),
		ContainerTimeout:     time.Duration(intPtrDefault(jc.ContainerTimeoutSec, 300)) * time.Second,
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

	var missing []string
	if cfg.DiscordToken == "" {
		missing = append(missing, "discord_token")
	}
	if cfg.DiscordAppID == "" {
		missing = append(missing, "discord_app_id")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required config fields: %v", missing)
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

// projectConfig is the structure for project-specific .loop/config.json files.
// Only mounts, mcp_servers, and claude_model can be specified for security reasons.
type projectConfig struct {
	Mounts      []string       `json:"mounts"`
	MCP         *jsonMCPConfig `json:"mcp"`
	ClaudeModel string         `json:"claude_model"`
}

// LoadProjectConfig loads project-specific config from {workDir}/.loop/config.json
// and merges it with the main config. Only mounts and mcp_servers are loaded from
// the project config for security reasons.
//
// Merge behavior:
// - Mounts: Project mounts are appended to main config mounts
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

	// Merge mounts: append project mounts to main mounts
	// Resolve relative paths relative to workDir
	if len(pc.Mounts) > 0 {
		resolvedMounts := make([]string, 0, len(pc.Mounts))
		for _, mount := range pc.Mounts {
			parts := strings.Split(mount, ":")
			if len(parts) < 2 {
				return nil, fmt.Errorf("invalid mount format in project config: %s", mount)
			}

			hostPath := parts[0]
			// Resolve relative paths relative to workDir
			if !filepath.IsAbs(hostPath) && !strings.HasPrefix(hostPath, "~") {
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

		// Append project mounts to main mounts
		merged.Mounts = append(append([]string{}, mainConfig.Mounts...), resolvedMounts...)
	}

	// Merge MCP servers: project takes precedence
	if pc.MCP != nil && len(pc.MCP.Servers) > 0 {
		// Start with main config servers
		mergedServers := make(map[string]MCPServerConfig)
		for name, srv := range mainConfig.MCPServers {
			mergedServers[name] = srv
		}
		// Override with project servers
		for name, srv := range pc.MCP.Servers {
			mergedServers[name] = srv
		}
		merged.MCPServers = mergedServers
	}

	if pc.ClaudeModel != "" {
		merged.ClaudeModel = pc.ClaudeModel
	}

	return &merged, nil
}
