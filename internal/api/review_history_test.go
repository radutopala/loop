package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/testutil"
)

// withHistory gives the server a mocked review history and the ready
// session ch1 its key.
func (s *ReviewHandlerSuite) withHistory() *testutil.MockStore {
	ms := new(testutil.MockStore)
	s.srv.review.history = ms
	if sess := s.rs.Get("ch1"); sess != nil {
		sess.HistoryDir = "/repo"
		s.rs.Put("ch1", sess)
	}
	return ms
}

func runMatches(scope review.Scope, from string) any {
	return mock.MatchedBy(func(r *db.ReviewRun) bool {
		return r.DirPath == "/repo" && r.PRNumber == 7 && r.BaseRef == "main" && r.ChannelID == "ch1" &&
			r.HeadSHA == "abc" && r.Scope == string(scope) && r.FromSHA == from
	})
}

func (s *ReviewHandlerSuite) TestWithReviewHistory() {
	ms := new(testutil.MockStore)
	srv := NewServer(nil, nil, nil, new(MockChannelLister), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), WithReviewHistory(ms))
	require.Same(s.T(), ms, srv.review.history)
}

// An incremental run names its range and the PR's agent findings, records
// the run, the findings it reports against it, how it ended, and what the
// dedup pass after it folded.
func (s *ReviewHandlerSuite) TestRunRecordsIncrementalHistory() {
	s.wireDedupSession()
	s.rs.AddComment("ch1", &review.Comment{ID: "gh-9", Path: "z.go", Line: 2, Body: "Theirs.", Source: "github", GitHubID: 9})
	ms := s.withHistory()
	ms.On("LastReviewedHead", mock.Anything, "/repo", 7).Return("old", nil)
	s.wt.onScope("/repo/.worktrees/pr-7", "main", review.ScopeAuto, "old").
		Return(review.ScopeResult{Scope: review.ScopeIncremental, FromSHA: "old", Rebased: true}, nil)
	ms.On("CreateReviewRun", mock.Anything, runMatches(review.ScopeIncremental, "old")).Return(int64(42), nil)
	fresh := review.NewComment("x.go", 12, "", "Writing to the map before make panics.")
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == fresh.ID && f.RunID == 42 && f.DirPath == "/repo" && f.PRNumber == 7 && f.Line == 12
	})).Return(nil)
	ms.On("FinishReviewRun", mock.Anything, int64(42), db.ReviewRunCompleted, "", "", "").Return(nil)
	ms.On("DeleteReviewFinding", mock.Anything, "/repo", 7, fresh.ID, db.ReviewFindingDedupFolded, "a").Return(nil)
	runner := dedupAfterRunRunner(fresh, func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{Response: `{"clusters":[{"keep":"a","drop":["` + fresh.ID + `"]}]}`}, nil
	})
	s.srv.review.setAgent(runner, "", "")
	s.srv.review.newReviewID = func() string { return "rev-1" }

	s.postRunAndWait()

	sys := runner.reqs[0].SystemPrompt
	require.Contains(s.T(), sys, "Scope: incremental. An earlier review covered this PR up to old.")
	require.Contains(s.T(), sys, "`git diff old HEAD -- $(git diff --name-only origin/main...HEAD)`")
	require.Contains(s.T(), sys, "rebased since that review, so the range can also show changes that came from main")
	require.Contains(s.T(), sys, "Findings already reported on this PR; don't report them again:\n- x.go:10 Nil map write panics on the first insert.\n")
	require.NotContains(s.T(), sys, "Theirs.")
	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.ScopeIncremental, sess.LastRunScope)
	require.Equal(s.T(), "old", sess.LastRunFromSHA)
	require.Equal(s.T(), "/repo", sess.HistoryDir)
	// A run outside a review starts one of its own.
	require.Equal(s.T(), "rev-1", sess.ReviewID)
	require.Equal(s.T(), []int64{42}, sess.ReviewRuns)
	require.Zero(s.T(), s.srv.review.historyRunID("ch1"))
	ms.AssertExpectations(s.T())
}

