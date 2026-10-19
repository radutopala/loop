package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
)

func (s *ReviewHandlerSuite) TestAssessThread() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abc"})
	s.rs.AddComment("ch1", &review.Comment{ID: "gh-9", Path: "x.go", Line: 3, Body: "Theirs.", Source: "github", GitHubID: 9, Pushed: true})
	s.rs.AddComment("ch1", &review.Comment{ID: "gh-10", Path: "x.go", Line: 3, Body: "Reply.", Source: "github", GitHubID: 10, ReplyTo: 9, Pushed: true})
	s.rs.AddComment("ch1", &review.Comment{ID: "a", Path: "x.go", Line: 3, Body: "Ours.", Source: "agent"})

	cases := []struct {
		name    string
		target  string
		body    string
		want    int
		wantMsg string
	}{
		{name: "bad json", target: "/api/channels/ch1/review/comments/gh-9/assess", body: `{`, want: http.StatusBadRequest, wantMsg: "invalid JSON body"},
		{name: "unknown status", target: "/api/channels/ch1/review/comments/gh-9/assess", body: `{"status":"wontfix"}`, want: http.StatusBadRequest, wantMsg: "status must be"},
		{name: "open without impact", target: "/api/channels/ch1/review/comments/gh-9/assess", body: `{"status":"open"}`, want: http.StatusBadRequest, wantMsg: "an open thread needs impact"},
		{name: "addressed with impact", target: "/api/channels/ch1/review/comments/gh-9/assess", body: `{"status":"addressed","impact":"blocking"}`, want: http.StatusBadRequest, wantMsg: "open thread only"},
		{name: "no session", target: "/api/channels/ch2/review/comments/gh-9/assess", body: `{"status":"addressed"}`, want: http.StatusNotFound, wantMsg: "no review session"},
		{name: "no comment", target: "/api/channels/ch1/review/comments/zz/assess", body: `{"status":"addressed"}`, want: http.StatusNotFound, wantMsg: "comment not found"},
		{name: "a reply", target: "/api/channels/ch1/review/comments/gh-10/assess", body: `{"status":"addressed"}`, want: http.StatusConflict, wantMsg: "only the first comment"},
		{name: "an agent comment", target: "/api/channels/ch1/review/comments/a/assess", body: `{"status":"addressed"}`, want: http.StatusConflict, wantMsg: "give an agent comment a verdict"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, agentRequest("POST", tc.target, strings.NewReader(tc.body)))
			require.Equal(s.T(), tc.want, w.Code)
			require.Contains(s.T(), w.Body.String(), tc.wantMsg)
		})
	}
	require.Nil(s.T(), s.rs.Get("ch1").Threads)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, agentRequest("POST", "/api/channels/ch1/review/comments/gh-9/assess", strings.NewReader(`{"status":"open","impact":"nice_to_have","reason":" naming only "}`)))
	require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(s.T(), `{"status":"open","impact":"nice_to_have","reason":"naming only"}`, w.Body.String())
	require.Equal(s.T(), map[int64]review.ThreadAssessment{9: {Status: review.ThreadOpen, Impact: review.ImpactNiceToHave, Reason: "naming only"}}, s.rs.Get("ch1").Threads)
}

func (s *ReviewHandlerSuite) TestAssessThreadNotConfigured() {
	srv := newServerForReviewTests(s.T())
	w := httptest.NewRecorder()
	srv.buildMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/comments/a/assess", strings.NewReader(`{}`)))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

// putApprovable puts a session reviewed at its head, with one nit left.
func (s *ReviewHandlerSuite) putApprovable() {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abcdef123456", ReviewedSHA: "abcdef123456", Status: review.StatusReady})
	s.rs.AddComment("ch1", &review.Comment{ID: "n", Path: "x.go", Line: 3, Body: "Rename.", Source: "agent", Verdict: review.VerdictReal, Impact: review.ImpactNiceToHave})
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil).Maybe()
}

func (s *ReviewHandlerSuite) TestApprove() {
	hub := NewEventsHub(slog.Default())
	var approved []Event
	var hubMu sync.Mutex
	hub.captureHook = func(e Event) {
		hubMu.Lock()
		defer hubMu.Unlock()
		if e.Type == EventReviewApproved {
			approved = append(approved, e)
		}
	}
	s.srv.SetEventsHub(hub)
	s.putApprovable()
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abcdef123456", nil).Once()
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil).Once()
	s.gh.On("ApprovePR", mock.Anything, "/repo", "", *slug, 7, "abcdef123456", "Nits only.").Return(nil).Once()

	w := s.doRaw("POST", "/api/channels/ch1/review/approve", []byte(`{"body":" Nits only. "}`))

	require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(s.T(), `{"approved_sha":"abcdef123456"}`, w.Body.String())
	require.Equal(s.T(), "abcdef123456", s.rs.Get("ch1").ApprovedSHA)
	hubMu.Lock()
	require.Len(s.T(), approved, 1)
	require.Equal(s.T(), "ch1", approved[0].ChannelID)
	require.Equal(s.T(), map[string]any{"channel_id": "ch1", "pr": 7, "commit": "abcdef123456"}, approved[0].Data)
	hubMu.Unlock()
	s.gh.AssertExpectations(s.T())
}

