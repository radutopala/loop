package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
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

func (m *MockPluginManager) AgentRun(ctx context.Context, pluginID, agentID string) (*install.AgentRun, error) {
	args := m.Called(ctx, pluginID, agentID)
	v, _ := args.Get(0).(*install.AgentRun)
	return v, args.Error(1)
}

func (m *MockPluginManager) CallTool(ctx context.Context, pluginID, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error) {
	args := m.Called(ctx, pluginID, tool, ch, req)
	v, _ := args.Get(0).(*proxy.ToolResult)
	return v, args.Error(1)
}

func (m *MockPluginManager) Containers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error) {
	args := m.Called(ctx, pluginID)
	v, _ := args.Get(0).([]*db.PluginContainer)
	return v, args.Error(1)
}

func (m *MockPluginManager) Stop(ctx context.Context, pluginID, channelID string) {
	m.Called(ctx, pluginID, channelID)
}

func (m *MockPluginManager) Audit(pluginID, channelID string, limit int) ([]agentgate.AuditEntry, error) {
	args := m.Called(pluginID, channelID, limit)
	v, _ := args.Get(0).([]agentgate.AuditEntry)
	return v, args.Error(1)
}

func (m *MockPluginManager) RemoveChannel(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockPluginManager) StopDisabled(ctx context.Context) bool {
	return m.Called(ctx).Bool(0)
}

