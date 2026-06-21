package api

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
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

// ── relocateSessionDir ──

func (s *ServerSuite) TestRelocateSessionDir_SourceNotExists() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)

	err := relocateSessionDir(sys, "/old", "/new")
	require.NoError(s.T(), err)
}

func (s *ServerSuite) TestRelocateSessionDir_DestExists() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	// First Stat (src) returns info (exists), second Stat (dst) also returns info (exists).
	callCount := 0
	sys.On("Stat", mock.Anything).Return(nil, nil).Run(func(args mock.Arguments) {
		callCount++
	})

	err := relocateSessionDir(sys, "/old", "/new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "destination session dir already exists")
}

func (s *ServerSuite) TestRelocateSessionDir_RenameSuccess() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	// src exists (first call), dst doesn't exist (second call).
	sys.On("Stat", mock.Anything).Return(nil, nil).Once()
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist).Once()
	sys.On("Rename", mock.Anything, mock.Anything).Return(nil)

	err := relocateSessionDir(sys, "/old", "/new")
	require.NoError(s.T(), err)
	sys.AssertCalled(s.T(), "Rename", mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestRelocateSessionDir_HomeDirError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))

	err := relocateSessionDir(sys, "/old", "/new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home dir")
}

func (s *ServerSuite) TestRelocateSessionDir_StatSrcError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	// Non-NotExist error on stat
	sys.On("Stat", mock.Anything).Return(nil, errors.New("permission denied")).Once()

	err := relocateSessionDir(sys, "/old", "/new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stat session dir")
}

func (s *ServerSuite) TestRelocateSessionDir_RenameError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	// src exists, dst doesn't exist.
	sys.On("Stat", mock.Anything).Return(nil, nil).Once()
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist).Once()
	sys.On("Rename", mock.Anything, mock.Anything).Return(errors.New("rename failed"))

	err := relocateSessionDir(sys, "/old", "/new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "rename failed")
}

// ── handleMoveWorktree ──

func (s *ServerSuite) TestMoveWorktree_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/worktrees/move", srv.handleMoveWorktree)

	req, _ := http.NewRequest("POST", "/api/worktrees/move", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestMoveWorktree_BadJSON() {
	rec := s.testRequest("POST", "/api/worktrees/move", `{bad}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestMoveWorktree_MissingChannelID() {
	rec := s.testRequest("POST", "/api/worktrees/move", `{"new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id is required")
}

func (s *ServerSuite) TestMoveWorktree_MissingNewName() {
	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "new_name is required")
}

func (s *ServerSuite) TestMoveWorktree_InvalidNewName() {
	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"../evil"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid new_name")
}

func (s *ServerSuite) TestMoveWorktree_GetChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db error"))
	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestMoveWorktree_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"missing","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestMoveWorktree_NotWorktree() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", Worktree: false}, nil)
	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "not a worktree thread")
}

func (s *ServerSuite) TestMoveWorktree_NoDirPath() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", Worktree: true, DirPath: ""}, nil)
	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "no dir_path")
}

func (s *ServerSuite) TestMoveWorktree_ActiveChatRun() {

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", Worktree: true, DirPath: "/proj/.worktrees/wt-old", ParentID: "parent",
	}, nil)

	chatLister := new(MockActiveChatLister)
	chatLister.On("ActiveChatChannelIDs").Return(map[string]struct{}{"ch1": {}})
	s.srv.SetActiveChatLister(chatLister)
	defer func() { s.srv.activeChatLister = nil }()

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusConflict, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "active run")
}

func (s *ServerSuite) TestMoveWorktree_ActiveContainerRun() {

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", Worktree: true, DirPath: "/proj/.worktrees/wt-old", ParentID: "parent",
	}, nil)

	containerReg := &mockContainerManager{runningIDs: map[string]struct{}{"ch1": {}}}
	s.srv.SetContainerRegistry(containerReg)
	defer func() { s.srv.containerRegistry = nil }()

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusConflict, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "active run")
}

func (s *ServerSuite) TestMoveWorktree_NoParentDir() {

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", Worktree: true, DirPath: "/proj/.worktrees/wt-old", ParentID: "",
	}, nil)

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"ch1","new_name":"new-name"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "cannot resolve parent directory")
}

