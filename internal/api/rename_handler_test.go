package api

import (
	"errors"
	"net/http"

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
