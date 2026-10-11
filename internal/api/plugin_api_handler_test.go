package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/types"
)

var echoPlugin = apiauth.Principal{Kind: apiauth.KindPlugin, PluginID: "echo", ChannelID: "ch-1"}

// pluginAPIRoutes registers the Plugin API routes with a plugin manager
// that lets echo use c in ch-1, whose dir is dir.
func (s *ServerSuite) pluginAPIRoutes(c plugin.Capability, dir string) *MockPluginManager {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("GET /api/plugin/v1/channel", s.srv.handlePluginChannel)
	s.mux.HandleFunc("GET /api/plugin/v1/channel/files", s.srv.handlePluginFile)
	s.mux.HandleFunc("GET /api/plugin/v1/channel/tree", s.srv.handlePluginTree)
	s.mux.HandleFunc("GET /api/plugin/v1/channel/git/recent", s.srv.handlePluginGitRecent)
	s.mux.HandleFunc("GET /api/plugin/v1/channel/quality/hotspots", s.srv.handlePluginHotspots)
	s.mux.HandleFunc("POST /api/plugin/v1/events", s.srv.handlePluginEvent)
	s.mux.HandleFunc("POST /api/plugin/v1/notify", s.srv.handlePluginNotify)
	s.mux.HandleFunc("PUT /api/plugin/v1/memory/{name}", s.srv.handlePutPluginMemory)
	s.mux.HandleFunc("DELETE /api/plugin/v1/memory/{name}", s.srv.handleDeletePluginMemory)
	s.srv.sys = osutil.RealSystem{}
	pm.On("Allows", mock.Anything, "echo", c).Return(nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", Name: "app", DirPath: dir, Platform: types.PlatformLocal,
		ModelOverride: "sonnet", EffortOverride: "high",
	}, nil).Maybe()
	return pm
}

