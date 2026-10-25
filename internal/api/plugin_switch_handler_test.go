package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
)

// switchManifest declares the composer switches mode and two.
var switchManifest = &plugin.Manifest{ID: "echo", UI: &plugin.UI{ComposerSwitches: []plugin.ComposerSwitch{
	{ID: "mode", Label: "Mode", Icon: "ui/mode.svg", Title: "Mode"},
	{ID: "two", Label: "Two", Icon: "ui/two.svg"},
}}}

// switchStore keeps the switches' state in a real SQLite and returns it with
// the events the server broadcasts.
func (s *ServerSuite) switchStore() (*db.SQLiteStore, *[]Event) {
	store := s.sqliteStore()
	s.srv.SetPluginSwitches(store)
	hub := NewEventsHub(testLogger())
	sent := &[]Event{}
	hub.captureHook = func(e Event) { *sent = append(*sent, e) }
	s.srv.SetEventsHub(hub)
	return store, sent
}

// pluginSwitchRoutes registers the plugin's switch routes for echo, allowed
// ui.composer_switch in ch-1 and declaring switchManifest's switches.
func (s *ServerSuite) pluginSwitchRoutes() (*MockPluginManager, *db.SQLiteStore, *[]Event) {
	pm := s.pluginAPIRoutes(plugin.CapUISwitch, "/src/app")
	s.mux.HandleFunc("PUT /api/plugin/v1/composer/switches/{id}", s.srv.handlePutPluginSwitch)
	s.mux.HandleFunc("DELETE /api/plugin/v1/composer/switches/{id}", s.srv.handleDeletePluginSwitch)
	s.pluginThreads()
	store, sent := s.switchStore()
	return pm, store, sent
}

// switchesIn returns the switches' state in channelID.
func (s *ServerSuite) switchesIn(store *db.SQLiteStore, channelID string) []composerSwitch {
	saved, err := store.ListPluginSwitches(context.Background())
	require.NoError(s.T(), err)
	var out []composerSwitch
	for _, sw := range saved {
		if sw.ChannelID == channelID {
			out = append(out, composerSwitch{PluginID: sw.PluginID, SwitchID: sw.SwitchID, composerSwitchState: composerSwitchState{On: sw.On, Status: sw.Status, Title: sw.Title}})
		}
	}
	return out
}

func (s *ServerSuite) TestPutPluginSwitch() {
	tests := []struct {
		name     string
		path     string
		body     string
		manifest error
		wantCode int
		wantBody string
		chat     string
		want     *composerSwitch
	}{
		{name: "sets", body: `{"on":true,"status":"ok","title":"Connected"}`, wantCode: http.StatusNoContent, chat: "ch-1",
			want: &composerSwitch{PluginID: "echo", SwitchID: "mode", composerSwitchState: composerSwitchState{On: true, Status: "ok", Title: "Connected"}}},
		{name: "in a thread", body: `{"on":false,"status":"error","thread_id":"th-1"}`, wantCode: http.StatusNoContent, chat: "th-1",
			want: &composerSwitch{PluginID: "echo", SwitchID: "mode", composerSwitchState: composerSwitchState{Status: "error"}}},
		{name: "unknown status", body: `{"status":"weird"}`, wantCode: http.StatusBadRequest, wantBody: "status is one of"},
		{name: "title too long", body: `{"title":"` + strings.Repeat("a", 201) + `"}`, wantCode: http.StatusBadRequest, wantBody: "at most 200"},
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "unknown switch", path: "nope", body: `{}`, wantCode: http.StatusNotFound, wantBody: "no such composer switch"},
		{name: "another channel's thread", body: `{"thread_id":"th-other"}`, wantCode: http.StatusNotFound},
		{name: "manifest", body: `{}`, manifest: errors.New("unreadable"), wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, store, sent := s.pluginSwitchRoutes()
			pm.On("UI", mock.Anything, "echo").Return("/plugins/echo", switchManifest, tt.manifest).Once()
			path := tt.path
			if path == "" {
				path = "mode"
			}
			rec := s.requestAs(echoPlugin, "PUT", "/api/plugin/v1/composer/switches/"+path, tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			if tt.want == nil {
				require.Empty(s.T(), s.switchesIn(store, "ch-1"))
				require.Empty(s.T(), *sent)
				return
			}
			require.Equal(s.T(), []composerSwitch{*tt.want}, s.switchesIn(store, tt.chat))
			require.Len(s.T(), *sent, 1)
			require.Equal(s.T(), "plugin.composer_switch", (*sent)[0].Type)
			require.Equal(s.T(), tt.chat, (*sent)[0].ChannelID)
			require.False(s.T(), (*sent)[0].Global)
			require.Equal(s.T(), *tt.want, (*sent)[0].Data)
		})
	}
}

