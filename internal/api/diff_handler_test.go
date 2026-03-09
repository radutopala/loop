package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
