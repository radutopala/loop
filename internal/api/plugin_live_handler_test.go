package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

// liveManifest declares the control request set_mode.
var liveManifest = &plugin.Manifest{ID: "echo", Live: &plugin.Live{Control: []string{"set_mode"}}}

// pluginThreads makes ch-1's threads known: th-1, a listed thread of it,
// th-other, one of another channel, side, a side chat of ch-1, th-gone, a
// deleted one, and th-err, one that can't be read.
func (s *ServerSuite) pluginThreads() {
	s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", DirPath: "/src/app"}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "th-other").Return(&db.Channel{ChannelID: "th-other", ParentID: "ch-9"}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "side").Return(&db.Channel{ChannelID: "side", ParentID: "ch-1", Kind: db.ChannelKindSide}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "th-gone").Return(nil, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "th-err").Return(nil, errors.New("boom")).Maybe()
}

// pluginLiveRoutes registers the live routes for echo, allowed
// live.control in ch-1, and returns its plugin manager and the live
// sessions.
func (s *ServerSuite) pluginLiveRoutes() (*MockPluginManager, *mockLiveSessions) {
	pm := s.pluginAPIRoutes(plugin.CapLiveControl, "/src/app")
	s.mux.HandleFunc("POST /api/plugin/v1/channel/live/control", s.srv.handlePluginLiveControl)
	s.mux.HandleFunc("PUT /api/plugin/v1/channel/live/hold", s.srv.handlePutPluginLiveHold)
	s.mux.HandleFunc("DELETE /api/plugin/v1/channel/live/hold", s.srv.handleDeletePluginLiveHold)
	s.pluginThreads()
	live := new(mockLiveSessions)
	s.srv.SetLiveSessions(live)
	s.T().Cleanup(func() { live.AssertExpectations(s.T()) })
	return pm, live
}

