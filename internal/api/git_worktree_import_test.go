package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// ── Import Worktree ──

func (s *ServerSuite) TestImportWorktree_Success() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/import-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "imp-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/import-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess-imp",
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("imp-thread-1", nil)
	s.store.On("GetChannel", mock.Anything, "imp-thread-1").Return(&db.Channel{
		ChannelID: "imp-thread-1", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	s.srv.eventsHub = NewEventsHub(testLogger())

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"thread_id":"imp-thread-1"`)
	require.Contains(s.T(), rec.Body.String(), `"worktree_path"`)

	// Verify UpsertChannel was called with worktree=true.
	upsertCall := s.store.Calls[len(s.store.Calls)-1]
	require.Equal(s.T(), "UpsertChannel", upsertCall.Method)
	ch := upsertCall.Arguments[1].(*db.Channel)
	require.True(s.T(), ch.Worktree)
	require.Equal(s.T(), wtPath, ch.DirPath)
}

func (s *ServerSuite) TestImportWorktree_AlreadyImported() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/already")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "already-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/already")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "existing-thread", ParentID: "ch1", DirPath: wtPath, Worktree: true},
	}, nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"thread_id":"existing-thread"`)
}

func (s *ServerSuite) TestImportWorktree_InvalidPath() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)

	body := `{"channel_id":"ch1","worktree_path":"/nonexistent/path"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "not a known git worktree")
}

func (s *ServerSuite) TestImportWorktree_MissingChannelID() {
	rec := s.testRequest("POST", "/api/worktrees/import", `{"worktree_path":"/some/path"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id is required")
}

