package config

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type ConfigSuite struct {
	suite.Suite
	origHomeDir  func() (string, error)
	origReadFile func(string) ([]byte, error)
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}

func (s *ConfigSuite) SetupTest() {
	s.origHomeDir = userHomeDir
	s.origReadFile = readFile
	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}
}

func (s *ConfigSuite) TearDownTest() {
	userHomeDir = s.origHomeDir
	readFile = s.origReadFile
}

func (s *ConfigSuite) minimalJSON() []byte {
	return []byte(`{"platform":"discord","discord_token":"test-token","discord_app_id":"test-app-id"}`)
}

func (s *ConfigSuite) TestLoadDefaults() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test-token", cfg.DiscordToken)
	require.Equal(s.T(), "test-app-id", cfg.DiscordAppID)
	require.Equal(s.T(), "claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "/home/testuser/.loop/loop.db", cfg.DBPath)
	require.Equal(s.T(), "/home/testuser/.loop/loop.log", cfg.LogFile)
	require.Equal(s.T(), "info", cfg.LogLevel)
	require.Equal(s.T(), "text", cfg.LogFormat)
	require.Equal(s.T(), "loop-agent:latest", cfg.ContainerImage)
	require.Equal(s.T(), 3600*time.Second, cfg.ContainerTimeout)
	require.Equal(s.T(), int64(512), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 1.0, cfg.ContainerCPUs)
	require.Equal(s.T(), 300*time.Second, cfg.ContainerKeepAlive)
	require.Equal(s.T(), 30*time.Second, cfg.PollInterval)
	require.Equal(s.T(), ":8222", cfg.APIAddr)
	require.Equal(s.T(), "/home/testuser/.loop", cfg.LoopDir)
	require.Empty(s.T(), cfg.ClaudeCodeOAuthToken)
	require.Empty(s.T(), cfg.DiscordGuildID)
	require.Nil(s.T(), cfg.MCPServers)
	require.True(s.T(), cfg.StreamingEnabled)
}

func (s *ConfigSuite) TestLoadCustomValues() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "custom-token",
			"discord_app_id": "custom-app-id",
			"claude_code_oauth_token": "sk-oauth",
			"discord_guild_id": "guild-123",
			"log_file": "/var/log/loop.log",
			"log_level": "debug",
			"log_format": "json",
			"db_path": "/tmp/test.db",
			"container_image": "custom-agent:v2",
			"container_timeout_sec": 600,
			"container_memory_mb": 1024,
			"container_cpus": 2.5,
			"container_keep_alive_sec": 120,
			"poll_interval_sec": 60,
			"api_addr": ":9999",
			"claude_bin_path": "/custom/claude"
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom-token", cfg.DiscordToken)
	require.Equal(s.T(), "custom-app-id", cfg.DiscordAppID)
	require.Equal(s.T(), "sk-oauth", cfg.ClaudeCodeOAuthToken)
	require.Equal(s.T(), "guild-123", cfg.DiscordGuildID)
	require.Equal(s.T(), "/var/log/loop.log", cfg.LogFile)
	require.Equal(s.T(), "debug", cfg.LogLevel)
	require.Equal(s.T(), "json", cfg.LogFormat)
	require.Equal(s.T(), "/tmp/test.db", cfg.DBPath)
	require.Equal(s.T(), "custom-agent:v2", cfg.ContainerImage)
	require.Equal(s.T(), 600*time.Second, cfg.ContainerTimeout)
	require.Equal(s.T(), int64(1024), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 2.5, cfg.ContainerCPUs)
	require.Equal(s.T(), 120*time.Second, cfg.ContainerKeepAlive)
	require.Equal(s.T(), 60*time.Second, cfg.PollInterval)
	require.Equal(s.T(), ":9999", cfg.APIAddr)
	require.Equal(s.T(), "/custom/claude", cfg.ClaudeBinPath)
}

func (s *ConfigSuite) TestLoadStreamingEnabledExplicitFalse() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"streaming_enabled": false
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.StreamingEnabled)
}

