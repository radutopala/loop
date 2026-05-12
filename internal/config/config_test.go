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
	require.True(s.T(), cfg.Desktop.Islands)
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
			"claude_bin_path": "/custom/claude",
			"desktop": {"auto_save_on_blur": true}
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
	require.True(s.T(), cfg.Desktop.AutoSaveOnBlur)
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

func (s *ConfigSuite) TestLoadGitHubConfig() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"github": { "gh_user": "radutopala" }
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "radutopala", cfg.GitHub.GHUser)
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

func (s *ConfigSuite) TestWorkflowsLoaded() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"workflows": [
				{
					"name": "code-review",
					"description": "Review changes",
					"nodes": [
						{"id": "diff", "type": "bash", "script": "git diff main...HEAD"},
						{"id": "review", "type": "prompt", "depends_on": ["diff"], "prompt": "Review: {{.NodeOutputs.diff}}"}
					]
				},
				{
					"name": "validate",
					"description": "Run tests in parallel",
					"inputs": {"branch": {"description": "Branch name", "required": true}},
					"nodes": [
						{"id": "test", "type": "bash", "script": "make test"},
						{"id": "lint", "type": "bash", "script": "make lint"},
						{"id": "report", "type": "prompt", "depends_on": ["test", "lint"], "prompt": "Summarize"}
					]
				}
			]
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Len(s.T(), cfg.Workflows, 2)

	wf1 := cfg.Workflows[0]
	require.Equal(s.T(), "code-review", wf1.Name)
	require.Equal(s.T(), "Review changes", wf1.Description)
	require.Len(s.T(), wf1.Nodes, 2)
	require.Equal(s.T(), "diff", wf1.Nodes[0].ID)
	require.Equal(s.T(), NodeTypeBash, wf1.Nodes[0].Type)
	require.Equal(s.T(), "git diff main...HEAD", wf1.Nodes[0].Script)
	require.Equal(s.T(), "review", wf1.Nodes[1].ID)
	require.Equal(s.T(), NodeTypePrompt, wf1.Nodes[1].Type)
	require.Equal(s.T(), []string{"diff"}, wf1.Nodes[1].DependsOn)

	wf2 := cfg.Workflows[1]
	require.Equal(s.T(), "validate", wf2.Name)
	require.Len(s.T(), wf2.Inputs, 1)
	require.True(s.T(), wf2.Inputs["branch"].Required)
	require.Len(s.T(), wf2.Nodes, 3)
}

func (s *ConfigSuite) TestWorkflowsAbsent() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.Workflows)
}

func (s *ConfigSuite) TestWorkflowsEmpty() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "tok",
			"discord_app_id": "app",
			"workflows": []
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Empty(s.T(), cfg.Workflows)
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
