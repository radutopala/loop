package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/testutil"
)

// initGitRepo creates a temporary git repo with an initial commit and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0644))
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	return dir
}

func (s *ServerSuite) TestListBranches_Success() {
	dir := initGitRepo(s.T())
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)
	// Create a second branch.
	cmd := exec.Command("git", "checkout", "-b", "feature/test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "checkout", "main")
	cmd.Dir = dir
	// Might fail if default branch is "master", try both.
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("git", "checkout", "master")
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch1/branches", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "feature/test")
	require.Contains(s.T(), rec.Body.String(), `"worktrees":`)
}

func (s *ServerSuite) TestListBranches_ExcludesOtherWorktreeBranches() {
	dir := initGitRepo(s.T())
	// Create two branches: one local, one checked out in another worktree.
	for _, b := range []string{"feature/local-only", "feature/in-worktree"} {
		cmd := exec.Command("git", "branch", b)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	cmd := exec.Command("git", "worktree", "add", filepath.Join(dir, ".worktrees", "wt1"), "feature/in-worktree")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)

	rec := s.testRequest("GET", "/api/channels/ch1/branches", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp branchListResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))

	// Local branch present, worktree-locked branch excluded.
	require.Contains(s.T(), resp.Branches, "feature/local-only")
	for _, b := range resp.Branches {
		require.NotEqual(s.T(), "feature/in-worktree", b)
	}
	// Worktree entry still present in worktrees list.
	found := false
	for _, wt := range resp.Worktrees {
		if wt.Branch == "feature/in-worktree" {
			found = true
		}
	}
	require.True(s.T(), found)
}

func (s *ServerSuite) TestListBranches_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/branches", srv.handleListBranches)

	req, _ := http.NewRequest("GET", "/api/channels/ch1/branches", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestSwitchBranch_Success() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "checkout", "-b", "feature/switch-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	// Go back to default branch.
	cmd = exec.Command("git", "checkout", "-")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/switch", `{"branch":"feature/switch-test"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"ok":true`)
}

