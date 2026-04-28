package api

import (
	"errors"
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
)

func (s *ServerSuite) TestGitDiffGetChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(nil, errors.New("db error"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-err/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestGitDiffStoreNotConfigured() {
	srv := &Server{logger: s.srv.logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestGitDiffChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "no-such").Return(nil, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/no-such/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestGitDiffNoDirPath() {
	s.store.On("GetChannel", mock.Anything, "ch-empty").
		Return(&db.Channel{ChannelID: "ch-empty"}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-empty/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"files":[]`)
}

func (s *ServerSuite) TestGitDiffNotGitDir() {
	s.store.On("GetChannel", mock.Anything, "ch-tmp").
		Return(&db.Channel{ChannelID: "ch-tmp", DirPath: s.T().TempDir()}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-tmp/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"files":[]`)
}

func (s *ServerSuite) TestGitDiffDirPathFallback() {
	s.store.On("GetChannel", mock.Anything, "ch-fb").
		Return(&db.Channel{ChannelID: "ch-fb"}, nil) // no DirPath
	s.srv.loopDir = s.T().TempDir()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-fb/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// The fallback dir won't be a git repo, so should return empty files list.
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Contains(s.T(), w.Body.String(), `"files":[]`)
}

func (s *ServerSuite) TestGitDiffWithChanges() {
	// Create a temp git repo with an unstaged change.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	// Create and commit a file.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644))
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	require.NoError(s.T(), add.Run())
	commit := exec.Command("git", "commit", "-m", "init")
	commit.Dir = dir
	require.NoError(s.T(), commit.Run())
	// Modify the file (unstaged).
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\nworld\n"), 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-git").
		Return(&db.Channel{ChannelID: "ch-git", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-git/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(s.T(), body, `"hello.txt"`)
	require.Contains(s.T(), body, `"status":"unstaged"`)
	require.Contains(s.T(), body, `"total_additions":1`)
}

func (s *ServerSuite) TestGitDiffWithStagedFiles() {
	// Repo with one staged-modified file, one unstaged-modified file, and
	// one untracked file. Verifies all three statuses appear distinctly.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "stage_me.txt"), []byte("orig\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "modify_me.txt"), []byte("orig\n"), 0o644))
	gitRun(s.T(), dir, "add", ".")
	gitRun(s.T(), dir, "commit", "-m", "init")

	// stage_me.txt: modify and stage.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "stage_me.txt"), []byte("staged\n"), 0o644))
	gitRun(s.T(), dir, "add", "stage_me.txt")

	// modify_me.txt: modify but do not stage.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "modify_me.txt"), []byte("modified\n"), 0o644))

	// untracked.txt: new file, never added.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-staged").
		Return(&db.Channel{ChannelID: "ch-staged", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-staged/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(s.T(), body, `"stage_me.txt"`)
	require.Contains(s.T(), body, `"status":"staged"`)
	require.Contains(s.T(), body, `"modify_me.txt"`)
	require.Contains(s.T(), body, `"status":"unstaged"`)
	require.Contains(s.T(), body, `"untracked.txt"`)
	require.Contains(s.T(), body, `"status":"untracked"`)
}

func (s *ServerSuite) TestGitDiffPartiallyStaged() {
	// Same path, both staged and unstaged: stage one version, then modify
	// the worktree without re-staging. Expect two adjacent rows for the
	// same path with staged before unstaged.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "split.txt"), []byte("v1\n"), 0o644))
	gitRun(s.T(), dir, "add", ".")
	gitRun(s.T(), dir, "commit", "-m", "init")

	// Stage v2.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "split.txt"), []byte("v2\n"), 0o644))
	gitRun(s.T(), dir, "add", "split.txt")
	// Then write v3 without staging.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "split.txt"), []byte("v3\n"), 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-partial").
		Return(&db.Channel{ChannelID: "ch-partial", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-partial/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	// Both statuses must appear in the response.
	require.Contains(s.T(), body, `"status":"staged"`)
	require.Contains(s.T(), body, `"status":"unstaged"`)
	// Staged row precedes unstaged row in the JSON output.
	stagedIdx := strings.Index(body, `"status":"staged"`)
	unstagedIdx := strings.Index(body, `"status":"unstaged"`)
	require.Less(s.T(), stagedIdx, unstagedIdx, "staged entry should sort before unstaged for the same path")
}

func (s *ServerSuite) TestGitDiffEmptyRepoNoHead() {
	// Brand-new repo with no commits — `git diff --cached` returns a
	// non-zero exit. Handler should not error; it should return any
	// unstaged/untracked entries (here: one untracked file) with empty
	// staged set.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("hi\n"), 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-fresh").
		Return(&db.Channel{ChannelID: "ch-fresh", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-fresh/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(s.T(), body, `"fresh.txt"`)
	require.Contains(s.T(), body, `"status":"untracked"`)
	require.NotContains(s.T(), body, `"status":"staged"`)
}

func (s *ServerSuite) TestGitDiffWithUntrackedFiles() {
	// Create a temp git repo with an untracked file.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	// Create and commit a file so HEAD exists.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("ok\n"), 0o644))
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	require.NoError(s.T(), add.Run())
	commit := exec.Command("git", "commit", "-m", "init")
	commit.Dir = dir
	require.NoError(s.T(), commit.Run())

	// Add an untracked text file.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("line1\nline2\n"), 0o644))
	// Add an untracked binary file.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "image.bin"), []byte{0x89, 0x50, 0x00, 0x47}, 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-untracked").
		Return(&db.Channel{ChannelID: "ch-untracked", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-untracked/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(s.T(), body, `"newfile.txt"`)
	require.Contains(s.T(), body, `"image.bin"`)
	// newfile.txt has 2 lines → 2 additions.
	require.Contains(s.T(), body, `+line1`)
	require.Contains(s.T(), body, `+line2`)
	// Binary file should be marked.
	require.Contains(s.T(), body, `"binary":true`)
}

func (s *ServerSuite) TestGitDiffSortedTogether() {
	// Verify tracked changes and untracked files are sorted together by path.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	// Commit a file that sorts after the untracked file alphabetically.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "zz_tracked.txt"), []byte("old\n"), 0o644))
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	require.NoError(s.T(), add.Run())
	commit := exec.Command("git", "commit", "-m", "init")
	commit.Dir = dir
	require.NoError(s.T(), commit.Run())
	// Modify tracked file.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "zz_tracked.txt"), []byte("new\n"), 0o644))
	// Add untracked file that sorts before tracked.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "aa_untracked.txt"), []byte("hi\n"), 0o644))

	s.store.On("GetChannel", mock.Anything, "ch-sorted").
		Return(&db.Channel{ChannelID: "ch-sorted", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-sorted/diff", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	// aa_untracked should appear before zz_tracked in the JSON array.
	aaIdx := strings.Index(body, `"aa_untracked.txt"`)
	zzIdx := strings.Index(body, `"zz_tracked.txt"`)
	require.Greater(s.T(), aaIdx, -1)
	require.Greater(s.T(), zzIdx, -1)
	require.Less(s.T(), aaIdx, zzIdx, "untracked file should sort before tracked file alphabetically")
}

func (s *ServerSuite) TestGitDiffBranchToBranch() {
	// Create a temp git repo with two branches that differ.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	// Create initial commit on main.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	gitRun(s.T(), dir, "add", ".")
	gitRun(s.T(), dir, "commit", "-m", "init")

	// Create a feature branch with two extra files so sort comparator is exercised.
	gitRun(s.T(), dir, "checkout", "-b", "feature")
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("new stuff\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "another.txt"), []byte("more\n"), 0o644))
	gitRun(s.T(), dir, "add", ".")
	gitRun(s.T(), dir, "commit", "-m", "add feature")

	// Go back to main.
	gitRun(s.T(), dir, "checkout", "main")

	s.store.On("GetChannel", mock.Anything, "ch-branch").
		Return(&db.Channel{ChannelID: "ch-branch", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-branch/diff?source=main&target=feature", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(s.T(), body, `"feature.txt"`)
	require.Contains(s.T(), body, `"another.txt"`)
	require.Contains(s.T(), body, `"total_additions":2`)
}

func (s *ServerSuite) TestGitDiffBranchInvalidSource() {
	dir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-inv").
		Return(&db.Channel{ChannelID: "ch-inv", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-inv/diff?source=..bad&target=main", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid source branch name")
}

func (s *ServerSuite) TestGitDiffBranchInvalidTarget() {
	dir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-inv2").
		Return(&db.Channel{ChannelID: "ch-inv2", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-inv2/diff?source=main&target=..evil", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid target branch name")
}

func (s *ServerSuite) TestGitDiffBranchNonexistentRef() {
	// Create a git repo but reference a branch that doesn't exist.
	dir := s.T().TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644))
	gitRun(s.T(), dir, "add", ".")
	gitRun(s.T(), dir, "commit", "-m", "init")

	s.store.On("GetChannel", mock.Anything, "ch-noref").
		Return(&db.Channel{ChannelID: "ch-noref", DirPath: dir}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/diff", s.srv.handleGitDiff)

	req := httptest.NewRequest("GET", "/api/channels/ch-noref/diff?source=main&target=nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "git diff failed")
}

// gitRun is a test helper to run a git command in a directory.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{"empty", "", nil},
		{"single", "hello\n", []string{"hello"}},
		{"multiple", "a\nb\nc\n", []string{"a", "b", "c"}},
		{"whitespace", "  a  \n  b  \n", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitLines(tt.input)
			if len(tt.expect) == 0 {
				require.Empty(t, result)
			} else {
				require.Equal(t, tt.expect, result)
			}
		})
	}
}

func TestBuildUntrackedEntry_Text(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello\nworld\n"), 0o644))

	entry, patch := buildUntrackedEntry(dir, "new.txt")
	require.NotNil(t, entry)
	require.Equal(t, "new.txt", entry.Path)
	require.Equal(t, 2, entry.Additions)
	require.False(t, entry.Binary)
	require.Contains(t, patch, "diff --git a/new.txt b/new.txt")
	require.Contains(t, patch, "+hello")
	require.Contains(t, patch, "+world")
}

func TestBuildUntrackedEntry_Binary(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0x89, 0x50, 0x00}, 0o644))

	entry, patch := buildUntrackedEntry(dir, "img.bin")
	require.NotNil(t, entry)
	require.True(t, entry.Binary)
	require.Contains(t, patch, "Binary files")
}

func TestBuildUntrackedEntry_LargeBinary(t *testing.T) {
	dir := t.TempDir()
	// File larger than 512 bytes with a null byte past the 512 boundary
	// should still be detected as binary since the null is within the first 512.
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'A'
	}
	data[100] = 0 // null byte within first 512
	require.NoError(t, os.WriteFile(filepath.Join(dir, "large.bin"), data, 0o644))

	entry, patch := buildUntrackedEntry(dir, "large.bin")
	require.NotNil(t, entry)
	require.True(t, entry.Binary)
	require.Contains(t, patch, "Binary files")
}

func TestBuildUntrackedEntry_ReadError(t *testing.T) {
	entry, patch := buildUntrackedEntry("/nonexistent", "missing.txt")
	require.Nil(t, entry)
	require.Empty(t, patch)
}

func TestStampStatus(t *testing.T) {
	entries := []diffFileEntry{
		{Path: "a", Additions: 1},
		{Path: "b", Deletions: 2},
	}
	stampStatus(entries, statusStaged)
	require.Equal(t, statusStaged, entries[0].Status)
	require.Equal(t, statusStaged, entries[1].Status)
	// Stamping an empty slice is a no-op.
	stampStatus(nil, statusStaged)
}

func TestStatusPriority(t *testing.T) {
	require.Equal(t, 0, statusPriority(statusStaged))
	require.Equal(t, 1, statusPriority(statusUnstaged))
	require.Equal(t, 2, statusPriority(statusUntracked))
	require.Equal(t, 3, statusPriority(""))
	require.Equal(t, 3, statusPriority("unknown"))
}

func TestParseNumstat(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []diffFileEntry
	}{
		{
			name:   "empty",
			input:  "",
			expect: []diffFileEntry{},
		},
		{
			name:  "normal files",
			input: "10\t5\tfile.go\x003\t0\tREADME.md\x00",
			expect: []diffFileEntry{
				{Path: "file.go", Additions: 10, Deletions: 5},
				{Path: "README.md", Additions: 3, Deletions: 0},
			},
		},
		{
			name:  "binary file",
			input: "-\t-\timage.png\x00",
			expect: []diffFileEntry{
				{Path: "image.png", Binary: true},
			},
		},
		{
			name:  "malformed line",
			input: "not-a-valid-line\x0010\t5\tfile.go\x00",
			expect: []diffFileEntry{
				{Path: "file.go", Additions: 10, Deletions: 5},
			},
		},
		{
			name:  "renamed file",
			input: "115\t45\t\x00app/src/splitPane/persistence.ts\x00app/src/layouts/persistence.ts\x00",
			expect: []diffFileEntry{
				{Path: "app/src/layouts/persistence.ts", OldPath: "app/src/splitPane/persistence.ts", Additions: 115, Deletions: 45},
			},
		},
		{
			name:  "mixed with rename",
			input: "10\t5\tfile.go\x00100\t50\t\x00old/path.ts\x00new/path.ts\x003\t0\tREADME.md\x00",
			expect: []diffFileEntry{
				{Path: "file.go", Additions: 10, Deletions: 5},
				{Path: "new/path.ts", OldPath: "old/path.ts", Additions: 100, Deletions: 50},
				{Path: "README.md", Additions: 3, Deletions: 0},
			},
		},
		{
			name:  "mixed",
			input: "100\t50\tsrc/main.ts\x00-\t-\ticon.icns\x000\t3\told.txt\x00",
			expect: []diffFileEntry{
				{Path: "src/main.ts", Additions: 100, Deletions: 50},
				{Path: "icon.icns", Binary: true},
				{Path: "old.txt", Additions: 0, Deletions: 3},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseNumstat(tt.input)
			if len(tt.expect) == 0 {
				require.Empty(t, result)
			} else {
				require.Equal(t, tt.expect, result)
			}
		})
	}
}
