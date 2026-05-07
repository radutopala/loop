package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/testutil"
)

func (s *ServerSuite) TestCreateWorktree_Success() {
	dir := initGitRepo(s.T())
	// Create a branch to use for the worktree (main is already checked out).
	cmd := exec.Command("git", "branch", "feature/wt-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir, SessionID: "sess-parent"}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("wt-thread-1", nil)
	s.store.On("GetChannel", mock.Anything, "wt-thread-1").Return(&db.Channel{
		ChannelID: "wt-thread-1",
		DirPath:   dir,
		ParentID:  "ch1",
		Active:    true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	// Set events hub so the broadcast path is covered.
	s.srv.eventsHub = NewEventsHub(testLogger())

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/wt-test","name":"test-wt"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"thread_id":"wt-thread-1"`)
	require.Contains(s.T(), rec.Body.String(), `"worktree_path"`)

	// Verify worktree was created on disk.
	wtPath := filepath.Join(dir, ".worktrees", "test-wt")
	require.DirExists(s.T(), wtPath)

	// Verify the UpsertChannel was called with worktree=true and correct DirPath.
	upsertCall := s.store.Calls[len(s.store.Calls)-1]
	require.Equal(s.T(), "UpsertChannel", upsertCall.Method)
	ch := upsertCall.Arguments[1].(*db.Channel)
	require.True(s.T(), ch.Worktree)
	require.Equal(s.T(), wtPath, ch.DirPath)

	// Verify worktree config was seeded with extra_dirs pointing to parent.
	s.sys.AssertCalled(s.T(), "MkdirAll", filepath.Join(wtPath, ".loop"), os.FileMode(0755))
	s.sys.AssertCalled(s.T(), "WriteFile", filepath.Join(wtPath, ".loop", "config.json"), mock.Anything, os.FileMode(0644))

	// Verify session file copy was attempted.
	s.sys.AssertCalled(s.T(), "ReadFile", mock.Anything)
}

func (s *ServerSuite) TestCreateWorktree_SessionCopy() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/sess-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	// Override sys so ReadFile returns session data for any path.
	s.sys = new(testutil.MockSystem)
	s.srv.sys = s.sys
	s.srv.worktreeCreator.Sys = s.sys
	s.sys.On("UserHomeDir").Return("/home/testuser", nil)
	s.sys.On("ReadFile", mock.Anything).Return([]byte(`{"session":"data"}`), nil)
	s.sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	s.sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.sys.On("ReadDir", mock.Anything).Return(nil, nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.sys.On("Remove", mock.Anything).Return(nil)

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess-123",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("sess-thread", nil)
	s.store.On("GetChannel", mock.Anything, "sess-thread").Return(&db.Channel{
		ChannelID: "sess-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/sess-test","name":"sess-wt"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	// Verify session copy: MkdirAll + WriteFile were called (session was copied).
	s.sys.AssertCalled(s.T(), "MkdirAll", mock.Anything, os.FileMode(0o755))
	s.sys.AssertCalled(s.T(), "WriteFile", mock.Anything, []byte(`{"session":"data"}`), os.FileMode(0o644))
}

func (s *ServerSuite) TestCopySessionFile_Errors() {
	s.srv.sys = s.sys

	// UserHomeDir error.
	sys1 := new(testutil.MockSystem)
	sys1.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys1
	err := s.srv.copySessionFile("/src", "/dst", "sess")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home dir")

	// ReadFile error.
	sys2 := new(testutil.MockSystem)
	sys2.On("UserHomeDir").Return("/home/test", nil)
	sys2.On("ReadFile", mock.Anything).Return(nil, errors.New("read fail"))
	s.srv.sys = sys2
	err = s.srv.copySessionFile("/src", "/dst", "sess")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading session file")

	// MkdirAll error.
	sys3 := new(testutil.MockSystem)
	sys3.On("UserHomeDir").Return("/home/test", nil)
	sys3.On("ReadFile", mock.Anything).Return([]byte("data"), nil)
	sys3.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir fail"))
	s.srv.sys = sys3
	err = s.srv.copySessionFile("/src", "/dst", "sess")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating project dir")

	// WriteFile error.
	sys4 := new(testutil.MockSystem)
	sys4.On("UserHomeDir").Return("/home/test", nil)
	sys4.On("ReadFile", mock.Anything).Return([]byte("data"), nil)
	sys4.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys4.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write fail"))
	s.srv.sys = sys4
	err = s.srv.copySessionFile("/src", "/dst", "sess")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing session file")
}

func (s *ServerSuite) TestCopySessionFile_TraversalSessionID() {
	// Traversal sessionID should be sanitised to base name.
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	// filepath.Base("../../etc/passwd") = "passwd", so ReadFile path ends with "passwd.jsonl".
	sys.On("ReadFile", mock.MatchedBy(func(p string) bool {
		return strings.HasSuffix(p, "passwd.jsonl") && !strings.Contains(p, "..")
	})).Return(nil, errors.New("not found"))
	s.srv.sys = sys
	err := s.srv.copySessionFile("/src", "/dst", "../../etc/passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading session file")
	sys.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCopySessionFile_DotDotSessionID() {
	err := s.srv.copySessionFile("/src", "/dst", "..")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid session ID")
}

func (s *ServerSuite) TestEncodeClaudeProjectPath() {
	require.Equal(s.T(), "-Users-me-project", osutil.EncodeClaudeProjectPath("/Users/me/project"))
	require.Equal(s.T(), "-Users-me--worktrees-wt1", osutil.EncodeClaudeProjectPath("/Users/me/.worktrees/wt1"))
	require.Equal(s.T(), "-Users-me--claude", osutil.EncodeClaudeProjectPath("/Users/me/.claude"))
	require.Equal(s.T(), "", osutil.EncodeClaudeProjectPath(""))
}

func (s *ServerSuite) TestCreateWorktree_AutoTriggerWithMessage() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/auto-trigger")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	// Empty message passed to CreateThread because msgHandler is set (HandleThreadCreated stores it instead).
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "user-42", "").Return("at-thread", nil)
	s.store.On("GetChannel", mock.Anything, "at-thread").Return(&db.Channel{
		ChannelID: "at-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	called := make(chan struct{}, 1)
	handler.On("HandleThreadCreated", mock.Anything, "at-thread", "user-42", "Implement the parser").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/auto-trigger","name":"at-wt","author_id":"user-42","message":"Implement the parser"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleThreadCreated was not called within 1s")
	}
	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateWorktree_MissingChannelID() {
	rec := s.testRequest("POST", "/api/worktrees", `{"branch":"main"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id is required")
}

func (s *ServerSuite) TestCreateWorktree_MissingBranch() {
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "branch is required")
}

func (s *ServerSuite) TestCreateWorktree_InvalidBranch() {
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"../evil"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid branch name")
}

func (s *ServerSuite) TestCreateWorktree_BadChannel() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"missing","branch":"main"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_GetChannelError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, errors.New("db error"))
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"err-ch","branch":"main"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_SessionCopyFailsGracefully() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/sc-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	// Set sys that will fail on UserHomeDir (causing session copy to fail).
	// MkdirAll/WriteFile succeed so worktree config seeding works, but session copy fails.
	sysFail := new(testutil.MockSystem)
	sysFail.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sysFail.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sysFail.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sysFail
	s.srv.worktreeCreator.Sys = sysFail

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess-will-fail",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("sc-thread", nil)
	s.store.On("GetChannel", mock.Anything, "sc-thread").Return(&db.Channel{
		ChannelID: "sc-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	// Session copy fails but handler should still succeed (non-fatal).
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/sc-fail","name":"sc-fail-wt"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_ConfigMkdirFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/mkdir-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sysFail := new(testutil.MockSystem)
	sysFail.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir fail"))
	sysFail.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sysFail
	s.srv.worktreeCreator.Sys = sysFail

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("mkdir-th", nil)
	s.store.On("GetChannel", mock.Anything, "mkdir-th").Return(&db.Channel{
		ChannelID: "mkdir-th", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	// MkdirAll fails but handler still succeeds (non-fatal warning).
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/mkdir-fail","name":"mkdir-fail-wt"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_ConfigWriteFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/write-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sysFail := new(testutil.MockSystem)
	sysFail.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sysFail.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write fail"))
	sysFail.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sysFail
	s.srv.worktreeCreator.Sys = sysFail

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("write-th", nil)
	s.store.On("GetChannel", mock.Anything, "write-th").Return(&db.Channel{
		ChannelID: "write-th", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	// WriteFile fails but handler still succeeds (non-fatal warning).
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/write-fail","name":"write-fail-wt"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_BadJSON() {
	rec := s.testRequest("POST", "/api/worktrees", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/worktrees", srv.handleCreateWorktree)

	req, _ := http.NewRequest("POST", "/api/worktrees", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_GitWorktreeAddFails() {
	dir := initGitRepo(s.T())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	// Non-existent branch → git worktree add will fail.
	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"nonexistent-branch"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "git worktree add failed")
}

func (s *ServerSuite) TestCreateWorktree_AutoGeneratesName() {
	dir := initGitRepo(s.T())
	// Create a branch to use for the worktree (main is already checked out).
	cmd := exec.Command("git", "branch", "feature/auto-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("wt-auto", nil)
	s.store.On("GetChannel", mock.Anything, "wt-auto").Return(&db.Channel{
		ChannelID: "wt-auto",
		DirPath:   dir,
		ParentID:  "ch1",
		Active:    true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/auto-test"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"thread_id":"wt-auto"`)
}

func (s *ServerSuite) TestCreateWorktree_ThreadsNotConfigured() {
	// Store configured but threads nil → should get 501 for threads.
	srv := NewServer(nil, nil, nil, s.store, nil, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/worktrees", srv.handleCreateWorktree)

	req, _ := http.NewRequest("POST", "/api/worktrees", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestCreateWorktree_CreateThreadFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/ct-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("", errors.New("thread creation error"))

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/ct-fail","name":"ct-fail"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating thread")
}

func (s *ServerSuite) TestCreateWorktree_GetThreadFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/gt-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("gt-fail-thread", nil)
	s.store.On("GetChannel", mock.Anything, "gt-fail-thread").Return(nil, nil)

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/gt-fail","name":"gt-fail"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to get created thread")
}

func (s *ServerSuite) TestCreateWorktree_UpsertFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/up-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("up-fail-thread", nil)
	s.store.On("GetChannel", mock.Anything, "up-fail-thread").Return(&db.Channel{
		ChannelID: "up-fail-thread",
		DirPath:   dir,
		ParentID:  "ch1",
		Active:    true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(errors.New("upsert error"))

	rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"ch1","branch":"feature/up-fail","name":"up-fail"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "updating thread")
}

// ── ListBranches: Worktree Thread IDs ──

func (s *ServerSuite) TestListBranches_WorktreeThreadIDs() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/wt-linked")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "linked-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/wt-linked")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "thread-42", ParentID: "ch1", DirPath: wtPath, Worktree: true},
	}, nil)

	rec := s.testRequest("GET", "/api/channels/ch1/branches", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp branchListResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))

	require.Len(s.T(), resp.Worktrees, 1)
	require.Equal(s.T(), "thread-42", resp.Worktrees[0].ThreadID)
	require.Equal(s.T(), "feature/wt-linked", resp.Worktrees[0].Branch)
}