func (s *ConfigSuite) TestMissingRequired() {
	tests := []struct {
		name    string
		json    string
		errText string
	}{
		{
			name:    "missing platform",
			json:    `{}`,
			errText: "\"platform\" must be set",
		},
		{
			name:    "discord missing token",
			json:    `{"platform":"discord"}`,
			errText: "requires discord_token and discord_app_id",
		},
		{
			name:    "discord partial",
			json:    `{"platform":"discord","discord_token":"tok"}`,
			errText: "requires discord_token and discord_app_id",
		},
		{
			name:    "slack missing tokens",
			json:    `{"platform":"slack"}`,
			errText: "requires slack_bot_token and slack_app_token",
		},
		{
			name:    "slack partial",
			json:    `{"platform":"slack","slack_bot_token":"xoxb-tok"}`,
			errText: "requires slack_bot_token and slack_app_token",
		},
		{
			name:    "unsupported platform",
			json:    `{"platform":"teams"}`,
			errText: "unsupported platform",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			readFile = func(_ string) ([]byte, error) {
				return []byte(tc.json), nil
			}
			_, err := Load()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.errText)
		})
	}
}

func (s *ConfigSuite) TestPlatformCaseInsensitive() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "Discord",
			"discord_token": "tok",
			"discord_app_id": "app"
		}`), nil
	}
	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.PlatformDiscord, cfg.Platform())
}

func (s *ConfigSuite) TestSlackConfigLoads() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "slack",
			"slack_bot_token": "xoxb-test-token",
			"slack_app_token": "xapp-test-token"
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "xoxb-test-token", cfg.SlackBotToken)
	require.Equal(s.T(), "xapp-test-token", cfg.SlackAppToken)
	require.Empty(s.T(), cfg.DiscordToken)
	require.Empty(s.T(), cfg.DiscordAppID)
	require.Equal(s.T(), types.PlatformSlack, cfg.Platform())
}

func (s *ConfigSuite) TestPlatformDiscord() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.PlatformDiscord, cfg.Platform())
}

func (s *ConfigSuite) TestPlatformSlack() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{"platform":"slack","slack_bot_token":"xoxb-tok","slack_app_token":"xapp-tok"}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.PlatformSlack, cfg.Platform())
}

func (s *ConfigSuite) TestFileNotFound() {
	readFile = func(_ string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading config file")
}

func (s *ConfigSuite) TestReadError() {
	readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading config file")
}

func (s *ConfigSuite) TestInvalidJSON() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{not valid json`), nil
	}
	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing config file")
}

