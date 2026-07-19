package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

func (s *ServerSuite) agentConfigGet(channelID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/channels/"+channelID+"/agent-config", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ServerSuite) agentConfigPatch(channelID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PATCH", "/api/channels/"+channelID+"/agent-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ServerSuite) TestAgentConfigGet() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID:      "ch-1",
		DirPath:        "/home/user/project",
		ModelOverride:  "claude-opus-4-8",
		EffortOverride: "high",
	}, nil)
	// Parent resolution for worktree defaults: not a worktree here.
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{ClaudeModel: "claude-sonnet-5", ClaudeEffort: "medium"}, nil
	}
	s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
		merged := *base
		merged.ClaudeEffort = "low" // project layer overrides effort
		return &merged, nil
	}

	w := s.agentConfigGet("ch-1")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp agentConfigResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "claude-opus-4-8", resp.Model)
	require.Equal(s.T(), "high", resp.Effort)
	require.Equal(s.T(), "claude-sonnet-5", resp.DefaultModel)
	require.Equal(s.T(), "low", resp.DefaultEffort)
}

func (s *ServerSuite) TestAgentConfigGetWorktreeDefaults() {
	// A worktree thread resolves defaults through the three-layer merge.
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1",
		DirPath:   "/home/user/project/.worktrees/wt-1",
		ParentID:  "ch-1",
		Worktree:  true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/home/user/project",
	}, nil)
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{ClaudeModel: "claude-sonnet-5"}, nil
	}
	s.srv.configs.loadWorktree = func(_, _ string, base *config.Config) (*config.Config, error) {
		merged := *base
		merged.ClaudeModel = "claude-opus-4-8"
		return &merged, nil
	}

	w := s.agentConfigGet("wt-1")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp agentConfigResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "", resp.Model)
	require.Equal(s.T(), "claude-opus-4-8", resp.DefaultModel)
}

func (s *ServerSuite) TestAgentConfigGetConfigLoadError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", DirPath: "/home/user/project",
	}, nil)
	s.srv.configs.load = func() (*config.Config, error) { return nil, os.ErrNotExist }

	w := s.agentConfigGet("ch-1")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp agentConfigResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "", resp.DefaultModel)
	require.Equal(s.T(), "", resp.DefaultEffort)
}

func (s *ServerSuite) TestAgentConfigGetProjectConfigError() {
	// A failing project-config load falls back to the global defaults.
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", DirPath: "/home/user/project",
	}, nil)
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{ClaudeModel: "claude-sonnet-5", ClaudeEffort: "medium"}, nil
	}
	s.srv.configs.loadProject = func(string, *config.Config) (*config.Config, error) { return nil, os.ErrNotExist }

	w := s.agentConfigGet("ch-1")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp agentConfigResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "claude-sonnet-5", resp.DefaultModel)
	require.Equal(s.T(), "medium", resp.DefaultEffort)
}

func (s *ServerSuite) TestAgentConfigGetWorktreeConfigError() {
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1", DirPath: "/home/user/project/.worktrees/wt-1",
		ParentID: "ch-1", Worktree: true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", DirPath: "/home/user/project",
	}, nil)
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{ClaudeModel: "claude-sonnet-5"}, nil
	}
	s.srv.configs.loadWorktree = func(string, string, *config.Config) (*config.Config, error) { return nil, os.ErrNotExist }

	w := s.agentConfigGet("wt-1")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp agentConfigResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "claude-sonnet-5", resp.DefaultModel)
}

func (s *ServerSuite) TestAgentConfigGetNilLoaderFallbacks() {
	// With no injected loaders the handler falls back to the real config
	// loaders; temp dirs have no .loop/config.json so the merge degrades
	// gracefully either way — we only assert the endpoint stays healthy.
	dir := s.T().TempDir()
	parent := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1", DirPath: dir, ParentID: "ch-1", Worktree: true,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", DirPath: parent,
	}, nil)
	s.srv.configs.load = nil
	s.srv.configs.loadWorktree = nil
	require.Equal(s.T(), http.StatusOK, s.agentConfigGet("wt-1").Code)

	// Non-worktree channel exercises the nil loadProjectConfig fallback.
	s.store.On("GetChannel", mock.Anything, "ch-2").Return(&db.Channel{
		ChannelID: "ch-2", DirPath: dir,
	}, nil)
	s.srv.configs.loadProject = nil
	require.Equal(s.T(), http.StatusOK, s.agentConfigGet("ch-2").Code)
}

func (s *ServerSuite) TestAgentConfigGetChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "unknown").Return(nil, nil)
	require.Equal(s.T(), http.StatusNotFound, s.agentConfigGet("unknown").Code)
}

func (s *ServerSuite) TestAgentConfigGetChannelError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, os.ErrPermission)
	require.Equal(s.T(), http.StatusInternalServerError, s.agentConfigGet("err-ch").Code)
}

func (s *ServerSuite) TestAgentConfigGetStoreNotConfigured() {
	oldStore := s.srv.store
	defer func() { s.srv.store = oldStore }()
	s.srv.store = nil
	require.Equal(s.T(), http.StatusNotImplemented, s.agentConfigGet("ch-1").Code)
}

func (s *ServerSuite) TestAgentConfigSet() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("UpdateChannelAgentOverrides", mock.Anything, "ch-1", "claude-opus-4-8", "xhigh").Return(nil)

	w := s.agentConfigPatch("ch-1", `{"model":" claude-opus-4-8 ","effort":"xhigh"}`)
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	s.store.AssertCalled(s.T(), "UpdateChannelAgentOverrides", mock.Anything, "ch-1", "claude-opus-4-8", "xhigh")
}

func (s *ServerSuite) TestAgentConfigSetClear() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("UpdateChannelAgentOverrides", mock.Anything, "ch-1", "", "").Return(nil)

	w := s.agentConfigPatch("ch-1", `{"model":"","effort":""}`)
	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestAgentConfigSetInvalidEffort() {
	w := s.agentConfigPatch("ch-1", `{"model":"","effort":"turbo"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid effort")
	s.store.AssertNotCalled(s.T(), "UpdateChannelAgentOverrides", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestAgentConfigSetInvalidBody() {
	require.Equal(s.T(), http.StatusBadRequest, s.agentConfigPatch("ch-1", `not json`).Code)
}

func (s *ServerSuite) TestAgentConfigSetChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "unknown").Return(nil, nil)
	require.Equal(s.T(), http.StatusNotFound, s.agentConfigPatch("unknown", `{"model":"m","effort":"low"}`).Code)
}

func (s *ServerSuite) TestAgentConfigSetChannelError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, os.ErrPermission)
	require.Equal(s.T(), http.StatusInternalServerError, s.agentConfigPatch("err-ch", `{"model":"m","effort":""}`).Code)
}

func (s *ServerSuite) TestAgentConfigSetUpdateError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("UpdateChannelAgentOverrides", mock.Anything, "ch-1", "m", "low").Return(os.ErrPermission)
	require.Equal(s.T(), http.StatusInternalServerError, s.agentConfigPatch("ch-1", `{"model":"m","effort":"low"}`).Code)
}

func (s *ServerSuite) TestAgentConfigSetStoreNotConfigured() {
	oldStore := s.srv.store
	defer func() { s.srv.store = oldStore }()
	s.srv.store = nil
	require.Equal(s.T(), http.StatusNotImplemented, s.agentConfigPatch("ch-1", `{"model":"","effort":""}`).Code)
}
