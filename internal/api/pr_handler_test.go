package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/githubapi"
)

// mockGitHubLookup is a testify mock implementing GitHubLookup.
type mockGitHubLookup struct {
	mock.Mock
}

func (m *mockGitHubLookup) LookupPR(ctx context.Context, workdir, ghUser, branch string) (*githubapi.PRInfo, error) {
	args := m.Called(ctx, workdir, ghUser, branch)
	pr, _ := args.Get(0).(*githubapi.PRInfo)
	return pr, args.Error(1)
}

// gitInitRepoWithBranch creates a temp git repo on a named branch with one
// commit. Returns the dir path. Branch creation lets the handler's gitBranch
// helper return a non-empty value.
func gitInitRepoWithBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", branch},
		{"git", "config", "user.email", "t@t"},
		{"git", "config", "user.name", "t"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
	for _, c := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "init"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	return dir
}

func (s *ServerSuite) TestChannelPRStoreNotConfigured() {
	srv := &Server{serverDeps: serverDeps{logger: s.srv.logger}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", srv.handleChannelPR)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestChannelPRGetChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").Return(nil, errors.New("db error"))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-err/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestChannelPRChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/missing/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestChannelPRNoLookupConfigured() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/tmp"}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-1/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
}

func (s *ServerSuite) TestChannelPRNoDirPathAndNoLoopDir() {
	gh := new(mockGitHubLookup)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-no-dir").
		Return(&db.Channel{ChannelID: "ch-no-dir"}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-no-dir/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
	gh.AssertNotCalled(s.T(), "LookupPR")
}

func (s *ServerSuite) TestChannelPRLoopDirFallback() {
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, mock.Anything, "", mock.Anything).Return(nil, nil)
	s.srv.prLookup.client = gh
	s.srv.loopDir = s.T().TempDir() // not a git repo → gitBranch returns ""
	s.store.On("GetChannel", mock.Anything, "ch-fb").
		Return(&db.Channel{ChannelID: "ch-fb"}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-fb/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
	gh.AssertNotCalled(s.T(), "LookupPR")
}

func (s *ServerSuite) TestChannelPRNotGitRepoReturnsEmpty() {
	gh := new(mockGitHubLookup)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-tmp").
		Return(&db.Channel{ChannelID: "ch-tmp", DirPath: s.T().TempDir()}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-tmp/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
	gh.AssertNotCalled(s.T(), "LookupPR")
}

func (s *ServerSuite) TestChannelPRFound() {
	dir := gitInitRepoWithBranch(s.T(), "feature-x")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feature-x").
		Return(&githubapi.PRInfo{
			Number: 42, URL: "https://github.com/o/r/pull/42",
			BaseRef: "main", HeadRef: "feature-x", State: "OPEN", Title: "feat",
		}, nil)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-pr").
		Return(&db.Channel{ChannelID: "ch-pr", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-pr/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp prResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(s.T(), resp.Present)
	require.NotNil(s.T(), resp.PR)
	require.Equal(s.T(), 42, resp.PR.Number)
	require.Equal(s.T(), "main", resp.PR.BaseRef)
}

func (s *ServerSuite) TestChannelPRLookupNoPR() {
	dir := gitInitRepoWithBranch(s.T(), "feature-y")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feature-y").Return(nil, nil)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-y").
		Return(&db.Channel{ChannelID: "ch-y", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-y/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
}

func (s *ServerSuite) TestChannelPRLookupGhNotInstalled() {
	dir := gitInitRepoWithBranch(s.T(), "feat-z")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feat-z").
		Return(nil, githubapi.ErrGhNotInstalled)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-z").
		Return(&db.Channel{ChannelID: "ch-z", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-z/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
}

func (s *ServerSuite) TestChannelPRLookupGenericError() {
	dir := gitInitRepoWithBranch(s.T(), "feat-w")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feat-w").
		Return(nil, errors.New("network broke"))
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-w").
		Return(&db.Channel{ChannelID: "ch-w", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-w/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
}

func (s *ServerSuite) TestChannelPRResolvesGHUserFromGlobalConfig() {
	dir := gitInitRepoWithBranch(s.T(), "feat-cfg")
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "radutopala"}}, nil
	}
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "radutopala", "feat-cfg").Return(nil, nil)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-cfg").
		Return(&db.Channel{ChannelID: "ch-cfg", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-cfg/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	gh.AssertCalled(s.T(), "LookupPR", mock.Anything, dir, "radutopala", "feat-cfg")
}

func (s *ServerSuite) TestChannelPRResolvesGHUserFromProjectOverride() {
	dir := gitInitRepoWithBranch(s.T(), "feat-proj")
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
	}
	s.srv.configs.loadProject = func(_ string, c *config.Config) (*config.Config, error) {
		out := *c
		out.GitHub.GHUser = "project-user"
		return &out, nil
	}
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "project-user", "feat-proj").Return(nil, nil)
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-proj").
		Return(&db.Channel{ChannelID: "ch-proj", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-proj/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	gh.AssertCalled(s.T(), "LookupPR", mock.Anything, dir, "project-user", "feat-proj")
}

func (s *ServerSuite) TestResolveGHUserNoLoaders() {
	c := configResolver{}
	require.Equal(s.T(), "", c.ghUser("/tmp", ""))
}

func (s *ServerSuite) TestResolveGHUserLoadConfigError() {
	c := configResolver{
		load: func() (*config.Config, error) { return nil, errors.New("boom") },
	}
	require.Equal(s.T(), "", c.ghUser("/tmp", ""))
}

func (s *ServerSuite) TestResolveGHUserLoadConfigNil() {
	c := configResolver{
		load: func() (*config.Config, error) { return nil, nil },
	}
	require.Equal(s.T(), "", c.ghUser("/tmp", ""))
}

func (s *ServerSuite) TestResolveGHUserProjectConfigError() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
		},
		loadProject: func(_ string, _ *config.Config) (*config.Config, error) {
			return nil, errors.New("read failed")
		},
	}
	// On project error we fall back to the global value, not "".
	require.Equal(s.T(), "global", c.ghUser("/tmp", ""))
}

func (s *ServerSuite) TestResolveGHUserProjectConfigNilFallsBackToGlobal() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
		},
		loadProject: func(_ string, _ *config.Config) (*config.Config, error) {
			return nil, nil
		},
	}
	require.Equal(s.T(), "global", c.ghUser("/tmp", ""))
}

func (s *ServerSuite) TestResolveGHUserEmptyWorkdirSkipsProject() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
		},
		loadProject: func(_ string, c *config.Config) (*config.Config, error) {
			out := *c
			out.GitHub.GHUser = "should-not-be-used"
			return &out, nil
		},
	}
	require.Equal(s.T(), "global", c.ghUser("", ""))
}

