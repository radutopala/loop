package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/types"
)

type mockLiveSessions struct {
	mock.Mock
}

func (m *mockLiveSessions) EndLiveSession(channelID, reason string) error {
	return m.Called(channelID, reason).Error(0)
}

func (m *mockLiveSessions) StopLiveTask(channelID, taskID string) error {
	return m.Called(channelID, taskID).Error(0)
}

func (m *mockLiveSessions) LiveSession(ctx context.Context, channelID string) orchestrator.LiveStatus {
	return m.Called(ctx, channelID).Get(0).(orchestrator.LiveStatus)
}

func (m *mockLiveSessions) SweepLiveSessions(ctx context.Context) {
	m.Called(ctx)
}

func (m *mockLiveSessions) WarmLiveSession(ctx context.Context, channelID, authorID string) (bool, error) {
	args := m.Called(ctx, channelID, authorID)
	return args.Bool(0), args.Error(1)
}

// useLive gives the suite's server a mock of its live sessions and
// registers the live and side chat routes.
func (s *ServerSuite) useLive() *mockLiveSessions {
	live := new(mockLiveSessions)
	s.srv.SetLiveSessions(live)
	s.srv.registerLiveRoutes(s.mux)
	return live
}

func (s *ServerSuite) TestLiveRoutesNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, testLogger())
	mux := http.NewServeMux()
	srv.registerLiveRoutes(mux)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/channels/ch-1/live", ""},
		{http.MethodPost, "/api/channels/ch-1/live", ""},
		{http.MethodDelete, "/api/channels/ch-1/live", ""},
		{http.MethodPost, "/api/channels/ch-1/live/tasks/b1/stop", ""},
		{http.MethodPost, "/api/channels/ch-1/side-chats", `{"mode":"fork"}`},
		{http.MethodGet, "/api/channels/ch-1/side-chats", ""},
		{http.MethodDelete, "/api/side-chats/side-1", ""},
	} {
		var body io.Reader
		if tc.body != "" {
			body = strings.NewReader(tc.body)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, body))
		require.Equal(s.T(), http.StatusNotImplemented, w.Code, tc.method+" "+tc.path)
	}
}

func (s *ServerSuite) TestGetLive() {
	live := s.useLive()
	status := orchestrator.LiveStatus{Running: true, Enabled: true, LiveInfo: agent.LiveInfo{ChannelID: "ch-1", SessionID: "sess-1"}}
	live.On("LiveSession", mock.Anything, "ch-1").Return(status)

	rec := s.testRequest(http.MethodGet, "/api/channels/ch-1/live", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var got orchestrator.LiveStatus
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&got))
	require.Equal(s.T(), status, got)
}

