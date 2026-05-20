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

func (m *mockGitHubReview) PostPRComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int, commitID, path, side string, line int, body string) error {
	args := m.Called(ctx, workdir, ghUser, slug, prNum, commitID, path, side, line, body)
	return args.Error(0)
}

func (m *mockGitHubReview) FetchPRReviewComments(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int) ([]githubapi.PRReviewComment, error) {
	args := m.Called(ctx, workdir, ghUser, slug, prNum)
	cs, _ := args.Get(0).([]githubapi.PRReviewComment)
	return cs, args.Error(1)
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

func (m *mockPR) Diff(ctx context.Context, parentDir, worktreePath, baseRef string) ([]byte, error) {
	args := m.Called(ctx, parentDir, worktreePath, baseRef)
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
	// Stub config loaders so resolveGHUser is deterministic in tests
	// (otherwise it shells out to the user's actual ~/.loop/config.json).
	s.srv.loadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
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
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main").Return(nil, errors.New("diff failed"))
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
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main").Return([]byte("diff text"), nil)
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
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main").Return([]byte("diff"), nil)
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
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main").Return([]byte("d"), nil)
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

func (s *ReviewHandlerSuite) TestLoadLoopDirFallback() {
	// Channel has no DirPath but loopDir is set — the handler should
	// synthesize <loopDir>/<channelID>/work.
	s.srv.loopDir = "/loop"
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	pr := &githubapi.PRInfo{Number: 7, BaseRef: "main"}
	s.gh.On("FetchPRByNumber", mock.Anything, "/loop/ch1/work", "", 7).Return(pr, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/loop/ch1/work", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/loop/ch1/work", 7).Return("/loop/ch1/work/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/loop/ch1/work", "/loop/ch1/work/.worktrees/pr-7", "main").Return([]byte("d"), nil)
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
	s.wt.On("Diff", mock.Anything, "/loop/ch1/work", "/loop/ch1/work/.worktrees/pr-7", "main").Return([]byte("d"), nil)
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
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main").Return(([]byte)(nil), errors.New("diff blew up"))
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
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main").Return([]byte("new-diff"), nil)
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
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "b").Return(errors.New("api 422"))
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
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "b").Return(nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.True(s.T(), c.Pushed)
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
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "body-a").Return(nil)
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "y.go", "RIGHT", 2, "body-b").Return(errors.New("422"))

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
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "alice"}}, nil
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
			{ID: "gh-1", Path: "a.go", Line: 5, Side: "RIGHT", Body: "nit body", Source: "github", Author: "bob"},
			{ID: "a", Path: "b.go", Line: 9, Side: "LEFT", Body: "issue body", Source: "agent"},
			// no author -> bare "github" label; empty side -> defaults to RIGHT;
			// body > 240 chars -> truncated with ellipsis.
			{ID: "gh-2", Path: "c.go", Line: 3, Side: "", Body: strings.Repeat("x", 300), Source: "github"},
			nil, // defensive: nil entries are skipped without panicking.
		},
		Status: review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
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

// When the channel is a worktree-thread, the real main repo (which
// holds the shared gitdir) is the *parent* channel's dir, not the
// worktree channel's own dir. The runner must see the parent dir so
// the container mounts the right location.
func (s *ReviewHandlerSuite) TestRunWorktreeThreadUsesParentChannelDir() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7},
		HeadSHA:      "abc",
		WorktreePath: "/wt/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1", DirPath: "/wt", Worktree: true, ParentID: "parent-ch"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(
		&db.Channel{ChannelID: "parent-ch", DirPath: "/main-repo"}, nil)

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
		PR:           &githubapi.PRInfo{Number: 7},
		HeadSHA:      "abc",
		WorktreePath: "/repo/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)

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
		PR:           &githubapi.PRInfo{Number: 7},
		HeadSHA:      "abc",
		WorktreePath: "/loop/ch1/work/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1"}, nil)

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
		PR:           &githubapi.PRInfo{Number: 7},
		HeadSHA:      "abc",
		WorktreePath: "/wt/.worktrees/pr-7",
		RawDiff:      "diff",
		Status:       review.StatusReady,
	})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(
		&db.Channel{ChannelID: "ch1", DirPath: "/wt", Worktree: true, ParentID: "parent-ch"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(nil, errors.New("db"))

	runner := &mockReviewRunner{done: make(chan struct{})}
	s.srv.SetReviewAgent(runner, "", "")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Equal(s.T(), "/wt", runner.lastParent)
}

// GetChannel error on both lookups leaves parentDirPath as "" — the
// run still proceeds, but the agent container is missing the parent
// mount (degraded; same as before this fix).
func (s *ReviewHandlerSuite) TestRunNoParentWhenGetChannelFails() {
	s.rs.Put("ch1", &review.Session{
		PR:           &githubapi.PRInfo{Number: 7},
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
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	require.Equal(s.T(), "", runner.lastParent)
}
