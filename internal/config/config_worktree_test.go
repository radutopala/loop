package config

import (
	"errors"
	"os"

	"github.com/stretchr/testify/require"
)

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
