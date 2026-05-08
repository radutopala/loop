package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// --- EnsureChannel tests ---

func (s *ServerSuite) TestEnsureChannelSuccess() {
	s.channels.On("EnsureChannel", mock.Anything, "/home/user/dev/loop", "").
		Return("ch-123", nil)

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/home/user/dev/loop"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-123", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelWithPlatform() {
	s.channels.On("EnsureChannel", mock.Anything, "/home/user/dev/loop", "discord").
		Return("ch-discord-1", nil)

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/home/user/dev/loop","platform":"discord"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-discord-1", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelMissingDirPath() {
	rec := s.testRequest("POST", "/api/channels", `{"dir_path":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureChannelError() {
	s.channels.On("EnsureChannel", mock.Anything, "/path", "").
		Return("", errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/path"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- EnsureAllChannels tests ---

func (s *ServerSuite) TestEnsureAllChannelsSuccess() {
	s.channels.On("EnsureChannelAllPlatforms", mock.Anything, "/home/user/dev/loop").
		Return([]EnsureResult{
			{Platform: "local", ChannelID: "ch-local", Created: true},
			{Platform: "discord", ChannelID: "ch-discord", Created: false},
		}, nil)

	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":"/home/user/dev/loop"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []EnsureResult
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureAllChannelsMissingDirPath() {
	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureAllChannelsBadJSON() {
	rec := s.testRequest("POST", "/api/channels/ensure-all", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureAllChannelsError() {
	s.channels.On("EnsureChannelAllPlatforms", mock.Anything, "/path").
		Return(nil, errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":"/path"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- CreateChannel tests ---

func (s *ServerSuite) TestCreateChannelSuccess() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "", "", "").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelMissingName() {
	rec := s.testRequest("POST", "/api/channels/create", `{"name":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateChannelWithAuthorID() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "user-42", "", "").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial","author_id":"user-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelWithChannelID() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "", "source-ch", "").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial","channel_id":"source-ch"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelError() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "", "", "").
		Return("", errors.New("create failed"))

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- DeleteThread tests ---

func (s *ServerSuite) TestDeleteThreadSuccess() {
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteThreadError() {
	s.threads.On("DeleteThread", mock.Anything, "thread-1").
		Return(errors.New("delete failed"))

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.threads.AssertExpectations(s.T())
}

// --- DeleteChannel tests ---

func (s *ServerSuite) TestDeleteChannelNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/channels/{id}", srv.handleDeleteChannel)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestDeleteChannelSuccess() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestDeleteChannelCleansUpContainers() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "agent-c1", ChannelID: "ch-1", Type: container.ContainerTypeAgent},
			{ContainerID: "shell-c2", ChannelID: "ch-1", Type: container.ContainerTypeShell},
			{ContainerID: "chrome-c3", ChannelID: "ch-1", Type: container.ContainerTypeChrome}, // skipped
		},
	}
	reg.On("RemoveContainer", mock.Anything, "agent-c1").Return(nil)
	reg.On("RemoveContainer", mock.Anything, "shell-c2").Return(nil)

	browserMgr := new(mockBrowserProvider)
	browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c3", nil)
	reg.On("RemoveContainer", mock.Anything, "chrome-c3").Return(nil)

	s.srv.containerRegistry = reg
	s.srv.SetBrowserProvider(browserMgr)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "agent-c1")
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "shell-c2")
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "chrome-c3")
	browserMgr.AssertCalled(s.T(), "StopBrowser", mock.Anything, "ch-1")
}

