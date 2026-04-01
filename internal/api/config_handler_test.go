package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// ── handleConfigSchema ──

func (s *ServerSuite) TestConfigSchemaReturns200() {
	s.srv.sys = s.sys
	rec := s.testRequest("GET", "/api/config/schema", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(s.T(), "object", body["type"])
	require.NotNil(s.T(), body["properties"])
}

// ── handleGetConfig ──

func (s *ServerSuite) TestGetConfigSuccess() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("ReadFile", "/home/test/.loop/config.json").Return(
		[]byte(`{"claude_model":"claude-opus-4-6"}`), nil,
	)
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp configResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "/home/test/.loop/config.json", resp.Path)
	require.Equal(s.T(), "claude-opus-4-6", resp.Content["claude_model"])
	require.Equal(s.T(), `{"claude_model":"claude-opus-4-6"}`, resp.Raw)
}

func (s *ServerSuite) TestGetConfigFileNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("ReadFile", "/home/test/.loop/config.json").Return(nil, os.ErrNotExist)
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp configResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "/home/test/.loop/config.json", resp.Path)
	require.Nil(s.T(), resp.Content)
	require.Empty(s.T(), resp.Raw)
}

func (s *ServerSuite) TestGetConfigHomeDirError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "cannot determine home directory")
}

// ── handleSaveConfig ──

func (s *ServerSuite) TestSaveConfigSuccess() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("WriteFile", "/home/test/.loop/config.json", []byte(`{"key":"val"}`), os.FileMode(0644)).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config", `{"content":"{\"key\":\"val\"}"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	sys.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSaveConfigInvalidJSON() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config", `{"content":"not valid json"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "content is not valid JSON")
}

func (s *ServerSuite) TestSaveConfigInvalidBody() {
	s.srv.sys = s.sys
	rec := s.testRequest("PUT", "/api/config", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSaveConfigWriteFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("disk full"))
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to write config file")
}

func (s *ServerSuite) TestSaveConfigHomeDirError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "cannot determine home directory")
}

// ── handleGetProjectConfig ──

func (s *ServerSuite) TestGetProjectConfigSuccess() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/projects/myapp/.loop/config.json").Return(
		[]byte(`{"claude_model":"claude-sonnet-4-6"}`), nil,
	)
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp configResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "/projects/myapp/.loop/config.json", resp.Path)
	require.Equal(s.T(), "claude-sonnet-4-6", resp.Content["claude_model"])
}

func (s *ServerSuite) TestGetProjectConfigChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=missing", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestGetProjectConfigMissingChannelID() {
	s.srv.sys = s.sys
	rec := s.testRequest("GET", "/api/config/project", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "dir_path or channel_id is required")
}

// ── handleSaveProjectConfig ──

func (s *ServerSuite) TestSaveProjectConfigSuccess() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/myapp/.loop", os.FileMode(0755)).Return(nil)
	sys.On("WriteFile", "/projects/myapp/.loop/config.json", []byte(`{"streaming_enabled":true}`), os.FileMode(0644)).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", `{"content":"{\"streaming_enabled\":true}"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	sys.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSaveProjectConfigChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	s.srv.sys = s.sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=missing", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSaveProjectConfigInvalidJSON() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)
	s.srv.sys = s.sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", `{"content":"not json"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "content is not valid JSON")
}

func (s *ServerSuite) TestSaveProjectConfigInvalidBody() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)
	s.srv.sys = s.sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSaveProjectConfigMkdirAllError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/myapp/.loop", os.FileMode(0755)).Return(errors.New("permission denied"))
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to create .loop directory")
}

func (s *ServerSuite) TestSaveProjectConfigWriteFileError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("disk full"))
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to write config file")
}

func (s *ServerSuite) TestSaveProjectConfigMissingChannelID() {
	s.srv.sys = s.sys
	rec := s.testRequest("PUT", "/api/config/project", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestGetProjectConfigWorktreeUsesParentDir() {
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1",
		DirPath:   "/projects/myapp/.worktrees/wt-1",
		ParentID:  "ch-1",
		Worktree:  true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/projects/myapp/.loop/config.json").Return(
		[]byte(`{"claude_model":"claude-opus-4-6"}`), nil,
	)
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=wt-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp configResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "/projects/myapp/.loop/config.json", resp.Path)
	require.Equal(s.T(), "claude-opus-4-6", resp.Content["claude_model"])
}

