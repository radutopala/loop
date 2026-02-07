package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config holds all application configuration loaded from environment variables.
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
	MountAllowlist       []string
	APIAddr              string
	ClaudeCodeOAuthToken string
	LoopDir              string
}

// userHomeDir is a package-level variable to allow overriding in tests.
var userHomeDir = os.UserHomeDir

// DefaultEnvFilePath returns the default path to the .env file (~/.loop/.env).
func DefaultEnvFilePath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".loop", ".env"), nil
}

// loadEnvFile reads a .env file and sets environment variables that are not already set.
// Lines starting with # are comments. Blank lines are ignored.
// Format: KEY=VALUE (values may optionally be wrapped in single or double quotes).
func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading env file: %w", err)
	}

	lines := splitOn(string(data), '\n')
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		eqIdx := -1
		for i := 0; i < len(line); i++ {
			if line[i] == '=' {
				eqIdx = i
				break
			}
		}
		if eqIdx < 0 {
			continue
		}

		key := trimSpace(line[:eqIdx])
		value := trimSpace(line[eqIdx+1:])

		// Strip matching quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// Only set if not already present in environment
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("setting env var %s: %w", key, err)
			}
		}
	}
	return nil
}

// Load reads configuration from ~/.loop/.env (if present) then from environment variables.
// Environment variables take precedence over the .env file.
func Load() (*Config, error) {
	home, err := userHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	loopDir := filepath.Join(home, ".loop")
	if err := loadEnvFile(filepath.Join(loopDir, ".env")); err != nil {
		return nil, err
	}

	cfg := &Config{}

	var missing []string

	cfg.DiscordToken = os.Getenv("DISCORD_TOKEN")
	if cfg.DiscordToken == "" {
		missing = append(missing, "DISCORD_TOKEN")
	}

	cfg.DiscordAppID = os.Getenv("DISCORD_APP_ID")
	if cfg.DiscordAppID == "" {
		missing = append(missing, "DISCORD_APP_ID")
	}

	cfg.ClaudeBinPath = getEnvDefault("CLAUDE_BIN_PATH", "claude")

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	cfg.DBPath = getEnvDefault("DB_PATH", filepath.Join(loopDir, "loop.db"))
	cfg.LogLevel = getEnvDefault("LOG_LEVEL", "info")
	cfg.LogFormat = getEnvDefault("LOG_FORMAT", "text")
	cfg.ContainerImage = getEnvDefault("CONTAINER_IMAGE", "loop-agent:latest")

	timeoutSec, err := getEnvInt("CONTAINER_TIMEOUT_SEC", 300)
	if err != nil {
		return nil, fmt.Errorf("invalid CONTAINER_TIMEOUT_SEC: %w", err)
	}
	cfg.ContainerTimeout = time.Duration(timeoutSec) * time.Second

	memMB, err := getEnvInt("CONTAINER_MEMORY_MB", 512)
	if err != nil {
		return nil, fmt.Errorf("invalid CONTAINER_MEMORY_MB: %w", err)
	}
	cfg.ContainerMemoryMB = int64(memMB)

	cpus, err := getEnvFloat("CONTAINER_CPUS", 1.0)
	if err != nil {
		return nil, fmt.Errorf("invalid CONTAINER_CPUS: %w", err)
	}
	cfg.ContainerCPUs = cpus

	pollSec, err := getEnvInt("POLL_INTERVAL_SEC", 30)
	if err != nil {
		return nil, fmt.Errorf("invalid POLL_INTERVAL_SEC: %w", err)
	}
	cfg.PollInterval = time.Duration(pollSec) * time.Second

	if v := os.Getenv("MOUNT_ALLOWLIST"); v != "" {
		cfg.MountAllowlist = splitAndTrim(v)
	}

	cfg.APIAddr = getEnvDefault("API_ADDR", ":8222")
	cfg.ClaudeCodeOAuthToken = os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")

	cfg.LoopDir = loopDir

	return cfg, nil
}

func getEnvDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	return strconv.Atoi(v)
}

func getEnvFloat(key string, defaultVal float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal, nil
	}
	return strconv.ParseFloat(v, 64)
}

func splitAndTrim(s string) []string {
	var result []string
	for _, part := range splitOn(s, ',') {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitOn(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
