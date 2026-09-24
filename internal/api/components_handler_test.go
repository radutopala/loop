package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/chatcomponents"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

func (s *ServerSuite) registerComponentRoutes() {
	s.mux.HandleFunc("GET /api/components", s.srv.handleListComponents)
	s.mux.HandleFunc("POST /api/components", s.srv.handleShowComponent)
}

func (s *ServerSuite) TestListComponents() {
	s.registerComponentRoutes()
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{
			LoopDir:        "/home/testuser/.loop",
			ChatComponents: []config.ChatComponent{{Name: "reaction", Description: "Reactions"}},
		}, nil
	}
	s.srv.readFile = func(p string) ([]byte, error) {
		if p == "/home/testuser/.loop/components/reaction/guide.md" {
			return []byte("Balance it."), nil
		}
		return nil, os.ErrNotExist
	}

	tests := []struct {
		name  string
		path  string
		check func(body string)
	}{
		{
			name: "json lists every template with its guide",
			path: "/api/components",
			check: func(body string) {
				var got []chatcomponents.Template
				require.NoError(s.T(), json.Unmarshal([]byte(body), &got))
				require.Len(s.T(), got, 4)
				require.Equal(s.T(), "math", got[0].Name)
				require.Equal(s.T(), chatcomponents.Template{Name: "reaction", Description: "Reactions", Guide: "Balance it."}, got[3])
			},
		},
		{
			name: "guide format is the text the MCP tool returns",
			path: "/api/components?format=guide",
			check: func(body string) {
				require.Contains(s.T(), body, "\n## reaction\nReactions\n\nBalance it.\n")
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			rec := s.testRequest("GET", tc.path, "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			tc.check(rec.Body.String())
		})
	}
}

func (s *ServerSuite) TestListComponentsConfigError() {
	s.registerComponentRoutes()
	s.srv.configs.load = func() (*config.Config, error) { return nil, errors.New("boom") }

	rec := s.testRequest("GET", "/api/components", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestShowComponent() {
	s.registerComponentRoutes()
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{LoopDir: "/home/testuser/.loop"}, nil }
	s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) { return base, nil }
	hub, caps := newCaptureHub()
	s.srv.SetEventsHub(hub)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ID: 7, ChannelID: "ch-1", DirPath: "/projects/app", Platform: types.PlatformLocal}, nil)
	s.store.On("RunningMessageID", mock.Anything, "ch-1").Return("user-msg-1", nil)
	var stored *db.Message
	s.store.On("InsertMessage", mock.Anything, mock.AnythingOfType("*db.Message")).Run(func(args mock.Arguments) {
		stored = args.Get(1).(*db.Message)
		stored.ID = 99
	}).Return(nil)

	rec := s.testRequest("POST", "/api/components?channel_id=ch-1", `{"template":"math","title":"  Fracții\n algebrice ","html":"<p>x</p>","js":"go()"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	var resp showComponentResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(s.T(), strings.HasPrefix(resp.MsgID, "component-"))

	require.NotNil(s.T(), stored)
	require.Equal(s.T(), resp.MsgID, stored.MsgID)
	require.Equal(s.T(), int64(7), stored.ChatID)
	require.Equal(s.T(), "ch-1", stored.ChannelID)
	require.Equal(s.T(), "agent", stored.AuthorName)
	require.True(s.T(), stored.IsBot)
	require.True(s.T(), stored.IsProcessed)
	require.False(s.T(), stored.IsTriggered)
	require.Equal(s.T(), "user-msg-1", stored.TriggerMsgID)
	require.True(s.T(), strings.HasPrefix(stored.Content, "```loop-component math Fracții algebrice\n<!doctype html>"))
	require.Contains(s.T(), stored.Content, `<div class="paper"><p>x</p></div>`)
	require.Contains(s.T(), stored.Content, "go()")

	evs := caps.snapshot()
	require.Len(s.T(), evs, 1)
	require.Equal(s.T(), EventMessageCreated, evs[0].Type)
	require.Equal(s.T(), "ch-1", evs[0].ChannelID)
	require.Equal(s.T(), events.MessageEventData{
		ID:           99,
		MsgID:        resp.MsgID,
		AuthorName:   "agent",
		Content:      stored.Content,
		IsBot:        true,
		IsProcessed:  true,
		TriggerMsgID: "user-msg-1",
	}, evs[0].Data)
}

