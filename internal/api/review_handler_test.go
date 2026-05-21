package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
)

func newServerForReviewTests(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(MockChannelLister)
	return NewServer(nil, nil, nil, store, nil, logger)
}

// mockGitHubReview is a testify mock for GitHubReview used by review handler tests.
type mockGitHubReview struct {
	mock.Mock
}

func (m *mockGitHubReview) FetchPRByNumber(ctx context.Context, workdir, ghUser string, number int) (*githubapi.PRInfo, error) {
	args := m.Called(ctx, workdir, ghUser, number)
	pr, _ := args.Get(0).(*githubapi.PRInfo)
	return pr, args.Error(1)
}

func (m *mockGitHubReview) FetchPRHeadSHA(ctx context.Context, workdir, ghUser string, number int) (string, error) {
	args := m.Called(ctx, workdir, ghUser, number)
	return args.String(0), args.Error(1)
}

func (m *mockGitHubReview) FetchRepoSlug(ctx context.Context, workdir, ghUser string) (*githubapi.RepoSlug, error) {
	args := m.Called(ctx, workdir, ghUser)
	slug, _ := args.Get(0).(*githubapi.RepoSlug)
	return slug, args.Error(1)
}

func (m *mockGitHubReview) ListOpenPRs(ctx context.Context, workdir, ghUser string) ([]githubapi.PRInfo, error) {
	args := m.Called(ctx, workdir, ghUser)
	prs, _ := args.Get(0).([]githubapi.PRInfo)
	return prs, args.Error(1)
}

func (m *mockGitHubReview) PostPRComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int, commitID, path, side string, line int, body string) (int64, error) {
	args := m.Called(ctx, workdir, ghUser, slug, prNum, commitID, path, side, line, body)
	id, _ := args.Get(0).(int64)
	return id, args.Error(1)
}

func (m *mockGitHubReview) FetchPRReviewComments(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int) ([]githubapi.PRReviewComment, error) {
	args := m.Called(ctx, workdir, ghUser, slug, prNum)
	cs, _ := args.Get(0).([]githubapi.PRReviewComment)
	return cs, args.Error(1)
}

func (m *mockGitHubReview) DeletePRReviewComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, commentID int64) error {
	args := m.Called(ctx, workdir, ghUser, slug, commentID)
	return args.Error(0)
}

// mockPR is a testify mock for review.PR.
type mockPR struct {
	mock.Mock
}

func (m *mockPR) Add(ctx context.Context, parentDir string, prNum int) (string, error) {
	args := m.Called(ctx, parentDir, prNum)
	return args.String(0), args.Error(1)
}

func (m *mockPR) Refresh(ctx context.Context, parentDir, worktreePath string, prNum int) error {
	args := m.Called(ctx, parentDir, worktreePath, prNum)
	return args.Error(0)
}

func (m *mockPR) Diff(ctx context.Context, parentDir, worktreePath, baseRef string, comments []*review.Comment) ([]byte, error) {
	args := m.Called(ctx, parentDir, worktreePath, baseRef, comments)
	b, _ := args.Get(0).([]byte)
	return b, args.Error(1)
}

func (m *mockPR) Remove(ctx context.Context, parentDir, worktreePath string) error {
	args := m.Called(ctx, parentDir, worktreePath)
	return args.Error(0)
}

type ReviewHandlerSuite struct {
	suite.Suite
	srv   *Server
	store *MockChannelLister
	gh    *mockGitHubReview
	wt    *mockPR
	rs    *review.Store
	mux   *http.ServeMux
}

func TestReviewHandlerSuite(t *testing.T) {
	suite.Run(t, new(ReviewHandlerSuite))
}

func (s *ReviewHandlerSuite) SetupTest() {
	s.srv = newServerForReviewTests(s.T())
	s.store = s.srv.store.(*MockChannelLister)
	s.gh = new(mockGitHubReview)
	s.wt = new(mockPR)
	s.rs = review.NewStore()
	s.srv.SetReviewService(s.gh, s.rs, s.wt)
	// Stub config loaders so resolveGHUser/resolveReviewEnabled are
	// deterministic in tests (otherwise they would shell out to the user's
	// actual ~/.loop/config.json). Review is enabled in the base stub so
	// the gate is transparent for the existing test cases; tests that
	// specifically exercise the gate override loadConfig to return
	// Enabled=false.
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: true}}, nil
	}
	s.srv.loadProjectConfig = func(string, *config.Config) (*config.Config, error) { return nil, nil }
	s.srv.loadWorktreeProjectConfig = func(string, string, *config.Config) (*config.Config, error) { return nil, nil }
	s.mux = s.srv.buildMux()
}

func (s *ReviewHandlerSuite) postJSON(path string, body any) *httptest.ResponseRecorder {
	buf, err := json.Marshal(body)
	require.NoError(s.T(), err)
	req := httptest.NewRequest("POST", path, bytes.NewReader(buf))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ReviewHandlerSuite) doRaw(method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

// ---- review.enabled gate ----

// When review.enabled is false in the merged config, every mutating
// endpoint returns 403. Each sub-case sets up just enough state to walk
// the handler past its in-memory validation and hit the dir-path-based
// gate (which always reads through resolveReviewEnabled).
func (s *ReviewHandlerSuite) TestReviewEnabledGate403s() {
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: false}}, nil
	}
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.srv.SetReviewAgent(&mockReviewRunner{}, "", "")

	readySession := func() *review.Session {
		return &review.Session{
			PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main", HeadRef: "feat"},
			HeadSHA:      "abc",
			WorktreePath: "/repo/.worktrees/pr-7",
			RawDiff:      "diff",
			Status:       review.StatusReady,
		}
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   []byte
		setup  func()
	}{
		{"load", "POST", "/api/channels/ch1/review/load", []byte(`{"pr_number":7}`), nil},
		{"list-prs", "GET", "/api/channels/ch1/review/prs", nil, nil},
		{"sync", "POST", "/api/channels/ch1/review/sync", nil, func() {
			s.rs.Put("ch1", readySession())
		}},
		{"run", "POST", "/api/channels/ch1/review/run", nil, func() {
			s.rs.Put("ch1", readySession())
		}},
		{"push-comment", "POST", "/api/channels/ch1/review/comments/a/push", nil, func() {
			s.rs.Put("ch1", readySession())
			s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Side: "RIGHT"})
		}},
		{"push-all", "POST", "/api/channels/ch1/review/push-all", nil, func() {
			s.rs.Put("ch1", readySession())
			s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Side: "RIGHT"})
		}},
		{"delete-comment", "DELETE", "/api/channels/ch1/review/comments/a", nil, func() {
			s.rs.Put("ch1", readySession())
			s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", GitHubID: 99})
		}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.rs.Delete("ch1")
			if tc.setup != nil {
				tc.setup()
			}
			w := s.doRaw(tc.method, tc.path, tc.body)
			require.Equal(s.T(), http.StatusForbidden, w.Code, "endpoint %s should 403 when review disabled", tc.name)
		})
	}
}

