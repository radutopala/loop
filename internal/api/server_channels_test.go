package api

import (
	"context"
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
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/types"
)

// --- EnsureChannel tests ---

func (s *ServerSuite) TestEnsureChannelSuccess() {
	dir := s.T().TempDir()
	s.channels.On("EnsureChannel", mock.Anything, dir, "").
		Return("ch-123", nil)

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"`+dir+`/"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-123", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelWithPlatform() {
	dir := s.T().TempDir()
	s.channels.On("EnsureChannel", mock.Anything, dir, "discord").
		Return("ch-discord-1", nil)

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"`+dir+`","platform":"discord"}`)

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
	dir := s.T().TempDir()
	s.channels.On("EnsureChannel", mock.Anything, dir, "").
		Return("", errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"`+dir+`"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- dir_path validation ---

func (s *ServerSuite) TestEnsureChannelInvalidDirPath() {
	parent := s.T().TempDir()
	project := filepath.Join(parent, "project")
	state := filepath.Join(project, "state")
	require.NoError(s.T(), os.MkdirAll(state, 0o755))
	file := filepath.Join(parent, "file.txt")
	require.NoError(s.T(), os.WriteFile(file, []byte("x"), 0o644))
	// A symlink to the project's parent: lexically unrelated to the protected
	// dir, but it resolves to an ancestor of it.
	alias := filepath.Join(s.T().TempDir(), "alias")
	require.NoError(s.T(), os.Symlink(parent, alias))
	s.srv.SetProtectedDirs([]string{filepath.Join(parent, "missing-protected"), state + "/"})

	tests := []struct {
		name    string
		dirPath string
		wantMsg string
	}{
		{"relative", "relative/dir", "must be an absolute path"},
		{"missing", filepath.Join(parent, "nope"), "is not an existing directory"},
		{"file", file, "is not a directory"},
		{"equals protected", state, "can't be a project folder: it contains "},
		{"ancestor of protected", project, "can't be a project folder: it contains "},
		{"root", "/", "can't be a project folder: it contains "},
		{"symlinked ancestor", alias, "can't be a project folder: it contains "},
		{"dotdot to ancestor", filepath.Join(state, "..", ".."), "can't be a project folder: it contains "},
	}
	for _, tc := range tests {
		for _, endpoint := range []string{"/api/channels", "/api/channels/ensure-all"} {
			s.Run(tc.name+" "+endpoint, func() {
				body, err := json.Marshal(map[string]string{"dir_path": tc.dirPath})
				require.NoError(s.T(), err)
				rec := s.testRequest("POST", endpoint, string(body))
				require.Equal(s.T(), http.StatusBadRequest, rec.Code)
				require.Contains(s.T(), rec.Body.String(), tc.wantMsg)
			})
		}
	}
	s.channels.AssertNotCalled(s.T(), "EnsureChannel", mock.Anything, mock.Anything, mock.Anything)
	s.channels.AssertNotCalled(s.T(), "EnsureChannelAllPlatforms", mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestEnsureChannelBesideProtectedDirAllowed() {
	parent := s.T().TempDir()
	state := filepath.Join(parent, "state")
	sibling := filepath.Join(parent, "state-sibling")
	require.NoError(s.T(), os.MkdirAll(state, 0o755))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(state, "inner"), 0o755))
	require.NoError(s.T(), os.MkdirAll(sibling, 0o755))
	s.srv.SetProtectedDirs([]string{state})

	for _, dir := range []string{sibling, filepath.Join(state, "inner")} {
		s.channels.On("EnsureChannel", mock.Anything, dir, "").Return("ch-1", nil).Once()
		rec := s.testRequest("POST", "/api/channels", `{"dir_path":"`+dir+`"}`)
		require.Equal(s.T(), http.StatusOK, rec.Code, dir)
	}
	s.channels.AssertExpectations(s.T())
}

// --- EnsureAllChannels tests ---

func (s *ServerSuite) TestEnsureAllChannelsSuccess() {
	dir := s.T().TempDir()
	s.channels.On("EnsureChannelAllPlatforms", mock.Anything, dir).
		Return([]EnsureResult{
			{Platform: "local", ChannelID: "ch-local", Created: true},
			{Platform: "discord", ChannelID: "ch-discord", Created: false},
		}, nil)

	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":"`+dir+`"}`)
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
	dir := s.T().TempDir()
	s.channels.On("EnsureChannelAllPlatforms", mock.Anything, dir).
		Return(nil, errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":"`+dir+`"}`)
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
	s.store.On("GetChannel", mock.Anything, "thread-1").Return((*db.Channel)(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "thread-1").Return([]*db.Channel(nil), nil)
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)
	hub := NewEventsHub(testLogger())
	var events []Event
	hub.captureHook = func(e Event) { events = append(events, e) }
	s.srv.eventsHub = hub

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.threads.AssertExpectations(s.T())
	// The app windows drop it from their sidebars.
	require.Len(s.T(), events, 1)
	require.Equal(s.T(), EventChannelDeleted, events[0].Type)
	require.Equal(s.T(), "thread-1", events[0].ChannelID)
}

