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
	loader Loader
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}

func (s *ConfigSuite) SetupTest() {
	s.loader = Loader{
		userHomeDir: func() (string, error) {
			return "/home/testuser", nil
		},
		readFile: os.ReadFile,
	}
}

func (s *ConfigSuite) minimalJSON() []byte {
	return []byte(`{"platforms":["discord"],"discord_token":"test-token","discord_app_id":"test-app-id"}`)
}

func (s *ConfigSuite) setupProjectReadFile(projectJSON string) {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(projectJSON), nil
		}
		return nil, errors.New("unexpected path")
	}
}

func (s *ConfigSuite) TestLoadDefaults() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test-token", cfg.DiscordToken)
	require.Equal(s.T(), "test-app-id", cfg.DiscordAppID)
	require.Equal(s.T(), "claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "/home/testuser/.loop/loop.db", cfg.DBPath)
	require.Equal(s.T(), "/home/testuser/.loop/loop.log", cfg.LogFile)
	require.Equal(s.T(), "info", cfg.LogLevel)
	require.Equal(s.T(), "text", cfg.LogFormat)
	require.Equal(s.T(), "loop-agent:latest", cfg.ContainerImage)
	require.Equal(s.T(), 43200*time.Second, cfg.ContainerTimeout)
	require.Equal(s.T(), int64(1024), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 1.0, cfg.ContainerCPUs)
	require.Equal(s.T(), 300*time.Second, cfg.ContainerKeepAlive)
	require.Equal(s.T(), 30*time.Second, cfg.PollInterval)
	require.Equal(s.T(), ":8222", cfg.APIAddr)
	require.Equal(s.T(), "/home/testuser/.loop", cfg.LoopDir)
	require.Empty(s.T(), cfg.ClaudeCodeOAuthToken)
	require.Empty(s.T(), cfg.DiscordGuildID)
	require.Nil(s.T(), cfg.MCPServers)
	require.True(s.T(), cfg.StreamingEnabled)
	require.True(s.T(), cfg.Browser.Enabled)
	require.Equal(s.T(), []string{"~/.claude.json"}, cfg.CopyFiles)
	require.False(s.T(), cfg.KeepMCPConfigs)
	require.False(s.T(), cfg.Desktop.AutoSaveOnBlur)
	require.True(s.T(), cfg.Desktop.PreviewTabs)
}

func (s *ConfigSuite) TestLoadCustomValues() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
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

	cfg, err := s.loader.load()
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
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"streaming_enabled": false
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.StreamingEnabled)
}

func (s *ConfigSuite) TestLoadBrowserEnabledExplicitFalse() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"browser": { "enabled": false }
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Browser.Enabled)
}

func (s *ConfigSuite) TestLoadBrowserFullConfig() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"browser": {
				"enabled": true,
				"chrome_image": "my-chrome:v2",
				"mode": "host",
				"host_cdp_port": 9333
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.True(s.T(), cfg.Browser.Enabled)
	require.Equal(s.T(), "my-chrome:v2", cfg.Browser.ChromeImage)
	require.Equal(s.T(), "host", cfg.Browser.Mode)
	require.Equal(s.T(), 9333, cfg.Browser.HostCDPPort)
}

func (s *ConfigSuite) TestMissingRequired() {
	tests := []struct {
		name    string
		json    string
		errText string
	}{
		{
			name:    "missing platforms",
			json:    `{}`,
			errText: "\"platforms\" must be set",
		},
		{
			name:    "discord missing token",
			json:    `{"platforms":["discord"]}`,
			errText: "requires discord_token and discord_app_id",
		},
		{
			name:    "discord partial",
			json:    `{"platforms":["discord"],"discord_token":"tok"}`,
			errText: "requires discord_token and discord_app_id",
		},
		{
			name:    "slack missing tokens",
			json:    `{"platforms":["slack"]}`,
			errText: "requires slack_bot_token and slack_app_token",
		},
		{
			name:    "slack partial",
			json:    `{"platforms":["slack"],"slack_bot_token":"xoxb-tok"}`,
			errText: "requires slack_bot_token and slack_app_token",
		},
		{
			name:    "unsupported platform",
			json:    `{"platforms":["teams"]}`,
			errText: "unsupported platform",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.loader.readFile = func(_ string) ([]byte, error) {
				return []byte(tc.json), nil
			}
			_, err := s.loader.load()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.errText)
		})
	}
}