// A failed run is recorded with its error, its session and model; the
// history's own failures are only logged.
func (s *ReviewHandlerSuite) TestRunRecordsFailure() {
	s.wireReadySession()
	ms := s.withHistory()
	ms.On("LastReviewedHead", mock.Anything, "/repo", 7).Return("", errors.New("db down"))
	s.wt.onScope("/repo/.worktrees/pr-7", "main", review.ScopeAuto, "").Return(review.ScopeResult{Scope: review.ScopeFull}, nil)
	ms.On("CreateReviewRun", mock.Anything, runMatches(review.ScopeFull, "")).Return(int64(7), nil)
	ms.On("FinishReviewRun", mock.Anything, int64(7), db.ReviewRunFailed, "sess-1", "opus", "agent boom").Return(errors.New("db down"))
	runner := &mockReviewRunner{runFn: func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{SessionID: "sess-1", Model: "opus"}, errors.New("agent boom")
	}}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Equal(s.T(), review.StatusError, s.rs.Get("ch1").Status)
	require.NotContains(s.T(), runner.lastSys, "Scope: incremental")
	ms.AssertExpectations(s.T())
}

// A run the history couldn't record goes ahead, and its end isn't recorded.
func (s *ReviewHandlerSuite) TestRunWithoutRecordedRun() {
	s.wireReadySession()
	ms := s.withHistory()
	ms.On("LastReviewedHead", mock.Anything, "/repo", 7).Return("", nil)
	ms.On("CreateReviewRun", mock.Anything, mock.Anything).Return(int64(0), errors.New("db down"))
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
	ms.AssertNotCalled(s.T(), "FinishReviewRun", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// The transcript a run attaches to its finding is recorded too, so a
// restored session still points Discuss at the pass that reported it.
func (s *ReviewHandlerSuite) TestRunRecordsFindingTranscript() {
	s.wireReadySession()
	s.withHome()
	ms := s.withHistory()
	ms.On("LastReviewedHead", mock.Anything, "/repo", 7).Return("", nil)
	s.wt.onScope("/repo/.worktrees/pr-7", "main", review.ScopeAuto, "").Return(review.ScopeResult{Scope: review.ScopeFull}, nil)
	// The run joins the review a Run started.
	s.rs.StartReview("ch1", "rev-0", "")
	s.rs.AddReviewRun("ch1", 4)
	ms.On("CreateReviewRun", mock.Anything, mock.MatchedBy(func(r *db.ReviewRun) bool {
		return r.ReviewID == "rev-0" && r.Scope == string(review.ScopeFull)
	})).Return(int64(5), nil)
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == "f-1" && f.RunID == 5 && len(f.Transcripts) == 0
	})).Return(nil).Once()
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == "f-1" && slices.Equal(f.Transcripts, []string{runTranscript("run-1")})
	})).Return(nil).Once()
	ms.On("FinishReviewRun", mock.Anything, int64(5), db.ReviewRunCompleted, "run-1", "", "").Return(nil)
	runner := &mockReviewRunner{findings: []*review.Comment{{ID: "f-1", Path: "x.go", Line: 3, Body: "bug", Source: "agent"}}}
	runner.runFn = func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{SessionID: "run-1"}, nil
	}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	ms.AssertExpectations(s.T())
}

