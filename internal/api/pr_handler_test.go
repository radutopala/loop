package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/radutopala/loop/internal/gitlabapi"
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
// commit and a GitHub origin. Returns the dir path. Branch creation lets the
// handler's gitBranch helper return a non-empty value; the origin routes the
// lookup to gh.
func gitInitRepoWithBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", branch},
		{"git", "config", "user.email", "t@t"},
		{"git", "config", "user.name", "t"},
		{"git", "remote", "add", "origin", "https://github.com/o/r.git"},
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

// TestConfigResolverDefaultLoaders: unset loaders fall back to the config
// package's, which return the base config when there's no project file.
func (s *ServerSuite) TestConfigResolverDefaultLoaders() {
	c := configResolver{}
	dir := s.T().TempDir()
	base := &config.Config{ClaudeModel: "base"}
	cfg, err := c.projectLoader()(dir, base)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "base", cfg.ClaudeModel)
	cfg, err = c.worktreeLoader()(dir, "", base)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "base", cfg.ClaudeModel)
}

func (s *ServerSuite) TestResolveClaudeEffort() {
	c := configResolver{
		load:        func() (*config.Config, error) { return &config.Config{ClaudeEffort: "high"}, nil },
		loadProject: func(_ string, cfg *config.Config) (*config.Config, error) { return cfg, nil },
	}
	require.Equal(s.T(), "high", c.claudeEffort("/tmp", ""))
	c.load = func() (*config.Config, error) { return nil, errors.New("boom") }
	require.Equal(s.T(), "", c.claudeEffort("/tmp", ""))
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

// mockGitLabLookup is a testify mock implementing GitLabLookup.
type mockGitLabLookup struct {
	mock.Mock
}

func (m *mockGitLabLookup) IsGitLabHost(host, configDir string) bool {
	return m.Called(host, configDir).Bool(0)
}

func (m *mockGitLabLookup) LookupMR(ctx context.Context, workdir, configDir, branch string) (*githubapi.PRInfo, error) {
	args := m.Called(ctx, workdir, configDir, branch)
	pr, _ := args.Get(0).(*githubapi.PRInfo)
	return pr, args.Error(1)
}

// setOrigin points dir's origin remote at url, or removes it when url is "".
func setOrigin(t *testing.T, dir, url string) {
	t.Helper()
	args := []string{"remote", "set-url", "origin", url}
	if url == "" {
		args = []string{"remote", "remove", "origin"}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
}

func (s *ServerSuite) TestWithGitLabLookup() {
	gl := new(mockGitLabLookup)
	srv := NewServer(nil, nil, nil, nil, nil, nil, WithGitLabLookup(gl))
	require.Equal(s.T(), gl, srv.prLookup.gitlab)
}

func (s *ServerSuite) TestChannelPRGitLabMR() {
	dir := gitInitRepoWithBranch(s.T(), "feat-mr")
	setOrigin(s.T(), dir, "git@lab.example.org:group/repo.git")
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{GitLab: config.GitLabConfig{GlabConfigDir: "~/glab-work"}}, nil
	}
	s.srv.sys = homeSystem{home: "/home/testuser"}
	gl := new(mockGitLabLookup)
	gl.On("IsGitLabHost", "lab.example.org", "/home/testuser/glab-work").Return(true)
	gl.On("LookupMR", mock.Anything, dir, "/home/testuser/glab-work", "feat-mr").
		Return(&githubapi.PRInfo{Number: 7, URL: "https://lab.example.org/group/repo/-/merge_requests/7", State: "opened", Kind: "mr"}, nil)
	gh := new(mockGitHubLookup)
	s.srv.prLookup.client = gh
	s.srv.prLookup.gitlab = gl
	s.store.On("GetChannel", mock.Anything, "ch-mr").
		Return(&db.Channel{ChannelID: "ch-mr", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-mr/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp prResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(s.T(), resp.Present)
	require.Equal(s.T(), 7, resp.PR.Number)
	require.Equal(s.T(), "mr", resp.PR.Kind)
	gh.AssertNotCalled(s.T(), "LookupPR", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestChannelPRGlabNotInstalled() {
	dir := gitInitRepoWithBranch(s.T(), "feat-noglab")
	setOrigin(s.T(), dir, "https://gitlab.com/group/repo.git")
	gl := new(mockGitLabLookup)
	gl.On("IsGitLabHost", "gitlab.com", "").Return(true)
	gl.On("LookupMR", mock.Anything, dir, "", "feat-noglab").Return(nil, fmt.Errorf("wrapped: %w", gitlabapi.ErrGlabNotInstalled))
	s.srv.prLookup.gitlab = gl
	s.store.On("GetChannel", mock.Anything, "ch-noglab").
		Return(&db.Channel{ChannelID: "ch-noglab", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	req := httptest.NewRequest("GET", "/api/channels/ch-noglab/pr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
}

func (s *ServerSuite) TestLookupBranchPRRouting() {
	tests := []struct {
		name      string
		origin    string
		withGH    bool
		withGL    bool
		isGitLab  bool
		wantPR    int
		wantGH    bool
		wantGL    bool
		checkHost string
	}{
		{name: "no origin skips both", origin: "", withGH: true, withGL: true},
		{name: "GitHub origin goes to gh", origin: "https://github.com/o/r.git", withGH: true, withGL: true, checkHost: "github.com", wantGH: true, wantPR: 1},
		{name: "GitLab origin goes to glab", origin: "git@gitlab.com:g/r.git", withGH: true, withGL: true, isGitLab: true, checkHost: "gitlab.com", wantGL: true, wantPR: 2},
		{name: "gh only", origin: "https://github.com/o/r.git", withGH: true, wantGH: true, wantPR: 1},
		{name: "glab only and not a GitLab host", origin: "https://github.com/o/r.git", withGL: true, checkHost: "github.com"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			dir := gitInitRepoWithBranch(s.T(), "b")
			setOrigin(s.T(), dir, tt.origin)
			gh := new(mockGitHubLookup)
			gh.On("LookupPR", mock.Anything, dir, "", "b").Return(&githubapi.PRInfo{Number: 1}, nil)
			gl := new(mockGitLabLookup)
			gl.On("IsGitLabHost", tt.checkHost, "").Return(tt.isGitLab)
			gl.On("LookupMR", mock.Anything, dir, "", "b").Return(&githubapi.PRInfo{Number: 2, Kind: "mr"}, nil)
			s.srv.prLookup.client = nil
			s.srv.prLookup.gitlab = nil
			if tt.withGH {
				s.srv.prLookup.client = gh
			}
			if tt.withGL {
				s.srv.prLookup.gitlab = gl
			}

			pr, err := s.srv.lookupBranchPR(context.Background(), dir, "", "b")
			require.NoError(s.T(), err)
			if tt.wantPR == 0 {
				require.Nil(s.T(), pr)
			} else {
				require.Equal(s.T(), tt.wantPR, pr.Number)
			}
			if !tt.wantGH {
				gh.AssertNotCalled(s.T(), "LookupPR", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
			if !tt.wantGL {
				gl.AssertNotCalled(s.T(), "LookupMR", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

func (s *ServerSuite) TestRemoteHost() {
	tests := []struct {
		url  string
		want string
	}{
		{url: "https://github.com/o/r.git", want: "github.com"},
		{url: "https://user:token@gitlab.example.com/g/r.git", want: "gitlab.example.com"},
		{url: "https://lab.example.org:8443/g/r.git", want: "lab.example.org"},
		{url: "ssh://git@lab.example.org:2222/g/r.git", want: "lab.example.org"},
		{url: "ssh://git@github.com", want: "github.com"},
		{url: "git@github.com:o/r.git", want: "github.com"},
		{url: "lab.example.org:g/r.git", want: "lab.example.org"},
		{url: "/srv/repos/r.git", want: ""},
		{url: "./a:b", want: ""},
		{url: ":nohost", want: ""},
		{url: "", want: ""},
	}
	for _, tt := range tests {
		s.Run(tt.url, func() {
			require.Equal(s.T(), tt.want, remoteHost(tt.url))
		})
	}
}

func (s *ServerSuite) TestGlabConfigDir() {
	tests := []struct {
		name string
		load func() (*config.Config, error)
		want string
	}{
		{name: "configured", load: func() (*config.Config, error) {
			return &config.Config{GitLab: config.GitLabConfig{GlabConfigDir: "/cfg/glab"}}, nil
		}, want: "/cfg/glab"},
		{name: "load error", load: func() (*config.Config, error) { return nil, errors.New("bad") }, want: ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			c := configResolver{load: tt.load}
			require.Equal(s.T(), tt.want, c.glabConfigDir("", ""))
		})
	}
}