func (s *ConfigSuite) TestPlatformCaseInsensitive() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["Discord"],
			"discord_token": "tok",
			"discord_app_id": "app"
		}`), nil
	}
	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []types.Platform{types.PlatformDiscord}, cfg.Platforms)
}

func (s *ConfigSuite) TestSlackConfigLoads() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["slack"],
			"slack_bot_token": "xoxb-test-token",
			"slack_app_token": "xapp-test-token"
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "xoxb-test-token", cfg.SlackBotToken)
	require.Equal(s.T(), "xapp-test-token", cfg.SlackAppToken)
	require.Empty(s.T(), cfg.DiscordToken)
	require.Empty(s.T(), cfg.DiscordAppID)
	require.Equal(s.T(), []types.Platform{types.PlatformSlack}, cfg.Platforms)
}

func (s *ConfigSuite) TestPlatformDiscord() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []types.Platform{types.PlatformDiscord}, cfg.Platforms)
}

func (s *ConfigSuite) TestPlatformSlack() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{"platforms":["slack"],"slack_bot_token":"xoxb-tok","slack_app_token":"xapp-tok"}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []types.Platform{types.PlatformSlack}, cfg.Platforms)
}

func (s *ConfigSuite) TestFileNotFound() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	_, err := s.loader.load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading config file")
}

func (s *ConfigSuite) TestReadError() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	_, err := s.loader.load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading config file")
}

func (s *ConfigSuite) TestInvalidJSON() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{not valid json`), nil
	}
	_, err := s.loader.load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing config file")
}

func (s *ConfigSuite) TestInvalidJSONTypes() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{"discord_token": 123}`), nil
	}
	_, err := s.loader.load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing config file")
}

func (s *ConfigSuite) TestHomeDirError() {
	s.loader.userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}
	_, err := s.loader.load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *ConfigSuite) TestMCPServersLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
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

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Len(s.T(), cfg.MCPServers, 1)
	srv := cfg.MCPServers["custom-tool"]
	require.Equal(s.T(), "/path/to/binary", srv.Command)
	require.Equal(s.T(), []string{"--flag"}, srv.Args)
	require.Equal(s.T(), map[string]string{"API_KEY": "secret"}, srv.Env)
}

func (s *ConfigSuite) TestMCPServersEmptyBlock() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"mcp": {"servers": {}}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Nil(s.T(), cfg.MCPServers)
}

func (s *ConfigSuite) TestZeroNumericValues() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"container_timeout_sec": 0,
			"container_memory_mb": 0,
			"container_cpus": 0,
			"container_keep_alive_sec": 0,
			"poll_interval_sec": 0
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Duration(0), cfg.ContainerTimeout)
	require.Equal(s.T(), int64(0), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 0.0, cfg.ContainerCPUs)
	require.Equal(s.T(), time.Duration(0), cfg.ContainerKeepAlive)
	require.Equal(s.T(), time.Duration(0), cfg.PollInterval)
}

func (s *ConfigSuite) TestJSONWithComments() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			// Required credentials
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			/* Optional settings */
			"log_level": "debug",
			// Trailing comma support
			"api_addr": ":9999",
		}`), nil
	}

	cfg, err := s.loader.load()
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
	_, err := os.ReadFile("/nonexistent/path/config.json")
	require.Error(s.T(), err)
}

func (s *ConfigSuite) TestTaskTemplatesLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
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

	cfg, err := s.loader.load()
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
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.TaskTemplates)
}

func (s *ConfigSuite) TestTaskTemplatesEmpty() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"task_templates": []
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.TaskTemplates)
}