func (s *ServerSuite) TestDeleteThreadError() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return((*db.Channel)(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "thread-1").Return([]*db.Channel{{ChannelID: "learn-1"}}, nil)
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
	s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-1").Return([]string(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "ch-1").Return([]*db.Channel(nil), nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

// TestDeleteChannelCleansUpContainers covers the containers a deleted
// channel leaves: its and its threads' agent and shell containers are marked
// for removal after the keep-alive, one already pending keeps its timer, and
// Chrome is removed now with its profile.
func (s *ServerSuite) TestDeleteChannelCleansUpContainers() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-1").Return([]string{"t-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "t-1").Return(&db.Channel{ChannelID: "t-1", ParentID: "ch-1"}, nil)
	s.store.On("ListHiddenThreads", mock.Anything, "t-1").Return([]*db.Channel(nil), nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "agent-c1", ChannelID: "ch-1", Type: container.ContainerTypeAgent},
			{ContainerID: "shell-c2", ChannelID: "ch-1", Type: container.ContainerTypeShell},
			{ContainerID: "chrome-c3", ChannelID: "ch-1", Type: container.ContainerTypeChrome},
			{ContainerID: "done-c4", ChannelID: "ch-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusPendingRemoval},
			{ContainerID: "shell-t1", ChannelID: "t-1", Type: container.ContainerTypeShell},
		},
	}
	reg.On("ScheduleRemove", "agent-c1", 5*time.Minute).Return()
	reg.On("ScheduleRemove", "shell-c2", 5*time.Minute).Return()
	reg.On("ScheduleRemove", "shell-t1", 5*time.Minute).Return()
	reg.On("RemoveContainer", mock.Anything, "chrome-c3").Return(nil)

	browserMgr := new(mockBrowserProvider)
	browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c3", nil)
	browserMgr.On("RemoveProfile", mock.Anything, "ch-1").Return(nil)
	browserMgr.On("StopBrowser", mock.Anything, "t-1").Return("", nil)
	browserMgr.On("RemoveProfile", mock.Anything, "t-1").Return(nil)

	s.srv.containerRegistry = reg
	s.srv.containerKeepAlive = 5 * time.Minute
	s.srv.browser.setProviders(browserMgr, s.srv.browser.hostProvider)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	reg.AssertExpectations(s.T())
	browserMgr.AssertExpectations(s.T())
}

// TestDeleteThreadMarksContainers covers a deleted thread's containers:
// its agent and shell containers are marked for removal after the
// keep-alive, and one already pending keeps its timer.
func (s *ServerSuite) TestDeleteThreadMarksContainers() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return((*db.Channel)(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "thread-1").Return([]*db.Channel(nil), nil)
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)
	reg := &mockContainerManager{byChannel: []*container.ContainerInfo{
		{ContainerID: "shell-t1", ChannelID: "thread-1", Type: container.ContainerTypeShell},
		{ContainerID: "agent-t1", ChannelID: "thread-1", Type: container.ContainerTypeAgent},
		{ContainerID: "done-t1", ChannelID: "thread-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusPendingRemoval},
		{ContainerID: "shell-other", ChannelID: "thread-2", Type: container.ContainerTypeShell},
	}}
	reg.On("ScheduleRemove", "shell-t1", time.Minute).Return()
	reg.On("ScheduleRemove", "agent-t1", time.Minute).Return()
	s.srv.containerRegistry = reg
	s.srv.containerKeepAlive = time.Minute

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	reg.AssertExpectations(s.T())
}

// TestDeleteThreadHiddenContainerRemoveError covers a hidden thread's
// container that fails to go: it's logged, and the thread is still deleted.
func (s *ServerSuite) TestDeleteThreadHiddenContainerRemoveError() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return((*db.Channel)(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "thread-1").Return([]*db.Channel{{ChannelID: "learn-1", Kind: db.ChannelKindLearn}}, nil)
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)
	reg := &mockContainerManager{byChannel: []*container.ContainerInfo{
		{ContainerID: "agent-l1", ChannelID: "learn-1", Type: container.ContainerTypeAgent},
	}}
	reg.On("RemoveContainer", mock.Anything, "agent-l1").Return(errors.New("remove failed"))
	s.srv.containerRegistry = reg

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	// Cleanup errors are logged, not surfaced.
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	reg.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteChannelChromeRemoveError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-1").Return([]string(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "ch-1").Return([]*db.Channel(nil), nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	reg := &mockContainerManager{byChannel: []*container.ContainerInfo{}}

	browserMgr := new(mockBrowserProvider)
	browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c1", nil)
	browserMgr.On("RemoveProfile", mock.Anything, "ch-1").Return(errors.New("volume in use"))
	reg.On("RemoveContainer", mock.Anything, "chrome-c1").Return(errors.New("chrome remove failed"))

	s.srv.containerRegistry = reg
	s.srv.browser.setProviders(browserMgr, s.srv.browser.hostProvider)

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
	s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-err").Return([]string(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "ch-err").Return([]*db.Channel(nil), nil)
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
	s.store.On("GetChannel", mock.Anything, "thread-locked").Return((*db.Channel)(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "thread-locked").Return([]*db.Channel(nil), nil)
	s.threads.On("DeleteThread", mock.Anything, "thread-locked").Return(ErrChannelLocked)

	rec := s.testRequest("DELETE", "/api/threads/thread-locked", "")

	require.Equal(s.T(), http.StatusConflict, rec.Code)
	s.threads.AssertExpectations(s.T())
}

