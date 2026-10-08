package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
)

func (s *ReviewHandlerSuite) postDedup() *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/dedup", nil))
	return w
}

// wireDedupSession is wireReadySession plus comments: three agent findings
// on x.go (the last one pushed), and one on y.go.
func (s *ReviewHandlerSuite) wireDedupSession() {
	s.wireReadySession()
	for _, c := range []*review.Comment{
		{ID: "a", Path: "x.go", Line: 10, Body: "Nil map write panics on the first insert.", Source: "agent"},
		{ID: "b", Path: "x.go", Line: 14, Body: "Assigning into an uninitialised map crashes.", Source: "agent"},
		{ID: "c", Path: "x.go", Line: 40, Body: "Map is never made before use here.", Source: "agent", GitHubID: 5, Pushed: true},
		{ID: "d", Path: "y.go", Line: 1, Body: "Unrelated.", Source: "agent"},
	} {
		require.True(s.T(), s.rs.AddComment("ch1", c))
	}
}

func (s *ReviewHandlerSuite) TestDedupNotConfigured() {
	srv := newServerForReviewTests(s.T())
	w := httptest.NewRecorder()
	srv.buildMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/dedup", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)

	// Sessions but no runner.
	w = s.postDedup()
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ReviewHandlerSuite) TestDedupRejects() {
	cases := []struct {
		name  string
		setup func()
		want  int
		body  string
	}{
		{name: "no session", setup: func() {}, want: http.StatusNotFound},
		{name: "no worktree", setup: func() {
			s.rs.Put("ch1", &review.Session{Status: review.StatusReady})
		}, want: http.StatusConflict, body: "no worktree"},
		{name: "run in flight", setup: func() {
			s.wireReadySession()
			require.True(s.T(), s.srv.review.registerReviewRun("ch1", func() {}))
		}, want: http.StatusConflict, body: "in progress"},
		{name: "not ready", setup: func() {
			s.wireReadySession()
			s.rs.UpdateStatus("ch1", review.StatusLoading, "")
		}, want: http.StatusConflict, body: "status=loading"},
		{name: "channel has no dir", setup: func() {
			s.rs.Put("ch1", &review.Session{WorktreePath: "/wt", Status: review.StatusReady})
			s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db"))
		}, want: http.StatusBadRequest, body: "no dir_path"},
		{name: "refresh fails", setup: func() {
			s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, WorktreePath: "/wt", Status: review.StatusReady})
			s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
			s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", mock.Anything, 7).Return("", errors.New("gh down"))
		}, want: http.StatusInternalServerError, body: "gh down"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			runner := &mockReviewRunner{}
			s.srv.review.setAgent(runner, "", "")
			tc.setup()
			w := s.postDedup()
			require.Equal(s.T(), tc.want, w.Code)
			require.Contains(s.T(), w.Body.String(), tc.body)
			require.Equal(s.T(), 0, runner.calls)
		})
	}
}