func (s *ConfigSuite) TestExampleConfigEmbedded() {
	// Verify the embedded ExampleConfig is not empty
	require.NotEmpty(s.T(), ExampleConfig)
	require.Contains(s.T(), string(ExampleConfig), "platforms")
	require.Contains(s.T(), string(ExampleConfig), "discord_token")
	require.Contains(s.T(), string(ExampleConfig), "task_templates")
}

func (s *ConfigSuite) TestTemplatesEmbedded() {
	entries, err := Templates.ReadDir("templates")
	require.NoError(s.T(), err)
	require.GreaterOrEqual(s.T(), len(entries), 2)

	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
		data, err := Templates.ReadFile("templates/" + e.Name())
		require.NoError(s.T(), err)
		require.NotEmpty(s.T(), data)
	}
	require.True(s.T(), names["heartbeat.md"])
	require.True(s.T(), names["tk-auto-worker.md"])
}

func (s *ConfigSuite) TestLoadProjectConfigNoFile() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), mainCfg, merged) // Should return same config
}

func (s *ConfigSuite) TestLoadProjectConfigReadError() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return nil, errors.New("permission denied")
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}
	_, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading project config file")
}

func (s *ConfigSuite) TestLoadProjectConfigInvalidJSON() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{invalid json`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}
	_, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing project config file")
}

func (s *ConfigSuite) TestLoadProjectConfigInvalidJSONTypes() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{"mounts": "not-an-array"}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}
	_, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing project config file")
}

func (s *ConfigSuite) TestLoadProjectConfigMountsOnly() {
	s.loader.readFile = func(path string) ([]byte, error) {
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

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
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
	s.loader.readFile = func(path string) ([]byte, error) {
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

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
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
	s.loader.readFile = func(path string) ([]byte, error) {
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

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
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
	s.loader.readFile = func(path string) ([]byte, error) {
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

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Project MCP server should override main
	require.Len(s.T(), merged.MCPServers, 1)
	require.Equal(s.T(), "/bin/override", merged.MCPServers["main-srv"].Command)
}

func (s *ConfigSuite) TestLoadProjectConfigAbsolutePath() {
	s.loader.readFile = func(path string) ([]byte, error) {
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

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Absolute and tilde paths should not be modified
	require.Len(s.T(), merged.Mounts, 2)
	require.Equal(s.T(), "/absolute/path:/app/data", merged.Mounts[0])
	require.Equal(s.T(), "~/home/path:/app/home", merged.Mounts[1])
}

func (s *ConfigSuite) TestLoadProjectConfigInvalidMountFormat() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": ["invalid-mount-format"]
			}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{}

	_, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid mount format")
}

func (s *ConfigSuite) TestLoadProjectConfigEmptyConfig() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{}`), nil
		}
		return nil, errors.New("unexpected path")
	}

	mainCfg := &Config{
		Mounts:     []string{"~/.gitconfig:~/.gitconfig:ro"},
		MCPServers: map[string]MCPServerConfig{"main-srv": {Command: "/bin/main"}},
	}

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	// Main config should be unchanged
	require.Len(s.T(), merged.Mounts, 1)
	require.Equal(s.T(), "~/.gitconfig:~/.gitconfig:ro", merged.Mounts[0])
	require.Len(s.T(), merged.MCPServers, 1)
	require.Equal(s.T(), "/bin/main", merged.MCPServers["main-srv"].Command)
}

func (s *ConfigSuite) TestAnthropicAPIKeyLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"anthropic_api_key": "sk-ant-api-key-123"
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sk-ant-api-key-123", cfg.AnthropicAPIKey)
	require.Empty(s.T(), cfg.ClaudeCodeOAuthToken)
}

func (s *ConfigSuite) TestAnthropicAPIKeyAbsent() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.AnthropicAPIKey)
}

func (s *ConfigSuite) TestClaudeModelLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"claude_model": "claude-sonnet-4-5-20250929"
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude-sonnet-4-5-20250929", cfg.ClaudeModel)
}

