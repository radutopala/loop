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
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

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

func (m *mockGitHubReview) PostPRComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int, commitID, path, side string, line int, body string) error {
	args := m.Called(ctx, workdir, ghUser, slug, prNum, commitID, path, side, line, body)
	return args.Error(0)
}

// mockPRWorktree is a testify mock for review.PRWorktree.
type mockPRWorktree struct {
	mock.Mock
}

func (m *mockPRWorktree) Add(ctx context.Context, parentDir string, prNum int) (string, error) {
	args := m.Called(ctx, parentDir, prNum)
	return args.String(0), args.Error(1)
}

func (m *mockPRWorktree) Diff(ctx context.Context, parentDir, worktreePath, baseRef string) ([]byte, error) {
	args := m.Called(ctx, parentDir, worktreePath, baseRef)
	b, _ := args.Get(0).([]byte)
	return b, args.Error(1)
}

func (m *mockPRWorktree) Remove(ctx context.Context, parentDir, worktreePath string) error {
	args := m.Called(ctx, parentDir, worktreePath)
	return args.Error(0)
}

type ReviewHandlerSuite struct {
	suite.Suite
	srv   *Server
	store *MockChannelLister
	gh    *mockGitHubReview
	wt    *mockPRWorktree
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
	s.wt = new(mockPRWorktree)
	s.rs = review.NewStore()
	s.srv.SetReviewService(s.gh, s.rs, s.wt)
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
	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code)
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