// TestDeleteThreadStopsHiddenThreads covers a deleted thread's hidden
// threads: both are stopped (their runs cancelled and forks deleted) and
// lose their containers.
func (s *ServerSuite) TestDeleteThreadStopsHiddenThreads() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return((*db.Channel)(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "thread-1").Return([]*db.Channel{
		{ChannelID: "learn-1", Kind: db.ChannelKindLearn},
		{ChannelID: "explain-1", Kind: db.ChannelKindExplain},
	}, nil)
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)
	canceller := new(MockRunCanceller)
	canceller.On("StopHiddenThread", "learn-1").Return()
	canceller.On("StopHiddenThread", "explain-1").Return()
	s.srv.SetRunCanceller(canceller)
	reg := &mockContainerManager{byChannel: []*container.ContainerInfo{
		{ContainerID: "agent-l1", ChannelID: "learn-1", Type: container.ContainerTypeAgent},
		{ContainerID: "chrome-l1", ChannelID: "learn-1", Type: container.ContainerTypeChrome}, // the BrowserProvider's
		{ContainerID: "agent-e1", ChannelID: "explain-1", Type: container.ContainerTypeAgent},
	}}
	reg.On("RemoveContainer", mock.Anything, "agent-l1").Return(nil)
	reg.On("RemoveContainer", mock.Anything, "agent-e1").Return(nil)
	s.srv.containerRegistry = reg

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	canceller.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteThreadRemovesMCPConfigs() {
	thread := &db.Channel{ChannelID: "t-1", ParentID: "ch-1", DirPath: "/wt"}
	tests := []struct {
		name      string
		thread    *db.Channel
		threadErr error
		parent    *db.Channel
		parentErr error
		learn     *db.Channel
		keepDir   string // the project dir whose config sets keep_mcp_configs
		removeErr error
		want      [][2]string
	}{
		{
			name:   "thread and learn thread",
			thread: thread,
			parent: &db.Channel{ChannelID: "ch-1", DirPath: "/p"},
			learn:  &db.Channel{ChannelID: "learn-1", DirPath: "/wt"},
			want:   [][2]string{{"/wt", "t-1"}, {"/wt", "learn-1"}},
		},
		{
			name:      "a failed removal is logged",
			thread:    thread,
			parent:    &db.Channel{ChannelID: "ch-1", DirPath: "/p"},
			removeErr: errors.New("permission denied"),
			want:      [][2]string{{"/wt", "t-1"}},
		},
		{
			name:    "the parent's project keeps them",
			thread:  thread,
			parent:  &db.Channel{ChannelID: "ch-1", DirPath: "/p"},
			learn:   &db.Channel{ChannelID: "learn-1", DirPath: "/wt"},
			keepDir: "/p",
		},
		{
			name:    "the worktree's config alone doesn't keep them",
			thread:  thread,
			parent:  &db.Channel{ChannelID: "ch-1", DirPath: "/p"},
			keepDir: "/wt",
			want:    [][2]string{{"/wt", "t-1"}},
		},
		{
			name:      "a failed parent lookup falls back to the thread",
			thread:    thread,
			parentErr: errors.New("db error"),
			keepDir:   "/wt",
		},
		{
			name:    "a parent gone meanwhile falls back to the thread",
			thread:  thread,
			keepDir: "/wt",
		},
		{
			name:      "a failed thread lookup skips removal",
			threadErr: errors.New("db error"),
			learn:     &db.Channel{ChannelID: "learn-1", DirPath: "/wt"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.srv.configs.loadProject = func(dir string, base *config.Config) (*config.Config, error) {
				merged := *base
				merged.KeepMCPConfigs = dir == tc.keepDir
				return &merged, nil
			}
			var removed [][2]string
			s.srv.removeMCPConfig = func(dir, id string) error {
				removed = append(removed, [2]string{dir, id})
				return tc.removeErr
			}
			s.store.On("GetChannel", mock.Anything, "t-1").Return(tc.thread, tc.threadErr)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(tc.parent, tc.parentErr)
			var hidden []*db.Channel
			if tc.learn != nil {
				hidden = append(hidden, tc.learn)
			}
			s.store.On("ListHiddenThreads", mock.Anything, "t-1").Return(hidden, nil)
			s.threads.On("DeleteThread", mock.Anything, "t-1").Return(nil)

			rec := s.testRequest("DELETE", "/api/threads/t-1", "")

			require.Equal(s.T(), http.StatusNoContent, rec.Code)
			require.Equal(s.T(), tc.want, removed)
		})
	}
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

func (s *ServerSuite) TestSetChannelLockedWorktreeCallsGitLock() {
	var ranArgs [][]string
	s.srv.worktreeCreator.Run = func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		ranArgs = append(ranArgs, append([]string{dir, name}, args...))
		return nil, nil
	}

	s.store.On("GetChannel", mock.Anything, "thread-wt").
		Return(&db.Channel{ChannelID: "thread-wt", ParentID: "parent-1", Worktree: true, DirPath: "/proj/.worktrees/wt-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-1").
		Return(&db.Channel{ChannelID: "parent-1", DirPath: "/proj"}, nil)
	s.store.On("UpdateChannelLocked", mock.Anything, "thread-wt", true).Return(nil)

	rec := s.testRequest("PATCH", "/api/channels/thread-wt/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.Len(s.T(), ranArgs, 1)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "lock", "/proj/.worktrees/wt-1", "--reason", "locked from Loop UI"}, ranArgs[0])
}

func (s *ServerSuite) TestSetChannelLockedWorktreeCallsGitUnlock() {
	var ranArgs [][]string
	s.srv.worktreeCreator.Run = func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		ranArgs = append(ranArgs, append([]string{dir, name}, args...))
		return nil, nil
	}

	s.store.On("GetChannel", mock.Anything, "thread-wt").
		Return(&db.Channel{ChannelID: "thread-wt", ParentID: "parent-1", Worktree: true, DirPath: "/proj/.worktrees/wt-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-1").
		Return(&db.Channel{ChannelID: "parent-1", DirPath: "/proj"}, nil)
	s.store.On("UpdateChannelLocked", mock.Anything, "thread-wt", false).Return(nil)

	rec := s.testRequest("PATCH", "/api/channels/thread-wt/lock", `{"locked":false}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.Len(s.T(), ranArgs, 1)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "unlock", "/proj/.worktrees/wt-1"}, ranArgs[0])
}

func (s *ServerSuite) TestSetChannelLockedWorktreeGitErrorLogsAndSucceeds() {
	s.srv.worktreeCreator.Run = func(_ context.Context, _, _ string, _ ...string) ([]byte, error) {
		return []byte("fatal: not a worktree"), errors.New("exit status 128")
	}

	s.store.On("GetChannel", mock.Anything, "thread-wt").
		Return(&db.Channel{ChannelID: "thread-wt", ParentID: "parent-1", Worktree: true, DirPath: "/proj/.worktrees/wt-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-1").
		Return(&db.Channel{ChannelID: "parent-1", DirPath: "/proj"}, nil)
	s.store.On("UpdateChannelLocked", mock.Anything, "thread-wt", true).Return(nil)

	rec := s.testRequest("PATCH", "/api/channels/thread-wt/lock", `{"locked":true}`)

	// HTTP response succeeds — the git-side failure is logged but does not
	// override the user's intent recorded in the DB.
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestSetChannelLockedWorktreeNoParentSkipsGit() {
	var called bool
	s.srv.worktreeCreator.Run = func(_ context.Context, _, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}

	// Worktree but ParentID is empty — can't resolve parent dir, so the git
	// call is skipped (DB update still applies).
	s.store.On("GetChannel", mock.Anything, "thread-wt").
		Return(&db.Channel{ChannelID: "thread-wt", Worktree: true, DirPath: "/proj/.worktrees/wt-1"}, nil)
	s.store.On("UpdateChannelLocked", mock.Anything, "thread-wt", true).Return(nil)

	rec := s.testRequest("PATCH", "/api/channels/thread-wt/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.False(s.T(), called, "expected git worktree lock to be skipped when parent dir is unresolvable")
}

func (s *ServerSuite) TestSetChannelLockedWorktreeParentLookupErrorSkipsGit() {
	var called bool
	s.srv.worktreeCreator.Run = func(_ context.Context, _, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}

	s.store.On("GetChannel", mock.Anything, "thread-wt").
		Return(&db.Channel{ChannelID: "thread-wt", ParentID: "parent-missing", Worktree: true, DirPath: "/proj/.worktrees/wt-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-missing").
		Return((*db.Channel)(nil), errors.New("db error"))
	s.store.On("UpdateChannelLocked", mock.Anything, "thread-wt", true).Return(nil)

	rec := s.testRequest("PATCH", "/api/channels/thread-wt/lock", `{"locked":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.False(s.T(), called)
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
	s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-err").Return([]string(nil), nil)
	s.store.On("ListHiddenThreads", mock.Anything, "ch-err").Return([]*db.Channel(nil), nil)
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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

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

func (s *ServerSuite) TestSearchChannelsLastActivity() {
	at := time.Date(2026, 9, 25, 11, 3, 6, 0, time.UTC)
	tests := []struct {
		name     string
		activity map[string]time.Time
		err      error
		want     map[string]*time.Time
	}{
		{
			name:     "newest message time per channel",
			activity: map[string]time.Time{"ch-1": at},
			want:     map[string]*time.Time{"ch-1": &at, "ch-2": nil},
		},
		{
			name: "lookup failure still lists the channels",
			err:  errors.New("db error"),
			want: map[string]*time.Time{"ch-1": nil, "ch-2": nil},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
				{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Platform: types.PlatformLocal},
				{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", Platform: types.PlatformLocal},
			}, nil)
			s.store.On("ChannelActivity", mock.Anything).Return(tc.activity, tc.err)

			rec := s.testRequest("GET", "/api/channels", "")

			require.Equal(s.T(), http.StatusOK, rec.Code)
			var resp []channelResponse
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
			got := make(map[string]*time.Time, len(resp))
			for _, ch := range resp {
				got[ch.ChannelID] = ch.LastActivityAt
			}
			require.Equal(s.T(), tc.want, got)
			s.store.AssertExpectations(s.T())
		})
	}
}

// TestSearchChannelsReviewSessions: review sessions live in memory, so the
// list asks the review store. A running review marks its channel running,
// and a session's latest change dates the channel when it is newer than
// the channel's newest message.
func (s *ServerSuite) TestSearchChannelsReviewSessions() {
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-1", Name: "reviewing", DirPath: "/a", Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "ready-newer-message", DirPath: "/b", Platform: types.PlatformLocal},
		{ChannelID: "ch-3", Name: "no-review", DirPath: "/c", Platform: types.PlatformLocal},
	}, nil)
	old := time.Date(2026, 9, 25, 11, 3, 6, 0, time.UTC)
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{"ch-1": old, "ch-2": future, "ch-3": old}, nil)
	sessions := review.NewStore()
	sessions.Put("ch-1", &review.Session{Status: review.StatusReviewing})
	sessions.Put("ch-2", &review.Session{Status: review.StatusReady})
	s.srv.review.sessions = sessions

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	byID := make(map[string]channelResponse, len(resp))
	for _, ch := range resp {
		byID[ch.ChannelID] = ch
	}
	require.True(s.T(), byID["ch-1"].ReviewRunning)
	require.True(s.T(), sessions.Get("ch-1").UpdatedAt.Equal(*byID["ch-1"].LastActivityAt), "the review is newer than the message")
	require.False(s.T(), byID["ch-2"].ReviewRunning)
	require.True(s.T(), future.Equal(*byID["ch-2"].LastActivityAt), "the message is newer than the review")
	require.False(s.T(), byID["ch-3"].ReviewRunning)
	require.True(s.T(), old.Equal(*byID["ch-3"].LastActivityAt))
}

