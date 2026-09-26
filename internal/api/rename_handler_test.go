package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// ── handleRenameChannel ──

func (s *ServerSuite) TestRenameChannel_Success() {

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", Name: "old-name"}, nil)
	s.store.On("UpdateChannelName", mock.Anything, "ch1", "new-name").Return(nil)

	s.srv.eventsHub = NewEventsHub(testLogger())

	rec := s.testRequest("POST", "/api/channels/ch1/rename", `{"name":"new-name"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"channel_id":"ch1"`)
	require.Contains(s.T(), rec.Body.String(), `"name":"new-name"`)
}

func (s *ServerSuite) TestRenameChannel_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/rename", srv.handleRenameChannel)

	req, _ := http.NewRequest("POST", "/api/channels/ch1/rename", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestRenameChannel_BadJSON() {
	rec := s.testRequest("POST", "/api/channels/ch1/rename", `{bad}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestRenameChannel_EmptyName() {
	rec := s.testRequest("POST", "/api/channels/ch1/rename", `{"name":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "name is required")
}

func (s *ServerSuite) TestRenameChannel_GetChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db error"))
	rec := s.testRequest("POST", "/api/channels/ch1/rename", `{"name":"new-name"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRenameChannel_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("POST", "/api/channels/missing/rename", `{"name":"new-name"}`)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestRenameChannel_UpdateError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	s.store.On("UpdateChannelName", mock.Anything, "ch1", "new-name").Return(errors.New("db error"))
	rec := s.testRequest("POST", "/api/channels/ch1/rename", `{"name":"new-name"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRenameChannel_NoEventsHub() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	s.store.On("UpdateChannelName", mock.Anything, "ch1", "new-name").Return(nil)
	// No eventsHub set — should not panic.
	rec := s.testRequest("POST", "/api/channels/ch1/rename", `{"name":"new-name"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

// ── handleSetChannelDescription ──

func (s *ServerSuite) TestSetChannelDescription() {
	long := strings.Repeat("é", maxDescriptionLen+1)
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
			id:   "t1", body: `{"description":"  fixes the login flow \n"}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelDescription", mock.Anything, "t1", "fixes the login flow").Return(nil)
			},
			withHub:  true,
			wantCode: http.StatusOK, wantBody: `"description":"fixes the login flow"`,
		},
		{
			name: "empty clears it, no hub",
			id:   "t1", body: `{"description":""}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelDescription", mock.Anything, "t1", "").Return(nil)
			},
			wantCode: http.StatusOK, wantBody: `"description":""`,
		},
		{
			name: "exactly the limit is fine",
			id:   "t1", body: `{"description":"` + long[:len(long)-len("é")] + `"}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelDescription", mock.Anything, "t1", mock.Anything).Return(nil)
			},
			wantCode: http.StatusOK,
		},
		{name: "too long", id: "t1", body: `{"description":"` + long + `"}`, wantCode: http.StatusBadRequest, wantBody: "longer than 500 characters"},
		{name: "bad json", id: "t1", body: `{bad}`, wantCode: http.StatusBadRequest},
		{
			name: "lookup error", id: "t1", body: `{"description":"x"}`,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "t1").Return(nil, errors.New("db error")) },
			wantCode: http.StatusInternalServerError,
		},
		{
			name: "not found", id: "gone", body: `{"description":"x"}`,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil) },
			wantCode: http.StatusNotFound,
		},
		{
			name: "update error", id: "t1", body: `{"description":"x"}`,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{ChannelID: "t1"}, nil)
				s.store.On("UpdateChannelDescription", mock.Anything, "t1", "x").Return(errors.New("db error"))
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
			rec := s.testRequest("POST", "/api/channels/"+tt.id+"/description", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestSetChannelDescription_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/description", srv.handleSetChannelDescription)

	req, _ := http.NewRequest("POST", "/api/channels/t1/description", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}
