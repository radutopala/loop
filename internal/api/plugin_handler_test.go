package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

type MockPluginManager struct {
	mock.Mock
}

func (m *MockPluginManager) List(ctx context.Context) ([]*db.PluginState, error) {
	args := m.Called(ctx)
	v, _ := args.Get(0).([]*db.PluginState)
	return v, args.Error(1)
}

func (m *MockPluginManager) Inspect(ctx context.Context, ref string, dev bool) (*install.Candidate, error) {
	args := m.Called(ctx, ref, dev)
	v, _ := args.Get(0).(*install.Candidate)
	return v, args.Error(1)
}

func (m *MockPluginManager) Install(ctx context.Context, ref string, dev bool, digest string, approved []plugin.Capability) (*install.Candidate, error) {
	args := m.Called(ctx, ref, dev, digest, approved)
	v, _ := args.Get(0).(*install.Candidate)
	return v, args.Error(1)
}

func (m *MockPluginManager) Remove(ctx context.Context, pluginID string, purge bool) error {
	return m.Called(ctx, pluginID, purge).Error(0)
}

func (m *MockPluginManager) RunAction(ctx context.Context, pluginID, action string, ch runtime.Channel, req proxy.ActionRequest) error {
	return m.Called(ctx, pluginID, action, ch, req).Error(0)
}

func (s *ServerSuite) pluginRoutes() *MockPluginManager {
	pm := new(MockPluginManager)
	s.srv.SetPluginManager(pm)
	s.mux.HandleFunc("GET /api/plugins", s.srv.handleListPlugins)
	s.mux.HandleFunc("POST /api/plugins/inspect", s.srv.handleInspectPlugin)
	s.mux.HandleFunc("POST /api/plugins", s.srv.handleInstallPlugin)
	s.mux.HandleFunc("DELETE /api/plugins/{id}", s.srv.handleRemovePlugin)
	s.mux.HandleFunc("POST /api/plugins/{plugin}/actions/{action}", s.srv.handleRunPluginAction)
	s.T().Cleanup(func() { pm.AssertExpectations(s.T()) })
	return pm
}

func echoCandidate() *install.Candidate {
	return &install.Candidate{
		Manifest: &plugin.Manifest{ID: "echo", Name: "Echo", Version: "1.1.0", API: "1", Capabilities: []plugin.Capability{plugin.CapUIPanel, plugin.CapFilesRead}},
		Image:    "ghcr.io/acme/echo@sha256:aa", Digest: "sha256:aa",
		Previous: &db.PluginState{Version: "1.0.0", ApprovedCaps: []string{"ui.panel"}},
	}
}

func (s *ServerSuite) TestPluginsDisabled() {
	s.mux.HandleFunc("GET /api/plugins", s.srv.handleListPlugins)
	s.mux.HandleFunc("POST /api/plugins/inspect", s.srv.handleInspectPlugin)
	s.mux.HandleFunc("POST /api/plugins", s.srv.handleInstallPlugin)
	s.mux.HandleFunc("DELETE /api/plugins/{id}", s.srv.handleRemovePlugin)
	s.mux.HandleFunc("POST /api/plugins/{plugin}/actions/{action}", s.srv.handleRunPluginAction)
	for _, req := range [][2]string{{"GET", "/api/plugins"}, {"POST", "/api/plugins/inspect"}, {"POST", "/api/plugins"}, {"DELETE", "/api/plugins/echo"}, {"POST", "/api/plugins/echo/actions/ping"}} {
		rec := s.testRequest(req[0], req[1], "")
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, req[1])
		require.Contains(s.T(), rec.Body.String(), "plugins.enabled")
	}
}