func (s *ConfigSuite) TestClaudeModelAbsent() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.ClaudeModel)
}

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
	s.setupProjectReadFile(`{
		"mounts": [
			"./data:/app/data",
			"loop-npmcache:~/.npm",
			"loop-gocache:/go",
			"~/.ssh:~/.ssh:ro"
		]
	}`)

	mainCfg := &Config{}

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Mounts, 4)
	require.Equal(s.T(), "/project/data:/app/data", merged.Mounts[0])
	require.Equal(s.T(), "loop-npmcache:~/.npm", merged.Mounts[1])
	require.Equal(s.T(), "loop-gocache:/go", merged.Mounts[2])
	require.Equal(s.T(), "~/.ssh:~/.ssh:ro", merged.Mounts[3])
}

func (s *ConfigSuite) TestLoadWorktreeProjectConfigFallsBackToParent() {
	// Worktree dir has no .loop/config.json; parent dir does.
	parentCfg := `{"mounts": ["/parent/data:/data"]}`
	s.loader.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/project/.worktrees/wt1/.loop/config.json":
			return nil, os.ErrNotExist
		case "/project/.loop/config.json":
			return []byte(parentCfg), nil
		default:
			return nil, errors.New("unexpected path: " + path)
		}
	}

	mainCfg := &Config{}
	merged, err := s.loader.loadWorktreeProjectConfig("/project/.worktrees/wt1", "/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/parent/data:/data"}, merged.Mounts)
}

func (s *ConfigSuite) TestLoadWorktreeProjectConfigMergesParentAndWorktree() {
	// Worktree has its own config; result should be global → parent → worktree.
	parentCfg := `{"claude_model": "claude-opus-4-6", "mounts": ["/parent/data:/data"]}`
	worktreeCfg := `{"extra_dirs": ["/Users/user/dev/loop"]}`
	s.loader.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/project/.worktrees/wt1/.loop/config.json":
			return []byte(worktreeCfg), nil
		case "/project/.loop/config.json":
			return []byte(parentCfg), nil
		default:
			return nil, errors.New("unexpected path: " + path)
		}
	}

	mainCfg := &Config{}
	merged, err := s.loader.loadWorktreeProjectConfig("/project/.worktrees/wt1", "/project", mainCfg)
	require.NoError(s.T(), err)
	// Parent model is inherited.
	require.Equal(s.T(), "claude-opus-4-6", merged.ClaudeModel)
	// Parent mounts are inherited (worktree config doesn't override them).
	require.Equal(s.T(), []string{"/parent/data:/data"}, merged.Mounts)
	// Worktree extra_dirs are applied.
	require.Equal(s.T(), []string{"/Users/user/dev/loop"}, merged.ExtraDirs)
}

func (s *ConfigSuite) TestLoadWorktreeProjectConfigParentLoadError() {
	// Parent config is invalid; should surface the error.
	s.loader.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/project/.worktrees/wt1/.loop/config.json":
			return []byte(`{"extra_dirs":["x"]}`), nil
		case "/project/.loop/config.json":
			return []byte(`{invalid json`), nil
		default:
			return nil, errors.New("unexpected path: " + path)
		}
	}

	mainCfg := &Config{}
	_, err := s.loader.loadWorktreeProjectConfig("/project/.worktrees/wt1", "/project", mainCfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing project config file")
}

func (s *ConfigSuite) TestLoadWorktreeProjectConfigReadError() {
	// Non-os.IsNotExist error when checking worktree config; should still
	// attempt loadProjectConfig for worktreeDir (which will also fail).
	s.loader.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/project/.worktrees/wt1/.loop/config.json":
			return nil, errors.New("permission denied")
		case "/project/.loop/config.json":
			return []byte(`{"claude_model":"opus"}`), nil
		default:
			return nil, errors.New("unexpected path: " + path)
		}
	}

	mainCfg := &Config{}
	_, err := s.loader.loadWorktreeProjectConfig("/project/.worktrees/wt1", "/project", mainCfg)
	// The worktree readFile returns "permission denied" which is not os.IsNotExist,
	// so loadProjectConfig for worktreeDir is called, and it also gets "permission denied".
	require.Error(s.T(), err)
}

