package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ConfigSuite struct {
	suite.Suite
	origEnv     map[string]string
	origHomeDir func() (string, error)
}

var envKeys = []string{
	"DISCORD_TOKEN", "DISCORD_APP_ID", "CLAUDE_BIN_PATH",
	"DB_PATH", "LOG_LEVEL", "LOG_FORMAT", "CONTAINER_IMAGE",
	"CONTAINER_TIMEOUT_SEC", "CONTAINER_MEMORY_MB", "CONTAINER_CPUS",
	"POLL_INTERVAL_SEC", "MOUNT_ALLOWLIST", "API_ADDR",
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}

func (s *ConfigSuite) SetupTest() {
	s.origEnv = map[string]string{}
	for _, k := range envKeys {
		s.origEnv[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	// Point home dir to a temp dir so Load() doesn't read the real ~/.loop/.env
	s.origHomeDir = userHomeDir
	userHomeDir = func() (string, error) {
		return s.T().TempDir(), nil
	}
}

func (s *ConfigSuite) TearDownTest() {
	userHomeDir = s.origHomeDir
	for k, v := range s.origEnv {
		if v != "" {
			os.Setenv(k, v)
		} else {
			os.Unsetenv(k)
		}
	}
}

func (s *ConfigSuite) setRequired() {
	os.Setenv("DISCORD_TOKEN", "test-token")
	os.Setenv("DISCORD_APP_ID", "test-app-id")
}

func (s *ConfigSuite) TestLoadDefaults() {
	s.setRequired()

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test-token", cfg.DiscordToken)
	require.Equal(s.T(), "test-app-id", cfg.DiscordAppID)
	require.Equal(s.T(), "claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "loop.db", cfg.DBPath)
	require.Equal(s.T(), "info", cfg.LogLevel)
	require.Equal(s.T(), "text", cfg.LogFormat)
	require.Equal(s.T(), "loop-agent:latest", cfg.ContainerImage)
	require.Equal(s.T(), 300*time.Second, cfg.ContainerTimeout)
	require.Equal(s.T(), int64(512), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 1.0, cfg.ContainerCPUs)
	require.Equal(s.T(), 30*time.Second, cfg.PollInterval)
	require.Empty(s.T(), cfg.MountAllowlist)
	require.Equal(s.T(), ":8222", cfg.APIAddr)
	require.Contains(s.T(), cfg.WorkDir, filepath.Join(".loop", "work"))
}

func (s *ConfigSuite) TestLoadCustomValues() {
	s.setRequired()
	os.Setenv("DB_PATH", "/tmp/test.db")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("LOG_FORMAT", "json")
	os.Setenv("CONTAINER_IMAGE", "custom-agent:v2")
	os.Setenv("CONTAINER_TIMEOUT_SEC", "600")
	os.Setenv("CONTAINER_MEMORY_MB", "1024")
	os.Setenv("CONTAINER_CPUS", "2.5")
	os.Setenv("POLL_INTERVAL_SEC", "60")
	os.Setenv("MOUNT_ALLOWLIST", "/home/user, /tmp/data , /var/log")
	os.Setenv("CLAUDE_BIN_PATH", "/usr/local/bin/claude")
	os.Setenv("API_ADDR", ":9999")

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/tmp/test.db", cfg.DBPath)
	require.Equal(s.T(), "debug", cfg.LogLevel)
	require.Equal(s.T(), "json", cfg.LogFormat)
	require.Equal(s.T(), "custom-agent:v2", cfg.ContainerImage)
	require.Equal(s.T(), 600*time.Second, cfg.ContainerTimeout)
	require.Equal(s.T(), int64(1024), cfg.ContainerMemoryMB)
	require.Equal(s.T(), 2.5, cfg.ContainerCPUs)
	require.Equal(s.T(), 60*time.Second, cfg.PollInterval)
	require.Equal(s.T(), []string{"/home/user", "/tmp/data", "/var/log"}, cfg.MountAllowlist)
	require.Equal(s.T(), "/usr/local/bin/claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), ":9999", cfg.APIAddr)
}

func (s *ConfigSuite) TestMissingRequired() {
	tests := []struct {
		name    string
		setEnv  map[string]string
		missing string
	}{
		{
			name:    "missing all",
			setEnv:  map[string]string{},
			missing: "DISCORD_TOKEN",
		},
		{
			name:    "missing discord app id",
			setEnv:  map[string]string{"DISCORD_TOKEN": "tok"},
			missing: "DISCORD_APP_ID",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			os.Unsetenv("DISCORD_TOKEN")
			os.Unsetenv("DISCORD_APP_ID")
			for k, v := range tc.setEnv {
				os.Setenv(k, v)
			}
			_, err := Load()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.missing)
		})
	}
}

func (s *ConfigSuite) TestInvalidNumericEnvVars() {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		errText string
	}{
		{"invalid timeout", "CONTAINER_TIMEOUT_SEC", "not-a-number", "CONTAINER_TIMEOUT_SEC"},
		{"invalid memory", "CONTAINER_MEMORY_MB", "bad", "CONTAINER_MEMORY_MB"},
		{"invalid cpus", "CONTAINER_CPUS", "xyz", "CONTAINER_CPUS"},
		{"invalid poll interval", "POLL_INTERVAL_SEC", "abc", "POLL_INTERVAL_SEC"},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.setRequired()
			os.Setenv(tc.envKey, tc.envVal)
			defer os.Unsetenv(tc.envKey)
			_, err := Load()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.errText)
		})
	}
}