func (s *ReviewHandlerSuite) TestRunRecordsCancel() {
	s.wireReadySession()
	ms := s.withHistory()
	ms.On("LastReviewedHead", mock.Anything, "/repo", 7).Return("", nil)
	ms.On("CreateReviewRun", mock.Anything, mock.Anything).Return(int64(3), nil)
	ms.On("FinishReviewRun", mock.Anything, int64(3), db.ReviewRunCancelled, "", "", "").Return(nil)
	runner := &mockReviewRunner{done: make(chan struct{})}
	runner.runWithCtxFn = func(ctx context.Context) (*agent.AgentResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.srv.review.setAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	w = httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/stop", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	<-runner.done
	s.waitFor(func() bool { return !s.srv.review.isReviewRunActive("ch1") })
	ms.AssertExpectations(s.T())
}

// The run body's scope overrides the session's for that run; one the
// worktree can't resolve falls back to the whole PR.
func (s *ReviewHandlerSuite) TestRunScopeOverride() {
	s.wireReadySession()
	s.rs.UpdateScope("ch1", review.ScopeFull)
	s.wt.onScope("/repo/.worktrees/pr-7", "main", review.ScopeIncremental, "").
		Return(review.ScopeResult{}, errors.New("git rev-parse HEAD: bad"))
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	w := s.doRaw("POST", "/api/channels/ch1/review/run", []byte(`{"scope":"incremental"}`))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	s.waitFor(func() bool { return !s.srv.review.isReviewRunActive("ch1") })

	require.NotContains(s.T(), runner.lastSys, "Scope: incremental")
	require.Equal(s.T(), review.ScopeFull, s.rs.Get("ch1").LastRunScope)
	s.wt.AssertExpectations(s.T())
}

func (s *ReviewHandlerSuite) TestRunRejectsUnknownScope() {
	s.wireReadySession()
	s.srv.review.setAgent(&mockReviewRunner{}, "", "")
	w := s.doRaw("POST", "/api/channels/ch1/review/run", []byte(`{"scope":"partial"}`))
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid scope")
}

// The findings an incremental run lists are capped.
func (s *ReviewHandlerSuite) TestIncrementalScopeCapsFindings() {
	sess := &review.Session{}
	for i := range reviewScopeFindings + 2 {
		sess.Comments = append(sess.Comments, &review.Comment{Path: "a.go", Line: i + 1, Body: fmt.Sprintf("issue %d\n\ndetail", i), Source: "agent"})
	}
	got := buildReviewContext(sess, "", review.ScopeResult{Scope: review.ScopeIncremental, FromSHA: "old"})
	require.Contains(s.T(), got, "origin/...HEAD")
	require.Contains(s.T(), got, "- a.go:50 issue 49\n- and 2 more\n")
	require.NotContains(s.T(), got, "issue 50")
	require.NotContains(s.T(), got, "detail")
	require.NotContains(s.T(), got, "rebased")

	none := buildReviewContext(&review.Session{}, "", review.ScopeResult{Scope: review.ScopeIncremental, FromSHA: "old"})
	require.NotContains(s.T(), none, "Findings already reported")
}

// Loading a PR brings its recorded agent findings back, merged with their
// GitHub copies, and records the outcomes GitHub changed since.
func (s *ReviewHandlerSuite) TestLoadRestoresFindings() {
	pushedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7, mock.Anything).Return("/repo/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff"), nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("FetchPRReviewComments", mock.Anything, "/repo", "", *slug, 7).Return([]githubapi.PRReviewComment{
		{ID: 5, Path: "a.go", Line: 3, Body: "Leak.", URL: "u5", Resolved: true},
		{ID: 9, Path: "b.go", Line: 1, Body: "Theirs."},
	}, nil)
	ms := s.withHistory()
	ms.On("ListReviewFindings", mock.Anything, "/repo", 7).Return([]*db.ReviewFinding{
		{ID: "a", RunID: 4, Path: "a.go", Line: 3, Side: "RIGHT", Body: "Leak.", Category: "bug", GitHubID: 5, PushedAt: pushedAt, Transcripts: []string{"/t/s1.jsonl"}},
		{ID: "b", Path: "c.go", Line: 8, Body: "Stale.", Verdict: review.VerdictReal, VerdictReason: "checked", Outcome: "outdated"},
	}, nil)
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == "a" && f.Outcome == "resolved" && f.URL == "u5" && f.PushedAt.Equal(pushedAt) && f.RunID == 4 && slices.Equal(f.Transcripts, []string{"/t/s1.jsonl"})
	})).Return(nil)
	// The channel's last run gives the last scope, and its review the
	// review's runs; another channel's runs are its own. A run recorded
	// without a review is one of its own.
	ms.On("ListReviewRuns", mock.Anything, "/repo", 7).Return([]*db.ReviewRun{
		{ID: 1, ChannelID: "ch1", SessionID: "s1", Scope: "full"},
		{ID: 2, ReviewID: "r0", ChannelID: "ch1", SessionID: "s1", Scope: "full"},
		{ID: 3, ReviewID: "r1", ChannelID: "ch2", SessionID: "other", Scope: "full"},
		{ID: 4, ReviewID: "r1", ChannelID: "ch1", SessionID: "s1", Scope: "full"},
		{ID: 5, ReviewID: "r1", ChannelID: "ch1", Scope: "incremental", FromSHA: "aaa"},
		{ID: 6, ChannelID: "ch2", Scope: "full"},
	}, nil)

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())

	sess := s.rs.Get("ch1")
	require.Equal(s.T(), "/repo", sess.HistoryDir)
	require.Equal(s.T(), review.ScopeIncremental, sess.LastRunScope)
	require.Equal(s.T(), "aaa", sess.LastRunFromSHA)
	require.Equal(s.T(), "r1", sess.ReviewID)
	require.Equal(s.T(), []int64{4, 5}, sess.ReviewRuns)
	require.Len(s.T(), sess.Comments, 3)
	a, b, gh := sess.Comments[0], sess.Comments[1], sess.Comments[2]
	require.Equal(s.T(), "a", a.ID)
	require.Equal(s.T(), int64(4), a.RunID)
	require.True(s.T(), a.Pushed)
	require.True(s.T(), a.Resolved)
	require.Equal(s.T(), "bug", a.Category)
	require.Equal(s.T(), []string{"/t/s1.jsonl"}, a.Transcripts)
	require.Equal(s.T(), "b", b.ID)
	require.False(s.T(), b.Pushed)
	require.True(s.T(), b.Outdated)
	require.Equal(s.T(), review.VerdictReal, b.Verdict)
	require.Equal(s.T(), "gh-9", gh.ID)
	ms.AssertExpectations(s.T())
}