func (s *ServerSuite) TestSwitchBranch_MissingBranch() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/tmp"}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/switch", `{"branch":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSwitchBranch_InvalidName() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/tmp"}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/switch", `{"branch":"../../evil"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSwitchBranch_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/branches/switch", srv.handleSwitchBranch)

	req, _ := http.NewRequest("POST", "/api/channels/ch1/branches/switch", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestSwitchBranch_NonexistentBranch() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/switch", `{"branch":"nonexistent"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "git checkout failed")
}

func (s *ServerSuite) TestCreateBranch_Success() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{"name":"feature/new-branch"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"ok":true`)
}

func (s *ServerSuite) TestCreateBranch_WithFrom() {
	dir := initGitRepo(s.T())
	// Create a source branch.
	cmd := exec.Command("git", "checkout", "-b", "source-branch")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "checkout", "-")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{"name":"feature/from-source","from":"source-branch"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_MissingName() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/tmp"}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{"name":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_InvalidName() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/tmp"}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{"name":"../evil"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_InvalidFrom() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/tmp"}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{"name":"ok-name","from":"../evil"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/branches/create", srv.handleCreateBranch)

	req, _ := http.NewRequest("POST", "/api/channels/ch1/branches/create", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_AlreadyExists() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "checkout", "-b", "existing")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "checkout", "-")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{"name":"existing"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "git checkout -b failed")
}

func (s *ServerSuite) TestListBranches_BadChannel() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("GET", "/api/channels/missing/branches", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListBranches_BadGitDir() {
	tmpDir := s.T().TempDir() // not a git repo
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch1/branches", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSwitchBranch_BadChannel() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("POST", "/api/channels/missing/branches/switch", `{"branch":"main"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSwitchBranch_BadJSON() {
	rec := s.testRequest("POST", "/api/channels/ch1/branches/switch", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_BadChannel() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("POST", "/api/channels/missing/branches/create", `{"name":"test"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateBranch_BadJSON() {
	rec := s.testRequest("POST", "/api/channels/ch1/branches/create", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func TestParseWorktrees(t *testing.T) {
	output := `worktree /repo
branch refs/heads/main

worktree /repo/.claude/worktrees/agent-123
branch refs/heads/feature/foo

worktree /repo/.worktrees/mobile
branch refs/heads/feature/bar

`
	wts := parseWorktrees(output, "/repo")
	require.Len(t, wts, 2)
	require.Equal(t, "/repo/.claude/worktrees/agent-123", wts[0].Path)
	require.Equal(t, "feature/foo", wts[0].Branch)
	require.Equal(t, "/repo/.worktrees/mobile", wts[1].Path)
	require.Equal(t, "feature/bar", wts[1].Branch)
}

func TestParseWorktreesEmpty(t *testing.T) {
	wts := parseWorktrees("", "/repo")
	require.Empty(t, wts)
}

func TestValidBranchName(t *testing.T) {
	require.True(t, validBranchName.MatchString("main"))
	require.True(t, validBranchName.MatchString("feature/foo"))
	require.True(t, validBranchName.MatchString("feature/foo-bar"))
	require.True(t, validBranchName.MatchString("v1.2.3"))
	require.False(t, validBranchName.MatchString(""))
	require.False(t, validBranchName.MatchString("../evil"))
	require.False(t, validBranchName.MatchString("-flag"))
	require.False(t, validBranchName.MatchString("name with spaces"))
}

// ── Worktree Tests ──

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
	rec := s.testRequest("POST", "/api/worktrees/remove", body)
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
	rec := s.testRequest("POST", "/api/worktrees/remove", body)
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
	rec := s.testRequest("POST", "/api/worktrees/remove", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRemoveWorktree_MissingFields() {
	rec := s.testRequest("POST", "/api/worktrees/remove", `{"channel_id":"ch1"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id and worktree_path required")
}

func (s *ServerSuite) TestRemoveWorktree_InvalidJSON() {
	rec := s.testRequest("POST", "/api/worktrees/remove", `{invalid}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestRemoveWorktree_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return((*db.Channel)(nil), nil)

	rec := s.testRequest("POST", "/api/worktrees/remove", `{"channel_id":"missing","worktree_path":"/tmp/wt"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel not found")
}

func (s *ServerSuite) TestRemoveWorktree_StoreNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/worktrees/remove", srv.handleRemoveWorktree)

	req := httptest.NewRequest("POST", "/api/worktrees/remove", strings.NewReader(`{"channel_id":"ch1","worktree_path":"/tmp/wt"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestRemoveWorktree_RemoveError() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)

	rec := s.testRequest("POST", "/api/worktrees/remove", `{"channel_id":"ch1","worktree_path":"/nonexistent/worktree"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to remove worktree")
}

// ── listCommits ──

func (s *ServerSuite) TestListCommits_Success() {
	// initGitRepo creates a repo with one "init" commit.
	dir := initGitRepo(s.T())
	// Add a second commit.
	f := filepath.Join(dir, "hello.txt")
	require.NoError(s.T(), os.WriteFile(f, []byte("hi"), 0644))
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "second commit")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/commits", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 2)
	require.Equal(s.T(), "second commit", resp.Commits[0].Subject)
	require.Equal(s.T(), "init", resp.Commits[1].Subject)
	require.NotEmpty(s.T(), resp.Commits[0].Hash)
	require.NotEmpty(s.T(), resp.Commits[0].Short)
	require.NotEmpty(s.T(), resp.Commits[0].Author)
	require.NotEmpty(s.T(), resp.Commits[0].Date)
}

func (s *ServerSuite) TestListCommits_WithSkip() {
	dir := initGitRepo(s.T())
	// Add a second commit.
	f := filepath.Join(dir, "extra.txt")
	require.NoError(s.T(), os.WriteFile(f, []byte("x"), 0644))
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "second")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	// Skip 1 should give us only the "init" commit.
	rec := s.testRequest("GET", "/api/channels/ch-1/commits?limit=1&skip=1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 1)
	require.Equal(s.T(), "init", resp.Commits[0].Subject)
}

func (s *ServerSuite) TestListCommits_InvalidBranch() {
	dir := initGitRepo(s.T())

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/commits?branch=../../etc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListCommits_EmptyRepo() {
	dir := s.T().TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/commits", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Commits)
}

func (s *ServerSuite) TestListCommits_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/commits", srv.handleListCommits)

	req, _ := http.NewRequest("GET", "/api/channels/ch1/commits", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestListCommits_BadChannel() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)
	rec := s.testRequest("GET", "/api/channels/missing/commits", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListCommits_InvalidLimit() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	// Non-numeric limit falls back to 50 — should still return commits.
	rec := s.testRequest("GET", "/api/channels/ch-1/commits?limit=abc", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 1) // only the "init" commit
}

func (s *ServerSuite) TestListCommits_LimitClamped() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	// limit=500 is clamped to 200 — still returns the one commit.
	rec := s.testRequest("GET", "/api/channels/ch-1/commits?limit=500", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 1)
}

func (s *ServerSuite) TestListCommits_NegativeSkip() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	// Negative skip falls back to 0.
	rec := s.testRequest("GET", "/api/channels/ch-1/commits?skip=-5", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 1)
}

func (s *ServerSuite) TestListCommits_InvalidSkip() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	// Non-numeric skip falls back to 0.
	rec := s.testRequest("GET", "/api/channels/ch-1/commits?skip=abc", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 1)
}

func (s *ServerSuite) TestListCommits_ValidBranch() {
	dir := initGitRepo(s.T())
	// Create a branch with its own commit.
	cmd := exec.Command("git", "checkout", "-b", "feature/commits-test")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "branch.txt"), []byte("branch"), 0644))
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "branch commit")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/commits?branch=feature/commits-test", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 2) // "branch commit" + "init"
	require.Equal(s.T(), "branch commit", resp.Commits[0].Subject)
}

// ── parseCommitLog unit tests ──

func TestParseCommitLog_Empty(t *testing.T) {
	commits := parseCommitLog("")
	require.Empty(t, commits)
	require.NotNil(t, commits)
}

func TestParseCommitLog_WhitespaceOnly(t *testing.T) {
	commits := parseCommitLog("  \n\n  ")
	require.Empty(t, commits)
	require.NotNil(t, commits)
}

func TestParseCommitLog_MalformedLines(t *testing.T) {
	// Lines with fewer than 5 record-separator-delimited fields are skipped.
	commits := parseCommitLog("abc\x1edef\nfoo\x1ebar\x1ebaz")
	require.Empty(t, commits)
	require.NotNil(t, commits)
}

func TestParseCommitLog_MixedValidAndInvalid(t *testing.T) {
	input := "abc123\x1eabc\x1esubject\x1eauthor\x1e2025-01-01\ngarbage line\n\ndef456\x1edef\x1esubject2\x1eauthor2\x1e2025-01-02"
	commits := parseCommitLog(input)
	require.Len(t, commits, 2)
	require.Equal(t, "abc123", commits[0].Hash)
	require.Equal(t, "def456", commits[1].Hash)
}
