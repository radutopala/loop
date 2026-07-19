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

// --- Bash shortcuts list tests ---

func (s *ServerSuite) TestListBashShortcutsEmpty() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{LoopDir: "/home/testuser/.loop"}, nil
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `[]`, rec.Body.String())
}

func (s *ServerSuite) TestListBashShortcutsWithInlineCommand() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			BashShortcuts: []config.BashShortcut{
				{Name: "ll", Description: "List", Command: "ls -la"},
			},
		}, nil
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []bashShortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "ll", result[0].Name)
	require.Equal(s.T(), "List", result[0].Description)
	require.Equal(s.T(), "ls -la", result[0].Command)
}

func (s *ServerSuite) TestListBashShortcutsWithCommandPath() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			BashShortcuts: []config.BashShortcut{
				{Name: "deploy", Description: "Deploy steps", CommandPath: "deploy.sh"},
			},
		}, nil
	}
	s.srv.readFile = func(path string) ([]byte, error) {
		if path == "/home/testuser/.loop/bash-shortcuts/deploy.sh" {
			return []byte("make deploy"), nil
		}
		return nil, os.ErrNotExist
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []bashShortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "deploy", result[0].Name)
	require.Equal(s.T(), "make deploy", result[0].Command)
}

func (s *ServerSuite) TestListBashShortcutsSkipsUnresolvable() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			BashShortcuts: []config.BashShortcut{
				{Name: "good", Description: "Works", Command: "echo hi"},
				{Name: "bad", Description: "Missing file", CommandPath: "missing.sh"},
			},
		}, nil
	}
	s.srv.readFile = func(_ string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []bashShortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "good", result[0].Name)
}

func (s *ServerSuite) TestListBashShortcutsConfigLoadError() {
	s.srv.configs.load = func() (*config.Config, error) {
		return nil, errors.New("config broken")
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListBashShortcutsWithChannelMerge() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			BashShortcuts: []config.BashShortcut{
				{Name: "global", Description: "Global shortcut", Command: "echo global"},
			},
		}, nil
	}
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/projects/app"}, nil)
	s.srv.configs.loadProject = func(dir string, base *config.Config) (*config.Config, error) {
		require.Equal(s.T(), "/projects/app", dir)
		merged := *base
		merged.BashShortcuts = append(merged.BashShortcuts,
			config.BashShortcut{Name: "local", Description: "Project shortcut", Command: "echo local"},
		)
		return &merged, nil
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts?channel_id=ch-proj", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []bashShortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 2)
	require.Equal(s.T(), "global", result[0].Name)
	require.Equal(s.T(), "local", result[1].Name)
}

func (s *ServerSuite) TestListBashShortcutsLoopDirFallback() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			BashShortcuts: []config.BashShortcut{
				{Name: "test", Description: "Test", Command: "echo test"},
			},
		}, nil
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []bashShortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
}

func (s *ServerSuite) TestListBashShortcutsProjectCommandPath() {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir: "/home/testuser/.loop",
			BashShortcuts: []config.BashShortcut{
				{Name: "build", Description: "Build it", CommandPath: "build.sh"},
			},
		}, nil
	}
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/projects/app"}, nil)
	s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
		return base, nil
	}
	s.srv.readFile = func(path string) ([]byte, error) {
		if path == "/projects/app/.loop/bash-shortcuts/build.sh" {
			return []byte("make build"), nil
		}
		return nil, os.ErrNotExist
	}

	rec := s.testRequest("GET", "/api/bash-shortcuts?channel_id=ch-proj", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var result []bashShortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), "build", result[0].Name)
	require.Equal(s.T(), "make build", result[0].Command)
}

// --- Bash shortcut CRUD tests ---