func (s *ReviewHandlerSuite) TestLoadRestoreFailureLoadsWithout() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7, mock.Anything).Return("/repo/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff"), nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	ms := s.withHistory()
	ms.On("ListReviewFindings", mock.Anything, "/repo", 7).Return(nil, errors.New("db down"))
	ms.On("ListReviewRuns", mock.Anything, "/repo", 7).Return(nil, errors.New("db down"))

	w := s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Empty(s.T(), s.rs.Get("ch1").Comments)
	require.Empty(s.T(), s.rs.Get("ch1").LastRunScope)
}

func (s *ReviewHandlerSuite) TestPushRecordsFinding() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc", HistoryDir: "/repo"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Side: "RIGHT", Source: "agent"})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil)
	s.gh.On("PostPRComment", mock.Anything, "/repo", "", *slug, 7, "abc", "x.go", "RIGHT", 1, "b").Return(int64(555), nil)
	ms := s.withHistory()
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == "a" && f.GitHubID == 555 && !f.PushedAt.IsZero() && f.RunID == 0
	})).Return(nil)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/push", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	ms.AssertExpectations(s.T())
}

// Edits and verdicts are recorded; a failure to is only logged.
func (s *ReviewHandlerSuite) TestEditRecordsFinding() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc", HistoryDir: "/repo"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 3, Body: "Race.", Source: "agent"})
	ms := s.withHistory()
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == "a" && f.Body == "Race, rarely."
	})).Return(errors.New("db down")).Once()
	ms.On("SaveReviewFinding", mock.Anything, mock.MatchedBy(func(f *db.ReviewFinding) bool {
		return f.ID == "a" && f.Verdict == review.VerdictFalsePositive && f.VerdictReason == "one goroutine"
	})).Return(nil).Once()

	for _, body := range []string{`{"body":"Race, rarely."}`, `{"verdict":"false_positive","verdict_reason":"one goroutine"}`} {
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, agentRequest("PATCH", "/api/channels/ch1/review/comments/a", strings.NewReader(body)))
		require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())
	}
	ms.AssertExpectations(s.T())
}

func (s *ReviewHandlerSuite) TestDeleteRecordsFinding() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc", HistoryDir: "/repo"})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 1, Body: "b", Source: "agent"})
	ms := s.withHistory()
	ms.On("DeleteReviewFinding", mock.Anything, "/repo", 7, "a", db.ReviewFindingUserDeleted, "").Return(errors.New("db down"))

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review/comments/a", nil))
	require.Equal(s.T(), http.StatusNoContent, w.Code)
	c, _ := s.rs.FindComment("ch1", "a")
	require.Nil(s.T(), c)
	ms.AssertExpectations(s.T())
}

// GitHub comments, and sessions without a history key, record nothing.
func (s *ReviewHandlerSuite) TestHistorySkips() {
	rs := s.srv.review
	require.Zero(s.T(), rs.lastReviewedHead(context.Background(), &review.Session{PR: &githubapi.PRInfo{Number: 7}, HistoryDir: "/repo"}))
	require.Nil(s.T(), rs.restoreFindings(context.Background(), "/repo", 7))

	ms := s.withHistory()
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HistoryDir: "/repo"})
	s.rs.Put("nodir", &review.Session{PR: &githubapi.PRInfo{Number: 7}})
	s.rs.Put("nopr", &review.Session{HistoryDir: "/repo"})
	gh := &review.Comment{ID: "gh-1", Source: "github"}
	agentC := &review.Comment{ID: "a", Source: "agent"}

	rs.saveFinding("ch1", nil)
	rs.saveFinding("ch1", gh)
	rs.deleteFinding("ch1", gh, db.ReviewFindingUserDeleted, "")
	for _, ch := range []string{"missing", "nodir", "nopr"} {
		rs.saveFinding(ch, agentC)
		rs.deleteFinding(ch, agentC, db.ReviewFindingUserDeleted, "")
	}
	rs.startHistoryRun(context.Background(), "nodir", s.rs.Get("nodir"), review.ScopeResult{})
	rs.finishHistoryRun("nodir", db.ReviewRunCompleted, "", "", "")
	require.Nil(s.T(), rs.restoreFindings(context.Background(), "", 7))
	require.Empty(s.T(), ms.Calls)
}

