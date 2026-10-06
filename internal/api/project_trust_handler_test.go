package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/testutil"
)

// trustProject sets up a real project dir for channel ch-1 and a trust store
// kept in a temp config dir, and returns the project dir.
func (s *ServerSuite) trustProject(content string) string {
	dir := s.T().TempDir()
	cfgDir := s.T().TempDir()
	s.srv.sys = osutil.RealSystem{}
	s.srv.projectTrust = config.NewTrustStoreIn(func() (string, error) { return cfgDir, nil })
	if content != "" {
		s.writeProject(dir, content)
	}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)
	s.mux.HandleFunc("GET /api/config/project/trust", s.srv.handleGetProjectTrust)
	s.mux.HandleFunc("POST /api/config/project/trust", s.srv.handleTrustProjectConfig)
	return dir
}

func (s *ServerSuite) writeProject(dir, content string) {
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".loop", "config.json"), []byte(content), 0o644))
}

func (s *ServerSuite) projectTrustStatus() config.TrustStatus {
	rec := s.testRequest("GET", "/api/config/project/trust?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var st config.TrustStatus
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &st))
	return st
}

func (s *ServerSuite) TestProjectTrustNotConfigured() {
	s.mux.HandleFunc("GET /api/config/project/trust", s.srv.handleGetProjectTrust)
	s.mux.HandleFunc("POST /api/config/project/trust", s.srv.handleTrustProjectConfig)
	for _, method := range []string{"GET", "POST"} {
		rec := s.testRequest(method, "/api/config/project/trust?channel_id=ch-1", `{"hash":"h"}`)
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, method)
	}
}

func (s *ServerSuite) TestProjectTrustFlow() {
	dir := s.trustProject(`{"mounts": ["/a:/a"]}`)

	st := s.projectTrustStatus()
	require.False(s.T(), st.Trusted)
	require.Contains(s.T(), st.Current, "/a:/a")
	require.Empty(s.T(), st.Approved)

	// The file changed after the owner looked: nothing is trusted.
	s.writeProject(dir, `{"mounts": ["/:/host"]}`)
	rec := s.testRequest("POST", "/api/config/project/trust?channel_id=ch-1", `{"hash":"`+st.Hash+`"}`)
	require.Equal(s.T(), http.StatusConflict, rec.Code)
	require.False(s.T(), s.projectTrustStatus().Trusted)

	st = s.projectTrustStatus()
	rec = s.testRequest("POST", "/api/config/project/trust?channel_id=ch-1", `{"hash":"`+st.Hash+`"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	require.True(s.T(), s.projectTrustStatus().Trusted)
}

func (s *ServerSuite) TestProjectTrustBadRequests() {
	s.trustProject(`{"mounts": ["/a:/a"]}`)
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	tests := []struct {
		name, method, path, body string
		want                     int
		wantBody                 string
	}{
		{"get no channel", "GET", "/api/config/project/trust", "", http.StatusBadRequest, "channel_id is required"},
		{"get unknown channel", "GET", "/api/config/project/trust?channel_id=missing", "", http.StatusBadRequest, "not found"},
		{"post no channel", "POST", "/api/config/project/trust", `{"hash":"h"}`, http.StatusBadRequest, "channel_id is required"},
		{"post bad body", "POST", "/api/config/project/trust?channel_id=ch-1", `not json`, http.StatusBadRequest, ""},
		{"post no hash", "POST", "/api/config/project/trust?channel_id=ch-1", `{}`, http.StatusBadRequest, "hash is required"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			rec := s.testRequest(tc.method, tc.path, tc.body)
			require.Equal(s.T(), tc.want, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tc.wantBody)
		})
	}
}

func (s *ServerSuite) TestProjectTrustStoreErrors() {
	s.trustProject(`{"mounts": "not a list"}`)
	rec := s.testRequest("GET", "/api/config/project/trust?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "parsing project config file")

	rec = s.testRequest("POST", "/api/config/project/trust?channel_id=ch-1", `{"hash":"h"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "parsing project config file")
}

// TestSaveProjectConfigKeepsTrust: the owner's own edit stays trusted, but
// an owner edit of a config an agent changed doesn't approve the change.
func (s *ServerSuite) TestSaveProjectConfigKeepsTrust() {
	dir := s.trustProject("")
	save := func(content string) {
		body, _ := json.Marshal(configSaveRequest{Content: content})
		rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", string(body))
		require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	}

	save(`{"mounts": ["/a:/a"]}`)
	require.True(s.T(), s.projectTrustStatus().Trusted, "a new file the owner wrote")
	save(`{"mounts": ["/a:/a", "/b:/b"]}`)
	require.True(s.T(), s.projectTrustStatus().Trusted, "an owner edit of a trusted file")

	s.writeProject(dir, `{"mounts": ["/:/host"]}`)
	save(`{"mounts": ["/:/host"], "claude_model": "opus"}`)
	require.False(s.T(), s.projectTrustStatus().Trusted, "an owner edit of an agent's change")
}

func (s *ServerSuite) TestSaveProjectConfigUnreadableBeforeKeepsNothing() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/projects/myapp"}, nil)
	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/projects/myapp/.loop", os.FileMode(0755)).Return(nil)
	sys.On("ReadFile", "/projects/myapp/.loop/config.json").Return(nil, errors.New("boom"))
	sys.On("WriteFile", "/projects/myapp/.loop/config.json", []byte("{}\n"), os.FileMode(0644)).Return(nil)
	s.srv.sys = sys
	trust := new(mockProjectTrust)
	s.srv.projectTrust = trust

	rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-1", `{"content":"{}"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	trust.AssertNotCalled(s.T(), "Keep", mock.Anything, mock.Anything, mock.Anything)
}