func (s *ServerSuite) TestImportWorktree_MissingPath() {
	rec := s.testRequest("POST", "/api/worktrees/import", `{"channel_id":"ch1"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "worktree_path is required")
}

func (s *ServerSuite) TestImportWorktree_BadChannel() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	body := `{"channel_id":"missing","worktree_path":"/some/path"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_GetChannelError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, errors.New("db error"))
	body := `{"channel_id":"err-ch","worktree_path":"/some/path"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/worktrees/import", srv.handleImportWorktree)

	req, _ := http.NewRequest("POST", "/api/worktrees/import", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_ThreadsNotConfigured() {
	srv := NewServer(nil, nil, nil, s.store, nil, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/worktrees/import", srv.handleImportWorktree)

	req, _ := http.NewRequest("POST", "/api/worktrees/import", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_CreateThreadFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/imp-ct-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "ct-fail-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/imp-ct-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("", errors.New("thread err"))

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating thread")
}

func (s *ServerSuite) TestImportWorktree_GetThreadFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/imp-gt-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "gt-fail-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/imp-gt-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("gt-fail-t", nil)
	s.store.On("GetChannel", mock.Anything, "gt-fail-t").Return(nil, nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to get created thread")
}

func (s *ServerSuite) TestImportWorktree_UpsertFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/imp-up-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "up-fail-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/imp-up-fail")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("up-fail-t", nil)
	s.store.On("GetChannel", mock.Anything, "up-fail-t").Return(&db.Channel{
		ChannelID: "up-fail-t", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(errors.New("upsert error"))

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "updating thread")
}

func (s *ServerSuite) TestImportWorktree_BadJSON() {
	rec := s.testRequest("POST", "/api/worktrees/import", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_GitWorktreeListFails() {
	// Channel with non-git DirPath → git worktree list fails.
	dir := s.T().TempDir() // not a git repo
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	body := `{"channel_id":"ch1","worktree_path":"/some/path"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to list worktrees")
}

func (s *ServerSuite) TestImportWorktree_SessionCopyError() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/sess-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "sess-err-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/sess-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	// Override sys so ReadFile fails (session copy error path).
	s.sys = new(testutil.MockSystem)
	s.sys.On("UserHomeDir").Return("/home/testuser", nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, errors.New("session read error"))
	s.sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.srv.sys = s.sys

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess-fail",
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("imp-sess-err", nil)
	s.store.On("GetChannel", mock.Anything, "imp-sess-err").Return(&db.Channel{
		ChannelID: "imp-sess-err", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	// Session copy error is logged but doesn't fail the request.
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"thread_id":"imp-sess-err"`)
}

func (s *ServerSuite) TestImportWorktree_DetachedHead() {
	dir := initGitRepo(s.T())

	wtPath := filepath.Join(dir, ".worktrees", "detached-wt")
	// Create a detached worktree (no branch).
	cmd := exec.Command("git", "worktree", "add", "--detach", wtPath, "HEAD")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", "detached-wt (detached)", "", "").Return("det-th", nil)
	s.store.On("GetChannel", mock.Anything, "det-th").Return(&db.Channel{
		ChannelID: "det-th", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	// Verify thread name uses "detached" label.
	s.threads.AssertCalled(s.T(), "CreateThread", mock.Anything, "ch1", "detached-wt (detached)", "", "")
}

func (s *ServerSuite) TestImportWorktree_ConfigMkdirFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/imp-mkdir")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "imp-mkdir-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/imp-mkdir")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir fail"))
	sys.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "",
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("imp-mkdir-th", nil)
	s.store.On("GetChannel", mock.Anything, "imp-mkdir-th").Return(&db.Channel{
		ChannelID: "imp-mkdir-th", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_ConfigWriteFails() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/imp-write")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "imp-write-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/imp-write")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write fail"))
	sys.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "",
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("imp-write-th", nil)
	s.store.On("GetChannel", mock.Anything, "imp-write-th").Return(&db.Channel{
		ChannelID: "imp-write-th", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestImportWorktree_ConfigAlreadyExists() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/imp-exists")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	wtPath := filepath.Join(dir, ".worktrees", "imp-exists-wt")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/imp-exists")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	// Stat returns nil error — config already exists, so WriteFile should NOT be called.
	sys.On("Stat", mock.Anything).Return(nil, nil)
	sys.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "",
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("imp-exists-th", nil)
	s.store.On("GetChannel", mock.Anything, "imp-exists-th").Return(&db.Channel{
		ChannelID: "imp-exists-th", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := `{"channel_id":"ch1","worktree_path":"` + wtPath + `"}`
	rec := s.testRequest("POST", "/api/worktrees/import", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	// WriteFile should not have been called since config already exists.
	sys.AssertNotCalled(s.T(), "WriteFile", mock.Anything, mock.Anything, mock.Anything)
}

// ── deleteWorktree ──

// ── removeWorktree ──

func (s *ServerSuite) TestRemoveWorktree_DiskOnly() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/rm-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	wtPath := filepath.Join(dir, ".worktrees", "rm-test")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/rm-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)

	body := fmt.Sprintf(`{"channel_id":"ch1","worktree_path":%q}`, wtPath)
	rec := s.testRequest("DELETE", "/api/worktrees", body)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NoDirExists(s.T(), wtPath)
}

func (s *ServerSuite) TestRemoveWorktree_WithThread() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/del-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	wtPath := filepath.Join(dir, ".worktrees", "del-test")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/del-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("DeleteThread", mock.Anything, "wt-thread-1").Return(nil)
	s.srv.eventsHub = NewEventsHub(testLogger())

	body := fmt.Sprintf(`{"channel_id":"ch1","worktree_path":%q,"thread_id":"wt-thread-1"}`, wtPath)
	rec := s.testRequest("DELETE", "/api/worktrees", body)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NoDirExists(s.T(), wtPath)
	s.threads.AssertCalled(s.T(), "DeleteThread", mock.Anything, "wt-thread-1")
}

func (s *ServerSuite) TestRemoveWorktree_DeleteThreadError() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/dt-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	wtPath := filepath.Join(dir, ".worktrees", "dt-err")
	cmd = exec.Command("git", "worktree", "add", wtPath, "feature/dt-err")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("DeleteThread", mock.Anything, "wt-1").Return(errors.New("db error"))

	body := fmt.Sprintf(`{"channel_id":"ch1","worktree_path":%q,"thread_id":"wt-1"}`, wtPath)
	rec := s.testRequest("DELETE", "/api/worktrees", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRemoveWorktree_MissingFields() {
	rec := s.testRequest("DELETE", "/api/worktrees", `{"channel_id":"ch1"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id and worktree_path required")
}

func (s *ServerSuite) TestRemoveWorktree_InvalidJSON() {
	rec := s.testRequest("DELETE", "/api/worktrees", `{invalid}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestRemoveWorktree_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return((*db.Channel)(nil), nil)

	rec := s.testRequest("DELETE", "/api/worktrees", `{"channel_id":"missing","worktree_path":"/tmp/wt"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel not found")
}

func (s *ServerSuite) TestRemoveWorktree_StoreNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/worktrees", srv.handleRemoveWorktree)

	req := httptest.NewRequest("DELETE", "/api/worktrees", strings.NewReader(`{"channel_id":"ch1","worktree_path":"/tmp/wt"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestRemoveWorktree_RemoveError() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)

	rec := s.testRequest("DELETE", "/api/worktrees", `{"channel_id":"ch1","worktree_path":"/nonexistent/worktree"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to remove worktree")
}
