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

func (s *ServerSuite) pluginRoutes() *MockPluginManager {
	pm := new(MockPluginManager)
	s.srv.SetPluginManager(pm)
	s.mux.HandleFunc("GET /api/plugins", s.srv.handleListPlugins)
	s.mux.HandleFunc("POST /api/plugins/inspect", s.srv.handleInspectPlugin)
	s.mux.HandleFunc("POST /api/plugins", s.srv.handleInstallPlugin)
	s.mux.HandleFunc("DELETE /api/plugins/{id}", s.srv.handleRemovePlugin)
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
	for _, req := range [][2]string{{"GET", "/api/plugins"}, {"POST", "/api/plugins/inspect"}, {"POST", "/api/plugins"}, {"DELETE", "/api/plugins/echo"}} {
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