func (s *ConfigSuite) TestInvalidJSONTypes() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{"discord_token": 123}`), nil
	}
	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing config file")
}

func (s *ConfigSuite) TestHomeDirError() {
	userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}
	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *ConfigSuite) TestMCPServersLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"mcp": {
				"servers": {
					"custom-tool": {
						"command": "/path/to/binary",
						"args": ["--flag"],
						"env": {"API_KEY": "secret"}
					}
				}
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Len(s.T(), cfg.MCPServers, 1)
	srv := cfg.MCPServers["custom-tool"]
	require.Equal(s.T(), "/path/to/binary", srv.Command)
	require.Equal(s.T(), []string{"--flag"}, srv.Args)
	require.Equal(s.T(), map[string]string{"API_KEY": "secret"}, srv.Env)
}

func (s *ConfigSuite) TestMCPServersEmptyBlock() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"mcp": {"servers": {}}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Nil(s.T(), cfg.MCPServers)
}

func (s *ConfigSuite) TestZeroNumericValues() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"container_timeout_sec": 0,
			"container_memory_mb": 0,
			"container_cpus": 0,
			"container_keep_alive_sec": 0,
			"poll_interval_sec": 0
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Duration(0), cfg.ContainerTimeout)
	require.Equal(s.T(), int64(0), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 0.0, cfg.ContainerCPUs)
	require.Equal(s.T(), time.Duration(0), cfg.ContainerKeepAlive)
	require.Equal(s.T(), time.Duration(0), cfg.PollInterval)
}

func (s *ConfigSuite) TestJSONWithComments() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			// Required credentials
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			/* Optional settings */
			"log_level": "debug",
			// Trailing comma support
			"api_addr": ":9999",
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "tok", cfg.DiscordToken)
	require.Equal(s.T(), "debug", cfg.LogLevel)
	require.Equal(s.T(), ":9999", cfg.APIAddr)
}

func (s *ConfigSuite) TestDefaultHelpers() {
	require.Equal(s.T(), "val", stringDefault("val", "def"))
	require.Equal(s.T(), "def", stringDefault("", "def"))

	require.Equal(s.T(), 42, ptrDefault(new(42), 10))
	require.Equal(s.T(), 10, ptrDefault((*int)(nil), 10))

	require.Equal(s.T(), int64(99), ptrDefault(new(int64(99)), 50))
	require.Equal(s.T(), int64(50), ptrDefault((*int64)(nil), 50))

	require.InDelta(s.T(), 3.14, ptrDefault(new(3.14), 1.0), 0.001)
	require.Equal(s.T(), 1.0, ptrDefault((*float64)(nil), 1.0))
}

func (s *ConfigSuite) TestDefaultReadFile() {
	_, err := s.origReadFile("/nonexistent/path/config.json")
	require.Error(s.T(), err)
}

func (s *ConfigSuite) TestTaskTemplatesLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"task_templates": [
				{
					"name": "tk-auto-worker",
					"description": "Auto work on tickets",
					"schedule": "*/5 * * * *",
					"type": "cron",
					"prompt": "Check tk queue and work on ready tickets"
				},
				{
					"name": "daily-summary",
					"description": "Daily summary",
					"schedule": "0 17 * * *",
					"type": "cron",
					"prompt": "Generate summary"
				}
			]
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Len(s.T(), cfg.TaskTemplates, 2)

	tmpl1 := cfg.TaskTemplates[0]
	require.Equal(s.T(), "tk-auto-worker", tmpl1.Name)
	require.Equal(s.T(), "Auto work on tickets", tmpl1.Description)
	require.Equal(s.T(), "*/5 * * * *", tmpl1.Schedule)
	require.Equal(s.T(), "cron", tmpl1.Type)
	require.Equal(s.T(), "Check tk queue and work on ready tickets", tmpl1.Prompt)

	tmpl2 := cfg.TaskTemplates[1]
	require.Equal(s.T(), "daily-summary", tmpl2.Name)
	require.Equal(s.T(), "Daily summary", tmpl2.Description)
	require.Equal(s.T(), "0 17 * * *", tmpl2.Schedule)
	require.Equal(s.T(), "cron", tmpl2.Type)
	require.Equal(s.T(), "Generate summary", tmpl2.Prompt)
}

func (s *ConfigSuite) TestTaskTemplatesAbsent() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.TaskTemplates)
}

func (s *ConfigSuite) TestTaskTemplatesEmpty() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"task_templates": []
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.TaskTemplates)
}

func (s *ConfigSuite) TestExampleConfigEmbedded() {
	// Verify the embedded ExampleConfig is not empty
	require.NotEmpty(s.T(), ExampleConfig)
	require.Contains(s.T(), string(ExampleConfig), "platform")
	require.Contains(s.T(), string(ExampleConfig), "discord_token")
	require.Contains(s.T(), string(ExampleConfig), "task_templates")
}

func (s *ConfigSuite) TestLoadProjectConfigNoFile() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), mainCfg, merged) // Should return same config
}

func (s *ConfigSuite) TestLoadProjectConfigReadError() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return nil, errors.New("permission denied")
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}
	_, err := LoadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading project config file")
}

func (s *ConfigSuite) TestLoadProjectConfigInvalidJSON() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{invalid json`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}
	_, err := LoadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing project config file")
}

func (s *ConfigSuite) TestLoadProjectConfigInvalidJSONTypes() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{"mounts": "not-an-array"}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}
	_, err := LoadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing project config file")
}

func (s *ConfigSuite) TestLoadProjectConfigMountsOnly() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": [
					"./data:/app/data",
					"./logs:/app/logs:ro"
				]
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Check mounts: project replaces global mounts
	require.Len(s.T(), merged.Mounts, 2)
	require.Equal(s.T(), "/project/data:/app/data", merged.Mounts[0])
	require.Equal(s.T(), "/project/logs:/app/logs:ro", merged.Mounts[1])

	// MCP servers unchanged
	require.Len(s.T(), merged.MCPServers, 1)
	require.Equal(s.T(), "/bin/main", merged.MCPServers["main-srv"].Command)
}

func (s *ConfigSuite) TestLoadProjectConfigMCPServersOnly() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mcp": {
					"servers": {
						"project-db": {
							"command": "npx",
							"args": ["-y", "@modelcontextprotocol/server-postgres"],
							"env": {"DB_URL": "postgresql://localhost/db"}
						}
					}
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Mounts unchanged
	require.Len(s.T(), merged.Mounts, 1)
	require.Equal(s.T(), "~/.gitconfig:~/.gitconfig:ro", merged.Mounts[0])

	// MCP servers merged
	require.Len(s.T(), merged.MCPServers, 2)
	require.Equal(s.T(), "/bin/main", merged.MCPServers["main-srv"].Command)
	require.Equal(s.T(), "npx", merged.MCPServers["project-db"].Command)
	require.Equal(s.T(), []string{"-y", "@modelcontextprotocol/server-postgres"}, merged.MCPServers["project-db"].Args)
	require.Equal(s.T(), map[string]string{"DB_URL": "postgresql://localhost/db"}, merged.MCPServers["project-db"].Env)
}

