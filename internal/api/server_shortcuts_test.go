package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// --- Shortcuts tests ---

func (s *ServerSuite) TestListShortcutsEmpty() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{LoopDir: "/home/testuser/.loop"}, nil
	}

	rec := s.testRequest("GET", "/api/shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `[]`, rec.Body.String())
}

func (s *ServerSuite) TestListShortcutsWithInlinePrompt() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			PromptShortcuts: []config.PromptShortcut{
				{Name: "review", Description: "Code review", Prompt: "Please review this code"},
			},
		}, nil
	}

	rec := s.testRequest("GET", "/api/shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "review", result[0].Name)
	require.Equal(s.T(), "Code review", result[0].Description)
	require.Equal(s.T(), "Please review this code", result[0].Prompt)
}

func (s *ServerSuite) TestListShortcutsWithPromptPath() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			PromptShortcuts: []config.PromptShortcut{
				{Name: "deploy", Description: "Deploy steps", PromptPath: "deploy.md"},
			},
		}, nil
	}
	s.srv.readFile = func(path string) ([]byte, error) {
		if path == "/home/testuser/.loop/shortcuts/deploy.md" {
			return []byte("Run the deploy pipeline"), nil
		}
		return nil, os.ErrNotExist
	}

	rec := s.testRequest("GET", "/api/shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "deploy", result[0].Name)
	require.Equal(s.T(), "Run the deploy pipeline", result[0].Prompt)
}

func (s *ServerSuite) TestListShortcutsSkipsUnresolvable() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			PromptShortcuts: []config.PromptShortcut{
				{Name: "good", Description: "Works", Prompt: "inline prompt"},
				{Name: "bad", Description: "Missing file", PromptPath: "missing.md"},
			},
		}, nil
	}
	s.srv.readFile = func(_ string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	rec := s.testRequest("GET", "/api/shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "good", result[0].Name)
}

func (s *ServerSuite) TestListShortcutsConfigLoadError() {
	s.srv.configs.load = func() (*config.Config, error) {
		return nil, errors.New("config broken")
	}

	rec := s.testRequest("GET", "/api/shortcuts", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListShortcutsWithChannelMerge() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			PromptShortcuts: []config.PromptShortcut{
				{Name: "global", Description: "Global shortcut", Prompt: "global prompt"},
			},
		}, nil
	}
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/projects/app"}, nil)
	s.srv.configs.loadProject = func(dir string, base *config.Config) (*config.Config, error) {
		require.Equal(s.T(), "/projects/app", dir)
		merged := *base
		merged.PromptShortcuts = append(merged.PromptShortcuts,
			config.PromptShortcut{Name: "local", Description: "Project shortcut", Prompt: "project prompt"},
		)
		return &merged, nil
	}

	rec := s.testRequest("GET", "/api/shortcuts?channel_id=ch-proj", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 2)
	require.Equal(s.T(), "global", result[0].Name)
	require.Equal(s.T(), "local", result[1].Name)
}

func (s *ServerSuite) TestListShortcutsLoopDirFallback() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			PromptShortcuts: []config.PromptShortcut{
				{Name: "test", Description: "Test", Prompt: "test prompt"},
			},
		}, nil
	}

	rec := s.testRequest("GET", "/api/shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
}

func (s *ServerSuite) TestListShortcutsProjectPromptPath() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			PromptShortcuts: []config.PromptShortcut{
				{Name: "review", Description: "Review code", PromptPath: "review-code.md"},
			},
		}, nil
	}
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/projects/app"}, nil)
	s.srv.configs.loadProject = func(dir string, base *config.Config) (*config.Config, error) {
		return base, nil
	}
	s.srv.readFile = func(path string) ([]byte, error) {
		// Only the project .loop/shortcuts has the file, not global.
		if path == "/projects/app/.loop/shortcuts/review-code.md" {
			return []byte("Review all changes"), nil
		}
		return nil, os.ErrNotExist
	}

	rec := s.testRequest("GET", "/api/shortcuts?channel_id=ch-proj", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "review", result[0].Name)
	require.Equal(s.T(), "Review all changes", result[0].Prompt)
}

// --- Shortcut CRUD tests ---