func (s *ConfigSuite) TestMountAllowlistEmpty() {
	s.setRequired()
	os.Setenv("MOUNT_ALLOWLIST", "")
	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.MountAllowlist)
}

func (s *ConfigSuite) TestSplitAndTrim() {
	require.Equal(s.T(), []string{"a", "b", "c"}, splitAndTrim("a, b, c"))
	require.Equal(s.T(), []string{"single"}, splitAndTrim("single"))
	require.Nil(s.T(), splitAndTrim(""))
	require.Equal(s.T(), []string{"a", "b"}, splitAndTrim(" a , b "))
}

func (s *ConfigSuite) TestTrimSpace() {
	require.Equal(s.T(), "hello", trimSpace("  hello  "))
	require.Equal(s.T(), "hello", trimSpace("hello"))
	require.Equal(s.T(), "", trimSpace(""))
	require.Equal(s.T(), "", trimSpace("   "))
	require.Equal(s.T(), "a b", trimSpace("\ta b\t"))
}

func (s *ConfigSuite) TestSplitOn() {
	require.Equal(s.T(), []string{"a", "b", "c"}, splitOn("a,b,c", ','))
	require.Equal(s.T(), []string{"single"}, splitOn("single", ','))
	require.Equal(s.T(), []string{""}, splitOn("", ','))
}

func (s *ConfigSuite) TestGetEnvDefault() {
	os.Setenv("TEST_ENV_DEFAULT_SET", "myval")
	defer os.Unsetenv("TEST_ENV_DEFAULT_SET")
	require.Equal(s.T(), "myval", getEnvDefault("TEST_ENV_DEFAULT_SET", "fallback"))
	require.Equal(s.T(), "fallback", getEnvDefault("TEST_ENV_DEFAULT_NOTSET", "fallback"))
}

func (s *ConfigSuite) TestGetEnvInt() {
	os.Setenv("TEST_ENV_INT", "42")
	defer os.Unsetenv("TEST_ENV_INT")
	v, err := getEnvInt("TEST_ENV_INT", 10)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 42, v)

	v, err = getEnvInt("TEST_ENV_INT_NOTSET", 10)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 10, v)

	os.Setenv("TEST_ENV_INT_BAD", "xyz")
	defer os.Unsetenv("TEST_ENV_INT_BAD")
	_, err = getEnvInt("TEST_ENV_INT_BAD", 10)
	require.Error(s.T(), err)
}

func (s *ConfigSuite) TestGetEnvFloat() {
	os.Setenv("TEST_ENV_FLOAT", "3.14")
	defer os.Unsetenv("TEST_ENV_FLOAT")
	v, err := getEnvFloat("TEST_ENV_FLOAT", 1.0)
	require.NoError(s.T(), err)
	require.InDelta(s.T(), 3.14, v, 0.001)

	v, err = getEnvFloat("TEST_ENV_FLOAT_NOTSET", 1.0)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1.0, v)

	os.Setenv("TEST_ENV_FLOAT_BAD", "abc")
	defer os.Unsetenv("TEST_ENV_FLOAT_BAD")
	_, err = getEnvFloat("TEST_ENV_FLOAT_BAD", 1.0)
	require.Error(s.T(), err)
}

// --- .env file loading tests ---

func (s *ConfigSuite) TestDefaultEnvFilePath() {
	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}
	path, err := DefaultEnvFilePath()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/testuser/.loop/.env", path)
}

func (s *ConfigSuite) TestDefaultEnvFilePathError() {
	userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}
	_, err := DefaultEnvFilePath()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *ConfigSuite) TestLoadEnvFileNotExist() {
	err := loadEnvFile("/nonexistent/path/.env")
	require.NoError(s.T(), err)
}