// TestSearchChannelsTrustPending: each row reports whether the project
// config Settings → Project trusts for it waits for trust. That's a worktree
// chain's root checkout, and each dir is checked once.
func (s *ServerSuite) TestSearchChannelsTrustPending() {
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-1", Name: "proj", DirPath: "/proj", Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "thread", DirPath: "/proj", ParentID: "ch-1", Platform: types.PlatformLocal},
		{ChannelID: "ch-3", Name: "wt", DirPath: "/wt", ParentID: "ch-1", Worktree: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-4", Name: "ok", DirPath: "/ok", Platform: types.PlatformLocal},
		{ChannelID: "ch-5", Name: "bad", DirPath: "/bad", Platform: types.PlatformLocal},
	}, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)
	trust := new(mockProjectTrust)
	trust.On("Status", "/proj").Return(config.TrustStatus{Trusted: false}, nil).Once()
	trust.On("Status", "/ok").Return(config.TrustStatus{Trusted: true}, nil).Once()
	trust.On("Status", "/bad").Return(config.TrustStatus{}, errors.New("parsing project config file")).Once()
	s.srv.projectTrust = trust

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	got := make(map[string]bool, len(resp))
	for _, ch := range resp {
		got[ch.ChannelID] = ch.TrustPending
	}
	require.Equal(s.T(), map[string]bool{"ch-1": true, "ch-2": true, "ch-3": true, "ch-4": false, "ch-5": false}, got)
	trust.AssertExpectations(s.T())
}