// GET .../review and DELETE .../review intentionally bypass the gate:
// the FE may need to inspect or tear down a session that already exists,
// even after the feature flag was flipped off.
func (s *ReviewHandlerSuite) TestReviewEnabledGateAllowsGetAndDelete() {
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: false}}, nil
	}
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}})

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels/ch1/review", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil))
	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

// ---- load ----

func (s *ReviewHandlerSuite) TestLoadInvalidPRNumber() {
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 0})
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadInvalidBody() {
	w := s.doRaw("POST", "/api/channels/ch1/review/load", []byte("not json"))
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadChannelLookupError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadNoDirPath() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadFetchPRError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(nil, errors.New("api fail"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Equal(s.T(), review.StatusError, s.rs.Get("ch1").Status)
	require.Equal(s.T(), "api fail", s.rs.Get("ch1").Error)
}

func (s *ReviewHandlerSuite) TestLoadFetchPRGhNotInstalled() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(nil, githubapi.ErrGhNotInstalled)
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadPRNotFound() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(nil, nil)
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadFetchSHAError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("", errors.New("no sha"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadWorktreeError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7).Return("", errors.New("git failed"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadDiffError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7).Return("/repo/.worktrees/pr-7", nil)
	// Comment fetch happens before Diff so its mock has to be in place.
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return(nil, errors.New("diff failed"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Equal(s.T(), review.StatusError, s.rs.Get("ch1").Status)
}

func (s *ReviewHandlerSuite) TestLoadHappyPath() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	pr := &githubapi.PRInfo{Number: 7, URL: "https://github.com/x/y/pull/7", HeadRef: "feat-x", BaseRef: "main", State: "OPEN"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("deadbeef", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7).Return("/repo/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff text"), nil)
	// No pre-existing GH comments — slug fetch fails so the best-effort
	// path short-circuits and Load still succeeds.
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp reviewSessionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(s.T(), resp.Present)
	require.Equal(s.T(), review.StatusReady, resp.Session.Status)
	require.Equal(s.T(), "deadbeef", resp.Session.HeadSHA)
	require.Equal(s.T(), "diff text", resp.Session.RawDiff)
	require.Equal(s.T(), "/repo/.worktrees/pr-7", resp.Session.WorktreePath)
	require.Equal(s.T(), 7, resp.Session.PR.Number)
	require.Empty(s.T(), resp.Session.Comments)
}

func (s *ReviewHandlerSuite) TestLoadSeedsGitHubComments() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	pr := &githubapi.PRInfo{Number: 7, BaseRef: "main"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("deadbeef", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7).Return("/repo/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff"), nil)
	slug := &githubapi.RepoSlug{Owner: "acme", Name: "widgets"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("FetchPRReviewComments", mock.Anything, "/repo", "", *slug, 7).Return([]githubapi.PRReviewComment{
		{ID: 1, Path: "a.go", Line: 10, Side: "RIGHT", Body: "fix me", Author: "alice", URL: "u1", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: 2, Path: "b.go", Line: 0}, // unanchored — should be dropped
		{ID: 3, Path: "c.go", Line: 5, Side: "RIGHT", Body: "stale", Outdated: true},
	}, nil)

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp reviewSessionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Session.Comments, 2)
	c0 := resp.Session.Comments[0]
	require.Equal(s.T(), "gh-1", c0.ID)
	require.Equal(s.T(), "github", c0.Source)
	require.Equal(s.T(), "alice", c0.Author)
	require.True(s.T(), c0.Pushed)
	require.False(s.T(), c0.Outdated)
	c1 := resp.Session.Comments[1]
	require.Equal(s.T(), "gh-3", c1.ID)
	require.True(s.T(), c1.Outdated)
}

func (s *ReviewHandlerSuite) TestLoadCommentFetchFailureIsNonFatal() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	pr := &githubapi.PRInfo{Number: 7, BaseRef: "main"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("deadbeef", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7).Return("/repo/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("d"), nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "n"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("FetchPRReviewComments", mock.Anything, "/repo", "", *slug, 7).
		Return(([]githubapi.PRReviewComment)(nil), errors.New("api fail"))

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp reviewSessionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(s.T(), resp.Present)
	require.Empty(s.T(), resp.Session.Comments)
}

func (s *ReviewHandlerSuite) TestLoadStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/load", bytes.NewReader([]byte(`{"pr_number":7}`))))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadReviewClientNotConfigured() {
	srv := newServerForReviewTests(s.T())
	// Don't call SetReviewService.
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/load", bytes.NewReader([]byte(`{"pr_number":7}`))))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadReviewStoreNotConfigured() {
	// Wire only client (and a worktree mock); leave store nil so the
	// pointer-nil branch fires.
	srv := newServerForReviewTests(s.T())
	srv.reviewClient = s.gh
	srv.reviewWorktree = s.wt
	// store stays nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/load", bytes.NewReader([]byte(`{"pr_number":7}`))))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadReviewWorktreeNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.reviewClient = s.gh
	srv.reviewStore = review.NewStore()
	// worktree stays nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/load", bytes.NewReader([]byte(`{"pr_number":7}`))))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadRefusedWhileRunActive() {
	// A run goroutine is in flight for ch1 — Loading a new PR over it
	// would let the background run stomp the new session on completion.
	// Reject with 409 so the user is forced to wait or close the session.
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	require.True(s.T(), s.srv.registerReviewRun("ch1"))
	s.T().Cleanup(func() { s.srv.unregisterReviewRun("ch1") })

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusConflict, w.Code)
	// No PR fetch should have happened — the guard short-circuits before
	// any gh shell-out.
	s.gh.AssertNotCalled(s.T(), "FetchPRByNumber", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ReviewHandlerSuite) TestLoadRemovesPreviousWorktreeOnOverwrite() {
	// Existing session for ch1 with a worktree on disk. Loading a new PR
	// must Remove the previous worktree first so the parent repo's
	// worktree metadata doesn't accumulate a dangling .worktrees/pr-N
	// per Load.
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 100, BaseRef: "main"},
		WorktreePath: "/repo/.worktrees/pr-100",
		Status:       review.StatusError,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.wt.On("Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-100").Return(nil).Once()
	pr := &githubapi.PRInfo{Number: 200, BaseRef: "main", State: "OPEN"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 200).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 200).Return("sha", nil)
	s.wt.On("Add", mock.Anything, "/repo", 200).Return("/repo/.worktrees/pr-200", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-200", "main", mock.Anything).Return([]byte("d"), nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 200})
	require.Equal(s.T(), http.StatusOK, w.Code)
	s.wt.AssertExpectations(s.T())
}

func (s *ReviewHandlerSuite) TestLoadPreviousWorktreeRemoveErrorIsNonFatal() {
	// Same as above but the Remove call fails — the new Load must still
	// succeed (the error is logged, not propagated).
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 100, BaseRef: "main"},
		WorktreePath: "/repo/.worktrees/pr-100",
		Status:       review.StatusError,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.wt.On("Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-100").Return(errors.New("stale ref"))
	pr := &githubapi.PRInfo{Number: 200, BaseRef: "main"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 200).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 200).Return("sha", nil)
	s.wt.On("Add", mock.Anything, "/repo", 200).Return("/repo/.worktrees/pr-200", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-200", "main", mock.Anything).Return([]byte("d"), nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 200})
	require.Equal(s.T(), http.StatusOK, w.Code)
}

func (s *ReviewHandlerSuite) TestLoadLoopDirFallback() {
	// Channel has no DirPath but loopDir is set — the handler should
	// synthesize <loopDir>/<channelID>/work.
	s.srv.loopDir = "/loop"
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	pr := &githubapi.PRInfo{Number: 7, BaseRef: "main"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/loop/ch1/work", "", 7).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/loop/ch1/work", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/loop/ch1/work", 7).Return("/loop/ch1/work/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/loop/ch1/work", "/loop/ch1/work/.worktrees/pr-7", "main", mock.Anything).Return([]byte("d"), nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/loop/ch1/work", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code)
}

// ---- sync ----

func (s *ReviewHandlerSuite) wireSyncSession() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "old",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "old-diff",
		Comments: []*review.Comment{
			{ID: "a", Path: "x.go", Line: 1, Body: "agent-emitted", Source: "agent"},
			{ID: "gh-1", Path: "y.go", Line: 2, Body: "stale gh", Source: "github"},
		},
		Status: review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
}