func (s *ReviewHandlerSuite) TestPruneHistory() {
	s.srv.review.pruneHistory(context.Background()) // no history: nothing to prune

	cases := []struct {
		name string
		n    int64
		err  error
	}{
		{name: "pruned", n: 2},
		{name: "nothing", n: 0},
		{name: "fails", err: errors.New("db down")},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			ms := s.withHistory()
			ms.On("PruneReviewHistory", mock.Anything, mock.MatchedBy(func(before time.Time) bool {
				age := time.Since(before)
				return age >= reviewHistoryKeep && age < reviewHistoryKeep+time.Minute
			})).Return(tc.n, tc.err).Once()
			s.srv.review.pruneHistory(context.Background())
			ms.AssertExpectations(s.T())
		})
	}
}

// The prune loop prunes at once, then on every tick, until ctx is done.
func (s *ReviewHandlerSuite) TestRunReviewHistoryPrune() {
	ms := s.withHistory()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	ms.On("PruneReviewHistory", mock.Anything, mock.Anything).Return(int64(0), nil).Run(func(mock.Arguments) {
		calls++
		if calls == 2 {
			cancel()
		}
	})
	s.srv.RunReviewHistoryPrune(ctx, time.Millisecond)
	require.Equal(s.T(), 2, calls)
}

func (s *ReviewHandlerSuite) TestSetScope() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}})
	cases := []struct {
		name  string
		path  string
		body  string
		code  int
		scope review.Scope
	}{
		{name: "bad json", path: "ch1", body: `{`, code: http.StatusBadRequest},
		{name: "unknown scope", path: "ch1", body: `{"scope":"partial"}`, code: http.StatusBadRequest},
		{name: "no session", path: "ch2", body: `{"scope":"full"}`, code: http.StatusNotFound},
		{name: "incremental", path: "ch1", body: `{"scope":"incremental"}`, code: http.StatusOK, scope: review.ScopeIncremental},
		{name: "auto", path: "ch1", body: `{"scope":""}`, code: http.StatusOK, scope: review.ScopeAuto},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			w := s.doRaw("PUT", "/api/channels/"+tc.path+"/review/scope", []byte(tc.body))
			require.Equal(s.T(), tc.code, w.Code, w.Body.String())
			if tc.code != http.StatusOK {
				return
			}
			var resp reviewSessionResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), tc.scope, resp.Session.Scope)
			require.Equal(s.T(), tc.scope, s.rs.Get("ch1").Scope)
		})
	}

	w := httptest.NewRecorder()
	newServerForReviewTests(s.T()).buildMux().ServeHTTP(w, httptest.NewRequest("PUT", "/api/channels/ch1/review/scope", strings.NewReader(`{}`)))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestStats() {
	w := s.doRaw("GET", "/api/channels/ch1/review/stats", nil)
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)

	ms := s.withHistory()
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch2").Return(nil, errors.New("gone"))
	within := func(pr, days int) any {
		return mock.MatchedBy(func(f db.ReviewStatsFilter) bool {
			age := time.Since(f.Since)
			want := time.Duration(days) * 24 * time.Hour
			return f.DirPath == "/repo" && f.PRNumber == pr && age >= want-time.Hour && age < want+time.Hour
		})
	}
	ms.On("ReviewStats", mock.Anything, within(7, 30)).Return(&db.ReviewStats{Runs: db.ReviewRunStats{Total: 3}}, nil)
	ms.On("ReviewStats", mock.Anything, within(0, reviewStatsDays)).Return(nil, errors.New("db down"))

	cases := []struct {
		name string
		path string
		code int
		body string
	}{
		{name: "bad pr", path: "ch1/review/stats?pr=x", code: http.StatusBadRequest, body: "pr must be a positive number"},
		{name: "bad days", path: "ch1/review/stats?days=0", code: http.StatusBadRequest, body: "days must be a positive number"},
		{name: "no dir", path: "ch2/review/stats", code: http.StatusNotFound, body: "no dir_path"},
		{name: "fails", path: "ch1/review/stats", code: http.StatusInternalServerError, body: "db down"},
		{name: "ok", path: "ch1/review/stats?pr=7&days=30", code: http.StatusOK, body: `"total":3`},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			w := s.doRaw("GET", "/api/channels/"+tc.path, nil)
			require.Equal(s.T(), tc.code, w.Code)
			require.Contains(s.T(), w.Body.String(), tc.body)
		})
	}
}

