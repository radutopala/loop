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
			"poll_interval_sec": 60,
			"api_addr": ":9999"
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
	require.Equal(s.T(), 60*time.Second, cfg.PollInterval)
	require.Equal(s.T(), ":9999", cfg.APIAddr)
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
			"poll_interval_sec": 0
		}`), nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Duration(0), cfg.ContainerTimeout)
	require.Equal(s.T(), int64(0), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 0.0, cfg.ContainerCPUs)
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