func (s *ConfigSuite) TestLoadProjectConfigBothMountsAndMCP() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": ["./data:/app/data"],
				"mcp": {
					"servers": {
						"project-tool": {"command": "/bin/tool"}
					}
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Check mounts: project replaces global
	require.Len(s.T(), merged.Mounts, 1)
	require.Equal(s.T(), "/project/data:/app/data", merged.Mounts[0])

	// Check MCP servers
	require.Len(s.T(), merged.MCPServers, 2)
	require.Equal(s.T(), "/bin/main", merged.MCPServers["main-srv"].Command)
	require.Equal(s.T(), "/bin/tool", merged.MCPServers["project-tool"].Command)
}

func (s *ConfigSuite) TestLoadProjectConfigMCPOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mcp": {
					"servers": {
						"main-srv": {"command": "/bin/override"}
					}
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Project MCP server should override main
	require.Len(s.T(), merged.MCPServers, 1)
	require.Equal(s.T(), "/bin/override", merged.MCPServers["main-srv"].Command)
}

func (s *ConfigSuite) TestLoadProjectConfigAbsolutePath() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": [
					"/absolute/path:/app/data",
					"~/home/path:/app/home"
				]
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Absolute and tilde paths should not be modified
	require.Len(s.T(), merged.Mounts, 2)
	require.Equal(s.T(), "/absolute/path:/app/data", merged.Mounts[0])
	require.Equal(s.T(), "~/home/path:/app/home", merged.Mounts[1])
}

func (s *ConfigSuite) TestLoadProjectConfigInvalidMountFormat() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": ["invalid-mount-format"]
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}

	_, err := LoadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid mount format")
}

func (s *ConfigSuite) TestLoadProjectConfigEmptyConfig() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Main config should be unchanged
	require.Len(s.T(), merged.Mounts, 1)
	require.Equal(s.T(), "~/.gitconfig:~/.gitconfig:ro", merged.Mounts[0])
	require.Len(s.T(), merged.MCPServers, 1)
	require.Equal(s.T(), "/bin/main", merged.MCPServers["main-srv"].Command)
}

func (s *ConfigSuite) TestSetReadFile() {
	// Test the test helper function
	mockCalled := false
	mockFn := func(_ string) ([]byte, error) {
		mockCalled = true
		return []byte("test"), nil
	}

	// Set mock
	orig := TestSetReadFile(mockFn)
	require.NotNil(s.T(), orig)

	// Call to verify it's set
	data, err := readFile("test")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []byte("test"), data)
	require.True(s.T(), mockCalled)

	// Restore
	TestSetReadFile(orig)
}

func (s *ConfigSuite) TestAnthropicAPIKeyLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"anthropic_api_key": "sk-ant-api-key-123"
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sk-ant-api-key-123", cfg.AnthropicAPIKey)
	require.Empty(s.T(), cfg.ClaudeCodeOAuthToken)
}

func (s *ConfigSuite) TestAnthropicAPIKeyAbsent() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.AnthropicAPIKey)
}

func (s *ConfigSuite) TestClaudeModelLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"claude_model": "claude-sonnet-4-5-20250929"
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude-sonnet-4-5-20250929", cfg.ClaudeModel)
}

func (s *ConfigSuite) TestClaudeModelAbsent() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.ClaudeModel)
}

func (s *ConfigSuite) TestLoadProjectConfigClaudeModelOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"claude_model": "claude-opus-4-6"
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ClaudeModel: "claude-sonnet-4-5-20250929",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude-opus-4-6", merged.ClaudeModel)
}

func (s *ConfigSuite) TestLoadProjectConfigClaudeModelNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ClaudeModel: "claude-sonnet-4-5-20250929",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude-sonnet-4-5-20250929", merged.ClaudeModel)
}

func (s *ConfigSuite) TestLoadProjectConfigOAuthTokenOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"claude_code_oauth_token": "sk-ant-project-oauth"
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		AnthropicAPIKey: "sk-ant-global-api-key",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sk-ant-project-oauth", merged.ClaudeCodeOAuthToken)
	require.Empty(s.T(), merged.AnthropicAPIKey) // OAuth takes precedence

	// Verify main not mutated
	require.Equal(s.T(), "sk-ant-global-api-key", mainCfg.AnthropicAPIKey)
}

