package api

import (
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
	s.rs.AddComment("ch1", &review.Comment{ID: "d", Path: "y.go", Line: 1, Body: "Unrelated.", Source: "agent"})
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	w := s.postDedup()
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{"removed":[],"clusters":[],"related":[],"moved":[],"checked":0}`, w.Body.String())
	require.Equal(s.T(), 0, runner.calls)
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// The model merges b and y.go's d into a, which takes the note; f into the
// pushed c, which can't; and p into a, whose GitHub delete fails so that
// cluster isn't reported. e vanishes mid-run and is skipped. a moves a line
// down; the pushed c can't be moved.
func (s *ReviewHandlerSuite) TestDedupRemovesDuplicates() {
	s.wireDedupSession()
	for _, c := range []*review.Comment{
		{ID: "e", Path: "x.go", Line: 60, Body: "Deleted by the user mid-run.", Source: "agent"},
		{ID: "f", Path: "x.go", Line: 70, Body: "Map missing make().", Source: "agent"},
		{ID: "p", Path: "z.go", Line: 3, Body: "Pushed copy.", Source: "agent", GitHubID: 6, Pushed: true},
	} {
		require.True(s.T(), s.rs.AddComment("ch1", c))
	}
	s.rs.UpdateAgent("ch1", "claude-opus-5-5", "high")
	s.srv.review.setRunTimeout(time.Minute)

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
		`"moves":[{"id":"a","line":11},{"id":"c","line":41}]}`
	runner := &mockReviewRunner{runFn: func() (*agent.AgentResponse, error) {
		require.Equal(s.T(), review.StatusReviewing, s.rs.Get("ch1").Status)
		s.rs.RemoveComment("ch1", "e")
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
		"checked":7,
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
	require.Equal(s.T(), []string{"a", "c", "p"}, left)
	keptA, _ := s.rs.FindComment("ch1", "a")
	require.Equal(s.T(), "Nil map write panics on the first insert.\n\nAlso flagged: y.go hits it too.", keptA.Body)
	require.Equal(s.T(), 11, keptA.Line)
	keptC, _ := s.rs.FindComment("ch1", "c")
	require.Equal(s.T(), "Map is never made before use here.", keptC.Body)
	require.Equal(s.T(), 40, keptC.Line)
	hubMu.Lock()
	require.Equal(s.T(), []string{"b", "d", "f"}, removed)
	require.Equal(s.T(), []events.ReviewCommentEventData{
		{ID: "a", Path: "x.go", Line: 10, Body: keptA.Body},
		{ID: "a", Path: "x.go", Line: 11, Body: keptA.Body},
	}, updated)
	hubMu.Unlock()
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// Without a hub the note still lands on the comment.
func (s *ReviewHandlerSuite) TestDedupNoteWithoutHub() {
	s.wireDedupSession()
	s.srv.review.setAgent(&mockReviewRunner{runFn: func() (*agent.AgentResponse, error) {
		return &agent.AgentResponse{Response: `{"clusters":[{"keep":"a","drop":["b"],"note":"n"}]}`}, nil
	}}, "", "")

	w := s.postDedup()
	require.Equal(s.T(), http.StatusOK, w.Code)
	kept, _ := s.rs.FindComment("ch1", "a")
	require.Equal(s.T(), "Nil map write panics on the first insert.\n\nAlso flagged: n", kept.Body)
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