func (m *MockPluginManager) SyncSchedules(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (s *ServerSuite) pluginRoutes() *MockPluginManager {
	pm := new(MockPluginManager)
	s.srv.SetPluginManager(pm)
	s.mux.HandleFunc("GET /api/plugins", s.srv.handleListPlugins)
	s.mux.HandleFunc("POST /api/plugins/inspect", s.srv.handleInspectPlugin)
	s.mux.HandleFunc("POST /api/plugins", s.srv.handleInstallPlugin)
	s.mux.HandleFunc("DELETE /api/plugins/{id}", s.srv.handleRemovePlugin)
	s.mux.HandleFunc("POST /api/plugins/{plugin}/actions/{action}", s.srv.handleRunPluginAction)
	s.mux.HandleFunc("POST /api/plugins/{plugin}/channels/{channel}/stop", s.srv.handleStopPluginContainer)
	s.mux.HandleFunc("GET /api/plugins/{plugin}/channels/{channel}/audit", s.srv.handlePluginAudit)
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
	s.mux.HandleFunc("POST /api/plugins/{plugin}/channels/{channel}/stop", s.srv.handleStopPluginContainer)
	for _, req := range [][2]string{{"GET", "/api/plugins"}, {"POST", "/api/plugins/inspect"}, {"POST", "/api/plugins"}, {"DELETE", "/api/plugins/echo"}, {"POST", "/api/plugins/echo/actions/ping"}, {"POST", "/api/plugins/echo/channels/ch-1/stop"}} {
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
	pm.On("UI", mock.Anything, "echo").Return("", nil, install.ErrNotRunnable).Once()
	pm.On("Containers", mock.Anything, "echo").Return([]*db.PluginContainer{
		{PluginID: "echo", ChannelID: "ch-1", Status: db.PluginContainerRunning, UpdatedAt: at},
		{PluginID: "echo", ChannelID: "gone", Status: db.PluginContainerStopped, LastError: "unhealthy", UpdatedAt: at},
	}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Name: "app"}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Once()
	rec := s.testRequest("GET", "/api/plugins", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"plugins":[{"id":"echo","image":"ghcr.io/acme/echo@sha256:aa","digest":"sha256:aa","version":"1.0.0","api":"1",
		"capabilities":["ui.panel"],"dev":false,"status":"installed","installed_at":"2026-10-10T09:00:00Z","updated_at":"2026-10-10T09:00:00Z",
		"enabled_by_default":false,"containers":[
			{"channel_id":"ch-1","channel_name":"app","status":"running","updated_at":"2026-10-10T09:00:00Z"},
			{"channel_id":"gone","status":"stopped","last_error":"unhealthy","updated_at":"2026-10-10T09:00:00Z"}]}]}`, rec.Body.String())

	// A plugin that may run shows its name, and its panels when approved.
	panels := &plugin.Manifest{Name: "Echo", UI: &plugin.UI{Panels: []plugin.Panel{{ID: "main", Label: "Echo", Entry: "ui/main/index.html", Singleton: true}}}}
	pm.On("List", mock.Anything).Return([]*db.PluginState{
		{PluginID: "echo", ApprovedCaps: []string{"ui.panel"}, InstalledAt: at, UpdatedAt: at},
		{PluginID: "quiet", InstalledAt: at, UpdatedAt: at},
		{PluginID: "bare", ApprovedCaps: []string{"ui.panel"}, InstalledAt: at, UpdatedAt: at},
	}, nil).Once()
	pm.On("UI", mock.Anything, "bare").Return("/d", &plugin.Manifest{Name: "Bare"}, nil).Once()
	pm.On("UI", mock.Anything, "echo").Return("/d", panels, nil).Once()
	pm.On("UI", mock.Anything, "quiet").Return("/d", panels, nil).Once()
	pm.On("Containers", mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	rec = s.testRequest("GET", "/api/plugins", "")
	var list struct {
		Plugins []pluginInfo `json:"plugins"`
	}
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &list))
	require.Equal(s.T(), "Echo", list.Plugins[0].Name)
	require.Equal(s.T(), panels.UI.Panels, list.Plugins[0].Panels)
	require.Equal(s.T(), "Echo", list.Plugins[1].Name)
	require.Empty(s.T(), list.Plugins[1].Panels)
	require.Empty(s.T(), list.Plugins[2].Panels)

	pm.On("List", mock.Anything).Return(nil, nil).Once()
	rec = s.testRequest("GET", "/api/plugins", "")
	require.JSONEq(s.T(), `{"plugins":[]}`, rec.Body.String())

	pm.On("List", mock.Anything).Return(nil, errors.New("boom")).Once()
	rec = s.testRequest("GET", "/api/plugins", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	pm.On("List", mock.Anything).Return([]*db.PluginState{{PluginID: "echo"}}, nil).Once()
	pm.On("UI", mock.Anything, "echo").Return("", nil, install.ErrNotRunnable).Once()
	pm.On("Containers", mock.Anything, "echo").Return(nil, errors.New("db down")).Once()
	rec = s.testRequest("GET", "/api/plugins", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "db down")

	// Without a global config, nothing is enabled by default.
	s.srv.configs.load = func() (*config.Config, error) { return nil, errors.New("bad config") }
	pm.On("List", mock.Anything).Return([]*db.PluginState{{PluginID: "echo"}}, nil).Once()
	pm.On("UI", mock.Anything, "echo").Return("", nil, install.ErrNotRunnable).Once()
	pm.On("Containers", mock.Anything, "echo").Return(nil, nil).Once()
	rec = s.testRequest("GET", "/api/plugins", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"enabled_by_default":false`)
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
	pm.On("SyncSchedules", mock.Anything).Return(nil).Once()
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
		{name: "a thread runs in its own container", body: `{"channel_id":"th-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", runtime.Channel{ID: "th-1", DirPath: "/src/app"}, manual).Return(nil).Once()
		}, wantCode: http.StatusAccepted, wantBody: `{"channel_id":"th-1"}`},
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
		{name: "not enabled", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(install.ErrNotEnabled).Once()
		}, wantCode: http.StatusConflict, wantBody: "not enabled"},
		{name: "a worktree thread runs in its worktree", body: `{"channel_id":"th-2"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "th-2").Return(&db.Channel{ChannelID: "th-2", ParentID: "ch-1", DirPath: "/src/app/.worktrees/wt", Worktree: true}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", runtime.Channel{ID: "th-2", DirPath: "/src/app/.worktrees/wt"}, manual).Return(nil).Once()
		}, wantCode: http.StatusAccepted, wantBody: `{"channel_id":"th-2"}`},
		{name: "plugin fails", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(errors.New("starting the plugin: unhealthy")).Once()
		}, wantCode: http.StatusBadGateway, wantBody: "unhealthy"},
		{name: "plugin not answering", body: `{"channel_id":"ch-1"}`, setup: func(pm *MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Once()
			pm.On("RunAction", mock.Anything, "echo", "ping", ch, manual).Return(fmt.Errorf("%w: 3 calls in a row failed", proxy.ErrUnavailable)).Once()
		}, wantCode: http.StatusServiceUnavailable, wantBody: "isn't answering"},
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

func (m *MockPluginManager) Allows(ctx context.Context, pluginID string, c plugin.Capability) error {
	return m.Called(ctx, pluginID, c).Error(0)
}

func (m *MockPluginManager) RunDone(ctx context.Context, pluginID string, ch runtime.Channel, runID string, req proxy.RunDoneRequest) error {
	return m.Called(ctx, pluginID, ch, runID, req).Error(0)
}

func (m *MockPluginManager) X(ctx context.Context, pluginID string, ch runtime.Channel, req proxy.XRequest) (*proxy.XResponse, error) {
	args := m.Called(ctx, pluginID, ch, req)
	v, _ := args.Get(0).(*proxy.XResponse)
	return v, args.Error(1)
}

func (m *MockPluginManager) UI(ctx context.Context, pluginID string) (string, *plugin.Manifest, error) {
	args := m.Called(ctx, pluginID)
	v, _ := args.Get(1).(*plugin.Manifest)
	return args.String(0), v, args.Error(2)
}

func (s *ServerSuite) TestPluginX() {
	ch := runtime.Channel{ID: "ch-1", DirPath: "/src/app"}
	tests := []struct {
		name     string
		method   string
		target   string
		body     string
		setup    func(pm *MockPluginManager)
		wantCode int
		wantBody string
		wantType string
	}{
		{name: "post", method: "POST", target: "/api/plugins/echo/x/quiz/answer?channel_id=th-1&q=1", body: `{"a":2}`, setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", runtime.Channel{ID: "th-1", DirPath: "/src/app"}, proxy.XRequest{
				Method: "POST", Path: "quiz/answer", RawQuery: "q=1", ContentType: "application/json", Body: []byte(`{"a":2}`),
			}).Return(&proxy.XResponse{Status: http.StatusCreated, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, nil).Once()
		}, wantCode: http.StatusCreated, wantBody: `{"ok":true}`, wantType: "application/json"},
		{name: "get without a type", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", ch, proxy.XRequest{Method: "GET", Path: "quiz", ContentType: "application/json", Body: []byte{}}).
				Return(&proxy.XResponse{Status: http.StatusOK, Body: []byte("hi")}, nil).Once()
		}, wantCode: http.StatusOK, wantBody: "hi", wantType: "application/octet-stream"},
		{name: "no channel", method: "GET", target: "/api/plugins/echo/x/quiz", wantCode: http.StatusBadRequest, wantBody: "channel_id is required"},
		{name: "unknown channel", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ghost", wantCode: http.StatusNotFound},
		{name: "body too large", method: "POST", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", body: strings.Repeat("a", proxy.MaxXBody+1), wantCode: http.StatusRequestEntityTooLarge},
		{name: "not installed", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", ch, mock.Anything).Return(nil, install.ErrNotInstalled).Once()
		}, wantCode: http.StatusNotFound},
		{name: "disabled", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", ch, mock.Anything).Return(nil, install.ErrNotRunnable).Once()
		}, wantCode: http.StatusConflict},
		{name: "not enabled", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", ch, mock.Anything).Return(nil, install.ErrNotEnabled).Once()
		}, wantCode: http.StatusConflict, wantBody: "not enabled"},
		{name: "unreachable", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", ch, mock.Anything).Return(nil, errors.New("calling the plugin: refused")).Once()
		}, wantCode: http.StatusBadGateway, wantBody: "refused"},
		{name: "not answering", method: "GET", target: "/api/plugins/echo/x/quiz?channel_id=ch-1", setup: func(pm *MockPluginManager) {
			pm.On("X", mock.Anything, "echo", ch, mock.Anything).Return(nil, proxy.ErrUnavailable).Once()
		}, wantCode: http.StatusServiceUnavailable, wantBody: "isn't answering"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginRoutes()
			s.mux.HandleFunc("/api/plugins/{plugin}/x/{path...}", s.srv.handlePluginX)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Maybe()
			s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", DirPath: "/src/app"}, nil).Maybe()
			s.store.On("GetChannel", mock.Anything, "ghost").Return(nil, nil).Maybe()
			if tt.setup != nil {
				tt.setup(pm)
			}
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			if tt.wantType != "" {
				require.Equal(s.T(), tt.wantType, rec.Header().Get("Content-Type"))
			}
		})
	}

	s.Run("disabled", func() {
		s.SetupTest()
		s.mux.HandleFunc("/api/plugins/{plugin}/x/{path...}", s.srv.handlePluginX)
		require.Equal(s.T(), http.StatusNotImplemented, s.testRequest("GET", "/api/plugins/echo/x/quiz", "").Code)
	})
}