func (s *ConfigSuite) TestLoadProjectConfigAPIKeyOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"anthropic_api_key": "sk-ant-project-api-key"
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ClaudeCodeOAuthToken: "sk-ant-global-oauth",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sk-ant-project-api-key", merged.AnthropicAPIKey)
	require.Empty(s.T(), merged.ClaudeCodeOAuthToken) // API key clears OAuth

	// Verify main not mutated
	require.Equal(s.T(), "sk-ant-global-oauth", mainCfg.ClaudeCodeOAuthToken)
}

func (s *ConfigSuite) TestLoadProjectConfigAuthNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ClaudeCodeOAuthToken: "sk-ant-global-oauth",
		AnthropicAPIKey:      "sk-ant-global-api-key",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sk-ant-global-oauth", merged.ClaudeCodeOAuthToken)
	require.Equal(s.T(), "sk-ant-global-api-key", merged.AnthropicAPIKey)
}

func (s *ConfigSuite) TestLoadProjectConfigClaudeBinPathOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"claude_bin_path": "/custom/bin/claude"
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ClaudeBinPath: "claude",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/custom/bin/claude", merged.ClaudeBinPath)
}

func (s *ConfigSuite) TestLoadProjectConfigClaudeBinPathNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ClaudeBinPath: "claude",
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude", merged.ClaudeBinPath)
}

func (s *ConfigSuite) TestLoadProjectConfigContainerOverrides() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"container_image": "custom-agent:v3",
				"container_memory_mb": 2048,
				"container_cpus": 4.0
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ContainerImage:    "loop-agent:latest",
		ContainerMemoryMB: 512,
		ContainerCPUs:     1.0,
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom-agent:v3", merged.ContainerImage)
	require.Equal(s.T(), int64(2048), merged.ContainerMemoryMB)
	require.Equal(s.T(), 4.0, merged.ContainerCPUs)

	// Verify main not mutated
	require.Equal(s.T(), "loop-agent:latest", mainCfg.ContainerImage)
	require.Equal(s.T(), int64(512), mainCfg.ContainerMemoryMB)
}

func (s *ConfigSuite) TestLoadProjectConfigContainerNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		ContainerImage:    "loop-agent:latest",
		ContainerMemoryMB: 512,
		ContainerCPUs:     1.0,
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "loop-agent:latest", merged.ContainerImage)
	require.Equal(s.T(), int64(512), merged.ContainerMemoryMB)
	require.Equal(s.T(), 1.0, merged.ContainerCPUs)
}

func (s *ConfigSuite) TestLoadProjectConfigMemoryEmbeddingsOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"memory": {
					"embeddings": {
						"provider": "ollama",
						"model": "mxbai-embed-large",
						"ollama_url": "http://gpu-server:11434"
					}
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Memory: MemoryConfig{
			Enabled: true,
			Embeddings: EmbeddingsConfig{
				Provider:  "ollama",
				Model:     "nomic-embed-text",
				OllamaURL: "http://localhost:11434",
			},
		},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ollama", merged.Memory.Embeddings.Provider)
	require.Equal(s.T(), "mxbai-embed-large", merged.Memory.Embeddings.Model)
	require.Equal(s.T(), "http://gpu-server:11434", merged.Memory.Embeddings.OllamaURL)

	// Verify main not mutated
	require.Equal(s.T(), "nomic-embed-text", mainCfg.Memory.Embeddings.Model)
}

func (s *ConfigSuite) TestLoadProjectConfigMemoryEmbeddingsNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Memory: MemoryConfig{
			Enabled: true,
			Embeddings: EmbeddingsConfig{
				Provider:  "ollama",
				Model:     "nomic-embed-text",
				OllamaURL: "http://localhost:11434",
			},
		},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ollama", merged.Memory.Embeddings.Provider)
	require.Equal(s.T(), "nomic-embed-text", merged.Memory.Embeddings.Model)
}

func (s *ConfigSuite) TestLoadProjectConfigEnvsMerged() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"envs": {"PROJECT_KEY": "proj-val", "SHARED": "proj"}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Envs: map[string]string{"GLOBAL_KEY": "global-val", "SHARED": "global"},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "global-val", merged.Envs["GLOBAL_KEY"])
	require.Equal(s.T(), "proj-val", merged.Envs["PROJECT_KEY"])
	require.Equal(s.T(), "proj", merged.Envs["SHARED"]) // project wins

	// Verify main not mutated
	require.Equal(s.T(), "global", mainCfg.Envs["SHARED"])
	require.Empty(s.T(), mainCfg.Envs["PROJECT_KEY"])
}