// TestSearchChannelsUsesBranchPollerSnapshot verifies the handler serves the
// poller's per-dir git snapshot (no inline recompute) and that channels
// sharing a dir get the same state.
func (s *ServerSuite) TestSearchChannelsUsesBranchPollerSnapshot() {
	channels := []*db.Channel{
		{ChannelID: "wt", Name: "wt", DirPath: "/repo/wt", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "wt-thread", Name: "t", DirPath: "/repo/wt", ParentID: "wt", Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	poller := NewBranchPoller(nil, nil, "", time.Second, testLogger())
	poller.dirState["/repo/wt"] = gitState{Branch: "feat/x", Commit: "abc1234", DiffAdditions: 5, DiffDeletions: 2}
	s.srv.SetBranchPoller(poller)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	for _, ch := range resp {
		require.Equal(s.T(), "feat/x", ch.Branch)
		require.Equal(s.T(), "abc1234", ch.Commit)
		require.Equal(s.T(), 5, ch.DiffAdditions)
		require.Equal(s.T(), 2, ch.DiffDeletions)
	}
}

// TestSearchChannelsPollerMissFallsBack verifies a dir the poller hasn't
// covered yet is computed inline (a non-repo dir yields empty git state, and
// the request still succeeds).
func (s *ServerSuite) TestSearchChannelsPollerMissFallsBack() {
	channels := []*db.Channel{
		{ChannelID: "fresh", Name: "fresh", DirPath: s.T().TempDir(), Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)
	s.srv.SetBranchPoller(NewBranchPoller(nil, nil, "", time.Second, testLogger()))

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Empty(s.T(), resp[0].Branch)
	require.Zero(s.T(), resp[0].DiffAdditions)
}

func (s *ServerSuite) TestSearchChannelsWithQuery() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels?query=nonexistent", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsEmpty() {
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)
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

func (s *ServerSuite) TestSearchChannelsHidesLearnThreads() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "chat", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "l-1", Name: "learn", ParentID: "ch-1", Platform: types.PlatformLocal, Active: true, Kind: db.ChannelKindLearn},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "ch-1", resp[0].ChannelID)
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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

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
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.NotEmpty(s.T(), resp[0].Branch)
}