func (s *ServerSuite) TestModifyBashShortcutAddGlobal() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{"platforms":["local"]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"scope": "global",
		"name": "ll",
		"description": "List files",
		"command": "ls -la"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NotNil(s.T(), written)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	shortcuts := cfg["bash_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 1)
	sc := shortcuts[0].(map[string]any)
	require.Equal(s.T(), "ll", sc["name"])
	require.Equal(s.T(), "ls -la", sc["command"])
}

func (s *ServerSuite) TestModifyBashShortcutAddDuplicate() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"bash_shortcuts":[{"name":"ll","command":"existing"}]}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "ll",
		"command": "new"
	}`)

	require.Equal(s.T(), http.StatusConflict, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutUpdate() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"bash_shortcuts":[{"name":"ll","command":"old","description":"old desc"}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "update",
		"name": "ll",
		"command": "ls -lah",
		"description": "new desc"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	sc := cfg["bash_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "ls -lah", sc["command"])
	require.Equal(s.T(), "new desc", sc["description"])
}

func (s *ServerSuite) TestModifyBashShortcutUpdateNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "update",
		"name": "nonexistent",
		"command": "something"
	}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutDelete() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"bash_shortcuts":[{"name":"a","command":"ca"},{"name":"b","command":"cb"}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "delete",
		"name": "a"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	shortcuts := cfg["bash_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 1)
	require.Equal(s.T(), "b", shortcuts[0].(map[string]any)["name"])
}

func (s *ServerSuite) TestModifyBashShortcutDeleteNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "delete",
		"name": "ghost"
	}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutProjectScope() {
	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/app/.loop", os.FileMode(0755)).Return(nil)
	sys.On("ReadFile", "/projects/app/.loop/config.json").Return([]byte(`{}`), nil)
	sys.On("WriteFile", "/projects/app/.loop/config.json", mock.Anything, mock.Anything).Return(nil)
	s.srv.sys = sys
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/projects/app"}, nil)

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-proj",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutProjectScopeMissingChannel() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"scope": "project",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id is required")
}

func (s *ServerSuite) TestModifyBashShortcutInvalidScope() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"scope": "bogus",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "scope must be")
}

func (s *ServerSuite) TestModifyBashShortcutMissingName() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"command": "something"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "name is required")
}

func (s *ServerSuite) TestModifyBashShortcutInvalidAction() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "destroy",
		"name": "ll"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "action must be")
}

func (s *ServerSuite) TestModifyBashShortcutMissingCommand() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "ll"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "command or command_path is required")
}

func (s *ServerSuite) TestModifyBashShortcutMutuallyExclusive() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "ll",
		"command": "inline",
		"command_path": "file.sh"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "mutually exclusive")
}

func (s *ServerSuite) TestModifyBashShortcutNewConfigFile() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(nil, os.ErrNotExist)
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "ll",
		"command": "ls -la"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutWithCommandPath() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "build",
		"description": "Build it",
		"command_path": "build.sh"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	sc := cfg["bash_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "build.sh", sc["command_path"])
	require.Nil(s.T(), sc["command"])
}

func (s *ServerSuite) TestModifyBashShortcutUpdateSwitchToCommandPath() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"bash_shortcuts":[{"name":"build","command":"inline build"}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "update",
		"name": "build",
		"command_path": "build.sh"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	sc := cfg["bash_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "build.sh", sc["command_path"])
	require.Nil(s.T(), sc["command"])
}

func (s *ServerSuite) TestModifyBashShortcutInvalidJSON() {
	rec := s.testRequest("POST", "/api/bash-shortcuts", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutProjectResolveError() {
	s.store.On("GetChannel", mock.Anything, "ch-bad").Return(nil, fmt.Errorf("db error"))

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-bad",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutProjectMkdirError() {
	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/app/.loop", os.FileMode(0755)).Return(fmt.Errorf("permission denied"))
	s.srv.sys = sys
	s.store.On("GetChannel", mock.Anything, "ch-dir").Return(&db.Channel{ChannelID: "ch-dir", DirPath: "/projects/app"}, nil)

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-dir",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to create .loop directory")
}

func (s *ServerSuite) TestModifyBashShortcutHomeDirError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", fmt.Errorf("no home"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "cannot determine home directory")
}

func (s *ServerSuite) TestModifyBashShortcutReadFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(nil, fmt.Errorf("disk error"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to read config file")
}

func (s *ServerSuite) TestModifyBashShortcutInvalidHJSON() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{bad hjson`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid HJSON")
}

func (s *ServerSuite) TestModifyBashShortcutWriteFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("disk full"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "test",
		"command": "make test"
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to write config file")
}

func (s *ServerSuite) TestListBashShortcutsDefaultLoadConfig() {
	s.srv.configs.load = nil
	rec := s.testRequest("GET", "/api/bash-shortcuts", "")
	require.Contains(s.T(), []int{http.StatusOK, http.StatusInternalServerError}, rec.Code)
}

func (s *ServerSuite) TestModifyBashShortcutUnmarshalError() {
	orig := jsonUnmarshalFn
	jsonUnmarshalFn = func(_ []byte, _ any) error { return fmt.Errorf("unmarshal fail") }
	s.T().Cleanup(func() { jsonUnmarshalFn = orig })

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "test",
		"command": "make test"
	}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid JSON")
}

func (s *ServerSuite) TestModifyBashShortcutMarshalError() {
	orig := jsonMarshalIndent
	jsonMarshalIndent = func(_ any, _, _ string) ([]byte, error) { return nil, fmt.Errorf("marshal fail") }
	s.T().Cleanup(func() { jsonMarshalIndent = orig })

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/bash-shortcuts", `{
		"action": "add",
		"name": "test",
		"command": "make test"
	}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to serialize config")
}