func (s *ConfigSuite) TestLoadWorktreeProjectConfigNoParentDir() {
	// No parentDir provided; falls back to regular loadProjectConfig for worktreeDir.
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/project/.worktrees/wt1/.loop/config.json" {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("unexpected path: " + path)
	}

	mainCfg := &Config{Mounts: []string{"~/.gitconfig:~/.gitconfig:ro"}}
	merged, err := s.loader.loadWorktreeProjectConfig("/project/.worktrees/wt1", "", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), mainCfg, merged)
}

func (s *ConfigSuite) TestLoadEnvsFromGlobal() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"envs": {"MY_VAR": "my-value", "NUM_VAR": 0, "BOOL_VAR": true}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "my-value", cfg.Envs["MY_VAR"])
	require.Equal(s.T(), "0", cfg.Envs["NUM_VAR"])
	require.Equal(s.T(), "true", cfg.Envs["BOOL_VAR"])
}

func (s *ConfigSuite) TestResolvePromptWithPrompt() {
	tmpl := &TaskTemplate{Name: "test", Prompt: "do stuff"}
	prompt, err := tmpl.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "do stuff", prompt)
}

func (s *ConfigSuite) TestResolvePromptWithPromptPath() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/loop/templates/daily.md" {
			return []byte("daily prompt content"), nil
		}
		return nil, os.ErrNotExist
	}

	tmpl := &TaskTemplate{Name: "test", PromptPath: "daily.md"}
	prompt, err := tmpl.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "daily prompt content", prompt)
}

func (s *ConfigSuite) TestResolvePromptWithBothSet() {
	tmpl := &TaskTemplate{Name: "test", Prompt: "inline", PromptPath: "file.md"}
	_, err := tmpl.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mutually exclusive")
}

func (s *ConfigSuite) TestResolvePromptWithNeitherSet() {
	tmpl := &TaskTemplate{Name: "test"}
	_, err := tmpl.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "one of prompt or prompt_path is required")
}

func (s *ConfigSuite) TestResolvePromptFileReadError() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("file not found")
	}

	tmpl := &TaskTemplate{Name: "test", PromptPath: "missing.md"}
	_, err := tmpl.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading prompt file")
}

func (s *ConfigSuite) TestLoadProjectConfigTemplatesWithPromptPath() {
	s.setupProjectReadFile(`{
		"task_templates": [
			{
				"name": "file-template",
				"description": "Template from file",
				"schedule": "0 9 * * *",
				"type": "cron",
				"prompt_path": "review.md"
			}
		]
	}`)

	mainCfg := &Config{}

	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)

	require.Len(s.T(), merged.TaskTemplates, 1)
	require.Equal(s.T(), "file-template", merged.TaskTemplates[0].Name)
	require.Equal(s.T(), "review.md", merged.TaskTemplates[0].PromptPath)
	require.Empty(s.T(), merged.TaskTemplates[0].Prompt)
}

func (s *ConfigSuite) TestMemoryConfigOllama() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
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

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.True(s.T(), cfg.Memory.Enabled)
	require.Equal(s.T(), "ollama", cfg.Memory.Embeddings.Provider)
	require.Equal(s.T(), "nomic-embed-text", cfg.Memory.Embeddings.Model)
	require.Equal(s.T(), "http://localhost:11434", cfg.Memory.Embeddings.OllamaURL)
}

func (s *ConfigSuite) TestMemoryConfigAbsent() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Memory.Enabled)
	require.Empty(s.T(), cfg.Memory.Embeddings.Provider)
}

func (s *ConfigSuite) TestMemoryConfigNotExplicitlyEnabled() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
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

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Memory.Enabled)
}

func (s *ConfigSuite) TestMemoryPathsLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"enabled": true,
				"paths": ["/shared/knowledge", "/path/to/notes.md"]
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/shared/knowledge", "/path/to/notes.md"}, cfg.Memory.Paths)
}