// Nothing to fold, so no agent run: every file holds one comment.
func (s *ReviewHandlerSuite) TestDedupNothingToDo() {
	s.wireReadySession()
	// Only GitHub comments: nothing to check, and nothing the pass may delete.
	s.rs.AddComment("ch1", &review.Comment{ID: "d", Path: "y.go", Line: 1, Body: "Unrelated.", Source: "github"})
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	w := s.postDedup()
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{"removed":[],"clusters":[],"related":[],"moved":[],"trimmed":[],"verdicts":[],"checked":0}`, w.Body.String())
	require.Equal(s.T(), 0, runner.calls)
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// The model merges b and y.go's d into a, which takes the note; f into the
// pushed c, which can't; and p into a, whose GitHub delete fails so that
// cluster isn't reported. e vanishes mid-run and is skipped. a moves a line
// down; the pushed c can't be moved. a and the pushed c get verdicts; g's is
// lost with g, which also vanishes mid-run, and p's is ignored since p is
// dropped.
func (s *ReviewHandlerSuite) TestDedupRemovesDuplicates() {
	s.wireDedupSession()
	for _, c := range []*review.Comment{
		{ID: "e", Path: "x.go", Line: 60, Body: "Deleted by the user mid-run.", Source: "agent"},
		{ID: "f", Path: "x.go", Line: 70, Body: "Map missing make().", Source: "agent"},
		{ID: "g", Path: "x.go", Line: 80, Body: "Also deleted mid-run.", Source: "agent"},
		{ID: "p", Path: "z.go", Line: 3, Body: "Pushed copy.", Source: "agent", GitHubID: 6, Pushed: true},
		{ID: "u", Path: "w.go", Line: 5, Body: "Never opened by the pass.", Source: "agent"},
	} {
		require.True(s.T(), s.rs.AddComment("ch1", c))
	}
	s.rs.UpdateAgent("ch1", "claude-opus-5-5", "high")
	s.srv.review.setDedupTimeout(time.Minute)

	hub := NewEventsHub(slog.Default())
	var removed []string
	var updated []events.ReviewCommentEventData
	var hubMu sync.Mutex
	hub.captureHook = func(e Event) {
		hubMu.Lock()
		defer hubMu.Unlock()
		switch e.Type {
		case EventReviewCommentRemoved:
			removed = append(removed, e.Data.(map[string]string)["id"])
		case EventReviewCommentUpdated:
			updated = append(updated, e.Data.(events.ReviewCommentEventData))
		}
	}
	s.srv.SetEventsHub(hub)

	reply := `{"clusters":[` +
		`{"keep":"a","drop":["b","e","d"],"reason":"map never made","note":"y.go hits it too."},` +
		`{"keep":"c","drop":["f"],"note":"extra"},` +
		`{"keep":"a","drop":["p"]}],` +
		`"related":[{"ids":["a","c"],"reason":"same map"}],` +
		`"moves":[{"id":"a","line":11},{"id":"c","line":41},{"id":"u","line":6}],` +
		`"verdicts":[{"id":"a","verdict":"real","reason":"no make()"},{"id":"c","verdict":"false_positive","reason":"made in init"},` +
		`{"id":"g","verdict":"real"},{"id":"p","verdict":"real"},{"id":"u","verdict":"false_positive","reason":"guessed"}]}`
	// u's file was never read in the worktree (aw.go only shares its suffix,
	// and /repo/w.go is the parent checkout's copy), so its move and verdict
	// are guesses and get dropped.
	runner := &mockReviewRunner{reads: []string{"/repo/.worktrees/pr-7/x.go", "z.go", "/repo/.worktrees/pr-7/aw.go", "/repo/w.go"}, runFn: func() (*agent.AgentResponse, error) {
		require.Equal(s.T(), review.StatusReviewing, s.rs.Get("ch1").Status)
		s.rs.RemoveComment("ch1", "e")
		s.rs.RemoveComment("ch1", "g")
		return &agent.AgentResponse{SessionID: "sess-1", Response: "```json\n" + reply + "\n```"}, nil
	}}
	s.srv.review.setAgent(runner, "", "")

	w := s.postDedup()
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{
		"removed":["b","d","f"],
		"clusters":[
			{"kept":"a","removed":["b","d"],"reason":"map never made","note":"y.go hits it too.","note_added":true},
			{"kept":"c","removed":["f"],"note":"extra"}
		],
		"related":[{"ids":["a","c"],"reason":"same map"}],
		"moved":[{"id":"a","from":10,"to":11}],
		"trimmed":[],
		"verdicts":[{"id":"a","verdict":"real","reason":"no make()"},{"id":"c","verdict":"false_positive","reason":"made in init"}],
		"checked":9,
		"errors":["p: slug skipped"]
	}`, w.Body.String())

	require.True(s.T(), runner.lastRO)
	require.Equal(s.T(), "/repo/.worktrees/pr-7", runner.lastDir)
	require.Equal(s.T(), "/repo", runner.lastParent)
	require.Equal(s.T(), "claude-opus-5-5", runner.lastModel)
	require.Equal(s.T(), "high", runner.lastEffort)
	require.Contains(s.T(), runner.lastUser, "- id=b [agent] L14 (RIGHT): Assigning into an uninitialised map crashes.")
	require.Contains(s.T(), runner.lastUser, "## y.go\n- id=d [agent] L1 (RIGHT): Unrelated.")

	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.StatusReady, sess.Status)
	require.Equal(s.T(), []string{"sess-1"}, sess.RunSessionIDs)
	var left []string
	for _, c := range sess.Comments {
		left = append(left, c.ID)
	}
	require.Equal(s.T(), []string{"a", "c", "p", "u"}, left)
	keptA, _ := s.rs.FindComment("ch1", "a")
	require.Equal(s.T(), "Nil map write panics on the first insert.\n\nAlso flagged: y.go hits it too.", keptA.Body)
	require.Equal(s.T(), 11, keptA.Line)
	keptC, _ := s.rs.FindComment("ch1", "c")
	require.Equal(s.T(), "Map is never made before use here.", keptC.Body)
	require.Equal(s.T(), 40, keptC.Line)
	require.Equal(s.T(), review.VerdictReal, keptA.Verdict)
	require.Equal(s.T(), "no make()", keptA.VerdictReason)
	require.Equal(s.T(), review.VerdictFalsePositive, keptC.Verdict)
	require.Equal(s.T(), "made in init", keptC.VerdictReason)
	unread, _ := s.rs.FindComment("ch1", "u")
	require.Equal(s.T(), 5, unread.Line)
	require.Empty(s.T(), unread.Verdict)
	hubMu.Lock()
	require.Equal(s.T(), []string{"b", "d", "f"}, removed)
	require.Equal(s.T(), []events.ReviewCommentEventData{
		{ID: "a", Path: "x.go", Line: 10, Body: keptA.Body},
		{ID: "a", Path: "x.go", Line: 11, Body: keptA.Body},
		{ID: "a", Path: "x.go", Line: 11, Body: keptA.Body, Verdict: review.VerdictReal, VerdictReason: "no make()"},
		{ID: "c", Path: "x.go", Line: 40, Body: keptC.Body, Verdict: review.VerdictFalsePositive, VerdictReason: "made in init"},
	}, updated)
	hubMu.Unlock()
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// A bundled comment shares one of its issues with a single-issue one. The
// model keeps the single one, trims the bundle to the rest and folds another
// finding into the bundle, whose note lands after the trimmed body. The
// pushed c bundles the same issue but can't be rewritten.
func (s *ReviewHandlerSuite) TestDedupTrimsBundledComment() {
	s.wireReadySession()
	for _, c := range []*review.Comment{
		{ID: "bundle", Path: "x.go", Line: 10, Body: "The expiry check copies the cache's age logic and calls time.Now instead of an injected clock.", Source: "agent"},
		{ID: "single", Path: "x.go", Line: 10, Body: "isExpired calls time.Now, so the expiry boundary can't be tested.", Source: "agent"},
		{ID: "copy", Path: "y.go", Line: 4, Body: "Age logic duplicated from the cache.", Source: "agent"},
		{ID: "c", Path: "x.go", Line: 40, Body: "Copies the age logic and reads the wall clock.", Source: "agent", GitHubID: 5, Pushed: true},
	} {
		require.True(s.T(), s.rs.AddComment("ch1", c))
	}
	hub := NewEventsHub(slog.Default())
	var updated []string
	var hubMu sync.Mutex
	hub.captureHook = func(e Event) {
		hubMu.Lock()
		defer hubMu.Unlock()
		if e.Type == EventReviewCommentUpdated {
			updated = append(updated, e.Data.(events.ReviewCommentEventData).Body)
		}
	}
	s.srv.SetEventsHub(hub)
	reply := `{"clusters":[{"keep":"bundle","drop":["copy"],"note":"y.go copies it too."}],` +
		`"trims":[{"id":"bundle","covered_by":"single","body":"The expiry check copies the cache's age logic.","reason":"clock covered by single"},` +
		`{"id":"c","covered_by":"single","body":"Copies the age logic."}]}`
	s.srv.review.setAgent(&mockReviewRunner{runFn: func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{Response: reply}, nil
	}}, "", "")

	w := s.postDedup()
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{
		"removed":["copy"],
		"clusters":[{"kept":"bundle","removed":["copy"],"note":"y.go copies it too.","note_added":true}],
		"related":[],
		"moved":[],
		"trimmed":[{"id":"bundle","covered_by":"single","reason":"clock covered by single"}],
		"verdicts":[],
		"checked":4
	}`, w.Body.String())
	bundle, _ := s.rs.FindComment("ch1", "bundle")
	require.Equal(s.T(), "The expiry check copies the cache's age logic.\n\nAlso flagged: y.go copies it too.", bundle.Body)
	single, _ := s.rs.FindComment("ch1", "single")
	require.Equal(s.T(), "isExpired calls time.Now, so the expiry boundary can't be tested.", single.Body)
	pushed, _ := s.rs.FindComment("ch1", "c")
	require.Equal(s.T(), "Copies the age logic and reads the wall clock.", pushed.Body)
	hubMu.Lock()
	require.Equal(s.T(), []string{"The expiry check copies the cache's age logic.", bundle.Body}, updated)
	hubMu.Unlock()
}

// Without a hub the note and the verdict still land on the comment.
func (s *ReviewHandlerSuite) TestDedupNoteWithoutHub() {
	s.wireDedupSession()
	s.srv.review.setAgent(&mockReviewRunner{reads: []string{"/repo/.worktrees/pr-7/x.go"}, runFn: func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{Response: `{"clusters":[{"keep":"a","drop":["b"],"note":"n"}],"verdicts":[{"id":"a","verdict":"already_fixed"}]}`}, nil
	}}, "", "")

	w := s.postDedup()
	require.Equal(s.T(), http.StatusOK, w.Code)
	kept, _ := s.rs.FindComment("ch1", "a")
	require.Equal(s.T(), "Nil map write panics on the first insert.\n\nAlso flagged: n", kept.Body)
	require.Equal(s.T(), review.VerdictAlreadyFixed, kept.Verdict)
}

func (s *ReviewHandlerSuite) TestDedupRunFailures() {
	cases := []struct {
		name string
		resp *agent.AgentResponse
		err  error
		body string
	}{
		{name: "agent error", err: errors.New("container died"), body: "dedup run: container died"},
		{name: "reply without json", resp: &agent.AgentResponse{Response: "no duplicates"}, body: "no JSON object"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.srv.review.setAgent(&mockReviewRunner{runFn: func() (*agent.AgentResponse, error) {
				return tc.resp, tc.err
			}}, "", "")

			w := s.postDedup()
			require.Equal(s.T(), http.StatusInternalServerError, w.Code)
			require.Contains(s.T(), w.Body.String(), tc.body)
			sess := s.rs.Get("ch1")
			require.Equal(s.T(), review.StatusReady, sess.Status)
			require.Len(s.T(), sess.Comments, 4)
		})
	}
}

func (s *ReviewHandlerSuite) TestReviewHTTPErrorMessage() {
	require.EqualError(s.T(), &reviewHTTPError{http.StatusForbidden, "denied"}, "denied")
}

// postRunAndWait starts a review run and waits for its goroutine, the dedup
// pass after it included, to give the run slot back.
func (s *ReviewHandlerSuite) postRunAndWait() {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", nil))
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	s.waitFor(func() bool { return !s.srv.review.isReviewRunActive("ch1") })
}

// dedupAfterRunRunner reports finding as the review and answers the dedup
// pass after it with dedup.
func dedupAfterRunRunner(finding *review.Comment, dedup func() (*agent.AgentResponse, error)) *mockReviewRunner {
	runner := &mockReviewRunner{findings: []*review.Comment{finding}}
	runner.runFn = func() (*agent.AgentResponse, error) {
		if runner.lastRO {
			return dedup()
		}
		return &agent.AgentResponse{}, nil
	}
	return runner
}

// The pass after a run checks only what the run added against the rest and
// records what it folded, so `loop review run --wait` can report it.
func (s *ReviewHandlerSuite) TestRunDedupsWhatItAdded() {
	s.wireDedupSession()
	s.rs.SetSuperseded("ch1", map[string]string{"old": "kept"})
	fresh := review.NewComment("x.go", 12, "", "Writing to the map before make panics.")
	runner := dedupAfterRunRunner(fresh, func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{Response: `{"clusters":[{"keep":"a","drop":["` + fresh.ID + `"]}]}`}, nil
	})
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Equal(s.T(), 2, runner.calls)
	require.Contains(s.T(), runner.lastUser, "- id="+fresh.ID+" [agent, new] L12 (RIGHT): ")
	// a has no verdict yet, so it is checked again.
	require.Contains(s.T(), runner.lastUser, "- id=a [agent, recheck] L10 (RIGHT): ")
	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.StatusReady, sess.Status)
	require.Equal(s.T(), map[string]string{fresh.ID: "a"}, sess.Superseded)
	gone, _ := s.rs.FindComment("ch1", fresh.ID)
	require.Nil(s.T(), gone)
	require.Len(s.T(), sess.Comments, 4)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels/ch1/review?diff=false", nil))
	require.Contains(s.T(), w.Body.String(), `"superseded":{"`+fresh.ID+`":"a"}`)
}

// A pass that folds nothing, or fails, leaves the run's findings as
// reported and the session ready, with the previous run's Superseded gone.
func (s *ReviewHandlerSuite) TestRunDedupFoldsNothing() {
	cases := []struct {
		name  string
		dedup func() (*agent.AgentResponse, error)
	}{
		{"no duplicates", func() (*agent.AgentResponse, error) {
			return &agent.AgentResponse{Response: `{"clusters":[]}`}, nil
		}},
		{"pass fails", func() (*agent.AgentResponse, error) { return nil, errors.New("container died") }},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.rs.SetSuperseded("ch1", map[string]string{"old": "kept"})
			runner := dedupAfterRunRunner(review.NewComment("x.go", 12, "", "new"), tc.dedup)
			s.srv.review.setAgent(runner, "", "")

			s.postRunAndWait()

			require.Equal(s.T(), 2, runner.calls)
			sess := s.rs.Get("ch1")
			require.Equal(s.T(), review.StatusReady, sess.Status)
			require.Nil(s.T(), sess.Superseded)
			require.Len(s.T(), sess.Comments, 5)
		})
	}
}

// A run that adds no comment the pass may delete has nothing to fold.
func (s *ReviewHandlerSuite) TestRunSkipsDedupWithoutNewComments() {
	s.wireDedupSession()
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Equal(s.T(), 1, runner.calls)
	require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
}

// The session going away during the run leaves nothing to dedup.
func (s *ReviewHandlerSuite) TestRunDedupSkippedWhenSessionDropped() {
	s.wireDedupSession()
	runner := &mockReviewRunner{}
	runner.runFn = func() (*agent.AgentResponse, error) {
		s.rs.Delete("ch1")
		return &agent.AgentResponse{}, nil
	}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Equal(s.T(), 1, runner.calls)
	require.Nil(s.T(), s.rs.Get("ch1"))
}

// Stopping the run during the pass after it leaves the session ready, like
// a stopped review.
func (s *ReviewHandlerSuite) TestRunDedupCancelled() {
	s.wireDedupSession()
	runner := &mockReviewRunner{findings: []*review.Comment{review.NewComment("x.go", 12, "", "new")}}
	runner.runWithCtxFn = func(ctx context.Context) (*agent.AgentResponse, error) {
		if !runner.lastRO {
			return &agent.AgentResponse{}, nil
		}
		s.srv.review.cancelReviewRun("ch1")
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Equal(s.T(), 2, runner.calls)
	require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
}

// The pass after a run gets a timeout of its own rather than what the
// review left of the shared one.
func (s *ReviewHandlerSuite) TestRunDedupGetsItsOwnTimeout() {
	s.wireDedupSession()
	s.srv.review.setRunTimeout(time.Hour)
	s.srv.review.setDedupTimeout(time.Hour)
	var deadlines []time.Time
	runner := &mockReviewRunner{findings: []*review.Comment{review.NewComment("x.go", 12, "", "new")}}
	runner.runWithCtxFn = func(ctx context.Context) (*agent.AgentResponse, error) {
		dl, ok := ctx.Deadline()
		require.True(s.T(), ok)
		deadlines = append(deadlines, dl)
		if !runner.lastRO {
			time.Sleep(5 * time.Millisecond) // the review uses some of its budget
			return &agent.AgentResponse{}, nil
		}
		return &agent.AgentResponse{Response: `{"clusters":[]}`}, nil
	}
	s.srv.review.setAgent(runner, "", "")

	s.postRunAndWait()

	require.Len(s.T(), deadlines, 2)
	require.True(s.T(), deadlines[1].After(deadlines[0]), "the dedup pass's deadline starts when it does")
}