func (s *ServerSuite) TestDeletePluginSwitch() {
	pm, store, sent := s.pluginSwitchRoutes()
	pm.On("UI", mock.Anything, "echo").Return("/plugins/echo", switchManifest, nil).Twice()
	ctx := context.Background()
	require.NoError(s.T(), store.SetPluginSwitch(ctx, &db.PluginSwitch{PluginID: "echo", ChannelID: "th-1", SwitchID: "mode", On: true}))
	require.NoError(s.T(), store.SetPluginSwitch(ctx, &db.PluginSwitch{PluginID: "echo", ChannelID: "th-1", SwitchID: "two", On: true}))

	rec := s.requestAs(echoPlugin, "DELETE", "/api/plugin/v1/composer/switches/mode?thread_id=th-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.Equal(s.T(), []composerSwitch{{PluginID: "echo", SwitchID: "two", composerSwitchState: composerSwitchState{On: true}}}, s.switchesIn(store, "th-1"))
	require.Len(s.T(), *sent, 1)
	require.Equal(s.T(), "th-1", (*sent)[0].ChannelID)
	require.Equal(s.T(), composerSwitch{PluginID: "echo", SwitchID: "mode"}, (*sent)[0].Data)

	rec = s.requestAs(echoPlugin, "DELETE", "/api/plugin/v1/composer/switches/mode?thread_id=th-other", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestPluginSwitchStoreFails() {
	for _, method := range []string{"PUT", "DELETE"} {
		s.Run(method, func() {
			s.SetupTest()
			pm, store, sent := s.pluginSwitchRoutes()
			pm.On("UI", mock.Anything, "echo").Return("/plugins/echo", switchManifest, nil).Once()
			require.NoError(s.T(), store.Close())
			rec := s.requestAs(echoPlugin, method, "/api/plugin/v1/composer/switches/mode", `{"on":true}`)
			require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
			require.Empty(s.T(), *sent)
		})
	}
}

func (s *ServerSuite) TestPluginSwitchNotConfigured() {
	s.pluginAPIRoutes(plugin.CapUISwitch, "/src/app")
	s.mux.HandleFunc("PUT /api/plugin/v1/composer/switches/{id}", s.srv.handlePutPluginSwitch)
	rec := s.requestAs(echoPlugin, "PUT", "/api/plugin/v1/composer/switches/mode", `{}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestPluginSwitchNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("PUT /api/plugin/v1/composer/switches/{id}", s.srv.handlePutPluginSwitch)
	s.mux.HandleFunc("DELETE /api/plugin/v1/composer/switches/{id}", s.srv.handleDeletePluginSwitch)
	pm.On("Allows", mock.Anything, "echo", plugin.CapUISwitch).Return(install.ErrNotApproved).Twice()
	for _, method := range []string{"PUT", "DELETE"} {
		rec := s.requestAs(echoPlugin, method, "/api/plugin/v1/composer/switches/mode", `{}`)
		require.Equal(s.T(), http.StatusForbidden, rec.Code, method)
	}
}

// composerRoutes registers the app's switch routes with ch-1 and its
// threads known, and returns the plugin manager and the switch store.
func (s *ServerSuite) composerRoutes() (*MockPluginManager, *db.SQLiteStore, *[]Event) {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("GET /api/channels/{id}/composer-switches", s.srv.handleListComposerSwitches)
	s.mux.HandleFunc("PUT /api/channels/{id}/composer-switches/{plugin}/{switch}", s.srv.handleTurnComposerSwitch)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "ch-gone").Return(nil, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "th-perr").Return(&db.Channel{ChannelID: "th-perr", ParentID: "th-err"}, nil).Maybe()
	s.pluginThreads()
	store, sent := s.switchStore()
	return pm, store, sent
}

func (s *ServerSuite) TestListComposerSwitches() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, "ui"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "ui", "mode.svg"), []byte("<svg/>"), 0o644))
	icon := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>"))
	rch := pluginRuntimeChannel(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"})
	listed := func(pm *MockPluginManager) {
		pm.On("List", mock.Anything).Return([]*db.PluginState{
			{PluginID: "echo"}, {PluginID: "off"}, {PluginID: "unsure"}, {PluginID: "broken"}, {PluginID: "bare"},
		}, nil).Once()
		pm.On("Shows", mock.Anything, "echo", plugin.CapUISwitch, rch).Return(true, nil).Once()
		pm.On("Shows", mock.Anything, "off", plugin.CapUISwitch, rch).Return(false, nil).Once()
		pm.On("Shows", mock.Anything, "unsure", plugin.CapUISwitch, rch).Return(false, errors.New("boom")).Once()
		pm.On("Shows", mock.Anything, "broken", plugin.CapUISwitch, rch).Return(true, nil).Once()
		pm.On("Shows", mock.Anything, "bare", plugin.CapUISwitch, rch).Return(true, nil).Once()
		pm.On("UI", mock.Anything, "echo").Return(dir, switchManifest, nil).Once()
		pm.On("UI", mock.Anything, "broken").Return("", nil, errors.New("unreadable")).Once()
		pm.On("UI", mock.Anything, "bare").Return(dir, &plugin.Manifest{ID: "bare"}, nil).Once()
	}
	tests := []struct {
		name     string
		chat     string
		setup    func(*MockPluginManager, *db.SQLiteStore)
		wantCode int
		want     []composerSwitch
	}{
		{name: "the channel's", chat: "ch-1", setup: func(pm *MockPluginManager, _ *db.SQLiteStore) { listed(pm) }, wantCode: http.StatusOK, want: []composerSwitch{
			{PluginID: "echo", SwitchID: "mode", Label: "Mode", Icon: icon, DefaultTitle: "Mode", composerSwitchState: composerSwitchState{On: true, Status: "ok", Title: "Connected"}},
			{PluginID: "echo", SwitchID: "two", Label: "Two"},
		}},
		{name: "a thread's", chat: "th-1", setup: func(pm *MockPluginManager, _ *db.SQLiteStore) { listed(pm) }, wantCode: http.StatusOK, want: []composerSwitch{
			{PluginID: "echo", SwitchID: "mode", Label: "Mode", Icon: icon, DefaultTitle: "Mode", composerSwitchState: composerSwitchState{Status: "busy"}},
			{PluginID: "echo", SwitchID: "two", Label: "Two"},
		}},
		{name: "a side chat", chat: "side", wantCode: http.StatusNotFound},
		{name: "a deleted chat", chat: "ch-gone", wantCode: http.StatusNotFound},
		{name: "chat lookup", chat: "th-err", wantCode: http.StatusInternalServerError},
		{name: "parent lookup", chat: "th-perr", wantCode: http.StatusInternalServerError},
		{name: "plugins lookup", chat: "ch-1", setup: func(pm *MockPluginManager, _ *db.SQLiteStore) {
			pm.On("List", mock.Anything).Return(nil, errors.New("boom")).Once()
		}, wantCode: http.StatusInternalServerError},
		{name: "switches lookup", chat: "ch-1", setup: func(pm *MockPluginManager, store *db.SQLiteStore) {
			pm.On("List", mock.Anything).Return([]*db.PluginState{}, nil).Once()
			require.NoError(s.T(), store.Close())
		}, wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, store, _ := s.composerRoutes()
			ctx := context.Background()
			require.NoError(s.T(), store.SetPluginSwitch(ctx, &db.PluginSwitch{PluginID: "echo", ChannelID: "ch-1", SwitchID: "mode", On: true, Status: "ok", Title: "Connected"}))
			require.NoError(s.T(), store.SetPluginSwitch(ctx, &db.PluginSwitch{PluginID: "echo", ChannelID: "th-1", SwitchID: "mode", Status: "busy"}))
			if tt.setup != nil {
				tt.setup(pm, store)
			}
			rec := s.testRequest("GET", "/api/channels/"+tt.chat+"/composer-switches", "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			if tt.want == nil {
				return
			}
			var got []composerSwitch
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&got))
			require.Equal(s.T(), tt.want, got)
		})
	}
}

// With plugins off a chat has no switches: the list is empty, not an error
// the app would keep retrying, while turning one is refused.
func (s *ServerSuite) TestComposerSwitchesNotConfigured() {
	s.mux.HandleFunc("GET /api/channels/{id}/composer-switches", s.srv.handleListComposerSwitches)
	s.mux.HandleFunc("PUT /api/channels/{id}/composer-switches/{plugin}/{switch}", s.srv.handleTurnComposerSwitch)
	check := func() {
		rec := s.testRequest("GET", "/api/channels/ch-1/composer-switches", "")
		require.Equal(s.T(), http.StatusOK, rec.Code)
		require.JSONEq(s.T(), `[]`, rec.Body.String())
		require.Equal(s.T(), http.StatusNotImplemented, s.testRequest("PUT", "/api/channels/ch-1/composer-switches/echo/mode", `{"on":true}`).Code)
	}
	check()
	s.srv.SetPluginManager(new(MockPluginManager))
	check()
}

func (s *ServerSuite) TestTurnComposerSwitch() {
	rch := pluginRuntimeChannel(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"})
	turned := func(req proxy.SwitchRequest, res *proxy.SwitchState, err error) func(*MockPluginManager) {
		return func(pm *MockPluginManager) {
			pm.On("Switch", mock.Anything, "echo", "mode", rch, req).Return(res, err).Once()
		}
	}
	on := proxy.SwitchRequest{On: true}
	tests := []struct {
		name     string
		chat     string
		body     string
		setup    func(*MockPluginManager)
		closed   bool
		wantCode int
		want     *composerSwitchState
	}{
		{name: "turned", chat: "ch-1", body: `{"on":true}`, setup: turned(on, &proxy.SwitchState{On: true, Status: "busy", Title: "Connecting"}, nil),
			wantCode: http.StatusOK, want: &composerSwitchState{On: true, Status: "busy", Title: "Connecting"}},
		{name: "in a thread", chat: "th-1", body: `{"on":false}`, setup: turned(proxy.SwitchRequest{ThreadID: "th-1"}, &proxy.SwitchState{}, nil),
			wantCode: http.StatusOK, want: &composerSwitchState{}},
		{name: "bad body", chat: "ch-1", body: `{`, wantCode: http.StatusBadRequest},
		{name: "a side chat", chat: "side", body: `{"on":true}`, wantCode: http.StatusNotFound},
		{name: "not installed", chat: "ch-1", body: `{"on":true}`, setup: turned(on, nil, install.ErrNotInstalled), wantCode: http.StatusNotFound},
		{name: "unknown switch", chat: "ch-1", body: `{"on":true}`, setup: turned(on, nil, install.ErrUnknownSwitch), wantCode: http.StatusNotFound},
		{name: "not approved", chat: "ch-1", body: `{"on":true}`, setup: turned(on, nil, install.ErrNotApproved), wantCode: http.StatusForbidden},
		{name: "not enabled", chat: "ch-1", body: `{"on":true}`, setup: turned(on, nil, install.ErrNotEnabled), wantCode: http.StatusForbidden},
		{name: "not runnable", chat: "ch-1", body: `{"on":true}`, setup: turned(on, nil, install.ErrNotRunnable), wantCode: http.StatusConflict},
		{name: "plugin fails", chat: "ch-1", body: `{"on":true}`, setup: turned(on, nil, errors.New("the plugin answered 500")), wantCode: http.StatusBadGateway},
		{name: "invalid state", chat: "ch-1", body: `{"on":true}`, setup: turned(on, &proxy.SwitchState{Status: "weird"}, nil), wantCode: http.StatusBadGateway},
		{name: "store", chat: "ch-1", body: `{"on":true}`, setup: turned(on, &proxy.SwitchState{On: true}, nil), closed: true, wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, store, sent := s.composerRoutes()
			if tt.setup != nil {
				tt.setup(pm)
			}
			if tt.closed {
				require.NoError(s.T(), store.Close())
			}
			rec := s.testRequest("PUT", "/api/channels/"+tt.chat+"/composer-switches/echo/mode", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			if tt.want == nil {
				require.Empty(s.T(), *sent)
				return
			}
			var got composerSwitchState
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&got))
			require.Equal(s.T(), *tt.want, got)
			sw := composerSwitch{PluginID: "echo", SwitchID: "mode", composerSwitchState: *tt.want}
			require.Equal(s.T(), []composerSwitch{sw}, s.switchesIn(store, tt.chat))
			require.Len(s.T(), *sent, 1)
			require.Equal(s.T(), tt.chat, (*sent)[0].ChannelID)
			require.Equal(s.T(), sw, (*sent)[0].Data)
		})
	}
}

// switchDeleteFails is a switch store whose deletes fail.
type switchDeleteFails struct{ *db.SQLiteStore }

func (switchDeleteFails) DeletePluginSwitch(context.Context, string, string, string) error {
	return errors.New("boom")
}

// TestPrunePluginSwitches forgets the switches of a deleted chat, of a
// plugin that may no longer show them and those no longer declared; a
// switch whose state can't be read is kept.
func (s *ServerSuite) TestPrunePluginSwitches() {
	pm, store, sent := s.composerRoutes()
	ctx := context.Background()
	for _, sw := range []*db.PluginSwitch{
		{PluginID: "echo", ChannelID: "ch-1", SwitchID: "mode"},
		{PluginID: "echo", ChannelID: "th-1", SwitchID: "mode"},
		{PluginID: "echo", ChannelID: "ch-1", SwitchID: "old"},
		{PluginID: "echo", ChannelID: "ch-gone", SwitchID: "mode"},
		{PluginID: "echo", ChannelID: "th-err", SwitchID: "mode"},
		{PluginID: "off", ChannelID: "ch-1", SwitchID: "mode"},
		{PluginID: "broken", ChannelID: "ch-1", SwitchID: "mode"},
	} {
		require.NoError(s.T(), store.SetPluginSwitch(ctx, sw))
	}
	rch := pluginRuntimeChannel(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"})
	pm.On("Shows", mock.Anything, "echo", plugin.CapUISwitch, rch).Return(true, nil)
	pm.On("Shows", mock.Anything, "off", plugin.CapUISwitch, rch).Return(false, nil)
	pm.On("Shows", mock.Anything, "broken", plugin.CapUISwitch, rch).Return(true, nil)
	pm.On("UI", mock.Anything, "echo").Return("/plugins/echo", switchManifest, nil)
	pm.On("UI", mock.Anything, "broken").Return("", nil, errors.New("unreadable"))

	s.srv.prunePluginSwitches(ctx)

	saved, err := store.ListPluginSwitches(ctx)
	require.NoError(s.T(), err)
	var left []string
	for _, sw := range saved {
		left = append(left, sw.PluginID+"/"+sw.SwitchID+"@"+sw.ChannelID)
	}
	require.ElementsMatch(s.T(), []string{"echo/mode@ch-1", "echo/mode@th-1", "echo/mode@th-err", "broken/mode@ch-1"}, left)
	require.Empty(s.T(), *sent, "the app learns of it from plugins.changed")

	// A failed delete is only logged.
	require.NoError(s.T(), store.SetPluginSwitch(ctx, &db.PluginSwitch{PluginID: "echo", ChannelID: "ch-gone", SwitchID: "mode"}))
	s.srv.SetPluginSwitches(switchDeleteFails{store})
	s.srv.prunePluginSwitches(ctx)
	saved, err = store.ListPluginSwitches(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), saved, 5)

	// Nor can a failed lookup clear anything.
	require.NoError(s.T(), store.Close())
	s.srv.prunePluginSwitches(ctx)
}

// At daemon start each plugin with a switch on gets its container started,
// once per channel, the channel's for a thread's switch.
func (s *ServerSuite) TestWakePluginSwitches() {
	pm, store, _ := s.composerRoutes()
	ctx := context.Background()
	for _, sw := range []*db.PluginSwitch{
		{PluginID: "echo", ChannelID: "ch-1", SwitchID: "mode", On: true},
		{PluginID: "echo", ChannelID: "th-1", SwitchID: "mode", On: true},
		{PluginID: "echo", ChannelID: "ch-1", SwitchID: "two"},
		{PluginID: "echo", ChannelID: "ch-gone", SwitchID: "mode", On: true},
		{PluginID: "echo", ChannelID: "th-err", SwitchID: "mode", On: true},
		{PluginID: "broken", ChannelID: "ch-1", SwitchID: "mode", On: true},
	} {
		require.NoError(s.T(), store.SetPluginSwitch(ctx, sw))
	}
	rch := pluginRuntimeChannel(&db.Channel{ChannelID: "ch-1", DirPath: "/src/app"})
	pm.On("Wake", ctx, "echo", rch).Return(nil).Once()
	pm.On("Wake", ctx, "broken", rch).Return(errors.New("no image")).Once()

	s.srv.WakePluginSwitches(ctx)
	pm.AssertExpectations(s.T())

	// A failed lookup wakes nothing.
	require.NoError(s.T(), store.Close())
	s.srv.WakePluginSwitches(ctx)
}

func (s *ServerSuite) TestWakePluginSwitchesOff() {
	s.srv.WakePluginSwitches(context.Background())
	s.srv.SetPluginManager(new(MockPluginManager))
	s.srv.WakePluginSwitches(context.Background())
}

func (s *ServerSuite) TestPrunePluginSwitchesOff() {
	s.srv.prunePluginSwitches(context.Background())
	s.srv.SetPluginManager(new(MockPluginManager))
	s.srv.prunePluginSwitches(context.Background())
}
