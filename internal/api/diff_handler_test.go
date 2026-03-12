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
	require.Contains(s.T(), body, `"total_additions":1`)
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
			input: "10\t5\tfile.go\n3\t0\tREADME.md\n",
			expect: []diffFileEntry{
				{Path: "file.go", Additions: 10, Deletions: 5},
				{Path: "README.md", Additions: 3, Deletions: 0},
			},
		},
		{
			name:  "binary file",
			input: "-\t-\timage.png\n",
			expect: []diffFileEntry{
				{Path: "image.png", Binary: true},
			},
		},
		{
			name:  "malformed line",
			input: "not-a-valid-line\n10\t5\tfile.go\n",
			expect: []diffFileEntry{
				{Path: "file.go", Additions: 10, Deletions: 5},
			},
		},
		{
			name:  "mixed",
			input: "100\t50\tsrc/main.ts\n-\t-\ticon.icns\n0\t3\told.txt\n",
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