func (s *ServerSuite) TestPluginLiveControl() {
	request := json.RawMessage(`{"subtype":"set_mode","enabled":true}`)
	body := `{"request":{"subtype":"set_mode","enabled":true}}`
	inThread := `{"request":{"subtype":"set_mode","enabled":true},"thread_id":"th-1"}`
	control := func(channelID string, res json.RawMessage, err error) func(*MockPluginManager, *mockLiveSessions) {
		return func(pm *MockPluginManager, live *mockLiveSessions) {
			pm.On("UI", mock.Anything, "echo").Return("/plugins/echo", liveManifest, nil).Once()
			live.On("LiveControl", mock.Anything, channelID, request).Return(res, err).Once()
		}
	}
	declared := func(pm *MockPluginManager, _ *mockLiveSessions) {
		pm.On("UI", mock.Anything, "echo").Return("/plugins/echo", liveManifest, nil).Once()
	}
	tests := []struct {
		name     string
		body     string
		setup    func(*MockPluginManager, *mockLiveSessions)
		wantCode int
		wantBody string
	}{
		{name: "answered", body: body, setup: control("ch-1", json.RawMessage(`{"mode":"u"}`), nil),
			wantCode: http.StatusOK, wantBody: `{"response":{"mode":"u"}}`},
		{name: "in a thread", body: inThread, setup: control("th-1", json.RawMessage(`{}`), nil),
			wantCode: http.StatusOK, wantBody: `{"response":{}}`},
		{name: "the channel as its thread", body: `{"request":{"subtype":"set_mode","enabled":true},"thread_id":"ch-1"}`,
			setup: control("ch-1", json.RawMessage(`{}`), nil), wantCode: http.StatusOK},
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "not an object", body: `{"request":[1]}`, wantCode: http.StatusBadRequest, wantBody: "an object with a subtype"},
		{name: "no subtype", body: `{"request":{}}`, wantCode: http.StatusBadRequest, wantBody: "an object with a subtype"},
		{name: "undeclared", body: `{"request":{"subtype":"interrupt"}}`, setup: declared,
			wantCode: http.StatusBadRequest, wantBody: "doesn't declare the control request interrupt"},
		{name: "manifest", body: body, setup: func(pm *MockPluginManager, _ *mockLiveSessions) {
			pm.On("UI", mock.Anything, "echo").Return("", nil, errors.New("unreadable")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "unreadable"},
		{name: "another channel's thread", body: `{"request":{"subtype":"set_mode"},"thread_id":"th-other"}`, setup: declared,
			wantCode: http.StatusNotFound, wantBody: "thread not found"},
		{name: "a side chat", body: `{"request":{"subtype":"set_mode"},"thread_id":"side"}`, setup: declared, wantCode: http.StatusNotFound},
		{name: "a deleted thread", body: `{"request":{"subtype":"set_mode"},"thread_id":"th-gone"}`, setup: declared, wantCode: http.StatusNotFound},
		{name: "thread lookup", body: `{"request":{"subtype":"set_mode"},"thread_id":"th-err"}`, setup: declared, wantCode: http.StatusInternalServerError},
		{name: "no session", body: body, setup: control("ch-1", nil, container.ErrNoLiveSession), wantCode: http.StatusConflict, wantBody: "no live session"},
		{name: "unavailable", body: body, setup: control("ch-1", nil, orchestrator.ErrLiveUnavailable), wantCode: http.StatusServiceUnavailable},
		{name: "timeout", body: body, setup: control("ch-1", nil, context.DeadlineExceeded), wantCode: http.StatusGatewayTimeout},
		{name: "refused", body: body, setup: control("ch-1", nil, &container.LiveControlError{Message: "not logged in"}),
			wantCode: http.StatusBadGateway, wantBody: "not logged in"},
		{name: "fails", body: body, setup: control("ch-1", nil, errors.New("writing the control request: gone")),
			wantCode: http.StatusInternalServerError, wantBody: "gone"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, live := s.pluginLiveRoutes()
			if tt.setup != nil {
				tt.setup(pm, live)
			}
			rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/channel/live/control", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestPluginLiveHold() {
	enabled := func(channelID string, on bool) func(*mockLiveSessions) {
		return func(live *mockLiveSessions) {
			live.On("LiveSession", mock.Anything, channelID).Return(orchestrator.LiveStatus{Enabled: on}).Once()
		}
	}
	held := func(channelID string, warmErr error) func(*mockLiveSessions) {
		return func(live *mockLiveSessions) {
			enabled(channelID, true)(live)
			live.On("HoldLive", channelID, "plugin:echo", true).Return(nil).Once()
			live.On("KeepLiveSession", mock.Anything, channelID, local.DefaultAuthorID).Return(warmErr).Once()
		}
	}
	tests := []struct {
		name     string
		method   string
		query    string
		setup    func(*mockLiveSessions)
		wantCode int
		wantBody string
	}{
		{name: "holds and starts", method: "PUT", setup: held("ch-1", nil), wantCode: http.StatusNoContent},
		{name: "holds a running session", method: "PUT", setup: held("ch-1", container.ErrLiveSessionRunning), wantCode: http.StatusNoContent},
		{name: "holds a thread's", method: "PUT", query: "?thread_id=th-1", setup: held("th-1", nil), wantCode: http.StatusNoContent},
		{name: "start fails", method: "PUT", setup: held("ch-1", errors.New("no image")), wantCode: http.StatusInternalServerError, wantBody: "no image"},
		{name: "not live", method: "PUT", setup: enabled("ch-1", false), wantCode: http.StatusConflict, wantBody: "doesn't run live"},
		{name: "hold fails", method: "PUT", setup: func(live *mockLiveSessions) {
			enabled("ch-1", true)(live)
			live.On("HoldLive", "ch-1", "plugin:echo", true).Return(orchestrator.ErrLiveUnavailable).Once()
		}, wantCode: http.StatusServiceUnavailable},
		{name: "another channel's thread", method: "PUT", query: "?thread_id=th-other", wantCode: http.StatusNotFound},
		{name: "releases", method: "DELETE", query: "?thread_id=th-1", setup: func(live *mockLiveSessions) {
			live.On("HoldLive", "th-1", "plugin:echo", false).Return(nil).Once()
		}, wantCode: http.StatusNoContent},
		{name: "release fails", method: "DELETE", setup: func(live *mockLiveSessions) {
			live.On("HoldLive", "ch-1", "plugin:echo", false).Return(orchestrator.ErrLiveUnavailable).Once()
		}, wantCode: http.StatusServiceUnavailable},
		{name: "release in another channel's thread", method: "DELETE", query: "?thread_id=th-other", wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			_, live := s.pluginLiveRoutes()
			if tt.setup != nil {
				tt.setup(live)
			}
			rec := s.requestAs(echoPlugin, tt.method, "/api/plugin/v1/channel/live/hold"+tt.query, "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestPluginLiveNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("POST /api/plugin/v1/channel/live/control", s.srv.handlePluginLiveControl)
	s.mux.HandleFunc("PUT /api/plugin/v1/channel/live/hold", s.srv.handlePutPluginLiveHold)
	s.mux.HandleFunc("DELETE /api/plugin/v1/channel/live/hold", s.srv.handleDeletePluginLiveHold)
	pm.On("Allows", mock.Anything, "echo", plugin.CapLiveControl).Return(install.ErrNotApproved).Times(3)
	require.Equal(s.T(), http.StatusForbidden, s.requestAs(echoPlugin, "POST", "/api/plugin/v1/channel/live/control", "{}").Code)
	require.Equal(s.T(), http.StatusForbidden, s.requestAs(echoPlugin, "PUT", "/api/plugin/v1/channel/live/hold", "").Code)
	require.Equal(s.T(), http.StatusForbidden, s.requestAs(echoPlugin, "DELETE", "/api/plugin/v1/channel/live/hold", "").Code)
}

func (s *ServerSuite) TestPluginLiveNotConfigured() {
	s.pluginAPIRoutes(plugin.CapLiveControl, "/src/app")
	s.mux.HandleFunc("POST /api/plugin/v1/channel/live/control", s.srv.handlePluginLiveControl)
	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/channel/live/control", "{}")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// TestForwardLiveSystem sends a live session's system message to the
// plugins, never to the app.
func (s *ServerSuite) TestForwardLiveSystem() {
	pm := new(MockPluginManager)
	s.srv.plugins = pm
	hub := NewEventsHub(testLogger())
	hub.captureHook = func(e Event) { s.Fail("broadcast", "%v", e) }
	s.srv.SetEventsHub(hub)
	done := make(chan struct{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Once()
	msg := json.RawMessage(`{"type":"system","subtype":"mode_state"}`)
	pm.On("Deliver", mock.Anything, runtime.Channel{ID: "ch-1"}, (*runtime.Channel)(nil), mock.MatchedBy(func(e proxy.Event) bool {
		return e.Type == plugin.EventLiveSystem && e.ChannelID == "ch-1" && e.Timestamp > 0 &&
			reflect.DeepEqual(e.Data, plugin.LiveSystem{Subtype: "mode_state", Message: msg})
	})).Run(func(mock.Arguments) { close(done) }).Return(nil).Once()

	s.srv.ForwardLiveSystem("ch-1", "mode_state", msg)

	<-done
	pm.AssertExpectations(s.T())
}

// TestReleasePluginHolds lets go of the holds of plugins that may no longer
// control their chat's session, keeping those that may and those whose
// state can't be read.
func (s *ServerSuite) TestReleasePluginHolds() {
	pm := s.pluginRoutes()
	live := new(mockLiveSessions)
	s.srv.SetLiveSessions(live)
	s.pluginThreads()
	ch := &db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)
	s.store.On("GetChannel", mock.Anything, "th-orphan").Return(&db.Channel{ChannelID: "th-orphan", ParentID: "ch-gone"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-gone").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "th-perr").Return(&db.Channel{ChannelID: "th-perr", ParentID: "th-err"}, nil)
	rch := pluginRuntimeChannel(ch)
	pm.On("Shows", mock.Anything, "echo", plugin.CapLiveControl, rch).Return(true, nil)
	pm.On("Shows", mock.Anything, "off", plugin.CapLiveControl, rch).Return(false, nil)
	pm.On("Shows", mock.Anything, "unsure", plugin.CapLiveControl, rch).Return(false, errors.New("boom"))
	live.On("LiveHolds").Return(map[string][]string{
		"ch-1":      {"plugin:echo", "plugin:off", "plugin:unsure", "user"},
		"th-1":      {"plugin:echo"},
		"th-gone":   {"plugin:echo"},
		"side":      {"plugin:echo"},
		"th-orphan": {"plugin:echo"},
		"th-err":    {"plugin:echo"},
		"th-perr":   {"plugin:echo"},
	}).Once()
	live.On("HoldLive", "ch-1", "plugin:off", false).Return(nil).Once()
	live.On("HoldLive", "th-gone", "plugin:echo", false).Return(nil).Once()
	live.On("HoldLive", "side", "plugin:echo", false).Return(errors.New("logged")).Once()
	live.On("HoldLive", "th-orphan", "plugin:echo", false).Return(nil).Once()

	s.srv.releasePluginHolds(context.Background())

	live.AssertExpectations(s.T())
}

func (s *ServerSuite) TestReleasePluginHoldsOff() {
	s.srv.releasePluginHolds(context.Background())
	s.srv.SetPluginManager(new(MockPluginManager))
	s.srv.releasePluginHolds(context.Background())
}
