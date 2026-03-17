package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
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

func (s *ServerSuite) TestListBranches_ExcludesWorktreeBranches() {
	dir := initGitRepo(s.T())
	// Create two branches: one stays local, one goes into a worktree.
	for _, b := range []string{"feature/local-only", "feature/in-worktree"} {
		cmd := exec.Command("git", "branch", b)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	cmd := exec.Command("git", "worktree", "add", filepath.Join(dir, ".worktrees", "wt1"), "feature/in-worktree")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch1/branches", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp branchListResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))

	// Local-only branch should be in branches list.
	require.Contains(s.T(), resp.Branches, "feature/local-only")
	// Worktree branch should NOT be in branches list.
	for _, b := range resp.Branches {
		require.NotEqual(s.T(), "feature/in-worktree", b)
	}
	// Worktree should appear in worktrees list.
	require.NotEmpty(s.T(), resp.Worktrees)
	found := false
	for _, wt := range resp.Worktrees {
		if wt.Branch == "feature/in-worktree" {
			found = true
		}
	}
	require.True(s.T(), found, "worktree branch should appear in worktrees list")
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