func (s *ServerSuite) TestMoveWorktree_Success() {

	dir := initGitRepo(s.T())
	// Create a worktree to move.
	cmd := exec.Command("git", "branch", "feature/to-move")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	oldWtPath := filepath.Join(dir, ".worktrees", "wt-old")
	cmd = exec.Command("git", "worktree", "add", "-b", "worktree/wt-old", oldWtPath, "feature/to-move")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: oldWtPath, ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)
	s.store.On("UpdateChannelDirPath", mock.Anything, "wt-ch1", mock.Anything).Return(nil)
	s.store.On("UpdateChannelName", mock.Anything, "wt-ch1", "wt-new").Return(nil)

	s.srv.eventsHub = NewEventsHub(testLogger())

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"channel_id":"wt-ch1"`)
	require.Contains(s.T(), rec.Body.String(), `"name":"wt-new"`)
}

func (s *ServerSuite) TestMoveWorktree_MoveFail() {

	dir := initGitRepo(s.T())

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: filepath.Join(dir, ".worktrees", "wt-old"), ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to move worktree")
}

func (s *ServerSuite) TestMoveWorktree_RelocateDestExists() {

	dir := initGitRepo(s.T())
	// Create a worktree to move.
	cmd := exec.Command("git", "branch", "feature/relocate-dest")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	oldWtPath := filepath.Join(dir, ".worktrees", "wt-relocate")
	cmd = exec.Command("git", "worktree", "add", "-b", "worktree/wt-relocate", oldWtPath, "feature/relocate-dest")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	// Make Stat always return "exists" (simulating dst already existing).
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, nil) // always exists
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: oldWtPath, ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "destination session dir already exists")
}

func (s *ServerSuite) TestMoveWorktree_RelocateOtherError() {

	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/relocate-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	oldWtPath := filepath.Join(dir, ".worktrees", "wt-relocate-err")
	cmd = exec.Command("git", "worktree", "add", "-b", "worktree/wt-relocate-err", oldWtPath, "feature/relocate-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	// Stat returns src=exists, dst=not-exists, then Rename fails.
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, nil).Once()
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist).Once()
	sys.On("Rename", mock.Anything, mock.Anything).Return(errors.New("rename failed"))
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: oldWtPath, ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to relocate session dir")
}

func (s *ServerSuite) TestMoveWorktree_UpdateDirPathError() {

	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/update-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	oldWtPath := filepath.Join(dir, ".worktrees", "wt-update-err")
	cmd = exec.Command("git", "worktree", "add", "-b", "worktree/wt-update-err", oldWtPath, "feature/update-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: oldWtPath, ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)
	s.store.On("UpdateChannelDirPath", mock.Anything, "wt-ch1", mock.Anything).Return(errors.New("db error"))

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to update dir_path")
}

func (s *ServerSuite) TestMoveWorktree_UpdateNameError() {

	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/update-name-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	oldWtPath := filepath.Join(dir, ".worktrees", "wt-name-err")
	cmd = exec.Command("git", "worktree", "add", "-b", "worktree/wt-name-err", oldWtPath, "feature/update-name-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: oldWtPath, ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)
	s.store.On("UpdateChannelDirPath", mock.Anything, "wt-ch1", mock.Anything).Return(nil)
	s.store.On("UpdateChannelName", mock.Anything, "wt-ch1", "wt-new").Return(errors.New("db error"))

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to update name")
}

func (s *ServerSuite) TestMoveWorktree_NoEventsHub() {

	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/no-hub")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	oldWtPath := filepath.Join(dir, ".worktrees", "wt-no-hub")
	cmd = exec.Command("git", "worktree", "add", "-b", "worktree/wt-no-hub", oldWtPath, "feature/no-hub")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "wt-ch1").Return(&db.Channel{
		ChannelID: "wt-ch1", Worktree: true, DirPath: oldWtPath, ParentID: "parent-ch",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: dir,
	}, nil)
	s.store.On("UpdateChannelDirPath", mock.Anything, "wt-ch1", mock.Anything).Return(nil)
	s.store.On("UpdateChannelName", mock.Anything, "wt-ch1", "wt-new").Return(nil)
	// No eventsHub — should not panic.

	rec := s.testRequest("POST", "/api/worktrees/move", `{"channel_id":"wt-ch1","new_name":"wt-new"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}