// A blocking finding refuses the approval unless forced; with no events
// hub and no body, a forced one still goes through.
func (s *ReviewHandlerSuite) TestApproveForced() {
	s.putApprovable()
	s.rs.AddComment("ch1", &review.Comment{ID: "b", Path: "x.go", Line: 9, Body: "Data loss.", Source: "agent", Verdict: review.VerdictReal, Impact: review.ImpactBlocking})

	w := s.doRaw("POST", "/api/channels/ch1/review/approve", nil)
	require.Equal(s.T(), http.StatusConflict, w.Code)
	require.Contains(s.T(), w.Body.String(), "the outcome is request changes: 1 blocking (b); pass force to approve anyway")

	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abcdef123456", nil).Once()
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil).Once()
	s.gh.On("ApprovePR", mock.Anything, "/repo", "", *slug, 7, "abcdef123456", "").Return(nil).Once()
	w = s.doRaw("POST", "/api/channels/ch1/review/approve", []byte(`{"force":true}`))
	require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())
	require.Equal(s.T(), "abcdef123456", s.rs.Get("ch1").ApprovedSHA)
}

func (s *ReviewHandlerSuite) TestApproveRejects() {
	slug := &githubapi.RepoSlug{Owner: "o", Name: "r"}
	cases := []struct {
		name    string
		setup   func()
		body    string
		want    int
		wantMsg string
	}{
		{name: "no session", setup: func() {}, want: http.StatusNotFound, wantMsg: "no review session"},
		{name: "bad json", setup: s.putApprovable, body: `{`, want: http.StatusBadRequest, wantMsg: "invalid JSON body"},
		{name: "no PR", setup: func() {
			s.rs.Put("ch1", &review.Session{Status: review.StatusLoading})
		}, want: http.StatusConflict, wantMsg: "no PR or head SHA"},
		{name: "head not reviewed", setup: func() {
			s.putApprovable()
			s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, HeadSHA: "abcdef123456"})
		}, want: http.StatusConflict, wantMsg: "the head commit isn't reviewed yet"},
		{name: "review disabled", setup: func() {
			s.putApprovable()
			s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
		}, want: http.StatusForbidden},
		{name: "head lookup fails", setup: func() {
			s.putApprovable()
			s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("", errors.New("gh down")).Once()
		}, want: http.StatusBadGateway, wantMsg: "reading the PR head: gh down"},
		{name: "head moved", setup: func() {
			s.putApprovable()
			s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("9999999999", nil).Once()
		}, want: http.StatusConflict, wantMsg: "the PR head moved to 9999999 since abcdef1 was reviewed"},
		{name: "slug lookup fails", setup: func() {
			s.putApprovable()
			s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abcdef123456", nil).Once()
			s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no remote")).Once()
		}, want: http.StatusBadGateway, wantMsg: "reading the repo: no remote"},
		{name: "github refuses", setup: func() {
			s.putApprovable()
			s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abcdef123456", nil).Once()
			s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return(slug, nil).Once()
			s.gh.On("ApprovePR", mock.Anything, "/repo", "", *slug, 7, "abcdef123456", "").Return(errors.New("can't approve your own PR")).Once()
		}, want: http.StatusBadGateway, wantMsg: "approving on GitHub: can't approve your own PR"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setup()
			w := s.doRaw("POST", "/api/channels/ch1/review/approve", []byte(tc.body))
			require.Equal(s.T(), tc.want, w.Code, w.Body.String())
			require.Contains(s.T(), w.Body.String(), tc.wantMsg)
			if sess := s.rs.Get("ch1"); sess != nil {
				require.Empty(s.T(), sess.ApprovedSHA)
			}
			s.gh.AssertExpectations(s.T())
		})
	}
}

func (s *ReviewHandlerSuite) TestApproveNotConfigured() {
	srv := newServerForReviewTests(s.T())
	w := httptest.NewRecorder()
	srv.buildMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/approve", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)

	// A client but no sessions.
	srv.review.client = s.gh
	w = httptest.NewRecorder()
	srv.buildMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/approve", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}