func (s *ConfigSuite) TestLoadProjectConfigEnvsNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Envs: map[string]string{"GLOBAL_KEY": "global-val"},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "global-val", merged.Envs["GLOBAL_KEY"])
}

func (s *ConfigSuite) TestIsNamedVolume() {
	tests := []struct {
		source   string
		expected bool
	}{
		{"gomodcache", true},
		{"my-volume", true},
		{"/absolute/path", false},
		{"~/home/path", false},
		{"./relative/path", false},
		{"relative/path", false},
		{"", true}, // edge case but won't reach here due to mount format validation
	}
	for _, tt := range tests {
		s.Run(tt.source, func() {
			require.Equal(s.T(), tt.expected, IsNamedVolume(tt.source))
		})
	}
}

func (s *ConfigSuite) TestLoadProjectConfigNamedVolumes() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": [
					"./data:/app/data",
					"loop-npmcache:~/.npm",
					"loop-gocache:/go",
					"~/.ssh:~/.ssh:ro"
				]
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Mounts, 4)
	require.Equal(s.T(), "/project/data:/app/data", merged.Mounts[0])
	require.Equal(s.T(), "loop-npmcache:~/.npm", merged.Mounts[1])
	require.Equal(s.T(), "loop-gocache:/go", merged.Mounts[2])
	require.Equal(s.T(), "~/.ssh:~/.ssh:ro", merged.Mounts[3])
}

func (s *ConfigSuite) TestLoadEnvsFromGlobal() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "tok",
			"discord_app_id": "app",
			"envs": {"MY_VAR": "my-value", "NUM_VAR": 0, "BOOL_VAR": true}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "my-value", cfg.Envs["MY_VAR"])
	require.Equal(s.T(), "0", cfg.Envs["NUM_VAR"])
	require.Equal(s.T(), "true", cfg.Envs["BOOL_VAR"])
}

func (s *ConfigSuite) TestResolvePromptWithPrompt() {
	tmpl := &TaskTemplate{Name: "test", Prompt: "do stuff"}
	prompt, err := tmpl.ResolvePrompt("/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "do stuff", prompt)
}

func (s *ConfigSuite) TestResolvePromptWithPromptPath() {
	readFile = func(path string) ([]byte, error) {
		if path == "/loop/templates/daily.md" {
			return []byte("daily prompt content"), nil
		}
		return nil, os.ErrNotExist
	}

	tmpl := &TaskTemplate{Name: "test", PromptPath: "daily.md"}
	prompt, err := tmpl.ResolvePrompt("/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "daily prompt content", prompt)
}

func (s *ConfigSuite) TestResolvePromptWithBothSet() {
	tmpl := &TaskTemplate{Name: "test", Prompt: "inline", PromptPath: "file.md"}
	_, err := tmpl.ResolvePrompt("/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mutually exclusive")
}

func (s *ConfigSuite) TestResolvePromptWithNeitherSet() {
	tmpl := &TaskTemplate{Name: "test"}
	_, err := tmpl.ResolvePrompt("/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "one of prompt or prompt_path is required")
}

func (s *ConfigSuite) TestResolvePromptFileReadError() {
	readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("file not found")
	}

	tmpl := &TaskTemplate{Name: "test", PromptPath: "missing.md"}
	_, err := tmpl.ResolvePrompt("/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading prompt file")
}

func (s *ConfigSuite) TestLoadProjectConfigTemplatesMerge() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
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
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		TaskTemplates: []TaskTemplate{
			{Name: "daily-summary", Description: "Daily summary", Schedule: "0 17 * * *", Type: "cron", Prompt: "Generate summary"},
			{Name: "global-only", Description: "Global template", Schedule: "0 9 * * *", Type: "cron", Prompt: "Global task"},
		},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Should have 3 templates: overridden daily-summary, preserved global-only, new project-only
	require.Len(s.T(), merged.TaskTemplates, 3)

	// daily-summary should be overridden by project
	require.Equal(s.T(), "daily-summary", merged.TaskTemplates[0].Name)
	require.Equal(s.T(), "Overridden daily summary", merged.TaskTemplates[0].Description)
	require.Equal(s.T(), "0 18 * * *", merged.TaskTemplates[0].Schedule)
	require.Equal(s.T(), "New summary prompt", merged.TaskTemplates[0].Prompt)

	// global-only should be preserved
	require.Equal(s.T(), "global-only", merged.TaskTemplates[1].Name)
	require.Equal(s.T(), "Global task", merged.TaskTemplates[1].Prompt)

	// project-only should be added
	require.Equal(s.T(), "project-only", merged.TaskTemplates[2].Name)
	require.Equal(s.T(), "Project task", merged.TaskTemplates[2].Prompt)

	// Verify main config not mutated
	require.Len(s.T(), mainCfg.TaskTemplates, 2)
	require.Equal(s.T(), "Generate summary", mainCfg.TaskTemplates[0].Prompt)
}

func (s *ConfigSuite) TestLoadProjectConfigTemplatesEmpty() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		TaskTemplates: []TaskTemplate{
			{Name: "global", Description: "Global", Schedule: "0 9 * * *", Type: "cron", Prompt: "Do global"},
		},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Templates unchanged
	require.Len(s.T(), merged.TaskTemplates, 1)
	require.Equal(s.T(), "global", merged.TaskTemplates[0].Name)
}

