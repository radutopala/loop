package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type MockQueueResumer struct {
	mock.Mock
}

func (m *MockQueueResumer) ResumeChannel(ctx context.Context, channelID string) {
	m.Called(ctx, channelID)
}

func (s *ServerSuite) TestHoldQueuedMessage() {
	cases := []struct {
		name   string
		held   bool
		err    error
		code   int
		body   string
		result bool
	}{
		{name: "held", held: true, code: http.StatusOK, result: true},
		{name: "already started", code: http.StatusConflict, body: errAlreadyStarted},
		{name: "store error", err: errors.New("boom"), code: http.StatusInternalServerError, body: "boom"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			before := time.Now().Add(editHoldTTL).Unix()
			withinTTL := mock.MatchedBy(func(until int64) bool {
				return until >= before && until <= time.Now().Add(editHoldTTL).Unix()
			})
			s.store.On("HoldQueuedMessage", mock.Anything, "ch-1", "msg-7", withinTTL).Return(tc.held, tc.err).Once()

			rec := s.testRequest("POST", "/api/channels/ch-1/queued/msg-7/hold", "")
			require.Equal(s.T(), tc.code, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tc.body)
			if tc.result {
				var resp editHoldResponse
				require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
				require.GreaterOrEqual(s.T(), resp.HoldUntil, before)
			}
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestReleaseQueuedHold() {
	cases := []struct {
		name     string
		released bool
		err      error
		code     int
		resumes  bool
	}{
		{name: "released resumes the queue", released: true, code: http.StatusNoContent, resumes: true},
		{name: "row already started", code: http.StatusNoContent},
		{name: "store error", err: errors.New("boom"), code: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			resumer := new(MockQueueResumer)
			if tc.resumes {
				resumer.On("ResumeChannel", mock.Anything, "ch-1").Once()
			}
			s.srv.SetQueueResumer(resumer)
			defer s.srv.SetQueueResumer(nil)
			s.store.On("ReleaseQueuedHold", mock.Anything, "ch-1", "msg-7").Return(tc.released, tc.err).Once()

			rec := s.testRequest("DELETE", "/api/channels/ch-1/queued/msg-7/hold", "")
			require.Equal(s.T(), tc.code, rec.Code)
			resumer.AssertExpectations(s.T())
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestUpdateQueuedMessage() {
	cases := []struct {
		name    string
		body    string
		store   bool // whether the store is reached
		updated bool
		err     error
		code    int
		resumes bool
	}{
		{name: "saved", body: `{"content":"edited"}`, store: true, updated: true, code: http.StatusNoContent, resumes: true},
		{name: "already started", body: `{"content":"edited"}`, store: true, code: http.StatusConflict},
		{name: "store error", body: `{"content":"edited"}`, store: true, err: errors.New("boom"), code: http.StatusInternalServerError},
		{name: "blank content", body: `{"content":"  \n"}`, code: http.StatusBadRequest},
		{name: "bad body", body: `nope`, code: http.StatusBadRequest},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			hub := NewEventsHub(slog.Default())
			s.srv.SetEventsHub(hub)
			defer func() { s.srv.eventsHub = nil }()
			resumer := new(MockQueueResumer)
			if tc.resumes {
				resumer.On("ResumeChannel", mock.Anything, "ch-1").Once()
			}
			s.srv.SetQueueResumer(resumer)
			defer s.srv.SetQueueResumer(nil)
			if tc.store {
				s.store.On("UpdateQueuedMessage", mock.Anything, "ch-1", "msg-7", "edited").Return(tc.updated, tc.err).Once()
			}

			rec := s.testRequest("PUT", "/api/channels/ch-1/queued/msg-7", tc.body)
			require.Equal(s.T(), tc.code, rec.Code, rec.Body.String())
			resumer.AssertExpectations(s.T())
			s.store.AssertExpectations(s.T())
		})
	}
}

// TestQueuedEditWithoutHubOrResumer saves an edit on a server with neither an
// events hub nor a queue resumer wired — both are optional.
func (s *ServerSuite) TestQueuedEditWithoutHubOrResumer() {
	s.store.On("UpdateQueuedMessage", mock.Anything, "ch-1", "msg-7", "edited").Return(true, nil).Once()

	rec := s.testRequest("PUT", "/api/channels/ch-1/queued/msg-7", `{"content":"edited"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestQueuedEditNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/channels/{id}/queued/{msg_id}", srv.handleUpdateQueuedMessage)
	mux.HandleFunc("POST /api/channels/{id}/queued/{msg_id}/hold", srv.handleHoldQueuedMessage)
	mux.HandleFunc("DELETE /api/channels/{id}/queued/{msg_id}/hold", srv.handleReleaseQueuedHold)
	for _, method := range []string{"PUT", "POST", "DELETE"} {
		path := "/api/channels/ch-1/queued/msg-7/hold"
		if method == "PUT" {
			path = "/api/channels/ch-1/queued/msg-7"
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, method)
	}
}