func (s *ServerSuite) TestListPlugins() {
	pm := s.pluginRoutes()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	pm.On("List", mock.Anything).Return([]*db.PluginState{{
		PluginID: "echo", Image: "ghcr.io/acme/echo@sha256:aa", Digest: "sha256:aa", Version: "1.0.0", API: "1",
		ApprovedCaps: []string{"ui.panel"}, Status: db.PluginStatusInstalled, InstalledAt: at, UpdatedAt: at,
	}}, nil).Once()
	rec := s.testRequest("GET", "/api/plugins", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"plugins":[{"id":"echo","image":"ghcr.io/acme/echo@sha256:aa","digest":"sha256:aa","version":"1.0.0","api":"1",
		"capabilities":["ui.panel"],"dev":false,"status":"installed","installed_at":"2026-10-10T09:00:00Z","updated_at":"2026-10-10T09:00:00Z"}]}`, rec.Body.String())

	pm.On("List", mock.Anything).Return(nil, nil).Once()
	rec = s.testRequest("GET", "/api/plugins", "")
	require.JSONEq(s.T(), `{"plugins":[]}`, rec.Body.String())

	pm.On("List", mock.Anything).Return(nil, errors.New("boom")).Once()
	rec = s.testRequest("GET", "/api/plugins", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestInspectPlugin() {
	pm := s.pluginRoutes()
	pm.On("Inspect", mock.Anything, "ghcr.io/acme/echo:1.1.0", false).Return(echoCandidate(), nil).Once()
	rec := s.testRequest("POST", "/api/plugins/inspect", `{"image":"ghcr.io/acme/echo:1.1.0"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"id":"echo","name":"Echo","version":"1.1.0","api":"1","image":"ghcr.io/acme/echo@sha256:aa","digest":"sha256:aa",
		"dev":false,"capabilities":["ui.panel","files.read"],"new_capabilities":["files.read"],"previous_version":"1.0.0"}`, rec.Body.String())

	pm.On("Inspect", mock.Anything, "echo:dev", true).Return(nil, errors.New("not a Loop plugin")).Once()
	rec = s.testRequest("POST", "/api/plugins/inspect", `{"image":"echo:dev","dev":true}`)
	require.Equal(s.T(), http.StatusUnprocessableEntity, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "not a Loop plugin")

	rec = s.testRequest("POST", "/api/plugins/inspect", `{}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	rec = s.testRequest("POST", "/api/plugins/inspect", `nope`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestInstallPlugin() {
	pm := s.pluginRoutes()
	caps := []plugin.Capability{plugin.CapUIPanel, plugin.CapFilesRead}
	c := echoCandidate()
	c.Previous = nil
	pm.On("Install", mock.Anything, "ghcr.io/acme/echo:1.1.0", false, "sha256:aa", caps).Return(c, nil).Once()
	rec := s.testRequest("POST", "/api/plugins", `{"image":"ghcr.io/acme/echo:1.1.0","digest":"sha256:aa","approve":["ui.panel","files.read"]}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var got pluginCandidate
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(s.T(), "echo", got.ID)
	require.Empty(s.T(), got.PreviousVersion)

	tests := []struct {
		err  error
		want int
	}{
		{install.ErrDigestChanged, http.StatusConflict},
		{install.ErrNotApproved, http.StatusBadRequest},
		{errors.New("pull failed"), http.StatusUnprocessableEntity},
	}
	for _, tc := range tests {
		pm.On("Install", mock.Anything, "echo:1", false, "", []plugin.Capability(nil)).Return(nil, tc.err).Once()
		rec := s.testRequest("POST", "/api/plugins", `{"image":"echo:1"}`)
		require.Equal(s.T(), tc.want, rec.Code, tc.err.Error())
	}

	rec = s.testRequest("POST", "/api/plugins", `{"approve":[]}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "image is required")
}

func (s *ServerSuite) TestRemovePlugin() {
	pm := s.pluginRoutes()
	pm.On("Remove", mock.Anything, "echo", true).Return(nil).Once()
	rec := s.testRequest("DELETE", "/api/plugins/echo?purge=true", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	pm.On("Remove", mock.Anything, "echo", false).Return(install.ErrNotInstalled).Once()
	rec = s.testRequest("DELETE", "/api/plugins/echo", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)

	pm.On("Remove", mock.Anything, "echo", false).Return(errors.New("boom")).Once()
	rec = s.testRequest("DELETE", "/api/plugins/echo", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRunPluginAction() {
	manual := proxy.ActionRequest{Trigger: proxy.TriggerManual}
	ch := runtime.Channel{ID: "ch-1", DirPath: "/src/app"}
	tests := []struct {
		name     string
		body     string
		setup    func(pm *MockPluginManager)
		wantCode int
		wantBody string
	}{
		{name: "accepted", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(nil).Once()
		}, wantCode: http.StatusAccepted, wantBody: `{"channel_id":"ch-1"}`},
		{name: "thread runs in its channel", body: `{"channel_id":"th-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", DirPath: "/src/app"}, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(nil).Once()
		}, wantCode: http.StatusAccepted, wantBody: `{"channel_id":"ch-1"}`},
		{name: "bad body", body: `{`, setup: func(*MockPluginManager) {}, wantCode: http.StatusBadRequest},
		{name: "no channel", body: `{}`, setup: func(*MockPluginManager) {}, wantCode: http.StatusBadRequest, wantBody: "channel_id is required"},
		{name: "channel lookup fails", body: `{"channel_id":"ch-1"}`, setup: func(*MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("boom")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "boom"},
		{name: "unknown channel", body: `{"channel_id":"ch-1"}`, setup: func(*MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "not installed", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(install.ErrNotInstalled).Once()
		}, wantCode: http.StatusNotFound},
		{name: "unknown action", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(install.ErrUnknownAction).Once()
		}, wantCode: http.StatusNotFound},
		{name: "disabled", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(install.ErrNotRunnable).Once()
		}, wantCode: http.StatusConflict},
		{name: "plugin fails", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(errors.New("starting the plugin: unhealthy")).Once()
		}, wantCode: http.StatusBadGateway, wantBody: "unhealthy"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginRoutes()
			tt.setup(pm)
			rec := s.testRequest("POST", "/api/plugins/echo/actions/ping", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}