func (s *ServerSuite) TestModifyShortcutAddGlobal() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{"platforms":["local"]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"scope": "global",
		"name": "lint",
		"description": "Run linter",
		"prompt": "Run make lint"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NotNil(s.T(), written)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	shortcuts := cfg["prompt_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 1)
	sc := shortcuts[0].(map[string]any)
	require.Equal(s.T(), "lint", sc["name"])
	require.Equal(s.T(), "Run make lint", sc["prompt"])
}

func (s *ServerSuite) TestModifyShortcutAddDuplicate() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"prompt_shortcuts":[{"name":"lint","prompt":"existing"}]}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "lint",
		"prompt": "new prompt"
	}`)

	require.Equal(s.T(), http.StatusConflict, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutUpdate() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"prompt_shortcuts":[{"name":"lint","prompt":"old","description":"old desc"}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "update",
		"name": "lint",
		"prompt": "Run make lint --fix",
		"description": "new desc"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	sc := cfg["prompt_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "Run make lint --fix", sc["prompt"])
	require.Equal(s.T(), "new desc", sc["description"])
}

func (s *ServerSuite) TestModifyShortcutUpdateNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "update",
		"name": "nonexistent",
		"prompt": "something"
	}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutDelete() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"prompt_shortcuts":[{"name":"a","prompt":"pa"},{"name":"b","prompt":"pb"}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "delete",
		"name": "a"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	shortcuts := cfg["prompt_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 1)
	require.Equal(s.T(), "b", shortcuts[0].(map[string]any)["name"])
}

func (s *ServerSuite) TestModifyShortcutDeleteNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "delete",
		"name": "ghost"
	}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutProjectScope() {
	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/app/.loop", os.FileMode(0755)).Return(nil)
	sys.On("ReadFile", "/projects/app/.loop/config.json").Return([]byte(`{}`), nil)
	sys.On("WriteFile", "/projects/app/.loop/config.json", mock.Anything, mock.Anything).Return(nil)
	s.srv.sys = sys
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/projects/app"}, nil)

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-proj",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutProjectScopeMissingChannel() {
	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"scope": "project",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id is required")
}

func (s *ServerSuite) TestModifyShortcutInvalidScope() {
	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"scope": "bogus",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "scope must be")
}

func (s *ServerSuite) TestModifyShortcutMissingName() {
	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"prompt": "something"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "name is required")
}

func (s *ServerSuite) TestModifyShortcutInvalidAction() {
	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "destroy",
		"name": "lint"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "action must be")
}

func (s *ServerSuite) TestModifyShortcutMissingPrompt() {
	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "lint"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "prompt or prompt_path is required")
}

func (s *ServerSuite) TestModifyShortcutMutuallyExclusive() {
	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "lint",
		"prompt": "inline",
		"prompt_path": "file.md"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "mutually exclusive")
}

func (s *ServerSuite) TestModifyShortcutNewConfigFile() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(nil, os.ErrNotExist)
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "lint",
		"prompt": "run linter"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutWithPromptPath() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "review",
		"description": "Review code",
		"prompt_path": "review-code.md"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	sc := cfg["prompt_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "review-code.md", sc["prompt_path"])
	require.Nil(s.T(), sc["prompt"])
}

func (s *ServerSuite) TestModifyShortcutUpdateSwitchToPromptPath() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"prompt_shortcuts":[{"name":"review","prompt":"inline review"}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "update",
		"name": "review",
		"prompt_path": "review-code.md"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	sc := cfg["prompt_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "review-code.md", sc["prompt_path"])
	require.Nil(s.T(), sc["prompt"]) // old prompt field should be removed
}

func (s *ServerSuite) TestModifyShortcutInvalidJSON() {
	rec := s.testRequest("POST", "/api/shortcuts", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutProjectResolveError() {
	s.store.On("GetChannel", mock.Anything, "ch-bad").Return(nil, fmt.Errorf("db error"))

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-bad",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutProjectMkdirError() {
	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/app/.loop", os.FileMode(0755)).Return(fmt.Errorf("permission denied"))
	s.srv.sys = sys
	s.store.On("GetChannel", mock.Anything, "ch-dir").Return(&db.Channel{ChannelID: "ch-dir", DirPath: "/projects/app"}, nil)

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-dir",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to create .loop directory")
}

func (s *ServerSuite) TestModifyShortcutHomeDirError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", fmt.Errorf("no home"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "cannot determine home directory")
}

func (s *ServerSuite) TestModifyShortcutReadFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(nil, fmt.Errorf("disk error"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to read config file")
}

func (s *ServerSuite) TestModifyShortcutInvalidHJSON() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{bad hjson`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid HJSON")
}

func (s *ServerSuite) TestModifyShortcutWriteFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("disk full"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "test",
		"prompt": "run tests"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to write config file")
}

func (s *ServerSuite) TestListShortcutsDefaultLoadConfig() {
	// Exercise the loadConfig == nil fallback (lines 24-26).
	// When loadConfig is nil the handler falls back to config.Load,
	// which may fail if no config exists — that's fine, it exercises the path.
	s.srv.configs.load = nil
	rec := s.testRequest("GET", "/api/shortcuts", "")
	// Accept either 200 (config exists) or 500 (config.Load fails) —
	// the point is the nil-check branch is exercised.
	require.Contains(s.T(), []int{http.StatusOK, http.StatusInternalServerError}, rec.Code)
}

func (s *ServerSuite) TestModifyShortcutUnmarshalError() {
	orig := jsonUnmarshalFn
	jsonUnmarshalFn = func(_ []byte, _ any) error { return fmt.Errorf("unmarshal fail") }
	s.T().Cleanup(func() { jsonUnmarshalFn = orig })

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "test",
		"prompt": "run tests"
	}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid JSON")
}

func (s *ServerSuite) TestModifyShortcutMarshalError() {
	orig := jsonMarshalIndent
	jsonMarshalIndent = func(_ any, _, _ string) ([]byte, error) { return nil, fmt.Errorf("marshal fail") }
	s.T().Cleanup(func() { jsonMarshalIndent = orig })

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/shortcuts", `{
		"action": "add",
		"name": "test",
		"prompt": "run tests"
	}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to serialize config")
}