func (s *ReviewHandlerSuite) TestSyncStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncReviewClientNotConfigured() {
	srv := newServerForReviewTests(s.T())
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncReviewStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.reviewClient = s.gh
	srv.reviewWorktree = s.wt
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncReviewWorktreeNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.reviewClient = s.gh
	srv.reviewStore = review.NewStore()
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncNoSession() {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncSessionMissingPRRejected() {
	s.rs.Put("ch1", &review.Session{Status: review.StatusReady, WorktreePath: "/wt"})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusConflict, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncSessionBusyRejected() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		WorktreePath: "/wt",
		Status:       review.StatusReviewing,
	})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusConflict, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncChannelLookupError() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		WorktreePath: "/wt",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncChannelNotFound() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		WorktreePath: "/wt",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return((*db.Channel)(nil), nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncChannelMissingDirPath() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		WorktreePath: "/wt",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncLoopDirFallback() {
	s.srv.loopDir = "/loop"
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		WorktreePath: "/loop/ch1/work/.worktrees/pr-7",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/loop/ch1/work", "", 7).Return("new", nil)
	s.wt.On("Refresh", mock.Anything, "/loop/ch1/work", "/loop/ch1/work/.worktrees/pr-7", 7).Return(nil)
	s.wt.On("Diff", mock.Anything, "/loop/ch1/work", "/loop/ch1/work/.worktrees/pr-7", "main", mock.Anything).Return([]byte("d"), nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/loop/ch1/work", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncHeadFetchError() {
	s.wireSyncSession()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("", errors.New("api boom"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncRefreshError() {
	s.wireSyncSession()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("new", nil)
	s.wt.On("Refresh", mock.Anything, "/repo", "/repo/.worktrees/pr-7", 7).Return(errors.New("checkout failed"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestSyncDiffError() {
	s.wireSyncSession()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("new", nil)
	s.wt.On("Refresh", mock.Anything, "/repo", "/repo/.worktrees/pr-7", 7).Return(nil)
	// Comment fetch runs before Diff during Sync — short-circuit it via
	// slug failure so the diff-error branch is the one we exercise.
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return(([]byte)(nil), errors.New("diff blew up"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

// Happy path: agent comment survives, github comment is replaced with
// the fresh snapshot, head + diff are updated.
func (s *ReviewHandlerSuite) TestSyncHappyPath() {
	s.wireSyncSession()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("new-sha", nil)
	s.wt.On("Refresh", mock.Anything, "/repo", "/repo/.worktrees/pr-7", 7).Return(nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("new-diff"), nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "n"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("FetchPRReviewComments", mock.Anything, "/repo", "", *slug, 7).Return([]githubapi.PRReviewComment{
		{ID: 42, Path: "z.go", Line: 9, Side: "RIGHT", Body: "fresh", Author: "alice"},
	}, nil)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/sync", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp reviewSessionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "new-sha", resp.Session.HeadSHA)
	require.Equal(s.T(), "new-diff", resp.Session.RawDiff)
	require.Len(s.T(), resp.Session.Comments, 2)
	require.Equal(s.T(), "a", resp.Session.Comments[0].ID)
	require.Equal(s.T(), "agent", resp.Session.Comments[0].Source)
	require.Equal(s.T(), "gh-42", resp.Session.Comments[1].ID)
	require.Equal(s.T(), "github", resp.Session.Comments[1].Source)
}

// ---- get ----

func (s *ReviewHandlerSuite) TestGetReviewServiceNotConfigured() {
	srv := newServerForReviewTests(s.T())
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels/ch1/review", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestGetNoSession() {
	req := httptest.NewRequest("GET", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"present":false`)
}

func (s *ReviewHandlerSuite) TestGetWithSession() {
	s.rs.Put("ch1", &review.Session{Status: review.StatusReady, RawDiff: "diff"})
	req := httptest.NewRequest("GET", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp reviewSessionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(s.T(), resp.Present)
	require.Equal(s.T(), "diff", resp.Session.RawDiff)
}

// ---- delete ----

func (s *ReviewHandlerSuite) TestDeleteNoSession() {
	req := httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteSessionWithWorktree() {
	s.rs.Put("ch1", &review.Session{WorktreePath: "/repo/.worktrees/pr-7"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.wt.On("Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-7").Return(nil)
	req := httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), s.rs.Get("ch1"))
	s.wt.AssertCalled(s.T(), "Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-7")
}

func (s *ReviewHandlerSuite) TestDeleteSessionWorktreeRemoveErrorLogged() {
	s.rs.Put("ch1", &review.Session{WorktreePath: "/repo/.worktrees/pr-7"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.wt.On("Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-7").Return(errors.New("boom"))
	req := httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	// Worktree removal errors are logged but don't fail the delete.
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), s.rs.Get("ch1"))
}

func (s *ReviewHandlerSuite) TestDeleteSessionGetChannelError() {
	s.rs.Put("ch1", &review.Session{WorktreePath: "/repo/.worktrees/pr-7"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
	req := httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	// Even when the channel lookup fails we still drop the session so a
	// stale entry can't block reload.
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), s.rs.Get("ch1"))
	s.wt.AssertNotCalled(s.T(), "Remove", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ReviewHandlerSuite) TestDeleteSessionNoWorktreePath() {
	s.rs.Put("ch1", &review.Session{}) // no worktree to clean
	req := httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), s.rs.Get("ch1"))
}

// ---- push single comment ----

func (s *ReviewHandlerSuite) TestPushCommentNoSession() {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/abc/push", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestPushCommentMissingComment() {
	s.rs.Put("ch1", &review.Session{})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/missing/push", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestPushCommentAlreadyPushed() {
	s.rs.Put("ch1", &review.Session{})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Pushed: true})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"already":true`)
	s.gh.AssertNotCalled(s.T(), "PostPRComment", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ReviewHandlerSuite) TestPushCommentSessionNotReady() {
	// PR + HeadSHA are required to push. Empty session fails fast.
	s.rs.Put("ch1", &review.Session{})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b"})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "session not ready")
}

func (s *ReviewHandlerSuite) TestPushCommentSlugError() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Side: "RIGHT"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(nil, errors.New("slug fail"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestPushCommentPostError() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Side: "RIGHT"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "b").Return(int64(0), errors.New("api 422"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	// Pushed flag not flipped on failure.
	c, _ := s.rs.FindComment("ch1", "a")
	require.False(s.T(), c.Pushed)
}

func (s *ReviewHandlerSuite) TestPushCommentHappyPath() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Side: "RIGHT"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "b").Return(int64(555), nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.True(s.T(), c.Pushed)
	require.Equal(s.T(), int64(555), c.GitHubID)
}

func (s *ReviewHandlerSuite) TestPushCommentChannelLookupError() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "no dir_path")
}

// ---- push all ----

func (s *ReviewHandlerSuite) TestPushAllNoSession() {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/push-all", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestPushAllMixedResults() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "body-a", Side: "RIGHT"})
	s.rs.AddComment("ch1", &review.Comment{ID: "b", Path: "y.go", Line: 2, Body: "body-b", Side: "RIGHT"})
	s.rs.AddComment("ch1", &review.Comment{ID: "c", Path: "z.go", Line: 3, Body: "body-c", Side: "RIGHT", Pushed: true})

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "body-a").Return(int64(1001), nil)
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "y.go", "RIGHT", 2, "body-b").Return(int64(0), errors.New("422"))

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/push-all", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	var res pushAllResult
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(s.T(), 1, res.Pushed)
	require.Equal(s.T(), 1, res.Failed)
	require.Len(s.T(), res.Errors, 1)
	require.Contains(s.T(), res.Errors[0], "b: ")
	// Already-pushed comment c is left alone (not re-pushed, not counted).
	s.gh.AssertNotCalled(s.T(), "PostPRComment", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "z.go", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ReviewHandlerSuite) TestPushAllEmptySession() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/push-all", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"pushed":0`)
}

func (s *ReviewHandlerSuite) TestPushCommentReviewServiceNotConfigured() {
	srv := newServerForReviewTests(s.T())
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/x/push", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestPushAllReviewServiceNotConfigured() {
	srv := newServerForReviewTests(s.T())
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/push-all", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteReviewStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	// store wired but reviewStore nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteWorktreeNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.reviewStore = review.NewStore()
	// reviewWorktree stays nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestPushCommentStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/x/push", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestPushCommentReviewStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.reviewClient = s.gh
	// reviewStore stays nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/x/push", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestPushAllStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/push-all", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestPushAllReviewStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.reviewClient = s.gh
	// reviewStore stays nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/push-all", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestErrorMessageNil() {
	require.Equal(s.T(), "", errorMessage(nil))
}

// ---- delete comment ----

func (s *ReviewHandlerSuite) TestDeleteCommentNoSession() {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteCommentNotFound() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/missing", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteCommentLocalOnlyAgentCommentSkipsGitHub() {
	// Agent comment that was never pushed — GitHubID==0 → no gh shell-out,
	// just remove from the in-memory session.
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Source: "agent"})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.Nil(s.T(), c)
	s.gh.AssertNotCalled(s.T(), "DeletePRReviewComment", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ReviewHandlerSuite) TestDeleteCommentGitHubSourceCallsAPI() {
	// GH-source comment authored by the configured gh user — the gate
	// allows the call and the comment is wiped both on GH and locally.
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "alice"}, Review: config.ReviewConfig{Enabled: true}}, nil
	}
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "gh-99", Path: "x.go", Line: 1, Body: "b", Source: "github", Author: "alice", GitHubID: 99, Pushed: true})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "alice").Return(slug, nil)
	s.gh.On("DeletePRReviewComment", mock.Anything, "/repo", "alice", *slug, int64(99)).Return(nil)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/gh-99", nil))
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	c, _ := s.rs.FindComment("ch1", "gh-99")
	require.Nil(s.T(), c)
}

func (s *ReviewHandlerSuite) TestDeleteCommentGitHubSourceForeignAuthorRefused() {
	// GH-source comment authored by someone other than the configured gh
	// user — we refuse rather than calling DELETE (which GH would 403
	// anyway). The local copy must survive so a re-Sync doesn't show
	// stale state, and so the user understands why nothing happened.
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "alice"}, Review: config.ReviewConfig{Enabled: true}}, nil
	}
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "gh-99", Source: "github", Author: "bob", GitHubID: 99, Pushed: true})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/gh-99", nil))
	require.Equal(s.T(), http.StatusForbidden, w.Code)
	c, _ := s.rs.FindComment("ch1", "gh-99")
	require.NotNil(s.T(), c, "local comment must survive a refused GH delete")
	s.gh.AssertNotCalled(s.T(), "DeletePRReviewComment", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.gh.AssertNotCalled(s.T(), "FetchRepoSlug", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ReviewHandlerSuite) TestDeleteCommentGitHubSourceUnconfiguredGHUserRefused() {
	// Without a configured gh user we can't validate ownership, so we
	// refuse the GH-side delete and keep the local copy.
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "gh-99", Source: "github", Author: "alice", GitHubID: 99, Pushed: true})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/gh-99", nil))
	require.Equal(s.T(), http.StatusForbidden, w.Code)
	c, _ := s.rs.FindComment("ch1", "gh-99")
	require.NotNil(s.T(), c)
}

