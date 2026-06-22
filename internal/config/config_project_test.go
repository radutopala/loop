package config

import (
	"errors"
	"os"

	"github.com/stretchr/testify/require"
)

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
	require.Equal(s.T(), "claude-sonnet-4-6", cfg.ClaudeModel)
}

func (s *ConfigSuite) TestClaudeBatchDisallowedToolsDefault() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), DefaultBatchDisallowedTools(), cfg.ClaudeBatchDisallowedTools)
	require.Contains(s.T(), cfg.ClaudeBatchDisallowedTools, "ScheduleWakeup")
}

func (s *ConfigSuite) TestClaudeBatchDisallowedToolsOverride() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{"platforms":["discord"],"discord_token":"t","discord_app_id":"a","claude_batch_disallowed_tools":["OnlyThis"]}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"OnlyThis"}, cfg.ClaudeBatchDisallowedTools)
}