func (s *ServerSuite) TestDeleteChannelContainerRemoveError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "agent-c1", ChannelID: "ch-1", Type: container.ContainerTypeAgent},
		},
	}
	reg.On("RemoveContainer", mock.Anything, "agent-c1").Return(errors.New("remove failed"))

	s.srv.containerRegistry = reg

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	// Still returns 204 — cleanup errors are logged, not surfaced.
	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestDeleteChannelChromeRemoveError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	reg := &mockContainerManager{byChannel: []*container.ContainerInfo{}}

	browserMgr := new(mockBrowserProvider)
	browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c1", nil)
	reg.On("RemoveContainer", mock.Anything, "chrome-c1").Return(errors.New("chrome remove failed"))

	s.srv.containerRegistry = reg
	s.srv.SetBrowserProvider(browserMgr)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	// Still returns 204 — cleanup errors are logged, not surfaced.
	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestDeleteChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/missing", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestDeleteChannelGetError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return((*db.Channel)(nil), errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-err", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestDeleteChannelChildrenError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(&db.Channel{ChannelID: "ch-err"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-err").
		Return(errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-err", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestDeleteChannelLockedReturnsConflict() {
	s.store.On("GetChannel", mock.Anything, "ch-locked").
		Return(&db.Channel{ChannelID: "ch-locked", Locked: true}, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-locked", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusConflict, w.Code)
	require.Contains(s.T(), w.Body.String(), "locked")
	// Must not progress to children or self deletion.
	s.store.AssertNotCalled(s.T(), "DeleteChannelsByParentID", mock.Anything, "ch-locked")
	s.store.AssertNotCalled(s.T(), "DeleteChannel", mock.Anything, "ch-locked")
}

func (s *ServerSuite) TestDeleteThreadLockedReturnsConflict() {
	s.threads.On("DeleteThread", mock.Anything, "thread-locked").Return(ErrChannelLocked)

	rec := s.testRequest("DELETE", "/api/threads/thread-locked", "")

	require.Equal(s.T(), http.StatusConflict, rec.Code)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSetChannelLockedSuccess() {
	s.srv.SetEventsHub(NewEventsHub(slog.New(slog.NewTextHandler(io.Discard, nil))))
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("UpdateChannelLocked", mock.Anything, "ch-1", true).Return(nil)

	rec := s.testRequest("PATCH", "/api/channels/ch-1/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSetChannelLockedNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("PATCH", "/api/channels/missing/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
	s.store.AssertNotCalled(s.T(), "UpdateChannelLocked", mock.Anything, "missing", mock.Anything)
}

func (s *ServerSuite) TestSetChannelLockedGetError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return((*db.Channel)(nil), errors.New("db error"))

	rec := s.testRequest("PATCH", "/api/channels/ch-err/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSetChannelLockedUpdateError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("UpdateChannelLocked", mock.Anything, "ch-1", true).
		Return(errors.New("db error"))

	rec := s.testRequest("PATCH", "/api/channels/ch-1/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSetChannelLockedInvalidJSON() {
	rec := s.testRequest("PATCH", "/api/channels/ch-1/lock", `{not json`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSetChannelLockedNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/channels/{id}/lock", srv.handleSetChannelLocked)

	req := httptest.NewRequest(http.MethodPatch, "/api/channels/ch-1/lock", strings.NewReader(`{"locked":true}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestDeleteChannelDeleteError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(&db.Channel{ChannelID: "ch-err"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-err").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-err").
		Return(errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-err", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

// --- SearchChannels tests ---

func (s *ServerSuite) TestSearchChannelsSuccess() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", ParentID: "ch-1", Active: false, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), "ch-1", resp[0].ChannelID)
	require.Equal(s.T(), "general", resp[0].Name)
	require.True(s.T(), resp[0].Active)
	require.Equal(s.T(), "ch-2", resp[1].ChannelID)
	require.Equal(s.T(), "ch-1", resp[1].ParentID)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsWithQuery() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels?query=gen", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "general", resp[0].Name)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsWithQueryNoMatch() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels?query=nonexistent", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsEmpty() {
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsFiltersByPlatform() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "local-ch", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "ch-2", Name: "discord-ch", Platform: types.PlatformDiscord, Active: true},
		{ChannelID: "ch-3", Name: "slack-ch", Platform: types.PlatformSlack, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels?platform=local", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "local-ch", resp[0].Name)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsRunningFromContainers() {
	reg := &mockContainerManager{
		runningIDs: map[string]struct{}{"ch-1": {}},
	}
	s.srv.SetContainerRegistry(reg)

	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "running-ch", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "ch-2", Name: "idle-ch", Platform: types.PlatformLocal, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.True(s.T(), resp[0].ContainerRunning)
	require.False(s.T(), resp[1].ContainerRunning)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsAgentRunning() {
	chatLister := new(MockActiveChatLister)
	s.srv.SetActiveChatLister(chatLister)

	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "active-chat", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "ch-2", Name: "idle-chat", Platform: types.PlatformLocal, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	chatLister.On("ActiveChatChannelIDs").Return(map[string]struct{}{"ch-1": {}})

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.True(s.T(), resp[0].AgentRunning)
	require.False(s.T(), resp[1].AgentRunning)
	s.store.AssertExpectations(s.T())
	chatLister.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsError() {
	s.store.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsDirPathFallback() {
	s.srv.SetLoopDir("/home/test/.loop")
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "no-dir", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "has-dir", DirPath: "/custom/path", Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), "/home/test/.loop/ch-1/work", resp[0].DirPath)
	require.Equal(s.T(), "/custom/path", resp[1].DirPath)
	s.srv.SetLoopDir("")
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsBranch() {
	// Create a temp git repo so gitBranch returns a real branch name.
	dir := s.T().TempDir()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t.com"},
		{"git", "config", "user.name", "T"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	require.NoError(s.T(), add.Run())
	ci := exec.Command("git", "commit", "-m", "init")
	ci.Dir = dir
	require.NoError(s.T(), ci.Run())

	channels := []*db.Channel{
		{ChannelID: "ch-br", Name: "with-branch", DirPath: dir, Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.NotEmpty(s.T(), resp[0].Branch)
}

func (s *ServerSuite) TestSearchChannelsDiffStats() {
	// Create a temp git repo with a committed file, then modify it and add an untracked file.
	dir := s.T().TempDir()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t.com"},
		{"git", "config", "user.name", "T"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	// Commit an initial file.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("line1\nline2\n"), 0o644))
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	require.NoError(s.T(), add.Run())
	ci := exec.Command("git", "commit", "-m", "init")
	ci.Dir = dir
	require.NoError(s.T(), ci.Run())

	// Modify tracked file (1 insertion, 1 deletion).
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("line1\nchanged\n"), 0o644))
	// Create an untracked file with 3 lines.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("a\nb\nc\n"), 0o644))

	channels := []*db.Channel{
		{ChannelID: "ch-diff", Name: "with-diff", DirPath: dir, Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	// Tracked: 1 insertion + 1 deletion; Untracked: 3 lines counted as additions.
	require.Equal(s.T(), 4, resp[0].DiffAdditions, "expected 1 tracked insertion + 3 untracked lines")
	require.Equal(s.T(), 1, resp[0].DiffDeletions, "expected 1 tracked deletion")
}
