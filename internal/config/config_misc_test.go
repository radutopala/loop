package config

import (
	"errors"
	"os"
	"strings"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

func (s *ConfigSuite) TestQualityConfigFullBlock() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"quality": {
				"max_files": 12000,
				"exclude_paths": ["./generated/**", "./vendor/**"],
				"rules": {
					"signal_floor":     { "enabled": true,  "threshold": 6000 },
					"parse_fail":       { "enabled": true,  "threshold": 0.005 },
					"no_import_cycles": { "enabled": false }
				}
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 12000, cfg.Quality.MaxFiles)
	require.Equal(s.T(), []string{"./generated/**", "./vendor/**"}, cfg.Quality.ExcludePaths)
	require.Len(s.T(), cfg.Quality.Rules, 3)
	require.True(s.T(), cfg.Quality.Rules["signal_floor"].Enabled)
	require.Equal(s.T(), 6000.0, cfg.Quality.Rules["signal_floor"].Threshold)
	require.True(s.T(), cfg.Quality.Rules["parse_fail"].Enabled)
	require.Equal(s.T(), 0.005, cfg.Quality.Rules["parse_fail"].Threshold)
	require.False(s.T(), cfg.Quality.Rules["no_import_cycles"].Enabled)
}

func (s *ConfigSuite) TestQualityComplexityAndClonesBlock() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"quality": {
				"complexity": {
					"cyclomatic_t": 12,
					"cognitive_t":  20,
					"nesting_t":    5,
					"params_t":     6,
					"loc_t":        80
				},
				"clones": {
					"min_loc":      8,
					"max_distance": 2
				}
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 12, cfg.Quality.Complexity.CyclomaticT)
	require.Equal(s.T(), 20, cfg.Quality.Complexity.CognitiveT)
	require.Equal(s.T(), 5, cfg.Quality.Complexity.NestingT)
	require.Equal(s.T(), 6, cfg.Quality.Complexity.ParamsT)
	require.Equal(s.T(), 80, cfg.Quality.Complexity.LOCT)
	require.Equal(s.T(), 8, cfg.Quality.Clones.MinLOC)
	require.Equal(s.T(), 2, cfg.Quality.Clones.MaxDistance)
}

func (s *ConfigSuite) TestQualityComplexityAndClonesAbsentLeavesZero() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"quality": {}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), QualityComplexityConfig{}, cfg.Quality.Complexity)
	require.Equal(s.T(), QualityClonesConfig{}, cfg.Quality.Clones)
}

func (s *ConfigSuite) TestQualityRuleEnabledDefaultsTrue() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"quality": {
				"rules": {
					"signal_floor": { "threshold": 5500 }
				}
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.True(s.T(), cfg.Quality.Rules["signal_floor"].Enabled)
	require.Equal(s.T(), 5500.0, cfg.Quality.Rules["signal_floor"].Threshold)
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

func (s *ConfigSuite) TestReloadPublicWrapper() {
	// Reload() calls newLoader().reload() which reads from real home dir.
	// Same as Load() but skips platform validation.
	_, err := Reload()
	_ = err
}

func (s *ConfigSuite) TestReloadSkipsPlatformValidation() {
	// reload() should succeed without platform credentials — it only calls parse().
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{"log_level": "debug"}`), nil
	}
	cfg, err := s.loader.reload()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "debug", cfg.LogLevel)
	require.Empty(s.T(), cfg.Platforms)
}

func (s *ConfigSuite) TestParseReturnsDefaults() {
	// parse() returns config with defaults when given minimal JSON.
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{}`), nil
	}
	cfg, err := s.loader.parse()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "/home/testuser/.loop", cfg.LoopDir)
}