func (s *ServerSuite) TestResolveGHUserWorktreeUsesParentConfig() {
	// Worktree's own .loop/config.json has no gh_user; the parent project's
	// gh_user must still apply via the three-layer merge.
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
		},
		loadProject: func(_ string, c *config.Config) (*config.Config, error) {
			// Would resolve to "global" if used — but the worktree loader
			// should be picked instead because parentDirPath is non-empty.
			return c, nil
		},
		loadWorktree: func(workdir, parent string, c *config.Config) (*config.Config, error) {
			require.Equal(s.T(), "/wt", workdir)
			require.Equal(s.T(), "/proj", parent)
			out := *c
			out.GitHub.GHUser = "parent-user"
			return &out, nil
		},
	}
	require.Equal(s.T(), "parent-user", c.ghUser("/wt", "/proj"))
}

func (s *ServerSuite) TestResolveGHUserWorktreeLoaderErrorFallsBackToGlobal() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
		},
		loadWorktree: func(_, _ string, _ *config.Config) (*config.Config, error) {
			return nil, errors.New("read failed")
		},
	}
	require.Equal(s.T(), "global", c.ghUser("/wt", "/proj"))
}

// TestResolveGHUserWorktreeNilLoaderUsesRealConfig exercises the
// `loadWorktreeProjectConfig == nil` fallback so the production
// config.LoadWorktreeProjectConfig is selected. Pointed at temp dirs with
// no .loop/config.json, the real loader returns an error and the function
// falls back to the global value.
func (s *ServerSuite) TestResolveGHUserWorktreeNilLoaderUsesRealConfig() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{GitHub: config.GitHubConfig{GHUser: "global"}}, nil
		},
	}
	wt := s.T().TempDir()
	parent := s.T().TempDir()
	require.Equal(s.T(), "global", c.ghUser(wt, parent))
}

func (s *ServerSuite) TestResolveReviewEnabledLoadConfigError() {
	c := configResolver{
		load: func() (*config.Config, error) { return nil, errors.New("boom") },
	}
	// Config-load failure must fail-closed (review hidden) regardless of layer.
	require.False(s.T(), c.reviewEnabled("/tmp", ""))
}