func (s *ConfigSuite) TestLoadEnvFileParsing() {
	tests := []struct {
		name     string
		content  string
		expected map[string]string
	}{
		{
			name:     "basic key=value",
			content:  "MY_TEST_KEY=my_value\n",
			expected: map[string]string{"MY_TEST_KEY": "my_value"},
		},
		{
			name:     "comments and blank lines",
			content:  "# This is a comment\n\nVALID_KEY=valid_value\n# Another comment\n\n",
			expected: map[string]string{"VALID_KEY": "valid_value"},
		},
		{
			name:     "quoted values",
			content:  "DOUBLE_Q=\"double quoted\"\nSINGLE_Q='single quoted'\nNO_QUOTE=plain\n",
			expected: map[string]string{"DOUBLE_Q": "double quoted", "SINGLE_Q": "single quoted", "NO_QUOTE": "plain"},
		},
		{
			name:     "skips lines without equals",
			content:  "NO_EQUALS_LINE\nGOOD_KEY=good_value\n",
			expected: map[string]string{"GOOD_KEY": "good_value"},
		},
		{
			name:     "spaces around key and value",
			content:  "  SPACED_KEY  =  spaced_value  \n",
			expected: map[string]string{"SPACED_KEY": "spaced_value"},
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			tmpDir := s.T().TempDir()
			envFile := filepath.Join(tmpDir, ".env")
			require.NoError(s.T(), os.WriteFile(envFile, []byte(tc.content), 0o644))
			for k := range tc.expected {
				defer os.Unsetenv(k)
			}

			err := loadEnvFile(envFile)
			require.NoError(s.T(), err)
			for k, v := range tc.expected {
				require.Equal(s.T(), v, os.Getenv(k), "key: %s", k)
			}
		})
	}
}

func (s *ConfigSuite) TestLoadEnvFileDoesNotOverrideExisting() {
	tmpDir := s.T().TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	require.NoError(s.T(), os.WriteFile(envFile, []byte("MY_OVERRIDE_KEY=from_file\n"), 0o644))

	os.Setenv("MY_OVERRIDE_KEY", "from_env")
	defer os.Unsetenv("MY_OVERRIDE_KEY")

	err := loadEnvFile(envFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "from_env", os.Getenv("MY_OVERRIDE_KEY"))
}

func (s *ConfigSuite) TestLoadEnvFileReadError() {
	// Create a directory where a file is expected — reading it will fail
	tmpDir := s.T().TempDir()
	dirAsFile := filepath.Join(tmpDir, ".env")
	require.NoError(s.T(), os.Mkdir(dirAsFile, 0o755))

	err := loadEnvFile(dirAsFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading env file")
}

func (s *ConfigSuite) TestLoadFromEnvFile() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.Mkdir(loopDir, 0o755))
	envFile := filepath.Join(loopDir, ".env")
	content := "DISCORD_TOKEN=file-token\nDISCORD_APP_ID=file-app-id\n"
	require.NoError(s.T(), os.WriteFile(envFile, []byte(content), 0o644))
	defer os.Unsetenv("DISCORD_TOKEN")
	defer os.Unsetenv("DISCORD_APP_ID")

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "file-token", cfg.DiscordToken)
	require.Equal(s.T(), "file-app-id", cfg.DiscordAppID)
}

func (s *ConfigSuite) TestLoadEnvOverridesFile() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.Mkdir(loopDir, 0o755))
	envFile := filepath.Join(loopDir, ".env")
	content := "DISCORD_TOKEN=file-token\nDISCORD_APP_ID=file-app-id\n"
	require.NoError(s.T(), os.WriteFile(envFile, []byte(content), 0o644))
	defer os.Unsetenv("DISCORD_TOKEN")
	defer os.Unsetenv("DISCORD_APP_ID")

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}

	os.Setenv("DISCORD_TOKEN", "env-token")

	cfg, err := Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "env-token", cfg.DiscordToken)
	require.Equal(s.T(), "file-app-id", cfg.DiscordAppID)
}

func (s *ConfigSuite) TestLoadHomeDirError() {
	userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}
	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *ConfigSuite) TestLoadEnvFileSetenvError() {
	tmpDir := s.T().TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	// Empty key before '=' triggers os.Setenv("", "value") which fails
	content := "=value\n"
	require.NoError(s.T(), os.WriteFile(envFile, []byte(content), 0o644))

	err := loadEnvFile(envFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "setting env var")
}

func (s *ConfigSuite) TestLoadEnvFileReadErrorViaLoad() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.Mkdir(loopDir, 0o755))
	// Create .env as a directory to cause a read error
	envDir := filepath.Join(loopDir, ".env")
	require.NoError(s.T(), os.Mkdir(envDir, 0o755))

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}

	_, err := Load()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading env file")
}
