package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SchemaSuite struct {
	suite.Suite
}

func TestSchemaSuite(t *testing.T) {
	suite.Run(t, new(SchemaSuite))
}

func (s *SchemaSuite) TestGlobalConfigSchemaReturnsNonNil() {
	schema := GlobalConfigSchema()
	require.NotNil(s.T(), schema)
	require.Equal(s.T(), "object", schema.Type)
	require.NotEmpty(s.T(), schema.Properties)
}

func (s *SchemaSuite) TestGlobalConfigSchemaSingleton() {
	schema1 := GlobalConfigSchema()
	schema2 := GlobalConfigSchema()
	// Same pointer — singleton behavior via sync.Once.
	require.Same(s.T(), schema1, schema2)
}

func (s *SchemaSuite) TestTopLevelProperties() {
	schema := GlobalConfigSchema()
	expectedKeys := []string{
		"claude_model", "claude_bin_path", "streaming_enabled",
		"claude_code_oauth_token", "anthropic_api_key",
		"container_image", "container_memory_mb", "container_cpus",
		"container_timeout_sec", "keep_mcp_configs",
		"browser", "memory",
		"extra_dirs", "mounts", "copy_files",
		"platforms",
		"discord_token", "discord_app_id", "discord_guild_id",
		"slack_bot_token", "slack_app_token",
		"envs",
		"log_level", "log_format", "log_file",
		"api_addr", "db_path", "poll_interval_sec",
	}
	for _, key := range expectedKeys {
		s.Run(key, func() {
			require.Contains(s.T(), schema.Properties, key)
		})
	}
}

func (s *SchemaSuite) TestClaudeModelHasEnum() {
	prop := GlobalConfigSchema().Properties["claude_model"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "string", prop.Type)
	require.NotEmpty(s.T(), prop.Enum)
	// Should contain the empty string (for "no override") plus model names.
	require.Contains(s.T(), prop.Enum, "")
}

func (s *SchemaSuite) TestContainerMemoryMBIsInteger() {
	prop := GlobalConfigSchema().Properties["container_memory_mb"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "integer", prop.Type)
	require.Equal(s.T(), 1024, prop.Default)
}

func (s *SchemaSuite) TestDiscordTokenIsSecret() {
	prop := GlobalConfigSchema().Properties["discord_token"]
	require.NotNil(s.T(), prop)
	require.True(s.T(), prop.XSecret)
}

func (s *SchemaSuite) TestBrowserNestedObject() {
	prop := GlobalConfigSchema().Properties["browser"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "object", prop.Type)
	require.NotNil(s.T(), prop.Properties)
	require.Contains(s.T(), prop.Properties, "enabled")
	require.Contains(s.T(), prop.Properties, "chrome_image")

	enabled := prop.Properties["enabled"]
	require.Equal(s.T(), "boolean", enabled.Type)
	require.Equal(s.T(), true, enabled.Default)

	chromeImage := prop.Properties["chrome_image"]
	require.Equal(s.T(), "string", chromeImage.Type)
	require.Equal(s.T(), "loop-chrome:latest", chromeImage.XPlaceholder)
}

func (s *SchemaSuite) TestMemoryNestedObject() {
	prop := GlobalConfigSchema().Properties["memory"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "object", prop.Type)
	require.Contains(s.T(), prop.Properties, "enabled")
	require.Contains(s.T(), prop.Properties, "paths")
	require.Contains(s.T(), prop.Properties, "reindex_interval_sec")
	require.Contains(s.T(), prop.Properties, "embeddings")

	paths := prop.Properties["paths"]
	require.Equal(s.T(), "array", paths.Type)
	require.NotNil(s.T(), paths.Items)
	require.Equal(s.T(), "string", paths.Items.Type)

	embeddings := prop.Properties["embeddings"]
	require.Equal(s.T(), "object", embeddings.Type)
	require.Contains(s.T(), embeddings.Properties, "provider")
	require.Contains(s.T(), embeddings.Properties, "model")
}

func (s *SchemaSuite) TestGlobalOnlyFields() {
	schema := GlobalConfigSchema()
	globalOnlyKeys := []string{
		"platforms",
		"discord_token", "discord_app_id", "discord_guild_id",
		"slack_bot_token", "slack_app_token",
		"log_level", "log_format", "log_file",
		"container_timeout_sec",
		"api_addr", "db_path", "poll_interval_sec",
	}
	for _, key := range globalOnlyKeys {
		s.Run(key, func() {
			prop := schema.Properties[key]
			require.NotNil(s.T(), prop)
			require.True(s.T(), prop.XGlobalOnly, "expected x-global-only for %s", key)
		})
	}
}

func (s *SchemaSuite) TestNonGlobalOnlyFields() {
	schema := GlobalConfigSchema()
	// These fields should NOT have x-global-only set.
	nonGlobalKeys := []string{
		"claude_model", "claude_bin_path", "streaming_enabled",
		"container_image", "container_memory_mb", "container_cpus",
		"browser", "memory", "extra_dirs", "mounts", "copy_files", "envs",
	}
	for _, key := range nonGlobalKeys {
		s.Run(key, func() {
			prop := schema.Properties[key]
			require.NotNil(s.T(), prop)
			require.False(s.T(), prop.XGlobalOnly, "expected no x-global-only for %s", key)
		})
	}
}

func (s *SchemaSuite) TestSecretFields() {
	schema := GlobalConfigSchema()
	secretKeys := []string{
		"discord_token",
		"claude_code_oauth_token",
		"anthropic_api_key",
		"slack_bot_token",
		"slack_app_token",
	}
	for _, key := range secretKeys {
		s.Run(key, func() {
			prop := schema.Properties[key]
			require.NotNil(s.T(), prop)
			require.True(s.T(), prop.XSecret, "expected x-secret for %s", key)
		})
	}
}

func (s *SchemaSuite) TestContainerCPUsHasStep() {
	prop := GlobalConfigSchema().Properties["container_cpus"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "number", prop.Type)
	require.Equal(s.T(), 0.5, prop.XStep)
	require.Equal(s.T(), 1.0, prop.Default)
}

func (s *SchemaSuite) TestEnvsHasAdditionalProperties() {
	prop := GlobalConfigSchema().Properties["envs"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "object", prop.Type)
	require.NotNil(s.T(), prop.AdditionalProperties)
	require.Equal(s.T(), "string", prop.AdditionalProperties.Type)
}

func (s *SchemaSuite) TestLogLevelEnum() {
	prop := GlobalConfigSchema().Properties["log_level"]
	require.NotNil(s.T(), prop)
	require.Equal(s.T(), "string", prop.Type)
	require.Equal(s.T(), []any{"info", "debug", "warn", "error"}, prop.Enum)
}

func (s *SchemaSuite) TestSectionAssignment() {
	schema := GlobalConfigSchema()
	sectionChecks := map[string]string{
		"claude_model":            "Claude",
		"claude_code_oauth_token": "Authentication",
		"container_image":         "Container",
		"browser":                 "Browser",
		"memory":                  "Memory",
		"extra_dirs":              "Workspace",
		"platforms":               "Platforms",
		"discord_token":           "Discord",
		"slack_bot_token":         "Slack",
		"envs":                    "Environment",
		"log_level":               "Logging",
		"api_addr":                "API",
	}
	for key, expectedSection := range sectionChecks {
		s.Run(key, func() {
			prop := schema.Properties[key]
			require.NotNil(s.T(), prop)
			require.Equal(s.T(), expectedSection, prop.XSection)
		})
	}
}