func (s *ServerSuite) TestWarmLive() {
	tests := []struct {
		name    string
		started bool
		err     error
		want    int
		body    string
	}{
		{name: "started", started: true, want: http.StatusOK, body: `{"started":true}`},
		{name: "not started", want: http.StatusOK, body: `{"started":false}`},
		{name: "already running", err: container.ErrLiveSessionRunning, want: http.StatusOK, body: `{"started":false}`},
		{name: "unavailable", err: orchestrator.ErrLiveUnavailable, want: http.StatusServiceUnavailable},
		{name: "fails", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			live := s.useLive()
			live.On("WarmLiveSession", mock.Anything, "ch-1", local.DefaultAuthorID).Return(tc.started, tc.err)

			rec := s.testRequest(http.MethodPost, "/api/channels/ch-1/live", "")
			require.Equal(s.T(), tc.want, rec.Code)
			if tc.body != "" {
				require.JSONEq(s.T(), tc.body, rec.Body.String())
			}
			live.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestStopLive() {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "stopped", want: http.StatusNoContent},
		{name: "no session", err: container.ErrNoLiveSession, want: http.StatusNotFound},
		{name: "fails", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			live := s.useLive()
			live.On("EndLiveSession", "ch-1", "stopped").Return(tc.err)

			rec := s.testRequest(http.MethodDelete, "/api/channels/ch-1/live", "")
			require.Equal(s.T(), tc.want, rec.Code)
			live.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestStopLiveTask() {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "stopped", want: http.StatusNoContent},
		{name: "no session", err: container.ErrNoLiveSession, want: http.StatusNotFound},
		{name: "no task", err: container.ErrNoLiveTask, want: http.StatusNotFound},
		{name: "unavailable", err: orchestrator.ErrLiveUnavailable, want: http.StatusServiceUnavailable},
		{name: "fails", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			live := s.useLive()
			live.On("StopLiveTask", "ch-1", "b1").Return(tc.err)

			rec := s.testRequest(http.MethodPost, "/api/channels/ch-1/live/tasks/b1/stop", "")
			require.Equal(s.T(), tc.want, rec.Code)
			live.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestCreateSideChat() {
	parent := &db.Channel{ChannelID: "ch-1", GuildID: "g", Name: "research", DirPath: "/proj", Platform: types.PlatformSlack, SessionID: "sess-1"}
	tests := []struct {
		name        string
		mode        string
		wantSession string
		wantFork    bool
	}{
		{name: "fork", mode: "fork", wantSession: "sess-1", wantFork: true},
		{name: "fresh", mode: "fresh"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.useLive()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil)
			var inserted *db.Channel
			s.store.On("InsertSideChat", mock.Anything, mock.MatchedBy(func(ch *db.Channel) bool {
				inserted = ch
				return true
			})).Return(nil)

			rec := s.testRequest(http.MethodPost, "/api/channels/ch-1/side-chats", `{"mode":"`+tc.mode+`"}`)
			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			var resp createSideChatResponse
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
			require.NotNil(s.T(), inserted)
			require.Equal(s.T(), inserted.ChannelID, resp.ChannelID)
			require.Regexp(s.T(), `^side-[0-9a-f]{12}$`, resp.ChannelID)
			require.Equal(s.T(), "research ("+tc.mode+")", inserted.Name)
			require.Equal(s.T(), types.PlatformLocal, inserted.Platform, "a side chat stays in the app whatever its parent's platform")
			require.Equal(s.T(), "ch-1", inserted.ParentID)
			require.Equal(s.T(), "/proj", inserted.DirPath)
			require.Equal(s.T(), "g", inserted.GuildID)
			require.Equal(s.T(), tc.wantSession, inserted.SessionID)
			require.Equal(s.T(), tc.wantFork, inserted.ForkPending)
		})
	}
}

func (s *ServerSuite) TestCreateSideChatErrors() {
	tests := []struct {
		name      string
		body      string
		parent    *db.Channel
		parentErr error
		insertErr error
		want      int
	}{
		{name: "bad body", body: `{`, want: http.StatusBadRequest},
		{name: "bad mode", body: `{"mode":"resume"}`, want: http.StatusBadRequest},
		{name: "parent lookup fails", body: `{"mode":"fresh"}`, parentErr: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "no parent", body: `{"mode":"fresh"}`, want: http.StatusNotFound},
		{name: "hidden parent", body: `{"mode":"fresh"}`, parent: &db.Channel{ChannelID: "ch-1", Kind: db.ChannelKindLearn}, want: http.StatusConflict},
		{name: "side chat parent", body: `{"mode":"fresh"}`, parent: &db.Channel{ChannelID: "ch-1", Kind: db.ChannelKindSide}, want: http.StatusConflict},
		{name: "fork with no session", body: `{"mode":"fork"}`, parent: &db.Channel{ChannelID: "ch-1"}, want: http.StatusConflict},
		{name: "parent gone", body: `{"mode":"fresh"}`, parent: &db.Channel{ChannelID: "ch-1"}, insertErr: db.ErrParentGone, want: http.StatusNotFound},
		{name: "insert fails", body: `{"mode":"fresh"}`, parent: &db.Channel{ChannelID: "ch-1"}, insertErr: errors.New("db down"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.useLive()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(tc.parent, tc.parentErr)
			s.store.On("InsertSideChat", mock.Anything, mock.Anything).Return(tc.insertErr)

			rec := s.testRequest(http.MethodPost, "/api/channels/ch-1/side-chats", tc.body)
			require.Equal(s.T(), tc.want, rec.Code, rec.Body.String())
		})
	}
}

func (s *ServerSuite) TestListSideChats() {
	tests := []struct {
		name  string
		sides []*db.Channel
		err   error
		want  int
		ids   []string
	}{
		{name: "some", sides: []*db.Channel{{ChannelID: "side-1", Kind: db.ChannelKindSide}}, want: http.StatusOK, ids: []string{"side-1"}},
		{name: "none", want: http.StatusOK, ids: []string{}},
		{name: "fails", err: errors.New("db down"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.useLive()
			s.store.On("ListSideChats", mock.Anything, "ch-1").Return(tc.sides, tc.err)

			rec := s.testRequest(http.MethodGet, "/api/channels/ch-1/side-chats", "")
			require.Equal(s.T(), tc.want, rec.Code)
			if tc.ids == nil {
				return
			}
			var got []db.Channel
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&got))
			ids := []string{}
			for _, ch := range got {
				ids = append(ids, ch.ChannelID)
			}
			require.Equal(s.T(), tc.ids, ids)
		})
	}
}

func (s *ServerSuite) TestDeleteSideChat() {
	side := &db.Channel{ChannelID: "side-1", ParentID: "ch-1", Kind: db.ChannelKindSide}
	tests := []struct {
		name      string
		noLive    bool
		ch        *db.Channel
		lookupErr error
		deleteErr error
		want      int
	}{
		{name: "deleted", ch: side, want: http.StatusNoContent},
		{name: "deleted without live sessions", ch: side, noLive: true, want: http.StatusNoContent},
		{name: "lookup fails", lookupErr: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "missing", want: http.StatusNotFound},
		{name: "not a side chat", ch: &db.Channel{ChannelID: "side-1", ParentID: "ch-1"}, want: http.StatusNotFound},
		{name: "delete fails", ch: side, deleteErr: errors.New("nope"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			live := s.useLive()
			if tc.noLive {
				s.srv.live = nil
			}
			live.On("EndLiveSession", "side-1", "closed").Return(container.ErrNoLiveSession)
			live.On("SweepLiveSessions", mock.Anything).Return()
			s.store.On("GetChannel", mock.Anything, "side-1").Return(tc.ch, tc.lookupErr)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
			s.store.On("ListHiddenThreads", mock.Anything, "side-1").Return([]*db.Channel(nil), nil)
			s.threads.On("DeleteThread", mock.Anything, "side-1").Return(tc.deleteErr)

			rec := s.testRequest(http.MethodDelete, "/api/side-chats/side-1", "")
			require.Equal(s.T(), tc.want, rec.Code, rec.Body.String())
			switch {
			case tc.want == http.StatusNoContent && !tc.noLive:
				live.AssertCalled(s.T(), "EndLiveSession", "side-1", "closed")
				live.AssertCalled(s.T(), "SweepLiveSessions", mock.Anything)
				s.threads.AssertCalled(s.T(), "DeleteThread", mock.Anything, "side-1")
			case tc.want == http.StatusNoContent:
				live.AssertNotCalled(s.T(), "EndLiveSession", mock.Anything, mock.Anything)
				s.threads.AssertCalled(s.T(), "DeleteThread", mock.Anything, "side-1")
			case tc.deleteErr == nil:
				s.threads.AssertNotCalled(s.T(), "DeleteThread", mock.Anything, mock.Anything)
			}
		})
	}
}

// TestDeleteChannelSweepsLive checks a deleted channel's live sessions, and
// its threads', are ended.
func (s *ServerSuite) TestDeleteChannelSweepsLive() {
	live := s.useLive()
	live.On("SweepLiveSessions", mock.Anything).Return()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-1").Return([]string(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "ch-1").Return([]*db.Channel(nil), nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	rec := s.testRequest(http.MethodDelete, "/api/channels/ch-1", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	live.AssertCalled(s.T(), "SweepLiveSessions", mock.Anything)
}
