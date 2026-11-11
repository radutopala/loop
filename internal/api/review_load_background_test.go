package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
)

// statusEvents sends every review.status the server broadcasts.
func (s *ReviewHandlerSuite) statusEvents() <-chan events.ReviewStatusEventData {
	got := make(chan events.ReviewStatusEventData, 8)
	hub := NewEventsHub(slog.Default())
	hub.captureHook = func(e Event) {
		if e.Type == EventReviewStatus {
			got <- e.Data.(events.ReviewStatusEventData)
		}
	}
	s.srv.SetEventsHub(hub)
	return got
}

// loadBackground posts a background load of PR 7.
func (s *ReviewHandlerSuite) loadBackground() *httptest.ResponseRecorder {
	return s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7, "background": true})
}

// stubLoadPR answers the gh and git calls of loading PR 7, with the PR
// fetch held until release is closed.
func (s *ReviewHandlerSuite) stubLoadPR(release <-chan struct{}) {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Run(func(mock.Arguments) { <-release }).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7, mock.Anything).Return("/repo/.worktrees/pr-7", nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
}

// A background load answers with the loading session at once, holds the
// channel's run slot while gh and git work, and settles ready with an
// event.
func (s *ReviewHandlerSuite) TestLoadBackground() {
	got := s.statusEvents()
	release := make(chan struct{})
	s.stubLoadPR(release)
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Return([]byte("diff text"), nil)

	w := s.loadBackground()
	require.Equal(s.T(), http.StatusAccepted, w.Code, w.Body.String())
	require.Contains(s.T(), w.Body.String(), `"status":"loading","loading_pr":7`)
	require.Equal(s.T(), review.StatusLoading, s.rs.Get("ch1").Status)
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusLoading)}, <-got)

	// Another load, either kind, waits for this one.
	w = s.loadBackground()
	require.Equal(s.T(), http.StatusConflict, w.Code)
	require.Contains(s.T(), w.Body.String(), "a PR is loading")
	w = s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusConflict, w.Code)

	close(release)
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusReady)}, <-got)
	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.StatusReady, sess.Status)
	require.Equal(s.T(), "diff text", sess.RawDiff)
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"), "the slot is free once it's ready")
}

func (s *ReviewHandlerSuite) TestLoadBackgroundFails() {
	got := s.statusEvents()
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(nil, errors.New("api fail"))

	require.Equal(s.T(), http.StatusAccepted, s.loadBackground().Code)
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusLoading)}, <-got)
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusError), Error: "api fail"}, <-got)
	require.Equal(s.T(), review.StatusError, s.rs.Get("ch1").Status)
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// What fails before the loading session is in place fails the request.
func (s *ReviewHandlerSuite) TestLoadBackgroundRefused() {
	s.Run("no dir", func() {
		s.SetupTest()
		s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
		require.Equal(s.T(), http.StatusBadRequest, s.loadBackground().Code)
		require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
	})
	s.Run("run in flight", func() {
		s.SetupTest()
		require.True(s.T(), s.srv.review.registerReviewRun("ch1", func() {}))
		require.Equal(s.T(), http.StatusConflict, s.loadBackground().Code)
	})
}

// Closing the session while its PR loads cancels the load, which settles
// nothing: no session comes back, and a checkout made meanwhile is removed.
func (s *ReviewHandlerSuite) TestLoadBackgroundClosed() {
	tests := []struct {
		name      string
		holdDiff  bool
		removeErr error
	}{
		{name: "while fetching"},
		{name: "while diffing", holdDiff: true},
		{name: "remove fails", holdDiff: true, removeErr: errors.New("busy")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			got := s.statusEvents()
			fetch, diff := make(chan struct{}), make(chan struct{})
			if tc.holdDiff {
				close(fetch)
			}
			s.stubLoadPR(fetch)
			// The cancelled fetch or diff fails, as gh and git do.
			if !tc.holdDiff {
				s.gh.ExpectedCalls = nil
				s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Run(func(mock.Arguments) { <-fetch }).Return(nil, errors.New("context canceled"))
			}
			s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7", "main", mock.Anything).Run(func(mock.Arguments) { <-diff }).Return([]byte("d"), nil)
			removed := make(chan struct{})
			s.wt.On("Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-7").Run(func(mock.Arguments) { close(removed) }).Return(tc.removeErr)

			require.Equal(s.T(), http.StatusAccepted, s.loadBackground().Code)
			require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusLoading)}, <-got)
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/channels/ch1/review", nil))
			require.Equal(s.T(), http.StatusNoContent, w.Code)
			require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusIdle)}, <-got)
			if tc.holdDiff {
				close(diff)
				<-removed
			} else {
				close(fetch)
			}
			require.Eventually(s.T(), func() bool { return !s.srv.review.isReviewRunActive("ch1") }, 5*time.Second, time.Millisecond)
			require.Nil(s.T(), s.rs.Get("ch1"), "the closed session stays closed")
			require.Empty(s.T(), got, "only the close is announced")
		})
	}
}

// A background load that outlives the load timeout fails, saying so.
func (s *ReviewHandlerSuite) TestLoadBackgroundTimesOut() {
	got := s.statusEvents()
	s.srv.review.loadTimeout = time.Millisecond
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).
		Run(func(args mock.Arguments) { <-args.Get(0).(context.Context).Done() }).
		Return(nil, errors.New("context deadline exceeded"))

	require.Equal(s.T(), http.StatusAccepted, s.loadBackground().Code)
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusLoading)}, <-got)
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusError), Error: "loading PR #7 timed out"}, <-got)
	require.Equal(s.T(), "loading PR #7 timed out", s.rs.Get("ch1").Error)
}

// A Stop while a PR loads ends the load: the session says it was stopped,
// not what the cancelled gh call printed.
func (s *ReviewHandlerSuite) TestLoadStopped() {
	got := s.statusEvents()
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).
		Run(func(args mock.Arguments) { <-args.Get(0).(context.Context).Done() }).
		Return(nil, errors.New("context canceled"))

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- s.postJSON("/api/channels/ch1/review/load", map[string]any{"pr_number": 7}) }()
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusLoading)}, <-got)
	require.Equal(s.T(), http.StatusAccepted, s.postJSON("/api/channels/ch1/review/stop", nil).Code)

	w := <-done
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "loading PR #7 was stopped")
	require.Equal(s.T(), events.ReviewStatusEventData{Status: string(review.StatusError), Error: "loading PR #7 was stopped"}, <-got)
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// A load that didn't settle leaves the checkout alone when the channel's
// session points at it after all.
func (s *ReviewHandlerSuite) TestRemoveLoadCheckoutKeepsTheSessions() {
	s.rs.Put("ch1", &review.Session{Status: review.StatusReady, WorktreePath: "/repo/.worktrees/pr-7"})
	s.srv.review.removeLoadCheckout(context.Background(), "ch1", "/repo", "/repo/.worktrees/pr-7")
	s.wt.AssertNotCalled(s.T(), "Remove", mock.Anything, mock.Anything, mock.Anything)
}

// A nil comment updates nothing.
func (s *ReviewHandlerSuite) TestCommentUpdatedNil() {
	got := s.statusEvents()
	require.NotPanics(s.T(), func() { s.srv.review.commentUpdated("ch1", nil) })
	require.Empty(s.T(), got)
}