// Worktree channel resolves its parent's dir from the in-memory channel
// list (not via a second GetChannel call) when computing review_enabled.
func (s *ServerSuite) TestSearchChannelsReviewEnabledWorktreeUsesParentDir() {
	channels := []*db.Channel{
		{ChannelID: "parent", Name: "p", DirPath: "/proj", Platform: types.PlatformLocal},
		{ChannelID: "wt", Name: "w", DirPath: "/proj/.worktrees/wt", ParentID: "parent", Worktree: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: false}}, nil
	}
	s.srv.configs.loadWorktree = func(workdir, parent string, c *config.Config) (*config.Config, error) {
		require.Equal(s.T(), "/proj/.worktrees/wt", workdir)
		require.Equal(s.T(), "/proj", parent)
		out := *c
		out.Review.Enabled = true
		return &out, nil
	}

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	// Parent: global Enabled=false, no project layer → false.
	// Worktree: worktree loader flips it to true.
	for _, r := range resp {
		if r.ChannelID == "parent" {
			require.False(s.T(), r.ReviewEnabled)
		} else {
			require.True(s.T(), r.ReviewEnabled)
		}
	}
}

// TestSearchChannelsRootDirPath covers every shape of worktree chain: only
// channels inside one carry the checkout it was cut from, resolved from the
// listed channels alone.
func (s *ServerSuite) TestSearchChannelsRootDirPath() {
	channels := []*db.Channel{
		{ChannelID: "proj", DirPath: "/proj", Platform: types.PlatformLocal},
		{ChannelID: "thread", DirPath: "/proj", ParentID: "proj", Platform: types.PlatformLocal},
		{ChannelID: "wt", DirPath: "/proj/.worktrees/wt", ParentID: "proj", Worktree: true, Platform: types.PlatformLocal},
		{ChannelID: "task", DirPath: "/proj/.worktrees/wt", ParentID: "wt", Platform: types.PlatformLocal},
		{ChannelID: "wt2", DirPath: "/proj/.worktrees/wt2", ParentID: "wt", Worktree: true, Platform: types.PlatformLocal},
		{ChannelID: "orphan", DirPath: "/gone/.worktrees/o", ParentID: "missing", Worktree: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))

	got := make(map[string]string, len(resp))
	for _, r := range resp {
		got[r.ChannelID] = r.RootDirPath
	}
	require.Equal(s.T(), map[string]string{
		"proj":   "",
		"thread": "",
		"wt":     "/proj",
		"task":   "/proj",
		"wt2":    "/proj",
		"orphan": "",
	}, got)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

// TestSearchChannelsAgentOverrides checks a channel's model/effort overrides
// are listed, and left out when it inherits them from config.
func (s *ServerSuite) TestSearchChannelsAgentOverrides() {
	channels := []*db.Channel{
		{ChannelID: "set", DirPath: "/a", Platform: types.PlatformLocal, ModelOverride: "claude-opus-5-5", EffortOverride: "high"},
		{ChannelID: "inherit", DirPath: "/b", Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []map[string]any
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), "claude-opus-5-5", resp[0]["model_override"])
	require.Equal(s.T(), "high", resp[0]["effort_override"])
	require.NotContains(s.T(), resp[1], "model_override")
	require.NotContains(s.T(), resp[1], "effort_override")
}

// TestSearchChannelsTaskID checks a task's thread lists the task that created
// it and a thread its description and ticket URL, and channels without them
// leave the fields out.
func (s *ServerSuite) TestSearchChannelsTaskID() {
	channels := []*db.Channel{
		{ChannelID: "task-thread", ParentID: "p", Platform: types.PlatformLocal, TaskID: 7},
		{ChannelID: "user-thread", ParentID: "p", Platform: types.PlatformLocal, Description: "fixes login", TicketURL: "https://example.atlassian.net/browse/PROJ-1"},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []map[string]any
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.InDelta(s.T(), 7, resp[0]["task_id"], 0)
	require.NotContains(s.T(), resp[1], "task_id")
	require.Equal(s.T(), "fixes login", resp[1]["description"])
	require.NotContains(s.T(), resp[0], "description")
	require.Equal(s.T(), "https://example.atlassian.net/browse/PROJ-1", resp[1]["ticket_url"])
	require.NotContains(s.T(), resp[0], "ticket_url")
}

// TestSearchChannelsGitDetails checks a worktree thread lists its commit's
// subject and how far it is from its base branch, computed inline when the
// poller hasn't covered its dir.
func (s *ServerSuite) TestSearchChannelsGitDetails() {
	dir := initGitRepo(s.T())
	for _, args := range [][]string{
		{"branch", "-M", "main"},
		{"checkout", "-q", "-b", "feat"},
		{"commit", "--allow-empty", "-m", "feat work"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "wt", DirPath: dir, Worktree: true, BaseBranch: "main", Platform: types.PlatformLocal},
	}, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "feat work", resp[0].Subject)
	require.Equal(s.T(), "main", resp[0].SyncBase)
	require.Equal(s.T(), 1, resp[0].BaseAhead)
	require.Zero(s.T(), resp[0].BaseBehind)
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

	// Modify tracked file (1 insertion, 1 deletion, unstaged).
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("line1\nchanged\n"), 0o644))
	// Stage a new file (2 insertions) — must be counted (index vs HEAD), like the
	// Uncommitted Diff panel; the old gitDiffStats omitted staged changes.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("s1\ns2\n"), 0o644))
	addStaged := exec.Command("git", "add", "staged.txt")
	addStaged.Dir = dir
	require.NoError(s.T(), addStaged.Run())
	// Create an untracked TEXT file with 3 lines.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("a\nb\nc\n"), 0o644))
	// Create an untracked BINARY file — it must NOT inflate additions (counts 0,
	// like the panel). Raw `wc -l` would have counted its 4 newline bytes.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("\x00\n\x00\n\x00\n\x00\n"), 0o644))

	channels := []*db.Channel{
		{ChannelID: "ch-diff", Name: "with-diff", DirPath: dir, Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	// 1 unstaged insertion + 2 staged insertions + 3 untracked text lines; the
	// binary blob.bin contributes 0 (matches the Uncommitted Diff panel).
	require.Equal(s.T(), 6, resp[0].DiffAdditions, "1 unstaged + 2 staged + 3 untracked text lines (binary excluded)")
	require.Equal(s.T(), 1, resp[0].DiffDeletions, "expected 1 tracked deletion")
}

// TestGitBranchEmptyDir pins the empty-dir short-circuit: no directory, no
// git subprocess, empty branch.
func (s *ServerSuite) TestGitBranchEmptyDir() {
	require.Empty(s.T(), gitBranch(context.Background(), ""))
}

// TestDeleteChannelCleansUpThreads covers what goes with a deleted channel:
// the channel's own learn thread (one of its threads) and its threads' learn
// threads are stopped and their containers removed, and every one's MCP
// config file is removed once.
func (s *ServerSuite) TestDeleteChannelCleansUpThreads() {
	learnC := &db.Channel{ChannelID: "learn-c", ParentID: "ch-1", DirPath: "/p", Kind: db.ChannelKindLearn}
	tests := []struct {
		name         string
		threads      map[string]*db.Channel // looked-up threads; a missing id is gone meanwhile
		listErr      error
		threadErr    error // looking up t-1
		learnErr     error // looking up t-1's learn thread
		noCanceller  bool
		keep         bool
		removeErr    error
		wantStops    []string
		wantMCPFiles [][2]string
	}{
		{
			name:      "channel, threads and learn threads",
			threads:   map[string]*db.Channel{"t-1": {ChannelID: "t-1", DirPath: "/wt"}, "learn-c": learnC},
			wantStops: []string{"learn-c", "learn-t"},
			wantMCPFiles: [][2]string{
				{"/p", "ch-1"}, {"/wt", "t-1"}, {"/p", "learn-c"}, {"/wt", "learn-t"},
			},
		},
		{
			name:      "a failed removal is logged",
			threads:   map[string]*db.Channel{"t-1": {ChannelID: "t-1", DirPath: "/wt"}, "learn-c": learnC},
			removeErr: errors.New("permission denied"),
			wantStops: []string{"learn-c", "learn-t"},
			wantMCPFiles: [][2]string{
				{"/p", "ch-1"}, {"/wt", "t-1"}, {"/p", "learn-c"}, {"/wt", "learn-t"},
			},
		},
		{
			name:         "a thread lookup error skips the thread",
			threads:      map[string]*db.Channel{"learn-c": learnC},
			threadErr:    errors.New("db error"),
			wantStops:    []string{"learn-c"},
			wantMCPFiles: [][2]string{{"/p", "ch-1"}, {"/p", "learn-c"}},
		},
		{
			name:         "a thread gone meanwhile is skipped",
			threads:      map[string]*db.Channel{"learn-c": learnC},
			wantStops:    []string{"learn-c"},
			wantMCPFiles: [][2]string{{"/p", "ch-1"}, {"/p", "learn-c"}},
		},
		{
			name:         "a learn lookup error skips the learn thread",
			threads:      map[string]*db.Channel{"t-1": {ChannelID: "t-1", DirPath: "/wt"}, "learn-c": learnC},
			learnErr:     errors.New("db error"),
			wantStops:    []string{"learn-c"},
			wantMCPFiles: [][2]string{{"/p", "ch-1"}, {"/wt", "t-1"}, {"/p", "learn-c"}},
		},
		{
			name:         "thread listing fails",
			listErr:      errors.New("db error"),
			wantMCPFiles: [][2]string{{"/p", "ch-1"}},
		},
		{
			name:        "no run canceller, keep_mcp_configs",
			threads:     map[string]*db.Channel{"t-1": {ChannelID: "t-1", DirPath: "/wt"}, "learn-c": learnC},
			noCanceller: true,
			keep:        true,
			wantStops:   []string{"learn-c", "learn-t"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.srv.configs.loadProject = func(dir string, base *config.Config) (*config.Config, error) {
				require.Equal(s.T(), "/p", dir)
				merged := *base
				merged.KeepMCPConfigs = tc.keep
				return &merged, nil
			}
			var removed [][2]string
			s.srv.removeMCPConfig = func(dir, id string) error {
				removed = append(removed, [2]string{dir, id})
				return tc.removeErr
			}
			var threadIDs []string
			if tc.listErr == nil {
				// As the store lists them: the channel's learn thread's
				// parent is the channel.
				threadIDs = []string{"t-1", "learn-c"}
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/p"}, nil)
			s.store.On("ListChannelIDsByParentID", mock.Anything, "ch-1").Return(threadIDs, tc.listErr)
			s.store.On("GetChannel", mock.Anything, "t-1").Return(tc.threads["t-1"], tc.threadErr)
			s.store.On("GetChannel", mock.Anything, "learn-c").Return(tc.threads["learn-c"], nil)
			s.store.On("ListHiddenThreads", mock.Anything, "t-1").Return([]*db.Channel{{ChannelID: "learn-t", DirPath: "/wt", Kind: db.ChannelKindLearn}}, tc.learnErr)
			s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
			s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)
			canceller := new(MockRunCanceller)
			if !tc.noCanceller {
				for _, id := range tc.wantStops {
					canceller.On("StopHiddenThread", id).Return()
				}
				s.srv.SetRunCanceller(canceller)
			}
			reg := &mockContainerManager{}
			for _, id := range tc.wantStops {
				reg.byChannel = append(reg.byChannel, &container.ContainerInfo{ContainerID: "agent-" + id, ChannelID: id, Type: container.ContainerTypeAgent})
				reg.On("RemoveContainer", mock.Anything, "agent-"+id).Return(nil)
			}
			s.srv.containerRegistry = reg

			rec := s.testRequest("DELETE", "/api/channels/ch-1", "")

			require.Equal(s.T(), http.StatusNoContent, rec.Code)
			require.Equal(s.T(), tc.wantMCPFiles, removed)
			canceller.AssertExpectations(s.T())
			s.store.AssertNotCalled(s.T(), "ListHiddenThreads", mock.Anything, "ch-1")
			s.store.AssertNotCalled(s.T(), "ListHiddenThreads", mock.Anything, "learn-c")
			reg.AssertExpectations(s.T())
		})
	}
}