func (s *ConfigSuite) TestLoadProjectConfigTemplatesWithPromptPath() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"task_templates": [
					{
						"name": "file-template",
						"description": "Template from file",
						"schedule": "0 9 * * *",
						"type": "cron",
						"prompt_path": "review.md"
					}
				]
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	require.Len(s.T(), merged.TaskTemplates, 1)
	require.Equal(s.T(), "file-template", merged.TaskTemplates[0].Name)
	require.Equal(s.T(), "review.md", merged.TaskTemplates[0].PromptPath)
	require.Empty(s.T(), merged.TaskTemplates[0].Prompt)
}

func (s *ConfigSuite) TestMemoryConfigOllama() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"enabled": true,
				"paths": ["./memory"],
				"embeddings": {
					"provider": "ollama",
					"model": "nomic-embed-text"
				}
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.True(s.T(), cfg.Memory.Enabled)
	require.Equal(s.T(), "ollama", cfg.Memory.Embeddings.Provider)
	require.Equal(s.T(), "nomic-embed-text", cfg.Memory.Embeddings.Model)
	require.Equal(s.T(), "http://localhost:11434", cfg.Memory.Embeddings.OllamaURL)
}

func (s *ConfigSuite) TestMemoryConfigAbsent() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Memory.Enabled)
	require.Empty(s.T(), cfg.Memory.Embeddings.Provider)
}

func (s *ConfigSuite) TestMemoryConfigNotExplicitlyEnabled() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"embeddings": {
					"provider": "ollama",
					"model": "nomic-embed-text"
				}
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Memory.Enabled)
}

func (s *ConfigSuite) TestMemoryPathsLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"enabled": true,
				"paths": ["/shared/knowledge", "/path/to/notes.md"]
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/shared/knowledge", "/path/to/notes.md"}, cfg.Memory.Paths)
}

func (s *ConfigSuite) TestMemoryPathsDefault() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"./memory"}, cfg.Memory.Paths)
}

func (s *ConfigSuite) TestLoadProjectConfigMemoryPathsAppended() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"memory": {
					"paths": ["./docs/arch.md"]
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Memory: MemoryConfig{Paths: []string{"/global/knowledge"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/global/knowledge", "./docs/arch.md"}, merged.Memory.Paths)

	// Verify main config not mutated
	require.Len(s.T(), mainCfg.Memory.Paths, 1)
}

func (s *ConfigSuite) TestLoadProjectConfigMemoryPathsEmptyPreservesGlobal() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Memory: MemoryConfig{Paths: []string{"/global/knowledge"}},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/global/knowledge"}, merged.Memory.Paths)
}

func (s *ConfigSuite) TestMemoryMaxChunkCharsLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"enabled": true,
				"max_chunk_chars": 8000
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 8000, cfg.Memory.MaxChunkChars)
}

func (s *ConfigSuite) TestMemoryMaxChunkCharsDefault() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, cfg.Memory.MaxChunkChars)
}

func (s *ConfigSuite) TestLoadProjectConfigMaxChunkCharsOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"memory": {
					"max_chunk_chars": 12000
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Memory: MemoryConfig{MaxChunkChars: 6000},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 12000, merged.Memory.MaxChunkChars)
}