func (s *ServerSuite) TestShowComponentWithoutEventsHub() {
	s.registerComponentRoutes()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/projects/app", Platform: types.PlatformLocal}, nil)
	s.store.On("RunningMessageID", mock.Anything, "ch-1").Return("", nil)
	s.store.On("InsertMessage", mock.Anything, mock.AnythingOfType("*db.Message")).Return(nil)

	rec := s.testRequest("POST", "/api/components?channel_id=ch-1", `{"template":"canvas","js":"ctx.fillRect(0,0,1,1)"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestShowComponentErrors() {
	local := &db.Channel{ChannelID: "ch-1", DirPath: "/projects/app", Platform: types.PlatformLocal}
	valid := `{"template":"math","html":"<p>x</p>"}`
	tests := []struct {
		name     string
		path     string
		body     string
		setup    func()
		wantCode int
		wantBody string
	}{
		{name: "missing channel", path: "/api/components", body: valid, wantCode: http.StatusBadRequest, wantBody: "channel_id is required"},
		{name: "invalid json", body: "{", wantCode: http.StatusBadRequest, wantBody: "invalid request body"},
		{
			name:     "too large",
			body:     `{"template":"math","html":"` + strings.Repeat("x", maxComponentBytes) + `"}`,
			wantCode: http.StatusRequestEntityTooLarge,
			wantBody: "under 512 KB",
		},
		{name: "no content", body: `{"template":"math","html":"  "}`, wantCode: http.StatusBadRequest, wantBody: "html or js is required"},
		{
			name:     "channel lookup fails",
			body:     valid,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db down")) },
			wantCode: http.StatusInternalServerError,
			wantBody: "failed to look up channel",
		},
		{
			name:     "unknown channel",
			body:     valid,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil) },
			wantCode: http.StatusNotFound,
			wantBody: "channel not found",
		},
		{
			name: "not the desktop app",
			body: valid,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformSlack}, nil)
			},
			wantCode: http.StatusBadRequest,
			wantBody: "desktop app only; answer in text instead",
		},
		{
			name: "config fails to load",
			body: valid,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(local, nil)
				s.srv.configs.load = func() (*config.Config, error) { return nil, errors.New("bad config") }
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "failed to load config",
		},
		{
			name:     "unknown template",
			body:     `{"template":"chart","html":"<p>x</p>"}`,
			setup:    func() { s.store.On("GetChannel", mock.Anything, "ch-1").Return(local, nil) },
			wantCode: http.StatusBadRequest,
			wantBody: `unknown template "chart"; available: math, canvas, react`,
		},
		{
			name: "running turn lookup fails",
			body: valid,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(local, nil)
				s.store.On("RunningMessageID", mock.Anything, "ch-1").Return("", errors.New("db down"))
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "failed to look up the running turn",
		},
		{
			name: "insert fails",
			body: valid,
			setup: func() {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(local, nil)
				s.store.On("RunningMessageID", mock.Anything, "ch-1").Return("", nil)
				s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(errors.New("disk full"))
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "failed to store component",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.registerComponentRoutes()
			if tc.setup != nil {
				tc.setup()
			}
			path := tc.path
			if path == "" {
				path = "/api/components?channel_id=ch-1"
			}
			rec := s.testRequest("POST", path, tc.body)
			require.Equal(s.T(), tc.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tc.wantBody)
		})
	}
}

func (s *ServerSuite) TestShowComponentWithoutStore() {
	s.srv = NewServer(s.scheduler, s.channels, s.threads, nil, s.messages, testLogger())
	s.mux.HandleFunc("POST /api/components", s.srv.handleShowComponent)

	rec := s.testRequest("POST", "/api/components?channel_id=ch-1", `{"template":"math","html":"<p>x</p>"}`)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}
