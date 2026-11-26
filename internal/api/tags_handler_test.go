package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// ── handleEditChannelTags ──

func (s *ServerSuite) TestEditChannelTags() {
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
			name: "adds and removes, normalized",
			id:   "t1", body: `{"add":["#Web"," api "],"remove":["Old"]}`,
			setup: func() {
				s.store.On("UpdateChannelTags", mock.Anything, "t1", []string{"api", "web"}, []string{"old"}).Return([]string{"api", "web"}, nil)
			},
			withHub:  true,
			wantCode: http.StatusOK, wantBody: `{"channel_id":"t1","tags":["api","web"]}`,
		},
		{
			name: "removing the last one returns an empty list",
			id:   "t1", body: `{"remove":["web"]}`,
			setup: func() {
				s.store.On("UpdateChannelTags", mock.Anything, "t1", []string{}, []string{"web"}).Return(nil, nil)
			},
			withHub:  true,
			wantCode: http.StatusOK, wantBody: `"tags":[]`,
		},
		{name: "bad add", id: "t1", body: `{"add":["two words"]}`, wantCode: http.StatusBadRequest, wantBody: "may hold only"},
		{name: "bad remove", id: "t1", body: `{"remove":["a:b"]}`, wantCode: http.StatusBadRequest, wantBody: "may hold only"},
		{name: "bad json", id: "t1", body: `{bad}`, wantCode: http.StatusBadRequest},
		{
			name: "not found", id: "gone", body: `{"add":["x"]}`,
			setup: func() {
				s.store.On("UpdateChannelTags", mock.Anything, "gone", []string{"x"}, []string{}).Return(nil, sql.ErrNoRows)
			},
			wantCode: http.StatusNotFound,
		},
		{
			name: "too many", id: "t1", body: `{"add":["x"]}`,
			setup: func() {
				s.store.On("UpdateChannelTags", mock.Anything, "t1", []string{"x"}, []string{}).Return(nil, types.ErrTooManyTags)
			},
			wantCode: http.StatusBadRequest, wantBody: "at most 20 tags",
		},
		{
			name: "store error", id: "t1", body: `{"add":["x"]}`,
			setup: func() {
				s.store.On("UpdateChannelTags", mock.Anything, "t1", []string{"x"}, []string{}).Return(nil, errors.New("db error"))
			},
			wantCode: http.StatusInternalServerError, wantBody: "updating tags: db error",
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
			rec := s.testRequest("POST", "/api/channels/"+tt.id+"/tags", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestListTags() {
	s.Run("lists", func() {
		s.SetupTest()
		s.store.On("ListChannelTags", mock.Anything).Return([]db.TagCount{{Tag: "api", Count: 2}}, nil)
		rec := s.testRequest("GET", "/api/tags", "")
		require.Equal(s.T(), http.StatusOK, rec.Code)
		require.JSONEq(s.T(), `{"tags":[{"tag":"api","count":2}]}`, rec.Body.String())
	})
	s.Run("store error", func() {
		s.SetupTest()
		s.store.On("ListChannelTags", mock.Anything).Return(nil, errors.New("db error"))
		rec := s.testRequest("GET", "/api/tags", "")
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	})
}

func (s *ServerSuite) TestTags_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/tags", srv.handleEditChannelTags)
	mux.HandleFunc("GET /api/tags", srv.handleListTags)
	for _, path := range []string{"POST /api/channels/t1/tags", "GET /api/tags"} {
		method, url, _ := strings.Cut(path, " ")
		req, _ := http.NewRequest(method, url, nil)
		rec := newRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, path)
	}
}