func (s *ConfigSuite) TestMemoryPathsDefault() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"./memory"}, cfg.Memory.Paths)
}

func (s *ConfigSuite) TestMemoryMaxChunkCharsLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"memory": {
				"enabled": true,
				"max_chunk_chars": 8000
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 8000, cfg.Memory.MaxChunkChars)
}

func (s *ConfigSuite) TestMemoryMaxChunkCharsDefault() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, cfg.Memory.MaxChunkChars)
}

func (s *ConfigSuite) TestPermissionsIsEmpty() {
	tests := []struct {
		name     string
		perms    types.Permissions
		expected bool
	}{
		{"both empty", types.Permissions{}, true},
		{"owners users only", types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}}, false},
		{"owners roles only", types.Permissions{Owners: types.RoleGrant{Roles: []string{"R1"}}}, false},
		{"members users only", types.Permissions{Members: types.RoleGrant{Users: []string{"U1"}}}, false},
		{"members roles only", types.Permissions{Members: types.RoleGrant{Roles: []string{"R1"}}}, false},
		{"all set", types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}, Roles: []string{"R1"}}, Members: types.RoleGrant{Users: []string{"U2"}}}, false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, tc.perms.IsEmpty())
		})
	}
}

func (s *ConfigSuite) TestPermissionsGetRole() {
	tests := []struct {
		name        string
		perms       types.Permissions
		authorID    string
		authorRoles []string
		expected    types.Role
	}{
		{"empty config returns empty role", types.Permissions{}, "any-user", nil, ""},
		{"owner by user", types.Permissions{Owners: types.RoleGrant{Users: []string{"U1", "U2"}}}, "U1", nil, types.RoleOwner},
		{"owner by role", types.Permissions{Owners: types.RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R1"}, types.RoleOwner},
		{"member by user", types.Permissions{Members: types.RoleGrant{Users: []string{"U1"}}}, "U1", nil, types.RoleMember},
		{"member by role", types.Permissions{Members: types.RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R1"}, types.RoleMember},
		{"owner beats member", types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}, Members: types.RoleGrant{Users: []string{"U1"}}}, "U1", nil, types.RoleOwner},
		{"user not in any list", types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}}, "U2", nil, ""},
		{"role match as owner", types.Permissions{Owners: types.RoleGrant{Roles: []string{"R1"}}}, "U2", []string{"R1", "R2"}, types.RoleOwner},
		{"role match as member", types.Permissions{Members: types.RoleGrant{Roles: []string{"R2"}}}, "U2", []string{"R1", "R2"}, types.RoleMember},
		{"no role match", types.Permissions{Owners: types.RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R2", "R3"}, ""},
		{"nil author roles with role restriction", types.Permissions{Owners: types.RoleGrant{Roles: []string{"R1"}}}, "U1", nil, ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, tc.perms.GetRole(tc.authorID, tc.authorRoles))
		})
	}
}

func (s *ConfigSuite) TestLoadWithPermissions() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"permissions": {
				"owners":  {"users": ["U1", "U2"], "roles": ["R1"]},
				"members": {"users": ["U3"], "roles": []}
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"U1", "U2"}, cfg.Permissions.Owners.Users)
	require.Equal(s.T(), []string{"R1"}, cfg.Permissions.Owners.Roles)
	require.Equal(s.T(), []string{"U3"}, cfg.Permissions.Members.Users)
}

func (s *ConfigSuite) TestCopyFilesDefault() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}
	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"~/.claude.json"}, cfg.CopyFiles)
}

