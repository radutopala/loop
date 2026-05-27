package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

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

// ── root scoping ──

func (s *ServerSuite) TestListCommits_RootParamSelectsExtraDir() {
	primary := initGitRepo(s.T())
	extra := initGitRepo(s.T())
	// Add a distinguishing commit to extra so we can tell which root was hit.
	require.NoError(s.T(), os.WriteFile(filepath.Join(extra, "extra.txt"), []byte("x"), 0644))
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = extra
	require.NoError(s.T(), cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "extra commit")
	cmd.Dir = extra
	require.NoError(s.T(), cmd.Run())

	require.NoError(s.T(), os.MkdirAll(filepath.Join(primary, ".loop"), 0o755))
	cfgJSON := `{"extra_dirs":[` + strconv.Quote(extra) + `]}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(primary, ".loop", "config.json"), []byte(cfgJSON), 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-roots").
		Return(&db.Channel{ChannelID: "ch-roots", DirPath: primary}, nil)

	// root=0 → primary (only the "init" commit).
	rec := s.testRequest("GET", "/api/channels/ch-roots/commits?root=0", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp commitsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 1)
	require.Equal(s.T(), "init", resp.Commits[0].Subject)

	// root=1 → extra (init + extra commit).
	rec = s.testRequest("GET", "/api/channels/ch-roots/commits?root=1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Commits, 2)
	require.Equal(s.T(), "extra commit", resp.Commits[0].Subject)
}

func (s *ServerSuite) TestListCommits_RootParamInvalid() {
	dir := initGitRepo(s.T())
	s.store.On("GetChannel", mock.Anything, "ch-bad-root").
		Return(&db.Channel{ChannelID: "ch-bad-root", DirPath: dir}, nil)

	// Non-numeric → 400.
	rec := s.testRequest("GET", "/api/channels/ch-bad-root/commits?root=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	// Negative → 400.
	rec = s.testRequest("GET", "/api/channels/ch-bad-root/commits?root=-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	// Out of range → 400 (no extra_dirs configured, only root=0 valid).
	rec = s.testRequest("GET", "/api/channels/ch-bad-root/commits?root=5", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
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