// TestKeepProjectTrustFailureOnlyLogs: the write landed, so a trust store
// error doesn't fail it.
func (s *ServerSuite) TestKeepProjectTrustFailureOnlyLogs() {
	var logs strings.Builder
	s.srv.logger = slog.New(slog.NewTextHandler(&logs, nil))
	trust := new(mockProjectTrust)
	trust.On("Keep", "/p", []byte("a"), []byte("b")).Return(errors.New("boom"))
	s.srv.projectTrust = trust
	s.srv.keepProjectTrust("/p", []byte("a"), []byte("b"))
	require.Contains(s.T(), logs.String(), "keeping the project config trusted")
	trust.AssertExpectations(s.T())
}

func (s *ServerSuite) TestApplyLearnConfigKeepsTrust() {
	s.trustProject("")
	s.srv.configs.loadProject = func(string, *config.Config) (*config.Config, error) { return &config.Config{}, nil }

	status, errText := s.applyProposal(db.LearnKindMount, `{"mount":"~/.aws:~/.aws:ro"}`)
	require.Equal(s.T(), db.LearnApplied, status, errText)
	st := s.projectTrustStatus()
	require.True(s.T(), st.Trusted)
	require.Contains(s.T(), st.Current, "~/.aws")
}

// TestModifyBashShortcutProjectTrust: the owner's project shortcut stays
// trusted; an agent's waits for review.
func (s *ServerSuite) TestModifyBashShortcutProjectTrust() {
	s.trustProject("")
	add := func(name string, agent bool) {
		body := `{"action":"add","scope":"project","channel_id":"ch-1","name":"` + name + `","command":"make ` + name + `"}`
		req := httptest.NewRequest("POST", "/api/bash-shortcuts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if agent {
			req = req.WithContext(apiauth.WithPrincipal(req.Context(), apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "ch-1"}))
		}
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	}

	add("build", false)
	require.True(s.T(), s.projectTrustStatus().Trusted)
	add("deploy", true)
	st := s.projectTrustStatus()
	require.False(s.T(), st.Trusted)
	require.Contains(s.T(), st.Current, "deploy")
	require.NotContains(s.T(), st.Approved, "deploy")
}

type mockProjectTrust struct {
	mock.Mock
}

func (m *mockProjectTrust) Status(dir string) (config.TrustStatus, error) {
	args := m.Called(dir)
	return args.Get(0).(config.TrustStatus), args.Error(1)
}

func (m *mockProjectTrust) Trust(dir, hash string) error {
	return m.Called(dir, hash).Error(0)
}

func (m *mockProjectTrust) Keep(dir string, before, after []byte) error {
	return m.Called(dir, before, after).Error(0)
}