func (s *ConfigSuite) TestCopyFilesExplicit() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms":["discord"],"discord_token":"t","discord_app_id":"a",
			"copy_files": ["~/.claude.json", "~/.npmrc"]
		}`), nil
	}
	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"~/.claude.json", "~/.npmrc"}, cfg.CopyFiles)
}

func (s *ConfigSuite) TestCopyFilesProjectOverride() {
	s.setupProjectReadFile(`{"copy_files": ["~/.npmrc"]}`)
	mainCfg := &Config{CopyFiles: []string{"~/.claude.json"}}
	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"~/.npmrc"}, merged.CopyFiles)
	require.Equal(s.T(), []string{"~/.claude.json"}, mainCfg.CopyFiles)
}

func (s *ConfigSuite) TestCopyFilesProjectNoOverride() {
	s.setupProjectReadFile(`{}`)
	mainCfg := &Config{CopyFiles: []string{"~/.claude.json"}}
	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"~/.claude.json"}, merged.CopyFiles)
}

func (s *ConfigSuite) TestLoadPlatformsArray() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord", "local"],
			"discord_token": "tok",
			"discord_app_id": "app"
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []types.Platform{types.PlatformDiscord, types.PlatformLocal}, cfg.Platforms)
}

func (s *ConfigSuite) TestLoadPlatformsLocalOnly() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["local"]
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []types.Platform{types.PlatformLocal}, cfg.Platforms)
}

func (s *ConfigSuite) TestLoadPlatformsValidation() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"]
		}`), nil
	}

	_, err := s.loader.load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord_token")
}

func (s *ConfigSuite) TestHasPlatform() {
	cfg := &Config{
		Platforms: []types.Platform{types.PlatformDiscord, types.PlatformLocal},
	}

	require.True(s.T(), cfg.HasPlatform(types.PlatformDiscord))
	require.True(s.T(), cfg.HasPlatform(types.PlatformLocal))
	require.False(s.T(), cfg.HasPlatform(types.PlatformSlack))
}

func (s *ConfigSuite) TestMemoryConfigOllamaCustomURL() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
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

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://gpu-server:11434", cfg.Memory.Embeddings.OllamaURL)
}

func (s *ConfigSuite) TestLoadPublicWrapper() {
	// Load() calls newLoader().load() which reads from real home dir.
	// Without a config file it returns an error — that's fine, we just verify it doesn't panic.
	_, err := Load()
	// Expect error since test home likely has no ~/.loop/config.json.
	// If it happens to succeed (dev machine), that's fine too.
	_ = err
}

func (s *ConfigSuite) TestLoadProjectConfigExtraDirs() {
	s.setupProjectReadFile(`{"extra_dirs": ["/home/user/lib", "/home/user/common"]}`)

	mainCfg := &Config{}
	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/home/user/lib", "/home/user/common"}, merged.ExtraDirs)
	// Main config should be unchanged.
	require.Empty(s.T(), mainCfg.ExtraDirs)
}

func (s *ConfigSuite) TestLoadProjectConfigExtraDirsEmpty() {
	s.setupProjectReadFile(`{}`)

	mainCfg := &Config{ExtraDirs: []string{"/global/dir"}}
	merged, err := s.loader.loadProjectConfig("/project", mainCfg)
	require.NoError(s.T(), err)
	// Empty project extra_dirs should not replace global.
	require.Equal(s.T(), []string{"/global/dir"}, merged.ExtraDirs)
}

func (s *ConfigSuite) TestLoadProjectConfigPublicWrapper() {
	dir := s.T().TempDir()
	base := &Config{Platforms: []types.Platform{types.PlatformLocal}}
	// No .loop/config.json in temp dir — returns main config unchanged.
	cfg, err := LoadProjectConfig(dir, base)
	require.NoError(s.T(), err)
	require.Equal(s.T(), base.Platforms, cfg.Platforms)
}

func (s *ConfigSuite) TestLoadWorktreeProjectConfigPublicWrapper() {
	worktree := s.T().TempDir()
	parent := s.T().TempDir()
	base := &Config{Platforms: []types.Platform{types.PlatformLocal}}
	// No .loop/config.json in either dir — returns main config unchanged.
	cfg, err := LoadWorktreeProjectConfig(worktree, parent, base)
	require.NoError(s.T(), err)
	require.Equal(s.T(), base.Platforms, cfg.Platforms)
}