func (s *ConfigSuite) TestPermissionsConfigIsEmpty() {
	tests := []struct {
		name     string
		perms    PermissionsConfig
		expected bool
	}{
		{"both empty", PermissionsConfig{}, true},
		{"owners users only", PermissionsConfig{Owners: RoleGrant{Users: []string{"U1"}}}, false},
		{"owners roles only", PermissionsConfig{Owners: RoleGrant{Roles: []string{"R1"}}}, false},
		{"members users only", PermissionsConfig{Members: RoleGrant{Users: []string{"U1"}}}, false},
		{"members roles only", PermissionsConfig{Members: RoleGrant{Roles: []string{"R1"}}}, false},
		{"all set", PermissionsConfig{Owners: RoleGrant{Users: []string{"U1"}, Roles: []string{"R1"}}, Members: RoleGrant{Users: []string{"U2"}}}, false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, tc.perms.IsEmpty())
		})
	}
}

func (s *ConfigSuite) TestPermissionsConfigGetRole() {
	tests := []struct {
		name        string
		perms       PermissionsConfig
		authorID    string
		authorRoles []string
		expected    types.Role
	}{
		{"empty config returns empty role", PermissionsConfig{}, "any-user", nil, ""},
		{"owner by user", PermissionsConfig{Owners: RoleGrant{Users: []string{"U1", "U2"}}}, "U1", nil, types.RoleOwner},
		{"owner by role", PermissionsConfig{Owners: RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R1"}, types.RoleOwner},
		{"member by user", PermissionsConfig{Members: RoleGrant{Users: []string{"U1"}}}, "U1", nil, types.RoleMember},
		{"member by role", PermissionsConfig{Members: RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R1"}, types.RoleMember},
		{"owner beats member", PermissionsConfig{Owners: RoleGrant{Users: []string{"U1"}}, Members: RoleGrant{Users: []string{"U1"}}}, "U1", nil, types.RoleOwner},
		{"user not in any list", PermissionsConfig{Owners: RoleGrant{Users: []string{"U1"}}}, "U2", nil, ""},
		{"role match as owner", PermissionsConfig{Owners: RoleGrant{Roles: []string{"R1"}}}, "U2", []string{"R1", "R2"}, types.RoleOwner},
		{"role match as member", PermissionsConfig{Members: RoleGrant{Roles: []string{"R2"}}}, "U2", []string{"R1", "R2"}, types.RoleMember},
		{"no role match", PermissionsConfig{Owners: RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R2", "R3"}, ""},
		{"nil author roles with role restriction", PermissionsConfig{Owners: RoleGrant{Roles: []string{"R1"}}}, "U1", nil, ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, tc.perms.GetRole(tc.authorID, tc.authorRoles))
		})
	}
}

func (s *ConfigSuite) TestLoadWithPermissions() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"permissions": {
				"owners":  {"users": ["U1", "U2"], "roles": ["R1"]},
				"members": {"users": ["U3"], "roles": []}
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"U1", "U2"}, cfg.Permissions.Owners.Users)
	require.Equal(s.T(), []string{"R1"}, cfg.Permissions.Owners.Roles)
	require.Equal(s.T(), []string{"U3"}, cfg.Permissions.Members.Users)
}

func (s *ConfigSuite) TestLoadProjectConfigPermissionsOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"permissions": {
					"owners":  {"users": [], "roles": []},
					"members": {"users": [], "roles": []}
				}
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Permissions: PermissionsConfig{
			Owners:  RoleGrant{Users: []string{"U1"}, Roles: []string{"R1"}},
			Members: RoleGrant{Users: []string{"U2"}},
		},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	// Project sets empty permissions, replacing the global restriction.
	require.True(s.T(), merged.Permissions.IsEmpty())
}

func (s *ConfigSuite) TestLoadProjectConfigPermissionsNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Permissions: PermissionsConfig{
			Owners: RoleGrant{Users: []string{"U1"}},
		},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	// No project permissions block — global permissions apply unchanged.
	require.Equal(s.T(), []string{"U1"}, merged.Permissions.Owners.Users)
}

func (s *ConfigSuite) TestLoadProjectConfigMaxChunkCharsNoOverride() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Memory: MemoryConfig{MaxChunkChars: 6000},
	}

	merged, err := LoadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 6000, merged.Memory.MaxChunkChars)
}

func (s *ConfigSuite) TestMemoryConfigOllamaCustomURL() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platform": "discord",
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"enabled": true,
				"embeddings": {
					"provider": "ollama",
					"model": "nomic-embed-text",
					"ollama_url": "http://gpu-server:11434"
				}
			}
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://gpu-server:11434", cfg.Memory.Embeddings.OllamaURL)
}