func (s *ServerSuite) TestPluginAPIDisabled() {
	s.mux.HandleFunc("GET /api/plugin/v1/channel", s.srv.handlePluginChannel)
	rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestPluginCaller() {
	tests := []struct {
		name     string
		as       apiauth.Principal
		setup    func(pm *MockPluginManager)
		wantCode int
		wantBody string
	}{
		{name: "not a plugin", as: apiauth.Principal{Kind: apiauth.KindOwner}, wantCode: http.StatusForbidden, wantBody: "only a plugin"},
		{name: "not approved", as: echoPlugin, setup: func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapFilesRead).Return(install.ErrNotApproved).Once()
		}, wantCode: http.StatusForbidden},
		{name: "removed", as: echoPlugin, setup: func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapFilesRead).Return(install.ErrNotInstalled).Once()
		}, wantCode: http.StatusForbidden},
		{name: "disabled", as: echoPlugin, setup: func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapFilesRead).Return(install.ErrNotRunnable).Once()
		}, wantCode: http.StatusConflict},
		{name: "store", as: echoPlugin, setup: func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapFilesRead).Return(errors.New("boom")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "boom"},
		{name: "channel fails", as: echoPlugin, setup: func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapFilesRead).Return(nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
		{name: "channel gone", as: echoPlugin, setup: func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapFilesRead).Return(nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginRoutes()
			s.mux.HandleFunc("GET /api/plugin/v1/channel/files", s.srv.handlePluginFile)
			if tt.setup != nil {
				tt.setup(pm)
			}
			rec := s.requestAs(tt.as, "GET", "/api/plugin/v1/channel/files?path=a", "")
			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestPluginAPIOnlyPlugins() {
	s.pluginAPIRoutes("", "/src/app")
	owner := apiauth.Principal{Kind: apiauth.KindOwner}
	for _, route := range []string{
		"GET /api/plugin/v1/channel", "GET /api/plugin/v1/channel/files", "GET /api/plugin/v1/channel/tree",
		"GET /api/plugin/v1/channel/git/recent", "GET /api/plugin/v1/channel/quality/hotspots",
		"POST /api/plugin/v1/events", "POST /api/plugin/v1/notify",
		"PUT /api/plugin/v1/memory/notes", "DELETE /api/plugin/v1/memory/notes",
	} {
		method, path, _ := strings.Cut(route, " ")
		rec := s.requestAs(owner, method, path, "{}")
		require.Equal(s.T(), http.StatusForbidden, rec.Code, route)
	}
}

func (s *ServerSuite) TestPluginChannel() {
	pm := s.pluginAPIRoutes("", "/src/app")
	pm.On("UI", mock.Anything, "echo").Return("/d", &plugin.Manifest{}, nil).Once()
	rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"id":"ch-1","name":"app","dir_path":"/src/app","platform":"local","model":"sonnet","effort":"high","settings":{}}`, rec.Body.String())
}

func (s *ServerSuite) TestPluginChannelSettings() {
	count := func(def any) *config.SchemaProperty { return &config.SchemaProperty{Type: "integer", Default: def} }
	m := &plugin.Manifest{Settings: map[string]*config.SchemaProperty{"count": count(5.0), "lang": {Type: "string", Default: "en"}, "depth": count(nil)}}
	tests := []struct {
		name     string
		global   map[string]any
		project  string
		wantCode int
		wantBody string
	}{
		{name: "defaults", wantCode: http.StatusOK, wantBody: `{"count":5,"lang":"en"}`},
		{
			name: "global then project", global: map[string]any{"count": 7.0, "depth": 2.0},
			project:  `{"plugins": {"echo": {"settings": {"count": 9, "lang": 3}}}}`,
			wantCode: http.StatusOK, wantBody: `{"count":9,"lang":"en","depth":2}`,
		},
		{
			name: "a refused project value leaves the global one", global: map[string]any{"count": 7.0},
			project:  `{"plugins": {"echo": {"settings": {"count": "many"}}}}`,
			wantCode: http.StatusOK, wantBody: `{"count":7,"lang":"en"}`,
		},
		{name: "broken project config", project: `{"plugins": {"echo": {"enabled": "yes"}}}`, wantCode: http.StatusInternalServerError, wantBody: "plugins.echo"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			dir := s.T().TempDir()
			if tt.project != "" {
				s.writeProject(dir, tt.project)
			}
			pm := s.pluginAPIRoutes("", dir)
			s.srv.configs.load = func() (*config.Config, error) {
				return &config.Config{Plugins: config.PluginsConfig{ByID: map[string]config.PluginConfig{"echo": {Settings: tt.global}}}}, nil
			}
			pm.On("UI", mock.Anything, "echo").Return("/d", m, nil).Once()
			rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel", "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			if tt.wantCode != http.StatusOK {
				require.Contains(s.T(), rec.Body.String(), tt.wantBody)
				return
			}
			var got struct {
				Settings json.RawMessage `json:"settings"`
			}
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
			require.JSONEq(s.T(), tt.wantBody, string(got.Settings))
		})
	}
}

func (s *ServerSuite) TestPluginChannelNotRunnable() {
	pm := s.pluginAPIRoutes("", "/src/app")
	pm.On("UI", mock.Anything, "echo").Return("", nil, install.ErrNotRunnable).Once()
	rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestPluginFile() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\ntwo\nthree\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "big"), make([]byte, maxFileSize+1), 0o644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, "sub", ".git"), 0o755))
	require.NoError(s.T(), os.Symlink(s.T().TempDir(), filepath.Join(dir, "out")))
	tests := []struct {
		name     string
		query    string
		wantCode int
		wantBody string
	}{
		{name: "whole", query: "path=a.go", wantCode: http.StatusOK, wantBody: `{"path":"a.go","content":"one\ntwo\nthree\n","start":1,"end":3,"total_lines":3}`},
		{name: "range", query: "path=a.go&start=2&end=3", wantCode: http.StatusOK, wantBody: `{"path":"a.go","content":"two\nthree","start":2,"end":3,"total_lines":3}`},
		{name: "from", query: "path=./a.go&start=3", wantCode: http.StatusOK, wantBody: `{"path":"a.go","content":"three","start":3,"end":3,"total_lines":3}`},
		{name: "to past the end", query: "path=a.go&end=9", wantCode: http.StatusOK, wantBody: `{"path":"a.go","content":"one\ntwo\nthree","start":1,"end":3,"total_lines":3}`},
		{name: "start past the end", query: "path=a.go&start=4", wantCode: http.StatusBadRequest, wantBody: "the file has 3 lines"},
		{name: "end before start", query: "path=a.go&start=3&end=2", wantCode: http.StatusBadRequest, wantBody: "the file has 3 lines"},
		{name: "bad start", query: "path=a.go&start=x", wantCode: http.StatusBadRequest, wantBody: "invalid start"},
		{name: "bad end", query: "path=a.go&end=0", wantCode: http.StatusBadRequest, wantBody: "invalid end"},
		{name: "no path", query: "", wantCode: http.StatusBadRequest, wantBody: "path is required"},
		{name: "escapes", query: "path=../x", wantCode: http.StatusBadRequest, wantBody: "path traversal"},
		{name: "through a link", query: "path=out/x", wantCode: http.StatusBadRequest, wantBody: "path traversal"},
		{name: "git dir", query: "path=sub/.git/config", wantCode: http.StatusBadRequest, wantBody: "paths under .git"},
		{name: "missing", query: "path=nope.go", wantCode: http.StatusNotFound},
		{name: "dir", query: "path=sub", wantCode: http.StatusBadRequest, wantBody: "path is a directory"},
		{name: "too large", query: "path=big", wantCode: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(plugin.CapFilesRead, dir)
			rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/files?"+tt.query, "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			if tt.wantCode == http.StatusOK {
				require.JSONEq(s.T(), tt.wantBody, rec.Body.String())
				return
			}
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

// unreadableSystem is the real filesystem except that no file can be read.
type unreadableSystem struct{ osutil.RealSystem }

func (unreadableSystem) ReadFile(string) ([]byte, error) { return nil, errors.New("permission denied") }

func (s *ServerSuite) TestPluginFileUnreadable() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o600))
	s.pluginAPIRoutes(plugin.CapFilesRead, dir)
	s.srv.sys = unreadableSystem{}
	rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/files?path=a.go", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestPluginFilesFollowTheGate() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, ".ssh"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".ssh", "id"), []byte("key"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644))
	gate := func(enabled bool, rules ...types.FileRule) func() (*config.Config, error) {
		return func() (*config.Config, error) {
			cfg := &config.Config{}
			cfg.Gates.Agentgate.Enabled = enabled
			cfg.Gates.Agentgate.DefaultDecision = types.DecisionDeny
			cfg.Gates.Agentgate.FileRules = rules
			return cfg, nil
		}
	}
	denySSH := types.FileRule{Paths: []string{"**/.ssh/**"}, Operations: []string{"read"}, Decision: types.DecisionDeny}
	tests := []struct {
		name     string
		load     func() (*config.Config, error)
		path     string
		wantCode int
		wantTree []string
	}{
		{name: "denied", load: gate(true, denySSH), path: ".ssh/id", wantCode: http.StatusForbidden, wantTree: []string{"a.go"}},
		{name: "the workspace is allowed", load: gate(true, denySSH), path: "a.go", wantCode: http.StatusOK, wantTree: []string{"a.go"}},
		{name: "gate off", load: gate(false, denySSH), path: ".ssh/id", wantCode: http.StatusOK, wantTree: []string{".ssh/id", "a.go"}},
		{name: "bad rule", load: gate(true, types.FileRule{Paths: []string{"/x"}, Operations: []string{"fly"}, Decision: types.DecisionDeny}), path: "a.go", wantCode: http.StatusInternalServerError},
		{name: "no config", load: func() (*config.Config, error) { return nil, errors.New("broken") }, path: "a.go", wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(plugin.CapFilesRead, dir)
			s.srv.configs.load = tt.load
			rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/files?path="+tt.path, "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			rec = s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/tree", "")
			if tt.wantTree == nil {
				require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
				return
			}
			var out pluginTreeResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &out))
			require.Equal(s.T(), tt.wantTree, out.Files)
		})
	}
}

func TestLineRange(t *testing.T) {
	tests := []struct {
		name                string
		content             string
		start, end          int
		want                string
		wantFrom, wantTo, n int
		wantErr             bool
	}{
		{name: "empty", want: "", wantFrom: 0, wantTo: 0, n: 0},
		{name: "empty with a range", start: 1, n: 0, wantErr: true},
		{name: "no trailing newline", content: "a\nb", start: 2, want: "b", wantFrom: 2, wantTo: 2, n: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, from, to, n, err := lineRange(tt.content, tt.start, tt.end)
			require.Equal(t, tt.n, n)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantFrom, from)
			require.Equal(t, tt.wantTo, to)
		})
	}
}

func (s *ServerSuite) TestPluginTree() {
	repo := initGitRepo(s.T())
	require.NoError(s.T(), os.MkdirAll(filepath.Join(repo, "pkg"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "pkg", "a.go"), []byte("package pkg"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "untracked.go"), []byte("x"), 0o644))
	gitRun(s.T(), repo, "add", "pkg")
	gitRun(s.T(), repo, "commit", "-m", "pkg")

	plain := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(plain, ".git"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(plain, ".git", "HEAD"), []byte("x"), 0o644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(plain, "src"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(plain, "src", "b.go"), []byte("x"), 0o644))
	require.NoError(s.T(), os.Symlink("src/b.go", filepath.Join(plain, "link.go")))

	emptyRepo := s.T().TempDir()
	gitRun(s.T(), emptyRepo, "init")

	tests := []struct {
		name     string
		dir      string
		query    string
		wantCode int
		want     []string
	}{
		{name: "tracked", dir: repo, want: []string{"README.md", "pkg/a.go"}},
		{name: "glob on the path", dir: repo, query: "glob=pkg/*", want: []string{"pkg/a.go"}},
		{name: "glob on the base name", dir: repo, query: "glob=*.go", want: []string{"pkg/a.go"}},
		{name: "not a repo", dir: plain, want: []string{"src/b.go"}},
		{name: "empty repo", dir: emptyRepo, want: []string{}},
		{name: "bad glob", dir: repo, query: "glob=[", wantCode: http.StatusBadRequest},
		{name: "missing dir", dir: filepath.Join(plain, "nope"), wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(plugin.CapFilesRead, tt.dir)
			rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/tree?"+tt.query, "")
			if tt.wantCode != 0 {
				require.Equal(s.T(), tt.wantCode, rec.Code)
				return
			}
			require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
			var out pluginTreeResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &out))
			require.Equal(s.T(), tt.want, out.Files)
			require.False(s.T(), out.Truncated)
		})
	}
}

func (s *ServerSuite) TestPluginTreeTruncated() {
	dir := s.T().TempDir()
	for i := range pluginTreeMax + 2 {
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), nil, 0o644))
	}
	s.pluginAPIRoutes(plugin.CapFilesRead, dir)
	rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/tree", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var out pluginTreeResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(s.T(), out.Files, pluginTreeMax)
	require.True(s.T(), out.Truncated)
}

func (s *ServerSuite) TestPluginGitRecent() {
	repo := initGitRepo(s.T())
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "a.go"), []byte("1"), 0o644))
	gitRun(s.T(), repo, "add", ".")
	gitRun(s.T(), repo, "commit", "-m", "add a")
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "a.go"), []byte("2"), 0o644))
	gitRun(s.T(), repo, "commit", "-am", "change a")

	s.Run("recent", func() {
		s.SetupTest()
		s.pluginAPIRoutes(plugin.CapGitRead, repo)
		rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/git/recent?days=3", "")
		require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
		var out pluginRecentResponse
		require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &out))
		require.Len(s.T(), out.Commits, 3)
		require.Equal(s.T(), "change a", out.Commits[0].Subject)
		require.Equal(s.T(), "Test", out.Commits[0].Author)
		require.Len(s.T(), out.Commits[0].SHA, 40)
		_, err := time.Parse(time.RFC3339, out.Commits[0].Time)
		require.NoError(s.T(), err)
		require.Equal(s.T(), []pluginChangedFile{{Path: "a.go", Commits: 2}, {Path: "README.md", Commits: 1}}, out.Files)
	})

	s.Run("bad days", func() {
		s.SetupTest()
		s.pluginAPIRoutes(plugin.CapGitRead, repo)
		rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/git/recent?days=0", "")
		require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	})

	s.Run("not a repo", func() {
		s.SetupTest()
		s.pluginAPIRoutes(plugin.CapGitRead, s.T().TempDir())
		rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/git/recent", "")
		require.Equal(s.T(), http.StatusConflict, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "git log")
	})
}

func TestParseRecentLog(t *testing.T) {
	res := parseRecentLog("junk\x1es1\x1fone\x1fA\x1f2026-01-01T00:00:00Z\n\nb.go\na.go\n\x1es2\x1ftwo\x1fB\x1f2026-01-02T00:00:00Z\n\nb.go\nc.go\n")
	require.Len(t, res.Commits, 2)
	require.Equal(t, []pluginChangedFile{{Path: "b.go", Commits: 2}, {Path: "a.go", Commits: 1}, {Path: "c.go", Commits: 1}}, res.Files)
}

func (s *ServerSuite) TestPluginHotspots() {
	s.pluginAPIRoutes(plugin.CapQualityRead, "/src/app")
	s.srv.quality.setGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/quality/hotspots", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)

	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec = s.requestAs(echoPlugin, "GET", "/api/plugin/v1/channel/quality/hotspots?limit=1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Functions, 1)
	require.Equal(s.T(), "Hot", resp.Functions[0].Name)
}

func (s *ServerSuite) TestPluginEventAndNotify() {
	tests := []struct {
		name     string
		path     string
		c        plugin.Capability
		body     string
		wantCode int
		want     string
	}{
		{name: "event", path: "events", body: `{"type":"quiz.ready","data":{"id":1}}`, wantCode: http.StatusNoContent,
			want: `{"type":"plugin.event","channel_id":"ch-1","data":{"plugin_id":"echo","type":"quiz.ready","data":{"id":1}}}`},
		{name: "event without a type", path: "events", body: `{}`, wantCode: http.StatusBadRequest},
		{name: "event too big", path: "events", body: `{"type":"x","data":"` + strings.Repeat("a", pluginEventMax) + `"}`, wantCode: http.StatusBadRequest},
		{name: "notify", path: "notify", c: plugin.CapUINotify, body: `{"title":"Quiz ready","body":"3 questions"}`, wantCode: http.StatusNoContent,
			want: `{"type":"plugin.notify","channel_id":"ch-1","data":{"plugin_id":"echo","title":"Quiz ready","body":"3 questions"}}`},
		{name: "notify without a title", path: "notify", c: plugin.CapUINotify, body: `{"body":"x"}`, wantCode: http.StatusBadRequest},
		{name: "notify bad body", path: "notify", c: plugin.CapUINotify, body: `{`, wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(tt.c, "/src/app")
			if tt.want == "" {
				rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/"+tt.path, tt.body)
				require.Equal(s.T(), tt.wantCode, rec.Code)
				return
			}
			// Without an events hub, nothing is sent.
			rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/"+tt.path, tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code)

			hub := NewEventsHub(testLogger())
			var sent []Event
			hub.captureHook = func(e Event) { sent = append(sent, e) }
			s.srv.SetEventsHub(hub)
			rec = s.requestAs(echoPlugin, "POST", "/api/plugin/v1/"+tt.path, tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Len(s.T(), sent, 1)
			sent[0].Timestamp = 0
			got, _ := json.Marshal(sent[0])
			want := strings.TrimSuffix(tt.want, "}") + `,"timestamp":0}`
			require.JSONEq(s.T(), want, string(got))
		})
	}
}