func (s *ReviewHandlerSuite) TestDeleteCommentChannelLookupErrorKeepsLocal() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", GitHubID: 42, Pushed: true})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.NotNil(s.T(), c, "comment should be preserved on GH-side failure")
}

func (s *ReviewHandlerSuite) TestDeleteCommentFetchRepoSlugFailure() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", GitHubID: 42, Pushed: true})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no remote"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.NotNil(s.T(), c)
}

func (s *ReviewHandlerSuite) TestDeleteCommentGitHubAPIFailureKeepsLocal() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", GitHubID: 42, Pushed: true})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("DeletePRReviewComment", mock.Anything, "/repo", "", *slug, int64(42)).Return(errors.New("403"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.NotNil(s.T(), c, "local comment must survive a GH-side failure")
}

func (s *ReviewHandlerSuite) TestDeleteCommentStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/x", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteCommentReviewStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	// store wired but reviewStore nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/x", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteCommentReviewClientNotConfigured() {
	// reviewStore wired with a session that has a GitHub-side comment, but
	// reviewClient missing — handler should refuse rather than silently
	// drop only the local copy.
	srv := newServerForReviewTests(s.T())
	srv.reviewStore = review.NewStore()
	srv.reviewStore.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}})
	srv.reviewStore.AddComment("ch1", &review.Comment{ID: "a", GitHubID: 42})
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDeleteCommentChannelDirPathMissing() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", GitHubID: 42})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.NotNil(s.T(), c)
}

