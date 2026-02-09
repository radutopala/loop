package config

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
	return []byte(`{"discord_token":"test-token","discord_app_id":"test-app-id"}`)
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
	require.Equal(s.T(), "info", cfg.LogLevel)
	require.Equal(s.T(), "text", cfg.LogFormat)
	require.Equal(s.T(), "loop-agent:latest", cfg.ContainerImage)
	require.Equal(s.T(), 300*time.Second, cfg.ContainerTimeout)
	require.Equal(s.T(), int64(512), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 1.0, cfg.ContainerCPUs)
	require.Equal(s.T(), 300*time.Second, cfg.ContainerKeepAlive)
	require.Equal(s.T(), 30*time.Second, cfg.PollInterval)
	require.Equal(s.T(), ":8222", cfg.APIAddr)
	require.Equal(s.T(), "/home/testuser/.loop", cfg.LoopDir)
	require.Empty(s.T(), cfg.ClaudeCodeOAuthToken)
	require.Empty(s.T(), cfg.DiscordGuildID)
	require.Nil(s.T(), cfg.MCPServers)
}

func (s *ConfigSuite) TestLoadCustomValues() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"discord_token": "custom-token",
			"discord_app_id": "custom-app-id",
			"claude_code_oauth_token": "sk-oauth",
			"discord_guild_id": "guild-123",
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

func (s *ConfigSuite) TestMissingRequired() {
	tests := []struct {
		name    string
		json    string
		missing string
	}{
		{
			name:    "missing all",
			json:    `{}`,
			missing: "discord_token",
		},
		{
			name:    "missing discord_app_id",
			json:    `{"discord_token":"tok"}`,
			missing: "discord_app_id",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			readFile = func(_ string) ([]byte, error) {
				return []byte(tc.json), nil
			}
			_, err := Load()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.missing)
		})
	}
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

func (s *ConfigSuite) TestMCPServersAbsent() {
	readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Nil(s.T(), cfg.MCPServers)
}

func (s *ConfigSuite) TestMCPServersEmptyBlock() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
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

	intVal := 42
	require.Equal(s.T(), 42, intPtrDefault(&intVal, 10))
	require.Equal(s.T(), 10, intPtrDefault(nil, 10))

	int64Val := int64(99)
	require.Equal(s.T(), int64(99), int64PtrDefault(&int64Val, 50))
	require.Equal(s.T(), int64(50), int64PtrDefault(nil, 50))

	floatVal := 3.14
	require.InDelta(s.T(), 3.14, floatPtrDefault(&floatVal, 1.0), 0.001)
	require.Equal(s.T(), 1.0, floatPtrDefault(nil, 1.0))
}

func (s *ConfigSuite) TestDefaultReadFile() {
	_, err := s.origReadFile("/nonexistent/path/config.json")
	require.Error(s.T(), err)
}

func (s *ConfigSuite) TestTaskTemplatesLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
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

func (s *ConfigSuite) TestClaudeModelLoaded() {
	readFile = func(_ string) ([]byte, error) {
		return []byte(`{
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

func (s *ConfigSuite) TestLoadProjectConfigDoesNotMutateMain() {
	readFile = func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{
				"mounts": ["./data:/app/data"],
				"mcp": {
					"servers": {
						"project-srv": {"command": "/bin/project"}
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

	// Verify main config was not mutated
	require.Len(s.T(), mainCfg.Mounts, 1)
	require.Equal(s.T(), "~/.gitconfig:~/.gitconfig:ro", mainCfg.Mounts[0])
	require.Len(s.T(), mainCfg.MCPServers, 1)
	require.Equal(s.T(), "/bin/main", mainCfg.MCPServers["main-srv"].Command)

	// Verify merged config has project mounts (replaced, not appended)
	require.Len(s.T(), merged.Mounts, 1)
	require.Equal(s.T(), "/project/data:/app/data", merged.Mounts[0])
	require.Len(s.T(), merged.MCPServers, 2)
	require.Equal(s.T(), "/bin/main", merged.MCPServers["main-srv"].Command)
	require.Equal(s.T(), "/bin/project", merged.MCPServers["project-srv"].Command)
}
