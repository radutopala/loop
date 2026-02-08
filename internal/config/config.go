package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	PollInterval         time.Duration
	APIAddr              string
	ClaudeCodeOAuthToken string
	DiscordGuildID       string
	LoopDir              string
	MCPServers           map[string]MCPServerConfig
	TaskTemplates        []TaskTemplate
	Mounts               []string
}

// jsonConfig is an intermediate struct for JSON unmarshalling.
// Pointer types for numerics distinguish "missing" (nil) from "zero".
type jsonConfig struct {
	DiscordToken         string         `json:"discord_token"`
	DiscordAppID         string         `json:"discord_app_id"`
	ClaudeCodeOAuthToken string         `json:"claude_code_oauth_token"`
	DiscordGuildID       string         `json:"discord_guild_id"`
	LogLevel             string         `json:"log_level"`
	LogFormat            string         `json:"log_format"`
	DBPath               string         `json:"db_path"`
	ContainerImage       string         `json:"container_image"`
	ContainerTimeoutSec  *int           `json:"container_timeout_sec"`
	ContainerMemoryMB    *int64         `json:"container_memory_mb"`
	ContainerCPUs        *float64       `json:"container_cpus"`
	PollIntervalSec      *int           `json:"poll_interval_sec"`
	APIAddr              string         `json:"api_addr"`
	MCP                  *jsonMCPConfig `json:"mcp"`
	TaskTemplates        []TaskTemplate `json:"task_templates"`
	Mounts               []string       `json:"mounts"`
}

type jsonMCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

// userHomeDir is a package-level variable to allow overriding in tests.
var userHomeDir = os.UserHomeDir

// readFile is a package-level variable to allow overriding in tests.
var readFile = os.ReadFile

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
		PollInterval:         time.Duration(intPtrDefault(jc.PollIntervalSec, 30)) * time.Second,
		APIAddr:              stringDefault(jc.APIAddr, ":8222"),
		LoopDir:              loopDir,
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