// ---- list PRs ----

func (s *ReviewHandlerSuite) TestListPRsHappyPath() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	prs := []githubapi.PRInfo{
		{Number: 1, Title: "first", BaseRef: "main", HeadRef: "f1", State: "OPEN"},
		{Number: 2, Title: "second", BaseRef: "main", HeadRef: "f2", State: "OPEN", IsDraft: true},
	}
	s.gh.On("ListOpenPRs", mock.Anything, "/repo", "").Return(prs, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp struct {
		PRs []githubapi.PRInfo `json:"prs"`
	}
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(s.T(), resp.PRs, 2)
	require.Equal(s.T(), 1, resp.PRs[0].Number)
	require.True(s.T(), resp.PRs[1].IsDraft)
}

func (s *ReviewHandlerSuite) TestListPRsEmptyReturnsEmptyArray() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("ListOpenPRs", mock.Anything, "/repo", "").Return(nil, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"prs":[]`)
}

func (s *ReviewHandlerSuite) TestListPRsChannelLookupError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsNoDirPath() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsLoopDirFallback() {
	s.srv.loopDir = "/loop"
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	s.gh.On("ListOpenPRs", mock.Anything, "/loop/ch1/work", "").Return([]githubapi.PRInfo{}, nil)
	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusOK, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsRunError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("ListOpenPRs", mock.Anything, "/repo", "").Return(nil, errors.New("api fail"))
	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsGhNotInstalled() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("ListOpenPRs", mock.Anything, "/repo", "").Return(nil, githubapi.ErrGhNotInstalled)
	req := httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	srv.store = nil
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestListPRsReviewClientNotConfigured() {
	srv := newServerForReviewTests(s.T())
	// store wired by newServerForReviewTests; reviewClient stays nil.
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels/ch1/review/prs", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

// ---- run ----

// mockReviewRunner stubs the agent run. The custom runFn lets each test
// drive comment dispatch and the eventual return synchronously.
type mockReviewRunner struct {
	mu         sync.Mutex
	calls      int
	lastDir    string
	lastParent string
	lastSys    string
	lastUser   string
	runFn      func(onComment func(*review.Comment)) (*agent.AgentResponse, error)
	done       chan struct{} // closed after Run returns
}

func (m *mockReviewRunner) Run(_ context.Context, _, dirPath, parentDirPath, systemPrompt, prompt string, onComment func(*review.Comment)) (*agent.AgentResponse, error) {
	m.mu.Lock()
	m.calls++
	m.lastDir = dirPath
	m.lastParent = parentDirPath
	m.lastSys = systemPrompt
	m.lastUser = prompt
	fn := m.runFn
	done := m.done
	m.mu.Unlock()
	defer func() {
		if done != nil {
			close(done)
		}
	}()
	if fn != nil {
		return fn(onComment)
	}
	return &agent.AgentResponse{}, nil
}

func (s *ReviewHandlerSuite) waitFor(cond func() bool) {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.True(s.T(), cond(), "condition never satisfied")
}

func (s *ReviewHandlerSuite) wireReadySession() {
	s.rs.Put("ch1", &review.Session{
		PR: &githubapi.PRInfo{
			Number:  7,
			URL:     "https://github.com/o/r/pull/7",
			Title:   "Add X",
			BaseRef: "main",
			HeadRef: "feat-x",
		},
		HeadSHA:      "abc",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "diff --git a/x b/x",
		Status:       review.StatusReady,
	})
	// handleReviewRun resolves the channel's repo dir so the container
	// can mount it alongside the worktree (the worktree's .git is a
	// pointer file into the shared gitdir). resolveParentDirPath calls
	// GetChannel once; the run handler may also call it for fallback
	// when the channel isn't a worktree — `.Maybe()` covers both paths.
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	s.wireRefreshMocks("/repo", "/repo/.worktrees/pr-7", 7, "main", []byte("diff --git a/x b/x"))
}

// wireRefreshMocks attaches Maybe expectations for the refresh chain
// Run executes before kicking off the agent (FetchPRHeadSHA → Refresh →
// FetchRepoSlug-fails → Diff). diff is what Diff returns; tests that
// don't want a review.diff broadcast should pass the same bytes the
// session's RawDiff is seeded with so the refresh is a visual no-op.
// Tests exercising the refresh path itself should override these with
// explicit expectations before the handler invocation.
func (s *ReviewHandlerSuite) wireRefreshMocks(dirPath, worktreePath string, prNum int, baseRef string, diff []byte) {
	s.gh.On("FetchPRHeadSHA", mock.Anything, dirPath, mock.Anything, prNum).Return("abc", nil).Maybe()
	s.wt.On("Refresh", mock.Anything, dirPath, worktreePath, prNum).Return(nil).Maybe()
	// FetchRepoSlug returning an error short-circuits the GH comment fetch
	// inside fetchExistingReviewComments, so no FetchPRReviewComments mock
	// is needed. Best-effort behavior matches production.
	s.gh.On("FetchRepoSlug", mock.Anything, dirPath, mock.Anything).Return((*githubapi.RepoSlug)(nil), errors.New("slug skipped")).Maybe()
	s.wt.On("Diff", mock.Anything, dirPath, worktreePath, baseRef, mock.Anything).Return(diff, nil).Maybe()
}

func (s *ReviewHandlerSuite) TestRunReviewStoreNotConfigured() {
	srv := newServerForReviewTests(s.T())
	mux := srv.buildMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestRunRunnerNotConfigured() {
	s.wireReadySession()
	// No SetReviewAgent call.
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestRunNoSession() {
	s.srv.SetReviewAgent(&mockReviewRunner{}, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ReviewHandlerSuite) TestRunNoWorktree() {
	s.rs.Put("ch1", &review.Session{Status: review.StatusReady})
	s.srv.SetReviewAgent(&mockReviewRunner{}, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusConflict, w.Code)
}

func (s *ReviewHandlerSuite) TestRunSessionNotReady() {
	s.rs.Put("ch1", &review.Session{Status: review.StatusLoading, WorktreePath: "/wt"})
	s.srv.SetReviewAgent(&mockReviewRunner{}, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusConflict, w.Code)
}

func (s *ReviewHandlerSuite) TestRunHappyPathDispatchesCommentsAndStatus() {
	s.wireReadySession()
	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)

	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(onComment func(*review.Comment)) (*agent.AgentResponse, error) {
		onComment(&review.Comment{ID: "a", Path: "x.go", Line: 1, Side: "RIGHT", Body: "issue"})
		return &agent.AgentResponse{}, nil
	}
	s.srv.SetReviewAgent(runner, "sys", "review-prompt-body")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)

	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReady })

	require.Equal(s.T(), 1, runner.calls)
	require.Equal(s.T(), "/repo/.worktrees/pr-7", runner.lastDir)
	require.Equal(s.T(), "/repo", runner.lastParent)
	require.Equal(s.T(), "sys", runner.lastSys)
	require.Contains(s.T(), runner.lastUser, "review-prompt-body")
	require.Contains(s.T(), runner.lastUser, "#7")
	require.Contains(s.T(), runner.lastUser, "https://github.com/o/r/pull/7")
	require.Contains(s.T(), runner.lastUser, "Add X")
	require.Contains(s.T(), runner.lastUser, "main")
	require.Contains(s.T(), runner.lastUser, "feat-x")
	require.Contains(s.T(), runner.lastUser, "abc") // head sha
	require.Contains(s.T(), runner.lastUser, "git diff origin/main...HEAD")
	require.NotContains(s.T(), runner.lastUser, "diff --git a/x b/x")

	sess := s.rs.Get("ch1")
	require.Len(s.T(), sess.Comments, 1)
	require.Equal(s.T(), "x.go", sess.Comments[0].Path)
}

// When a gh_user is configured, the run prompt includes a switch hint so
// the agent can `gh auth switch -u <user>` before running gh commands.
func (s *ReviewHandlerSuite) TestRunPromptIncludesConfiguredGHUser() {
	s.wireReadySession()
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "alice"}, Review: config.ReviewConfig{Enabled: true}}, nil
	}
	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Contains(s.T(), runner.lastUser, "GitHub CLI account: alice")
	require.Contains(s.T(), runner.lastUser, "gh auth switch -u alice")
}

// When the session already carries comments (from a prior run or from
// the GH-seed on load), the run prompt lists them so the agent can dedup.
func (s *ReviewHandlerSuite) TestRunPromptListsExistingCommentsForDedup() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "abc",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "diff",
		Comments: []*review.Comment{
			{ID: "a", Path: "b.go", Line: 9, Side: "LEFT", Body: "issue body", Source: "agent"},
			nil, // defensive: nil entries are skipped without panicking.
		},
		Status: review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	// Refresh re-fetches GH comments before kicking off the agent — so
	// the GH side of the dedup list must come back from FetchPRReviewComments,
	// not from the seed session (which refresh discards for source=github
	// entries). The agent comment from the seed survives untouched.
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", mock.Anything, 7).Return("abc", nil).Maybe()
	s.wt.On("Refresh", mock.Anything, "/repo", "/repo/.worktrees/pr-7", 7).Return(nil).Maybe()
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", mock.Anything).Return(slug, nil).Maybe()
	s.gh.On("FetchPRReviewComments", mock.Anything, "/repo", mock.Anything, *slug, 7).Return([]githubapi.PRReviewComment{
		{ID: 1, Path: "a.go", Line: 5, Side: "RIGHT", Body: "nit body", Author: "bob"},
		// no author -> bare "github" label; empty side -> defaults to RIGHT;
		// body > 240 chars -> truncated with ellipsis.
		{ID: 2, Path: "c.go", Line: 3, Side: "", Body: strings.Repeat("x", 300)},
	}, nil).Maybe()
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff"), nil).Maybe()
	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Contains(s.T(), runner.lastUser, "do NOT re-emit")
	require.Contains(s.T(), runner.lastUser, "[github @bob] a.go:L5 (RIGHT): nit body")
	require.Contains(s.T(), runner.lastUser, "[agent] b.go:L9 (LEFT): issue body")
	// authorless github comment falls back to bare "github" label and empty side
	// defaults to RIGHT; long body is truncated with ellipsis.
	require.Contains(s.T(), runner.lastUser, "[github] c.go:L3 (RIGHT): "+strings.Repeat("x", 240)+"...")
}

func (s *ReviewHandlerSuite) TestRunAgentErrorTransitionsToErrorStatus() {
	s.wireReadySession()
	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(_ func(*review.Comment)) (*agent.AgentResponse, error) {
		return nil, errors.New("agent boom")
	}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusError })
	require.Equal(s.T(), "agent boom", s.rs.Get("ch1").Error)
}

func (s *ReviewHandlerSuite) TestRunUsesDefaultPromptWhenUnconfigured() {
	s.wireReadySession()
	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "") // empty user prompt -> default
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Contains(s.T(), runner.lastUser, defaultReviewPrompt[:50])
}

func (s *ReviewHandlerSuite) TestRunSecondCallCoalescesWhileInFlight() {
	s.wireReadySession()
	gate := make(chan struct{})
	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(_ func(*review.Comment)) (*agent.AgentResponse, error) {
		<-gate
		return &agent.AgentResponse{}, nil
	}
	s.srv.SetReviewAgent(runner, "", "")

	w1 := httptest.NewRecorder()
	s.mux.ServeHTTP(w1, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w1.Code)

	// Wait for the goroutine to enter Run (calls==1) so the in-flight
	// marker is definitely set before we fire the second call.
	s.waitFor(func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.calls == 1
	})

	w2 := httptest.NewRecorder()
	s.mux.ServeHTTP(w2, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w2.Code)
	require.Contains(s.T(), w2.Body.String(), "in_progress")
	runner.mu.Lock()
	require.Equal(s.T(), 1, runner.calls)
	runner.mu.Unlock()

	close(gate)
	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReady })
}

func (s *ReviewHandlerSuite) TestRunCommentDispatchSkippedWhenSessionDropped() {
	s.wireReadySession()
	gate := make(chan struct{})
	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(onComment func(*review.Comment)) (*agent.AgentResponse, error) {
		<-gate
		// Session was deleted in the meantime — AddComment returns false
		// and the broadcast is skipped. We just confirm no panic.
		onComment(&review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b"})
		return &agent.AgentResponse{}, nil
	}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReviewing })
	s.rs.Delete("ch1")
	close(gate)
	<-runner.done
	// Status update on a deleted session is a no-op; the unregister still happens.
}

func (s *ReviewHandlerSuite) TestBroadcastReviewStatusNoHubIsSafe() {
	s.wireReadySession()
	require.Nil(s.T(), s.srv.eventsHub)
	s.srv.broadcastReviewStatus("ch1", review.StatusReady, "")
}

// When the agent emits a comment whose path is in the diff but whose
// line falls outside every existing hunk, the handler must re-run Diff
// with the full comment list (so `-U` widens), swap the session's
// raw_diff, and broadcast review.diff so the FE re-parses.
func (s *ReviewHandlerSuite) TestRunRediffsOnCommentOutsideHunk() {
	// The seed diff covers x.go but only line 1 falls inside its hunk.
	// The agent's comment on line 42 is outside that hunk → re-diff.
	seedDiff := []byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,1 @@\n-old\n+new\n")
	s.rs.Put("ch1", &review.Session{
		PR: &githubapi.PRInfo{
			Number: 7, BaseRef: "main", HeadRef: "feat-x",
		},
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      string(seedDiff),
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", mock.Anything, 7).Return("abc", nil).Maybe()
	s.wt.On("Refresh", mock.Anything, "/repo", "/repo/.worktrees/pr-7", 7).Return(nil).Maybe()
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", mock.Anything).Return((*githubapi.RepoSlug)(nil), errors.New("slug skipped")).Maybe()

	hub := NewEventsHub(slog.Default())
	var diffEvents []Event
	var hubMu sync.Mutex
	hub.captureHook = func(e Event) {
		if e.Type == EventReviewDiff {
			hubMu.Lock()
			diffEvents = append(diffEvents, e)
			hubMu.Unlock()
		}
	}
	s.srv.SetEventsHub(hub)

	// First Diff call (refresh, before agent kicks off) returns the seed
	// bytes — raw == sess.RawDiff, so no review.diff broadcast. Second
	// Diff call (re-diff after the agent's out-of-hunk comment) returns
	// widened bytes, triggering the one broadcast we assert on.
	widened := []byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,50 +1,50 @@\n widened\n")
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return(seedDiff, nil).Once()
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return(widened, nil).Once()

	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(onComment func(*review.Comment)) (*agent.AgentResponse, error) {
		onComment(&review.Comment{ID: "c1", Path: "x.go", Line: 42, Side: "RIGHT", Body: "issue"})
		return &agent.AgentResponse{}, nil
	}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)

	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReady })

	require.Equal(s.T(), string(widened), s.rs.Get("ch1").RawDiff)
	hubMu.Lock()
	require.Len(s.T(), diffEvents, 1)
	hubMu.Unlock()
	s.wt.AssertExpectations(s.T())
}

// Comments that already land inside an existing hunk must NOT trigger
// a re-diff — that would thrash the FE for nothing on every comment.
func (s *ReviewHandlerSuite) TestRunSkipsRediffWhenCommentInsideHunk() {
	seedDiff := []byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,5 +1,5 @@\n line\n line\n-old\n+new\n line\n line\n")
	s.rs.Put("ch1", &review.Session{
		PR: &githubapi.PRInfo{
			Number: 7, BaseRef: "main", HeadRef: "feat-x",
		},
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      string(seedDiff),
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	// Refresh mocks: Diff returns the same seed so no review.diff broadcast.
	s.wireRefreshMocks("/repo", "/repo/.worktrees/pr-7", 7, "main", seedDiff)

	hub := NewEventsHub(slog.Default())
	var diffEvents []Event
	hub.captureHook = func(e Event) {
		if e.Type == EventReviewDiff {
			diffEvents = append(diffEvents, e)
		}
	}
	s.srv.SetEventsHub(hub)

	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(onComment func(*review.Comment)) (*agent.AgentResponse, error) {
		// Line 3 is inside +1,5 (covers 1..5).
		onComment(&review.Comment{ID: "c1", Path: "x.go", Line: 3, Side: "RIGHT", Body: "in-hunk"})
		return &agent.AgentResponse{}, nil
	}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReady })

	require.Empty(s.T(), diffEvents)
	// Diff was never called: mockPR has no Diff expectation; AssertExpectations
	// would catch a stray call via mock.Anything matchers if one were set up.
}

// Comments on files NOT in the diff must NOT trigger a re-diff —
// widening -U won't bring a new file into the diff; the FE shows them
// in the outside-of-diff section.
func (s *ReviewHandlerSuite) TestRunSkipsRediffWhenPathAbsentFromDiff() {
	seedDiff := []byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,1 @@\n-old\n+new\n")
	s.rs.Put("ch1", &review.Session{
		PR: &githubapi.PRInfo{
			Number: 7, BaseRef: "main", HeadRef: "feat-x",
		},
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      string(seedDiff),
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	s.wireRefreshMocks("/repo", "/repo/.worktrees/pr-7", 7, "main", seedDiff)

	hub := NewEventsHub(slog.Default())
	var diffEvents []Event
	hub.captureHook = func(e Event) {
		if e.Type == EventReviewDiff {
			diffEvents = append(diffEvents, e)
		}
	}
	s.srv.SetEventsHub(hub)

	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runFn = func(onComment func(*review.Comment)) (*agent.AgentResponse, error) {
		onComment(&review.Comment{ID: "c1", Path: "other.go", Line: 1, Side: "RIGHT", Body: "orphan"})
		return &agent.AgentResponse{}, nil
	}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReady })

	require.Empty(s.T(), diffEvents)
}

// When the channel is a worktree-thread, the real main repo (which
// holds the shared gitdir) is the *parent* channel's dir, not the
// worktree channel's own dir. The runner must see the parent dir so
// the container mounts the right location.
func (s *ReviewHandlerSuite) TestRunWorktreeThreadUsesParentChannelDir() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "abc",
		WorktreePath: "/wt/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1", DirPath: "/wt", Worktree: true, ParentID: "parent-ch"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(
		&db.Channel{ChannelID: "parent-ch", DirPath: "/main-repo"}, nil)
	// Refresh chain runs against the channel's own dir ("/wt"), not the
	// parent — the parent dir is only used to mount the shared gitdir
	// into the agent container.
	s.wireRefreshMocks("/wt", "/wt/.worktrees/pr-7", 7, "main", []byte("diff"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Equal(s.T(), "/main-repo", runner.lastParent)
}

// Root (non-worktree) channels: resolveParentDirPath returns "", and
// the handler falls back to the channel's own dir, which IS the main
// repo.
func (s *ReviewHandlerSuite) TestRunRootChannelUsesOwnDir() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "abc",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.wireRefreshMocks("/repo", "/repo/.worktrees/pr-7", 7, "main", []byte("diff"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Equal(s.T(), "/repo", runner.lastParent)
}

// When a root channel has no DirPath, the handler synthesizes one from
// loopDir — same pattern as load.
func (s *ReviewHandlerSuite) TestRunLoopDirFallbackForParent() {
	s.srv.loopDir = "/loop"
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "abc",
		WorktreePath: "/loop/ch1/work/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1"}, nil)
	s.wireRefreshMocks("/loop/ch1/work", "/loop/ch1/work/.worktrees/pr-7", 7, "main", []byte("diff"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Equal(s.T(), "/loop/ch1/work", runner.lastParent)
}

// When the worktree-thread's parent lookup fails, the handler falls
// back to the channel's own dir. Degraded but doesn't break the run.
func (s *ReviewHandlerSuite) TestRunWorktreeThreadParentLookupFailureFallsBack() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "abc",
		WorktreePath: "/wt/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1", DirPath: "/wt", Worktree: true, ParentID: "parent-ch"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(nil, errors.New("db"))
	s.wireRefreshMocks("/wt", "/wt/.worktrees/pr-7", 7, "main", []byte("diff"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Equal(s.T(), "/wt", runner.lastParent)
}

// Refresh widens / shrinks the diff: when the post-refresh raw_diff
// differs from the session's prior diff, the hub must broadcast so the
// FE re-renders. Covers the conditional broadcast branch in
// refreshReviewSession that the no-op refresh paths skip.
func (s *ReviewHandlerSuite) TestRunRefreshBroadcastsWhenDiffChanges() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main", HeadRef: "feat-x"},
		HeadSHA:      "old",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "OLD",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", mock.Anything, 7).Return("new", nil).Maybe()
	s.wt.On("Refresh", mock.Anything, "/repo", "/repo/.worktrees/pr-7", 7).Return(nil).Maybe()
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", mock.Anything).Return((*githubapi.RepoSlug)(nil), errors.New("slug skipped")).Maybe()
	// Refresh's Diff returns bytes different from sess.RawDiff → broadcast.
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("NEW"), nil).Maybe()

	hub := NewEventsHub(slog.Default())
	var diffEvents []Event
	var hubMu sync.Mutex
	hub.captureHook = func(e Event) {
		if e.Type == EventReviewDiff {
			hubMu.Lock()
			diffEvents = append(diffEvents, e)
			hubMu.Unlock()
		}
	}
	s.srv.SetEventsHub(hub)

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	s.waitFor(func() bool { return s.rs.Get("ch1").Status == review.StatusReady })

	require.Equal(s.T(), "NEW", s.rs.Get("ch1").RawDiff)
	hubMu.Lock()
	require.Len(s.T(), diffEvents, 1)
	hubMu.Unlock()
}

// Refresh chain failure: the Run handler unregisters the in-flight slot
// and returns an error response — the agent must NOT run against a
// half-prepared worktree.
func (s *ReviewHandlerSuite) TestRunRefreshFailureReturnsError() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main", HeadRef: "feat-x"},
		HeadSHA:      "abc",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", mock.Anything, 7).Return("", errors.New("gh down"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "gh down")
	// Agent must not have been called.
	require.Equal(s.T(), 0, runner.calls)
	// In-flight slot must be released so a retry isn't shut out.
	require.False(s.T(), s.srv.isReviewRunActive("ch1"))
}

// maybeRediffForComment is a guarded best-effort helper. Each guard
// (no worktree set, no session, no PR, no base-ref, ShouldRediff=false,
// Diff error, identical raw_diff) is exercised here so a regression in
// any of them shows up immediately.
func (s *ReviewHandlerSuite) TestMaybeRediffGuards() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := &review.Comment{Path: "x.go", Line: 50, Side: "RIGHT"}

	s.Run("no worktree configured", func() {
		srv := newServerForReviewTests(s.T())
		srv.logger = logger
		// reviewWorktree intentionally left nil — call is a no-op.
		srv.maybeRediffForComment("ch1", "/wt", "/repo", c)
	})

	s.Run("no session", func() {
		// reviewStore is fresh; Get returns nil.
		s.srv.maybeRediffForComment("ch1", "/wt", "/repo", c)
	})

	s.Run("session missing base ref", func() {
		s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}})
		s.srv.maybeRediffForComment("ch1", "/wt", "/repo", c)
		s.rs.Delete("ch1")
	})

	s.Run("diff error logs and returns", func() {
		// Seed a diff that puts the comment outside any hunk so
		// ShouldRediff returns true and the Diff path is reached.
		raw := "diff --git a/x.go b/x.go\n@@ -1,1 +1,1 @@\n"
		s.rs.Put("ch1", &review.Session{
			PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
			WorktreePath: "/wt",
			RawDiff:      raw,
		})
		wt := new(mockPR)
		wt.On("Diff", mock.Anything, "/repo", "/wt", "main", mock.Anything).Return([]byte(nil), errors.New("boom"))
		s.srv.SetReviewService(s.gh, s.rs, wt)
		s.srv.logger = logger
		s.srv.maybeRediffForComment("ch1", "/wt", "/repo", c)
		require.Equal(s.T(), raw, s.rs.Get("ch1").RawDiff)
		wt.AssertExpectations(s.T())
		s.rs.Delete("ch1")
	})

	s.Run("diff identical → no broadcast", func() {
		raw := "diff --git a/x.go b/x.go\n@@ -1,1 +1,1 @@\n"
		s.rs.Put("ch1", &review.Session{
			PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
			WorktreePath: "/wt",
			RawDiff:      raw,
		})
		wt := new(mockPR)
		wt.On("Diff", mock.Anything, "/repo", "/wt", "main", mock.Anything).Return([]byte(raw), nil)
		s.srv.SetReviewService(s.gh, s.rs, wt)
		hub := NewEventsHub(logger)
		var events []Event
		hub.captureHook = func(e Event) { events = append(events, e) }
		s.srv.SetEventsHub(hub)
		s.srv.maybeRediffForComment("ch1", "/wt", "/repo", c)
		require.Empty(s.T(), events)
		s.rs.Delete("ch1")
	})
}

// buildReviewContext must tolerate nil entries in the comment slice —
// they can show up after a partial cleanup and shouldn't panic the
// prompt assembly.
func (s *ReviewHandlerSuite) TestBuildReviewContextSkipsNilComment() {
	sess := &review.Session{
		PR: &githubapi.PRInfo{Number: 7, BaseRef: "main", HeadRef: "feat-x"},
		Comments: []*review.Comment{
			nil,
			{ID: "c1", Path: "x.go", Line: 1, Side: "RIGHT", Body: "real"},
		},
	}
	ctx := buildReviewContext(sess, "alice")
	require.Contains(s.T(), ctx, "real")
	require.Contains(s.T(), ctx, "gh auth switch -u alice")
}

// GetChannel error on both lookups leaves channelDirPath as "" — the
// run now refuses to proceed because the refresh chain has no dir to
// fetch into. 400 here is preferable to silently starting the agent
// container with no parent mount (which would fail with "no result
// event found" downstream).
func (s *ReviewHandlerSuite) TestRunRejectedWhenGetChannelFails() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7, BaseRef: "main"},
		HeadSHA:      "abc",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "no dir_path")
	// Runner never gets invoked — no need to wait on runner.done.
	require.Equal(s.T(), 0, runner.calls)
}
