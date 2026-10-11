package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/testutil"
)

// pluginProjectSetup gives channel ch-1 a real project dir with content as
// its config, project loads that honor a real trust store, and a global
// config enabling the plugins in byDefault. It returns the project dir.
func (s *ServerSuite) pluginProjectSetup(content string, byDefault ...string) string {
	dir := s.trustProject(content)
	trust := s.srv.projectTrust.(*config.TrustStore)
	loader := config.NewProjectLoader(trust)
	s.srv.configs.loadProject = loader.LoadProject
	on := true
	global := &config.Config{Plugins: config.PluginsConfig{ByID: map[string]config.PluginConfig{}}}
	for _, id := range byDefault {
		global.Plugins.ByID[id] = config.PluginConfig{Enabled: &on}
	}
	s.srv.configs.load = func() (*config.Config, error) { return global, nil }
	return dir
}

type pluginList struct {
	Plugins        []pluginInfo `json:"plugins"`
	ProjectTrusted *bool        `json:"project_trusted"`
}

func (s *ServerSuite) listPluginsIn(channelID string) pluginList {
	rec := s.testRequest("GET", "/api/plugins?channel_id="+channelID, "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var list pluginList
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &list))
	return list
}

func (s *ServerSuite) TestListPluginsInProject() {
	pm := s.pluginRoutes()
	dir := s.pluginProjectSetup(`{"plugins": {"echo": {"enabled": true}, "quiz": {"enabled": false}}}`, "quiz", "other")
	pm.On("List", mock.Anything).Return([]*db.PluginState{{PluginID: "echo"}, {PluginID: "quiz"}, {PluginID: "other"}}, nil)
	pm.On("UI", mock.Anything, "echo").Return("/d", &plugin.Manifest{Name: "Echo", Actions: []plugin.Action{{ID: "ping"}, {ID: "pong"}}}, nil)
	pm.On("UI", mock.Anything, mock.Anything).Return("", nil, install.ErrNotRunnable)
	pm.On("Containers", mock.Anything, mock.Anything).Return(nil, nil)

	// Untrusted, the project's own settings don't apply yet: only the
	// global defaults do.
	list := s.listPluginsIn("ch-1")
	require.False(s.T(), *list.ProjectTrusted)
	echo, quiz, other := list.Plugins[0], list.Plugins[1], list.Plugins[2]
	require.Equal(s.T(), []string{"ping", "pong"}, echo.Actions)
	require.False(s.T(), echo.EnabledByDefault)
	require.False(s.T(), *echo.Enabled)
	require.True(s.T(), *echo.ProjectEnabled)
	require.True(s.T(), quiz.EnabledByDefault)
	require.True(s.T(), *quiz.Enabled)
	require.False(s.T(), *quiz.ProjectEnabled)
	require.True(s.T(), *other.Enabled)
	require.Nil(s.T(), other.ProjectEnabled)

	// Trusted, they do; a thread, in its channel's dir, answers for its
	// channel's project.
	st := s.projectTrustStatus()
	pm.On("StopDisabled", mock.Anything).Return(false).Once()
	rec := s.testRequest("POST", "/api/config/project/trust?channel_id=ch-1", `{"hash":"`+st.Hash+`"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", DirPath: dir}, nil)
	list = s.listPluginsIn("th-1")
	require.True(s.T(), *list.ProjectTrusted)
	require.True(s.T(), *list.Plugins[0].Enabled)
	require.False(s.T(), *list.Plugins[1].Enabled)
	require.True(s.T(), *list.Plugins[2].Enabled)
}

func (s *ServerSuite) TestListPluginsInProjectWithoutConfig() {
	pm := s.pluginRoutes()
	s.pluginProjectSetup("", "echo")
	pm.On("List", mock.Anything).Return([]*db.PluginState{{PluginID: "echo"}}, nil)
	pm.On("UI", mock.Anything, "echo").Return("", nil, install.ErrNotRunnable)
	pm.On("Containers", mock.Anything, "echo").Return(nil, nil)
	list := s.listPluginsIn("ch-1")
	require.True(s.T(), *list.ProjectTrusted)
	require.True(s.T(), *list.Plugins[0].Enabled)
	require.Nil(s.T(), list.Plugins[0].ProjectEnabled)

	// Without a trust store, the project config applies as written.
	s.srv.projectTrust = nil
	require.True(s.T(), *s.listPluginsIn("ch-1").ProjectTrusted)
}

func (s *ServerSuite) TestListPluginsInProjectErrors() {
	tests := []struct {
		name     string
		setup    func(dir string)
		wantCode int
		wantBody string
	}{
		{name: "unknown channel", setup: func(string) {
			s.store.ExpectedCalls = nil
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil)
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "no project dir", setup: func(string) {
			s.store.ExpectedCalls = nil
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
		}, wantCode: http.StatusInternalServerError, wantBody: "has no dir_path"},
		{name: "no global config", setup: func(string) {
			s.srv.configs.load = func() (*config.Config, error) { return nil, errors.New("bad") }
		}, wantCode: http.StatusInternalServerError, wantBody: "loading config failed"},
		{name: "unreadable project config", setup: func(dir string) {
			path := filepath.Join(dir, ".loop", "config.json")
			require.NoError(s.T(), os.Remove(path))
			require.NoError(s.T(), os.Mkdir(path, 0o755))
		}, wantCode: http.StatusInternalServerError, wantBody: "reading the project config"},
		{name: "broken project config", setup: func(dir string) {
			s.writeProject(dir, `{"plugins": {"echo": {"enabled": "yes"}}}`)
		}, wantCode: http.StatusInternalServerError, wantBody: "plugins.echo"},
		{name: "broken trust store", setup: func(string) {
			cfgDir := s.T().TempDir()
			require.NoError(s.T(), os.MkdirAll(filepath.Join(cfgDir, "loop"), 0o755))
			require.NoError(s.T(), os.WriteFile(filepath.Join(cfgDir, "loop", "project-trust.json"), []byte("nope"), 0o600))
			s.srv.projectTrust = config.NewTrustStoreIn(func() (string, error) { return cfgDir, nil })
		}, wantCode: http.StatusInternalServerError, wantBody: "project-trust.json"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginRoutes()
			dir := s.pluginProjectSetup(`{"plugins": {"echo": {"enabled": true}}}`)
			tt.setup(dir)
			rec := s.testRequest("GET", "/api/plugins?channel_id=ch-1", "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestStopPluginContainer() {
	pm := s.pluginRoutes()
	pm.On("List", mock.Anything).Return([]*db.PluginState{{PluginID: "echo"}}, nil)
	pm.On("Stop", mock.Anything, "echo", "ch-1").Once()
	rec := s.testRequest("POST", "/api/plugins/echo/channels/ch-1/stop", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	rec = s.testRequest("POST", "/api/plugins/ghost/channels/ch-1/stop", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "plugin ghost is not installed")
}

func (s *ServerSuite) TestStopPluginContainerListFails() {
	pm := s.pluginRoutes()
	pm.On("List", mock.Anything).Return(nil, errors.New("db down")).Once()
	rec := s.testRequest("POST", "/api/plugins/echo/channels/ch-1/stop", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestPluginsChangedEvents() {
	pm := s.pluginRoutes()
	hub := NewEventsHub(testLogger())
	var sent []string
	hub.captureHook = func(e Event) {
		require.True(s.T(), e.Global)
		sent = append(sent, e.Type)
	}
	s.srv.SetEventsHub(hub)

	c := echoCandidate()
	pm.On("Install", mock.Anything, "echo:1", false, "", []plugin.Capability(nil)).Return(c, nil).Once()
	require.Equal(s.T(), http.StatusOK, s.testRequest("POST", "/api/plugins", `{"image":"echo:1"}`).Code)
	pm.On("Remove", mock.Anything, "echo", false).Return(nil).Once()
	require.Equal(s.T(), http.StatusNoContent, s.testRequest("DELETE", "/api/plugins/echo", "").Code)
	pm.On("List", mock.Anything).Return([]*db.PluginState{{PluginID: "echo"}}, nil).Once()
	pm.On("Stop", mock.Anything, "echo", "ch-1").Once()
	require.Equal(s.T(), http.StatusNoContent, s.testRequest("POST", "/api/plugins/echo/channels/ch-1/stop", "").Code)
	require.Equal(s.T(), []string{EventPluginsChanged, EventPluginsChanged, EventPluginsChanged}, sent)

	// A config write stops the containers of projects that disabled their
	// plugin, then tells the app.
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("WriteFile", "/home/test/.loop/config.json", mock.Anything, os.FileMode(0644)).Return(nil)
	s.srv.sys = sys
	pm.On("StopDisabled", mock.Anything).Return(true).Once()
	require.Equal(s.T(), http.StatusNoContent, s.testRequest("PUT", "/api/config", `{"content":"{}"}`).Code)
	require.Len(s.T(), sent, 4)
	sent = sent[:3]

	// A failed install changes nothing.
	pm.On("Install", mock.Anything, "echo:1", false, "", []plugin.Capability(nil)).Return(nil, errors.New("pull failed")).Once()
	s.testRequest("POST", "/api/plugins", `{"image":"echo:1"}`)
	require.Len(s.T(), sent, 3)
}
