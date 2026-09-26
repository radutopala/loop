package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// ── handleSetChannelTicketURL ──

func (s *ServerSuite) TestSetChannelTicketURL() {
	const jira = "https://example.atlassian.net/browse/PROJ-123"
	tests := []struct {
		name     string
		id       string
		body     string
		setup    func()
		withHub  bool
		wantCode int
		wantBody string
	}{
		{
			name: "sets it, trimmed",
			id:   "t1", body: `{"ticket_url":"  ` + jira + ` \n"}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelTicketURL", mock.Anything, "t1", jira).Return(nil)
			},
			withHub:  true,
			wantCode: http.StatusOK, wantBody: `"ticket_url":"` + jira + `"`,
		},
		{
			name: "any tracker over http",
			id:   "c1", body: `{"ticket_url":"http://tracker.internal/issues/7"}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "c1").Return(&db.Channel{ChannelID: "c1"}, nil)
				s.store.On("UpdateChannelTicketURL", mock.Anything, "c1", "http://tracker.internal/issues/7").Return(nil)
			},
			wantCode: http.StatusOK,
		},
		{
			name: "empty clears it",
			id:   "t1", body: `{"ticket_url":" "}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelTicketURL", mock.Anything, "t1", "").Return(nil)
			},
			wantCode: http.StatusOK, wantBody: `"ticket_url":""`,
		},
		{name: "not a URL", id: "t1", body: `{"ticket_url":"PROJ-123"}`, wantCode: http.StatusBadRequest, wantBody: "absolute http(s) URL"},
		{name: "other scheme", id: "t1", body: `{"ticket_url":"javascript:alert(1)"}`, wantCode: http.StatusBadRequest, wantBody: "absolute http(s) URL"},
		{name: "no host", id: "t1", body: `{"ticket_url":"https:///browse/X-1"}`, wantCode: http.StatusBadRequest, wantBody: "absolute http(s) URL"},
		{name: "unparsable", id: "t1", body: `{"ticket_url":"https://ex ample.com/%zz"}`, wantCode: http.StatusBadRequest, wantBody: "absolute http(s) URL"},
		{name: "too long", id: "t1", body: `{"ticket_url":"https://x.com/` + strings.Repeat("a", maxTicketURLLen) + `"}`, wantCode: http.StatusBadRequest, wantBody: "longer than 2048 characters"},
		{name: "bad json", id: "t1", body: `{bad}`, wantCode: http.StatusBadRequest},
		{
			name: "lookup error", id: "t1", body: `{"ticket_url":"` + jira + `"}`,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "t1").Return(nil, errors.New("db error")) },
			wantCode: http.StatusInternalServerError,
		},
		{
			name: "not found", id: "gone", body: `{"ticket_url":"` + jira + `"}`,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil) },
			wantCode: http.StatusNotFound,
		},
		{
			name: "update error", id: "t1", body: `{"ticket_url":"` + jira + `"}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelTicketURL", mock.Anything, "t1", jira).Return(errors.New("db error"))
			},
			wantCode: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if tt.setup != nil {
				tt.setup()
			}
			if tt.withHub {
				s.srv.eventsHub = NewEventsHub(testLogger())
			}
			rec := s.testRequest("POST", "/api/channels/"+tt.id+"/ticket", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestSetChannelTicketURL_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/ticket", srv.handleSetChannelTicketURL)

	req, _ := http.NewRequest("POST", "/api/channels/t1/ticket", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}