func (s *ServerSuite) TestSaveProjectConfigWorktreeUsesParentDir() {
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1",
		DirPath:   "/projects/myapp/.worktrees/wt-1",
		ParentID:  "ch-1",
		Worktree:  true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/projects/myapp",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/myapp/.loop", os.FileMode(0755)).Return(nil)
	sys.On("WriteFile", "/projects/myapp/.loop/config.json", []byte(`{"streaming_enabled":true}`), os.FileMode(0644)).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("PUT", "/api/config/project?channel_id=wt-1", `{"content":"{\"streaming_enabled\":true}"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	sys.AssertExpectations(s.T())
}

func (s *ServerSuite) TestGetProjectConfigWorktreeParentLookupError() {
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1",
		DirPath:   "/projects/myapp/.worktrees/wt-1",
		ParentID:  "ch-1",
		Worktree:  true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db error"))
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=wt-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "looking up parent channel")
}

func (s *ServerSuite) TestGetProjectConfigWorktreeParentNoDirPath() {
	// Parent has no DirPath → falls back to worktree's own DirPath.
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1",
		DirPath:   "/projects/myapp/.worktrees/wt-1",
		ParentID:  "ch-1",
		Worktree:  true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/projects/myapp/.worktrees/wt-1/.loop/config.json").Return(nil, os.ErrNotExist)
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=wt-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestResolveProjectConfigDirPathStoreNil() {
	s.srv.store = nil
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel lookup not configured")
}

func (s *ServerSuite) TestResolveProjectConfigDirPathNoDirPathUsesLoopDir() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "",
	}, nil)
	s.srv.loopDir = "/home/test/.loop"

	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/home/test/.loop/ch-1/work/.loop/config.json").Return(nil, os.ErrNotExist)
	s.srv.sys = sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp configResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "/home/test/.loop/ch-1/work/.loop/config.json", resp.Path)
}

func (s *ServerSuite) TestResolveProjectConfigDirPathNoDirPathNoLoopDir() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "",
	}, nil)
	s.srv.loopDir = ""
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "has no dir_path")
}

func (s *ServerSuite) TestResolveProjectConfigDirPathGetChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").Return(nil, errors.New("db down"))
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-err", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "looking up channel")
}

// ── readConfigFile ──

func (s *ServerSuite) TestReadConfigFileWithHJSON() {
	sys := new(testutil.MockSystem)
	hjsonContent := `{
  // This is a comment
  "claude_model": "claude-opus-4-6"
}`
	sys.On("ReadFile", "/some/config.json").Return([]byte(hjsonContent), nil)

	resp := readConfigFile(sys, "/some/config.json")
	require.Equal(s.T(), "/some/config.json", resp.Path)
	require.Equal(s.T(), "claude-opus-4-6", resp.Content["claude_model"])
	require.Equal(s.T(), hjsonContent, resp.Raw)
}

func (s *ServerSuite) TestReadConfigFileWithPlainJSON() {
	sys := new(testutil.MockSystem)
	content := `{"key":"value"}`
	sys.On("ReadFile", "/some/config.json").Return([]byte(content), nil)

	resp := readConfigFile(sys, "/some/config.json")
	require.Equal(s.T(), "/some/config.json", resp.Path)
	require.Equal(s.T(), "value", resp.Content["key"])
	require.Equal(s.T(), content, resp.Raw)
}

func (s *ServerSuite) TestReadConfigFileNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/missing/config.json").Return(nil, os.ErrNotExist)

	resp := readConfigFile(sys, "/missing/config.json")
	require.Equal(s.T(), "/missing/config.json", resp.Path)
	require.Nil(s.T(), resp.Content)
	require.Empty(s.T(), resp.Raw)
}

func (s *ServerSuite) TestReadConfigFileReadError() {
	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/bad/config.json").Return(nil, errors.New("I/O error"))

	resp := readConfigFile(sys, "/bad/config.json")
	require.Equal(s.T(), "/bad/config.json", resp.Path)
	require.Nil(s.T(), resp.Content)
	require.Empty(s.T(), resp.Raw)
}

func (s *ServerSuite) TestReadConfigFileInvalidHJSON() {
	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/bad/config.json").Return([]byte(`{not valid hjson at all`), nil)

	resp := readConfigFile(sys, "/bad/config.json")
	require.Equal(s.T(), "/bad/config.json", resp.Path)
	require.Nil(s.T(), resp.Content)
	require.Equal(s.T(), `{not valid hjson at all`, resp.Raw)
}

func (s *ServerSuite) TestReadConfigFileInvalidJSONAfterStandardize() {
	// Content that hujson.Standardize accepts but json.Unmarshal rejects for
	// map[string]any (e.g. a bare value that is not an object).
	sys := new(testutil.MockSystem)
	sys.On("ReadFile", "/bare/config.json").Return([]byte(`"just a string"`), nil)

	resp := readConfigFile(sys, "/bare/config.json")
	require.Equal(s.T(), "/bare/config.json", resp.Path)
	// json.Unmarshal into map[string]any fails for a bare string, so content is nil.
	require.Nil(s.T(), resp.Content)
	require.Equal(s.T(), `"just a string"`, resp.Raw)
}