func (s *ServerSuite) TestResolveReviewEnabledGlobalEnabled() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{Enabled: true}}, nil
		},
		loadProject: func(_ string, c *config.Config) (*config.Config, error) {
			return c, nil
		},
	}
	require.True(s.T(), c.reviewEnabled("/proj", ""))
}

func (s *ServerSuite) TestResolveReviewEnabledWorktreeOverride() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{Enabled: false}}, nil
		},
		loadWorktree: func(workdir, parent string, c *config.Config) (*config.Config, error) {
			require.Equal(s.T(), "/wt", workdir)
			require.Equal(s.T(), "/proj", parent)
			out := *c
			out.Review.Enabled = true
			return &out, nil
		},
	}
	require.True(s.T(), c.reviewEnabled("/wt", "/proj"))
}

// Nil worktree loader falls through to config.LoadWorktreeProjectConfig.
// With no .loop/config.json present the real loader returns an error and
// we fall back to the global value.
func (s *ServerSuite) TestResolveReviewEnabledWorktreeNilLoaderUsesRealConfig() {
	c := configResolver{
		load: func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{Enabled: true}}, nil
		},
	}
	wt := s.T().TempDir()
	parent := s.T().TempDir()
	require.True(s.T(), c.reviewEnabled(wt, parent))
}

// --- PR lookup cache ---

// TestChannelPRCache verifies the (dir,branch) cache: the second request is
// served without a new gh lookup, ?fresh=1 bypasses the cache, TTL expiry
// re-fetches, and InvalidatePRCacheForDir forces the next lookup fresh.
func (s *ServerSuite) TestChannelPRCache() {
	dir := gitInitRepoWithBranch(s.T(), "feat-c")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feat-c").Return(nil, nil)
	s.srv.prLookup.client = gh
	now := time.Unix(1000, 0)
	s.srv.prLookup.clock = func() time.Time { return now }
	s.store.On("GetChannel", mock.Anything, "ch-c").
		Return(&db.Channel{ChannelID: "ch-c", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	do := func(url string) {
		req := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(s.T(), http.StatusOK, w.Code)
		require.Contains(s.T(), w.Body.String(), `"present":false`)
	}

	// First request populates; second is served from cache (miss cached too).
	do("/api/channels/ch-c/pr")
	do("/api/channels/ch-c/pr")
	gh.AssertNumberOfCalls(s.T(), "LookupPR", 1)

	// fresh=1 bypasses the cache.
	do("/api/channels/ch-c/pr?fresh=1")
	gh.AssertNumberOfCalls(s.T(), "LookupPR", 2)

	// TTL expiry re-fetches.
	now = now.Add(prCacheTTL + time.Second)
	do("/api/channels/ch-c/pr")
	gh.AssertNumberOfCalls(s.T(), "LookupPR", 3)

	// Poller-driven invalidation forces the next lookup fresh.
	do("/api/channels/ch-c/pr")
	gh.AssertNumberOfCalls(s.T(), "LookupPR", 3)
	s.srv.InvalidatePRCacheForDir(dir)
	do("/api/channels/ch-c/pr")
	gh.AssertNumberOfCalls(s.T(), "LookupPR", 4)
}

// TestChannelPRLookupErrorNotCached verifies transient lookup failures are
// never cached: the next request retries the lookup.
func (s *ServerSuite) TestChannelPRLookupErrorNotCached() {
	dir := gitInitRepoWithBranch(s.T(), "feat-e")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feat-e").Return(nil, errors.New("network down"))
	s.srv.prLookup.client = gh
	s.store.On("GetChannel", mock.Anything, "ch-e").
		Return(&db.Channel{ChannelID: "ch-e", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	for range 2 {
		req := httptest.NewRequest("GET", "/api/channels/ch-e/pr", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(s.T(), http.StatusOK, w.Code)
		require.Contains(s.T(), w.Body.String(), `"present":false`)
	}
	gh.AssertNumberOfCalls(s.T(), "LookupPR", 2)
}

// TestResolveReviewEnabledDefaultLoader pins the nil-loadConfig fallback to
// config.Load. The result depends on the host's real config, so only the
// fallback path itself (no panic, a definite answer) is asserted.
func (s *ServerSuite) TestResolveReviewEnabledDefaultLoader() {
	c := configResolver{}
	_ = c.reviewEnabled(s.T().TempDir(), "")
}