// Without the review backends or a history there is nothing to restore
// into, and the store keeps no saver.
func (s *ReviewHandlerSuite) TestRestoreReviewSessionsNotConfigured() {
	s.srv.RestoreReviewSessions(context.Background())
	ms := new(testutil.MockStore)
	s.srv.review.history = ms
	s.srv.review.client = nil
	s.srv.RestoreReviewSessions(context.Background())
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}})
	ms.AssertExpectations(s.T())
}

// A failed listing restores nothing, but the sessions are kept from then
// on; a failed save is only logged.
func (s *ReviewHandlerSuite) TestRestoreReviewSessionsListFails() {
	ms := s.withHistory()
	ms.On("ListReviewSessions", mock.Anything).Return(nil, errors.New("db down"))
	ms.On("SaveReviewSession", mock.Anything, &db.ReviewSession{ChannelID: "ch1", PRNumber: 7, Model: "opus"}).Return(errors.New("db down"))

	s.srv.RestoreReviewSessions(context.Background())
	s.rs.Put("ch1", &review.Session{ChannelID: "ch1", PR: &githubapi.PRInfo{Number: 7}, Model: "opus"})
	ms.AssertExpectations(s.T())
}

// Each kept session loads its PR again with its choices. One loaded since
// the start is left alone, one whose channel is gone is dropped, and one
// that fails otherwise is kept for the next start.
func (s *ReviewHandlerSuite) TestRestoreReviewSessions() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.store.On("GetChannel", mock.Anything, "gone").Return((*db.Channel)(nil), nil)
	s.store.On("GetChannel", mock.Anything, "broken").Return(&db.Channel{ChannelID: "broken", DirPath: "/b"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/b", "", 5).Return((*githubapi.PRInfo)(nil), errors.New("gh down"))
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	s.wt.On("Add", mock.Anything, "/repo", 7, "ch1").Return("/repo/.worktrees/pr-7", nil)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff"), nil)
	ms := s.withHistory()
	ms.On("ListReviewSessions", mock.Anything).Return([]*db.ReviewSession{
		{ChannelID: "busy", PRNumber: 3},
		{ChannelID: "gone", PRNumber: 4},
		{ChannelID: "broken", PRNumber: 5},
		{ChannelID: "ch1", PRNumber: 7, ForkMode: "custom", ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: "full"},
	}, nil)
	ms.On("ListReviewFindings", mock.Anything, "/repo", 7).Return(nil, nil)
	ms.On("ListReviewRuns", mock.Anything, "/repo", 7).Return(nil, nil)
	ms.On("DeleteReviewSession", mock.Anything, "gone").Return(errors.New("db down"))
	ms.On("SaveReviewSession", mock.Anything, &db.ReviewSession{ChannelID: "ch1", PRNumber: 7, ForkMode: "custom", ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: "full"}).Return(nil)
	s.rs.Put("busy", &review.Session{Status: review.StatusLoading})

	s.srv.RestoreReviewSessions(context.Background())

	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.StatusReady, sess.Status)
	require.Equal(s.T(), review.ForkCustom, sess.ForkMode)
	require.Equal(s.T(), "s0", sess.ForkSessionID)
	require.Equal(s.T(), "opus", sess.Model)
	require.Equal(s.T(), "high", sess.Effort)
	require.Equal(s.T(), review.ScopeFull, sess.Scope)
	require.Nil(s.T(), s.rs.Get("gone"))
	require.Equal(s.T(), review.StatusError, s.rs.Get("broken").Status)
	require.Equal(s.T(), review.StatusLoading, s.rs.Get("busy").Status)
	ms.AssertExpectations(s.T())
}