func (s *ConfigSuite) TestParseWorkflowConcurrency() {
	// parse() populates WorkflowConcurrency when present in JSON.
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{"workflow_concurrency": {"max_concurrent_runs": 3, "max_concurrent_nodes": 8}}`), nil
	}
	cfg, err := s.loader.parse()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, cfg.WorkflowConcurrency.MaxConcurrentRuns)
	require.Equal(s.T(), 8, cfg.WorkflowConcurrency.MaxConcurrentNodes)
}

func (s *ConfigSuite) TestParseHomeDirError() {
	s.loader.userHomeDir = func() (string, error) {
		return "", errors.New("no home")
	}
	_, err := s.loader.parse()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *ConfigSuite) TestParseReadError() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	_, err := s.loader.parse()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading config file")
}

func (s *ConfigSuite) TestPromptShortcutResolveInline() {
	sc := &PromptShortcut{Name: "review", Prompt: "Review this code"}
	prompt, err := sc.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Review this code", prompt)
}

func (s *ConfigSuite) TestReviewConfigResolveEmptyReturnsEmpty() {
	rc := &ReviewConfig{}
	prompt, err := rc.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "", prompt)
}

func (s *ConfigSuite) TestReviewConfigResolveInline() {
	rc := &ReviewConfig{Prompt: "custom review prompt"}
	prompt, err := rc.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom review prompt", prompt)
}

func (s *ConfigSuite) TestReviewConfigResolveFromFile() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/loop/review/team.md" {
			return []byte("file-based review prompt"), nil
		}
		return nil, errors.New("not found")
	}
	rc := &ReviewConfig{PromptPath: "team.md"}
	prompt, err := rc.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "file-based review prompt", prompt)
}

func (s *ConfigSuite) TestReviewConfigResolveBothSetIsError() {
	rc := &ReviewConfig{Prompt: "x", PromptPath: "y.md"}
	_, err := rc.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
}

func (s *ConfigSuite) TestReviewConfigLoadFromJSON() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"review": { "prompt": "my prompt" }
		}`), nil
	}
	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "my prompt", cfg.Review.Prompt)
}

func (s *ConfigSuite) TestPromptShortcutResolveFromFile() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/loop/shortcuts/review.md" {
			return []byte("file-based prompt"), nil
		}
		return nil, errors.New("not found")
	}
	sc := &PromptShortcut{Name: "review", PromptPath: "review.md"}
	prompt, err := sc.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "file-based prompt", prompt)
}

func (s *ConfigSuite) TestLoadProjectConfigShortcutsMerge() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "config.json") {
			return s.minimalJSON(), nil
		}
		return nil, os.ErrNotExist
	}
	base, err := s.loader.load()
	require.NoError(s.T(), err)
	base.PromptShortcuts = []PromptShortcut{
		{Name: "global", Prompt: "global prompt"},
		{Name: "override-me", Prompt: "old prompt"},
	}

	s.setupProjectReadFile(`{
		"prompt_shortcuts": [
			{"name": "override-me", "description": "updated", "prompt": "new prompt"},
			{"name": "local-only", "prompt": "local prompt"}
		]
	}`)

	merged, err := s.loader.loadProjectConfig("/project", base)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.PromptShortcuts, 3)
	require.Equal(s.T(), "global", merged.PromptShortcuts[0].Name)
	require.Equal(s.T(), "override-me", merged.PromptShortcuts[1].Name)
	require.Equal(s.T(), "new prompt", merged.PromptShortcuts[1].Prompt)
	require.Equal(s.T(), "local-only", merged.PromptShortcuts[2].Name)
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

// Tests for NodeDef.ResolvePrompt (line 93 in config.go).

func (s *ConfigSuite) TestNodeDefResolvePromptInline() {
	node := &NodeDef{ID: "my-node", Prompt: "do the thing"}
	prompt, err := node.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "do the thing", prompt)
}

func (s *ConfigSuite) TestNodeDefResolvePromptFromFile() {
	s.loader.readFile = func(path string) ([]byte, error) {
		if path == "/loop/workflows/review.md" {
			return []byte("workflow file prompt"), nil
		}
		return nil, errors.New("unexpected path: " + path)
	}

	node := &NodeDef{ID: "my-node", PromptPath: "review.md"}
	prompt, err := node.ResolvePrompt("/loop", s.loader.readFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "workflow file prompt", prompt)
}

func (s *ConfigSuite) TestNodeDefResolvePromptFileReadError() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}

	node := &NodeDef{ID: "my-node", PromptPath: "missing.md"}
	_, err := node.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading prompt file")
}

func (s *ConfigSuite) TestNodeDefResolvePromptBothSet() {
	node := &NodeDef{ID: "my-node", Prompt: "inline", PromptPath: "file.md"}
	_, err := node.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mutually exclusive")
}

func (s *ConfigSuite) TestNodeDefResolvePromptNeitherSet() {
	node := &NodeDef{ID: "my-node"}
	_, err := node.ResolvePrompt("/loop", s.loader.readFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "one of prompt or prompt_path is required")
}
