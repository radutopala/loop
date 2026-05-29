package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// ── validateFilePath ──

func (s *ServerSuite) TestValidateFilePath_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "src"), 0755))

	abs, err := s.srv.validateFilePath(tmpDir, "src")
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join(tmpDir, "src"), abs)
}

func (s *ServerSuite) TestValidateFilePath_Root() {
	tmpDir := s.T().TempDir()

	abs, err := s.srv.validateFilePath(tmpDir, ".")
	require.NoError(s.T(), err)
	require.Equal(s.T(), tmpDir, abs)
}

func (s *ServerSuite) TestValidateFilePath_EmptyPath() {
	_, err := s.srv.validateFilePath("/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path is required")
}

func (s *ServerSuite) TestValidateFilePath_AbsolutePath() {
	_, err := s.srv.validateFilePath("/tmp", "/etc/passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "absolute paths are not allowed")
}

func (s *ServerSuite) TestValidateFilePath_Traversal() {
	tmpDir := s.T().TempDir()

	_, err := s.srv.validateFilePath(tmpDir, "../etc/passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_DotDot() {
	tmpDir := s.T().TempDir()

	_, err := s.srv.validateFilePath(tmpDir, "..")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_NewFile() {
	tmpDir := s.T().TempDir()

	abs, err := s.srv.validateFilePath(tmpDir, "newfile.txt")
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join(tmpDir, "newfile.txt"), abs)
}

func (s *ServerSuite) TestValidateFilePath_InvalidRoot() {
	_, err := s.srv.validateFilePath("/nonexistent-root-dir-12345", "file.txt")
	require.Error(s.T(), err)
}

func (s *ServerSuite) TestValidateFilePath_SymlinkTraversal() {
	tmpDir := s.T().TempDir()
	outside := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))

	require.NoError(s.T(), os.Symlink(outside, filepath.Join(tmpDir, "escape")))

	_, err := s.srv.validateFilePath(tmpDir, "escape/secret.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_NewFileParentMissing() {
	tmpDir := s.T().TempDir()

	_, err := s.srv.validateFilePath(tmpDir, "nosuchdir/file.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path not found")
}

func (s *ServerSuite) TestValidateFilePath_NullByte() {
	tmpDir := s.T().TempDir()
	_, err := s.srv.validateFilePath(tmpDir, "file\x00.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid characters")
}

func (s *ServerSuite) TestValidateFilePath_SiblingDirPrefixBypass() {
	// Ensure /projects/foo doesn't match /projects/foobar via prefix.
	parent := s.T().TempDir()
	root := filepath.Join(parent, "foo")
	sibling := filepath.Join(parent, "foobar")
	require.NoError(s.T(), os.MkdirAll(root, 0755))
	require.NoError(s.T(), os.MkdirAll(sibling, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(sibling, "secret.txt"), []byte("secret"), 0644))

	// Create a symlink inside root that points to the sibling.
	require.NoError(s.T(), os.Symlink(sibling, filepath.Join(root, "link")))

	_, err := s.srv.validateFilePath(root, "link/secret.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_NewFileParentSymlinkOutside() {
	tmpDir := s.T().TempDir()
	outside := s.T().TempDir()

	require.NoError(s.T(), os.Symlink(outside, filepath.Join(tmpDir, "escape")))

	_, err := s.srv.validateFilePath(tmpDir, "escape/newfile.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

// ── handleListFiles ──

func (s *ServerSuite) TestListFiles_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "src"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=.", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"name":"src"`)
	require.Contains(s.T(), rec.Body.String(), `"type":"dir"`)
	require.Contains(s.T(), rec.Body.String(), `"name":"main.go"`)
	require.Contains(s.T(), rec.Body.String(), `"type":"file"`)
}

func (s *ServerSuite) TestListFiles_SubDir() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "src"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "src", "app.go"), []byte("package src"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=src", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"name":"app.go"`)
}

func (s *ServerSuite) TestListFiles_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("GET", "/api/channels/missing/files?path=.", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListFiles_PathTraversal() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=../etc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "path traversal")
}

func (s *ServerSuite) TestListFiles_StoreNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/files", srv.handleListFiles)

	req, _ := http.NewRequest("GET", "/api/channels/ch-1/files?path=.", nil)
	w := newRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestListFiles_DefaultPath() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("hi"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"name":"hello.txt"`)
}

func (s *ServerSuite) TestListFiles_DirsFirst() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "aaa.txt"), []byte("a"), 0644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "zzz"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=.", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	zzzIdx := indexOf(body, `"name":"zzz"`)
	aaaIdx := indexOf(body, `"name":"aaa.txt"`)
	require.Less(s.T(), zzzIdx, aaaIdx)
}

func (s *ServerSuite) TestListFiles_DirNotExists() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "gone"), 0755))
	os.RemoveAll(filepath.Join(tmpDir, "gone"))

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=gone", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListFiles_ReadDirError() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("ReadDir", mock.Anything).Return(nil, fmt.Errorf("injected readdir error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=.", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListFiles_MockReadDir() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("ReadDir", mock.Anything).Return([]fs.DirEntry{fakeDirEntry{name: "mock.go"}}, nil)
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=.", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"name":"mock.go"`)
}

// ── handleSearchFiles ──

func (s *ServerSuite) TestSearchFiles_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "src"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "src", "app.go"), []byte("package src"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=app", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(s.T(), body, `"rel_path":"src/app.go"`)
	require.Contains(s.T(), body, `"name":"app.go"`)
	require.Contains(s.T(), body, `"root_index":0`)
}

func (s *ServerSuite) TestSearchFiles_EmptyQReturnsAll() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(s.T(), body, `"rel_path":"a.txt"`)
	require.Contains(s.T(), body, `"rel_path":"b.txt"`)
}

func (s *ServerSuite) TestSearchFiles_BasenamePrefixRanksFirst() {
	tmpDir := s.T().TempDir()
	// "main.go" — basename starts with "ma".
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("a"), 0644))
	// "schema.go" — contains "ma" inside relPath but basename does not start with it.
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "schema.go"), []byte("b"), 0644))
	// "marshall.go" — basename starts with "ma".
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "marshall.go"), []byte("c"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=ma", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Basename-prefix matches should sort before contains-only.
	mainIdx := indexOf(body, `"name":"main.go"`)
	marshIdx := indexOf(body, `"name":"marshall.go"`)
	schemaIdx := indexOf(body, `"name":"schema.go"`)
	require.GreaterOrEqual(s.T(), schemaIdx, 0)
	require.GreaterOrEqual(s.T(), mainIdx, 0)
	require.GreaterOrEqual(s.T(), marshIdx, 0)
	require.Less(s.T(), mainIdx, schemaIdx)
	require.Less(s.T(), marshIdx, schemaIdx)
}

func (s *ServerSuite) TestSearchFiles_FuzzyMatch() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "matrix.go"), []byte("m"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	// "mtx" appears as subsequence in "matrix" but not as a substring.
	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=mtx", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"name":"matrix.go"`)
}

func (s *ServerSuite) TestSearchFiles_SkipsHardCodedDirs() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "node_modules"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "node_modules", "secret.go"), []byte("x"), 0644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".git", "secret.go"), []byte("x"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "secret.go"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=secret", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	// The root-level secret.go is included.
	require.Contains(s.T(), body, `"rel_path":"secret.go"`)
	// The node_modules/secret.go and .git/secret.go are skipped.
	require.NotContains(s.T(), body, "node_modules")
	require.NotContains(s.T(), body, ".git")
}

func (s *ServerSuite) TestSearchFiles_GitignoreFilter() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("secret-*.txt\n"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "secret-token.txt"), []byte("ignored"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "public.txt"), []byte("ok"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(s.T(), body, `"rel_path":"public.txt"`)
	require.NotContains(s.T(), body, "secret-token.txt")
}

func (s *ServerSuite) TestSearchFiles_MultiRoot() {
	primary := s.T().TempDir()
	secondary := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(primary, "p.go"), []byte("a"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(secondary, "s.go"), []byte("b"), 0644))

	// Project config in the primary dir adds the secondary as an extra root.
	require.NoError(s.T(), os.MkdirAll(filepath.Join(primary, ".loop"), 0755))
	cfgJSON := fmt.Sprintf(`{"extra_dirs": [%q]}`, secondary)
	require.NoError(s.T(), os.WriteFile(filepath.Join(primary, ".loop", "config.json"), []byte(cfgJSON), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: primary}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(s.T(), body, `"rel_path":"p.go"`)
	require.Contains(s.T(), body, `"rel_path":"s.go"`)
	require.Contains(s.T(), body, `"root_index":0`)
	require.Contains(s.T(), body, `"root_index":1`)
}

func (s *ServerSuite) TestSearchFiles_LimitCapped() {
	tmpDir := s.T().TempDir()
	for i := range 10 {
		require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0644))
	}

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=&limit=3", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp fileSearchResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Results, 3)
}

func (s *ServerSuite) TestSearchFiles_InvalidLimit() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?limit=notanumber", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchFiles_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("GET", "/api/channels/missing/files/search?q=foo", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchFiles_StoreNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/files/search", srv.handleSearchFiles)

	req, _ := http.NewRequest("GET", "/api/channels/ch-1/files/search?q=foo", nil)
	w := newRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

// ── fuzzyMatchSeq ──

func (s *ServerSuite) TestFuzzyMatchSeq() {
	require.True(s.T(), fuzzyMatchSeq("", "anything"))
	require.True(s.T(), fuzzyMatchSeq("abc", "aXbYcZ"))
	require.True(s.T(), fuzzyMatchSeq("ABC", "axbycz"))
	require.False(s.T(), fuzzyMatchSeq("abc", "acb"))
	require.False(s.T(), fuzzyMatchSeq("abc", ""))
}

// ── gitignoreMatch ──

func (s *ServerSuite) TestGitignoreMatch() {
	patterns := []string{"*.log", "secret-*.txt"}
	require.True(s.T(), gitignoreMatch(patterns, "debug.log"))
	require.True(s.T(), gitignoreMatch(patterns, "logs/debug.log"))
	require.True(s.T(), gitignoreMatch(patterns, "secret-token.txt"))
	require.False(s.T(), gitignoreMatch(patterns, "main.go"))
	require.False(s.T(), gitignoreMatch(nil, "anything.txt"))
}

func (s *ServerSuite) TestGitignoreMatch_RelPathOnly() {
	// Pattern with a path separator only matches relPath, not basename.
	patterns := []string{"subdir/*.txt"}
	require.True(s.T(), gitignoreMatch(patterns, "subdir/foo.txt"))
	require.False(s.T(), gitignoreMatch(patterns, "foo.txt"))
}

// ── loadGitignorePatterns ──

func (s *ServerSuite) TestLoadGitignorePatterns_EmptyAfterTrim() {
	tmpDir := s.T().TempDir()
	// Lines that become empty after trimming leading "/" and trailing "/"
	// must be skipped (no panic, no empty pattern in output).
	content := "/\n//\n*.log\n"
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(content), 0644))
	patterns := loadGitignorePatterns(s.srv.sys, tmpDir)
	require.Equal(s.T(), []string{"*.log"}, patterns)
}

// ── handleSearchFiles edge cases ──

func (s *ServerSuite) TestSearchFiles_FuzzyOnlyRanksLast() {
	tmpDir := s.T().TempDir()
	// "azbcz" fuzzy-matches "abz" (a→a, b→b, z→z) but neither starts with
	// "abz" nor contains it as a contiguous substring → rank 2 (fuzzy-only).
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "abzfile.txt"), []byte("x"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "azbcz.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=abz", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp fileSearchResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Results, 2)
	// Basename-prefix wins over fuzzy-only.
	require.Equal(s.T(), "abzfile.txt", resp.Results[0].RelPath)
	require.Equal(s.T(), "azbcz.txt", resp.Results[1].RelPath)
}

func (s *ServerSuite) TestSearchFiles_DirGitignored() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "secrets"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "secrets", "passwd.txt"), []byte("x"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "ok.txt"), []byte("x"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("secrets\n"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(s.T(), body, `"rel_path":"ok.txt"`)
	require.NotContains(s.T(), body, "passwd.txt")
}

func (s *ServerSuite) TestSearchFiles_EmptyExtraDir() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, ".loop"), 0755))
	// Project config with an empty extra_dirs entry — handler must skip the
	// empty rootAbs without crashing.
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(tmpDir, ".loop", "config.json"),
		[]byte(`{"extra_dirs":[""]}`),
		0644,
	))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=main", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "main.go")
}

func (s *ServerSuite) TestSearchFiles_WalkDirReturnsError() {
	s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(fmt.Errorf("injected walk error"))
	s.sys.Override("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
	s.srv.sys = &realOpenSys{s.sys}

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/tmp/fake"}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=foo", "")
	// Walk errors are logged but not surfaced; the response is still 200 with empty results.
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp fileSearchResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Results)
}

func (s *ServerSuite) TestSearchFiles_WalkCallbackErrorPaths() {
	// Mock WalkDir to invoke fn three times: once with walkErr+dir (→ SkipDir),
	// once with walkErr+file (→ nil), once with walkErr+nil (→ nil), and once
	// with a non-absolute path so filepath.Rel returns an error.
	call := s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(nil)
	call.Run(func(args mock.Arguments) {
		root := args.String(0)
		fn := args.Get(1).(fs.WalkDirFunc)
		injectedErr := fmt.Errorf("injected callback error")
		_ = fn(filepath.Join(root, "subdir"), fakeDirEntry{name: "subdir", isDir: true}, injectedErr)
		_ = fn(filepath.Join(root, "broken.txt"), fakeDirEntry{name: "broken.txt"}, injectedErr)
		_ = fn(filepath.Join(root, "nilentry"), nil, injectedErr)
		// Non-absolute path triggers filepath.Rel error.
		_ = fn("relative/path.txt", fakeDirEntry{name: "path.txt"}, nil)
	})
	s.sys.Override("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
	s.srv.sys = &realOpenSys{s.sys}

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/tmp/fake"}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/files/search?q=", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp fileSearchResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	// All callback invocations either errored or produced a Rel error → no results.
	require.Empty(s.T(), resp.Results)
}

// fakeDirEntry implements fs.DirEntry for testing.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.isDir }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, fmt.Errorf("no info") }

// ── handleReadFile ──

func (s *ServerSuite) TestReadFile_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("world"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=hello.txt", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "world", rec.Body.String())
	require.Equal(s.T(), "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
}

func (s *ServerSuite) TestReadFile_Binary() {
	tmpDir := s.T().TempDir()
	data := []byte{0x7F, 0x45, 0x4C, 0x46, 0x00}
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "thing.bin"), data, 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=thing.bin", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "true", rec.Header().Get("X-File-Binary"))
	require.Empty(s.T(), rec.Body.String())
}

func (s *ServerSuite) TestReadFile_Image() {
	cases := []struct {
		name string
		mime string
	}{
		{"image.png", "image/png"},
		{"image.PNG", "image/png"},
		{"image.jpg", "image/jpeg"},
		{"image.jpeg", "image/jpeg"},
		{"image.gif", "image/gif"},
		{"image.webp", "image/webp"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			tmpDir := s.T().TempDir()
			data := []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x01, 0x02}
			require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, tc.name), data, 0644))

			chID := "ch-" + tc.name
			s.store.On("GetChannel", mock.Anything, chID).
				Return(&db.Channel{ChannelID: chID, DirPath: tmpDir}, nil)

			rec := s.testRequest("GET", "/api/channels/"+chID+"/file?path="+tc.name, "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			require.Equal(s.T(), tc.mime, rec.Header().Get("Content-Type"))
			require.Empty(s.T(), rec.Header().Get("X-File-Binary"))
			require.Equal(s.T(), data, rec.Body.Bytes())
		})
	}
}

func (s *ServerSuite) TestReadFile_Video() {
	cases := []struct {
		name string
		mime string
	}{
		{"clip.mp4", "video/mp4"},
		{"clip.MP4", "video/mp4"},
		{"clip.webm", "video/webm"},
		{"clip.mov", "video/quicktime"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			tmpDir := s.T().TempDir()
			data := []byte{0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70} // fake ftyp header
			require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, tc.name), data, 0644))

			chID := "ch-" + tc.name
			s.store.On("GetChannel", mock.Anything, chID).
				Return(&db.Channel{ChannelID: chID, DirPath: tmpDir}, nil)

			rec := s.testRequest("GET", "/api/channels/"+chID+"/file?path="+tc.name, "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			require.Equal(s.T(), tc.mime, rec.Header().Get("Content-Type"))
			require.Equal(s.T(), "bytes", rec.Header().Get("Accept-Ranges")) // ServeContent enables Range/seek
			require.Equal(s.T(), data, rec.Body.Bytes())
		})
	}
}

// Videos must stream even when larger than maxFileSize — they bypass the text
// cap (and are never buffered whole into memory). Regression guard: an earlier
// fix accidentally moved the video branch below the size check, 413-ing clips.
func (s *ServerSuite) TestReadFile_VideoLargerThanMaxFileSize() {
	tmpDir := s.T().TempDir()
	data := make([]byte, maxFileSize+1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "big.mp4"), data, 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=big.mp4", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "video/mp4", rec.Header().Get("Content-Type"))
	require.Equal(s.T(), "bytes", rec.Header().Get("Accept-Ranges"))
	require.Equal(s.T(), data, rec.Body.Bytes())
}

// A Range request on a video returns 206 with just the requested bytes, so the
// <video> player can seek.
func (s *ServerSuite) TestReadFile_VideoRangeRequest() {
	tmpDir := s.T().TempDir()
	data := []byte("0123456789abcdef")
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "clip.mp4"), data, 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/file?path=clip.mp4", nil)
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("Range", "bytes=4-7")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusPartialContent, rec.Code)
	require.Equal(s.T(), "video/mp4", rec.Header().Get("Content-Type"))
	require.Equal(s.T(), "bytes 4-7/16", rec.Header().Get("Content-Range"))
	require.Equal(s.T(), []byte("4567"), rec.Body.Bytes())
}

// failOpenSys validates paths and stats against the real OS but fails Open, so
// handleReadFile's video open-error branch can be exercised.
type failOpenSys struct{ *testutil.MockSystem }

func (failOpenSys) Stat(name string) (os.FileInfo, error)    { return os.Stat(name) }
func (failOpenSys) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }
func (failOpenSys) Open(string) (*os.File, error)            { return nil, fmt.Errorf("injected open error") }

func (s *ServerSuite) TestReadFile_VideoOpenError() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "clip.mp4"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)
	s.srv.sys = failOpenSys{s.sys}

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=clip.mp4", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to read file")
}

func (s *ServerSuite) TestReadFile_LargeTextFile() {
	tmpDir := s.T().TempDir()
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'A'
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "large.txt"), data, 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=large.txt", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), 1024, len(rec.Body.Bytes()))
}

func (s *ServerSuite) TestReadFile_TooLarge() {
	tmpDir := s.T().TempDir()
	data := make([]byte, 6*1024*1024)
	for i := range data {
		data[i] = 'a'
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "big.txt"), data, 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=big.txt", "")
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, rec.Code)
}

func (s *ServerSuite) TestReadFile_NotFound() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=missing.txt", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestReadFile_PathTraversal() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=../etc/passwd", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReadFile_EmptyPath() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReadFile_Directory() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=subdir", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "directory")
}

func (s *ServerSuite) TestReadFile_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/file", srv.handleReadFile)

	req, _ := http.NewRequest("GET", "/api/channels/ch-1/file?path=test.txt", nil)
	w := newRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestReadFile_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("GET", "/api/channels/missing/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReadFile_StatError() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("Stat", mock.Anything).Return(nil, fmt.Errorf("injected stat error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestReadFile_ReadError() {
	tmpDir := s.T().TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	require.NoError(s.T(), os.WriteFile(filePath, []byte("ok"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	info, err := os.Stat(filePath)
	require.NoError(s.T(), err)
	s.sys.Override("Stat", mock.Anything).Return(info, nil)
	s.sys.Override("ReadFile", mock.Anything).Return(nil, fmt.Errorf("injected read error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to read file")
}

// ── handleWriteFile ──

func (s *ServerSuite) TestWriteFile_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("old"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=test.txt", "new content")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"ok":true`)

	data, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new content", string(data))
}

func (s *ServerSuite) TestWriteFile_PathTraversal() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=../evil.txt", "hack")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestWriteFile_PreservesPermissions() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "script.sh"), []byte("#!/bin/sh"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=script.sh", "#!/bin/sh\necho hi")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	info, err := os.Stat(filepath.Join(tmpDir, "script.sh"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), fs.FileMode(0755), info.Mode().Perm())
}

func (s *ServerSuite) TestWriteFile_NewFile() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=new.txt", "hello")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	data, err := os.ReadFile(filepath.Join(tmpDir, "new.txt"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello", string(data))
}

func (s *ServerSuite) TestWriteFile_EmptyPath() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=", "content")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestWriteFile_TooLarge() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	bigBody := string(make([]byte, maxFileSize+1))
	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=big.txt", bigBody)
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, rec.Code)
}

func (s *ServerSuite) TestWriteFile_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/channels/{id}/file", srv.handleWriteFile)

	req, _ := http.NewRequest("PUT", "/api/channels/ch-1/file?path=test.txt", nil)
	w := newRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestWriteFile_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("PUT", "/api/channels/missing/file?path=test.txt", "content")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestWriteFile_WriteError() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "readonly.txt"), []byte("old"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("injected write error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=readonly.txt", "content")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestWriteFile_WriteFileVarError() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("injected write error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=new.txt", "content")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to write file")
}

func (s *ServerSuite) TestWriteFile_BodyReadError() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	req, _ := http.NewRequest("PUT", "/api/channels/ch-1/file?path=new.txt", &errReader{})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "failed to read request body")
}

type errReader struct{}

func (r *errReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("injected read error")
}

// ── handleDeleteFile ──

func (s *ServerSuite) TestDeleteFile_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "trash.txt"), []byte("bye"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=trash.txt", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"ok":true`)

	_, err := os.Stat(filepath.Join(tmpDir, "trash.txt"))
	require.True(s.T(), os.IsNotExist(err))
}

func (s *ServerSuite) TestDeleteFile_NotFound() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=missing.txt", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestDeleteFile_Directory() {
	tmpDir := s.T().TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(s.T(), os.MkdirAll(subDir, 0755))
	// Place a file inside so we can verify recursive removal.
	require.NoError(s.T(), os.WriteFile(filepath.Join(subDir, "inner.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=subdir", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	// Directory should be gone.
	_, err := os.Stat(subDir)
	require.True(s.T(), os.IsNotExist(err))
}

func (s *ServerSuite) TestDeleteFile_PathTraversal() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=../evil.txt", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestDeleteFile_EmptyPath() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestDeleteFile_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/channels/{id}/file", srv.handleDeleteFile)

	req, _ := http.NewRequest("DELETE", "/api/channels/ch-1/file?path=test.txt", nil)
	w := newRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestDeleteFile_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("DELETE", "/api/channels/missing/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestDeleteFile_RemoveError() {
	tmpDir := s.T().TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	require.NoError(s.T(), os.WriteFile(filePath, []byte("ok"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	info, err := os.Stat(filePath)
	require.NoError(s.T(), err)
	s.sys.Override("Stat", mock.Anything).Return(info, nil)
	s.sys.Override("Remove", mock.Anything).Return(fmt.Errorf("injected remove error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to delete file")
}

func (s *ServerSuite) TestDeleteFile_StatError() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("Stat", mock.Anything).Return(nil, fmt.Errorf("injected stat error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

// ── listDir ──

func (s *ServerSuite) TestListDir_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("hello"), 0644))

	entries, err := listDir(os.ReadDir, tmpDir)
	require.NoError(s.T(), err)
	require.Len(s.T(), entries, 2)
	require.Equal(s.T(), "subdir", entries[0].Name)
	require.Equal(s.T(), "dir", entries[0].Type)
	require.Equal(s.T(), "file.txt", entries[1].Name)
	require.Equal(s.T(), "file", entries[1].Type)
}

func (s *ServerSuite) TestListDir_SortsCaseInsensitive() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "Zebra.txt"), []byte("z"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "apple.txt"), []byte("a"), 0644))

	entries, err := listDir(os.ReadDir, tmpDir)
	require.NoError(s.T(), err)
	require.Len(s.T(), entries, 2)
	require.Equal(s.T(), "apple.txt", entries[0].Name)
	require.Equal(s.T(), "Zebra.txt", entries[1].Name)
}

func (s *ServerSuite) TestListDir_Error() {
	_, err := listDir(os.ReadDir, "/nonexistent-dir-12345")
	require.Error(s.T(), err)
}

// ── helpers ──

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func newRecorder() *httpRecorder {
	return &httpRecorder{header: http.Header{}}
}

type httpRecorder struct {
	Code   int
	body   []byte
	header http.Header
}

func (r *httpRecorder) Header() http.Header  { return r.header }
func (r *httpRecorder) WriteHeader(code int) { r.Code = code }
func (r *httpRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}

func (s *ServerSuite) TestValidateFilePathUnit() {
	tests := []struct {
		name    string
		root    string
		path    string
		wantErr string
	}{
		{"empty path", "/tmp", "", "path is required"},
		{"absolute path", "/tmp", "/etc/passwd", "absolute paths are not allowed"},
		{"dotdot", "/tmp", "..", "path traversal not allowed"},
		{"dotdot prefix", "/tmp", "../etc", "path traversal not allowed"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := s.srv.validateFilePath(tt.root, tt.path)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

// ── createDir ──

func (s *ServerSuite) TestCreateDir_Success() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/dir?path=newdir", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	info, err := os.Stat(filepath.Join(tmpDir, "newdir"))
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *ServerSuite) TestCreateDir_Nested() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/dir?path=a/b/c", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	info, err := os.Stat(filepath.Join(tmpDir, "a", "b", "c"))
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *ServerSuite) TestCreateDir_EmptyPath() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/dir?path=", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateDir_PathTraversal() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/dir?path=../escape", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// ── handleDeleteFile: RemoveAll error ──

// fakeFileInfo implements os.FileInfo for testing directory stat results.
type fakeFileInfo struct {
	name  string
	isDir bool
}

func (f fakeFileInfo) Name() string      { return f.name }
func (f fakeFileInfo) Size() int64       { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode { return 0755 }
func (f fakeFileInfo) ModTime() time.Time {
	return time.Time{}
}
func (f fakeFileInfo) IsDir() bool { return f.isDir }
func (f fakeFileInfo) Sys() any    { return nil }

func (s *ServerSuite) TestDeleteFile_RemoveAllError() {
	tmpDir := s.T().TempDir()
	subDir := filepath.Join(tmpDir, "mydir")
	require.NoError(s.T(), os.MkdirAll(subDir, 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	// Override Stat to return a directory FileInfo.
	s.sys.Override("Stat", mock.Anything).Return(fakeFileInfo{name: "mydir", isDir: true}, nil)
	// Override RemoveAll to return an error.
	s.sys.On("RemoveAll", mock.Anything).Return(fmt.Errorf("injected removeall error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=mydir", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to delete directory")
}

// ── validateDirPath ──

func (s *ServerSuite) TestValidateDirPath_AbsolutePath() {
	_, err := s.srv.validateDirPath("/tmp/root", "/etc/passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "absolute paths are not allowed")
}

func (s *ServerSuite) TestValidateDirPath_NullByte() {
	_, err := s.srv.validateDirPath("/tmp/root", "foo\x00bar")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path contains invalid characters")
}

func (s *ServerSuite) TestValidateDirPath_InvalidRoot() {
	_, err := s.srv.validateDirPath("/nonexistent-root-dir-99999", "sub")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid root directory")
}

func (s *ServerSuite) TestValidateDirPath_EmptyPath() {
	_, err := s.srv.validateDirPath("/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path is required")
}

func (s *ServerSuite) TestValidateDirPath_Traversal() {
	_, err := s.srv.validateDirPath("/tmp", "../escape")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateDirPath_DotDot() {
	_, err := s.srv.validateDirPath("/tmp", "..")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateDirPath_Success() {
	tmpDir := s.T().TempDir()
	abs, err := s.srv.validateDirPath(tmpDir, "newdir/sub")
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join(tmpDir, "newdir/sub"), abs)
}

func (s *ServerSuite) TestValidateDirPath_SymlinkTraversal() {
	root := s.T().TempDir()
	outside := s.T().TempDir()

	// Create a symlink inside root that points outside.
	require.NoError(s.T(), os.Symlink(outside, filepath.Join(root, "escape")))

	_, err := s.srv.validateDirPath(root, "escape/sub")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateDirPath_PathNotFound() {
	sys := new(testutil.MockSystem)
	// First call to EvalSymlinks (rootDir) succeeds.
	sys.On("EvalSymlinks", "/root").Return("/root", nil).Once()
	// All subsequent calls fail — including when the walk-up loop
	// reaches rootDir again, so it falls through to parent==ancestor at "/".
	sys.On("EvalSymlinks", mock.Anything).Return("", fmt.Errorf("no such file"))
	s.srv.sys = sys

	_, err := s.srv.validateDirPath("/root", "a")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path not found")
}

// ── handleCreateDir: missing coverage ──

func (s *ServerSuite) TestCreateDir_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/dir", srv.handleCreateDir)

	req, _ := http.NewRequest("POST", "/api/channels/ch-1/dir?path=newdir", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestCreateDir_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("POST", "/api/channels/missing/dir?path=newdir", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateDir_MkdirAllError() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(fmt.Errorf("injected mkdir error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("POST", "/api/channels/ch-1/dir?path=newdir", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to create directory")
}

// ── handleFilesExists ──

func (s *ServerSuite) TestFilesExists_Relative() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "real.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body := `{"paths":["real.txt","missing.txt"]}`
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	// Wire format: root_index must be emitted even for the primary root (0),
	// so the FE can distinguish "field present, value 0" from "field missing".
	require.Contains(s.T(), rec.Body.String(), `"root_index":0`)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 2)
	require.True(s.T(), resp.Results[0].Exists)
	require.Equal(s.T(), "real.txt", resp.Results[0].RelPath)
	require.Equal(s.T(), 0, resp.Results[0].RootIndex)
	require.False(s.T(), resp.Results[1].Exists)
}

func (s *ServerSuite) TestFilesExists_Absolute() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "abs.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body := fmt.Sprintf(`{"paths":[%q]}`, filepath.Join(tmpDir, "abs.txt"))
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 1)
	require.True(s.T(), resp.Results[0].Exists)
	require.Equal(s.T(), "abs.txt", resp.Results[0].RelPath)
}

func (s *ServerSuite) TestFilesExists_AbsoluteOutsideRoot() {
	tmpDir := s.T().TempDir()
	otherDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(otherDir, "outside.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body := fmt.Sprintf(`{"paths":[%q]}`, filepath.Join(otherDir, "outside.txt"))
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 1)
	require.False(s.T(), resp.Results[0].Exists)
}

func (s *ServerSuite) TestFilesExists_DirectoryNotFile() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body := `{"paths":["sub"]}`
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.False(s.T(), resp.Results[0].Exists)
}

func (s *ServerSuite) TestFilesExists_EmptyPath() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body := `{"paths":[""]}`
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.False(s.T(), resp.Results[0].Exists)
}

func (s *ServerSuite) TestFilesExists_TruncatesBatch() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "x.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	paths := make([]string, maxExistsBatch+10)
	for i := range paths {
		paths[i] = "x.txt"
	}
	enc, err := json.Marshal(filesExistsRequest{Paths: paths})
	require.NoError(s.T(), err)

	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", string(enc))
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, maxExistsBatch)
}

func (s *ServerSuite) TestFilesExists_BadJSON() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestFilesExists_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/files/exists", srv.handleFilesExists)

	req, _ := http.NewRequest("POST", "/api/channels/ch-1/files/exists", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestFilesExists_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("POST", "/api/channels/missing/files/exists", `{"paths":["x"]}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestFilesExists_AbsoluteRootSymlinkFail() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "real.txt"), []byte("x"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	// Force the absolute-path branch to fail on the path's EvalSymlinks call
	// by passing a non-existent absolute path under the (real) root.
	body := fmt.Sprintf(`{"paths":[%q]}`, filepath.Join(tmpDir, "ghost.txt"))
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.False(s.T(), resp.Results[0].Exists)
}

// resolveAndStat unit tests — exercise rarely-hit branches via mock sys.

func (s *ServerSuite) TestResolveAndStat_AbsoluteIsDir() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755))

	exists, _, _ := s.srv.resolveAndStat([]string{tmpDir}, filepath.Join(tmpDir, "subdir"))
	require.False(s.T(), exists)
}

func (s *ServerSuite) TestResolveAndStat_AbsoluteRootEvalSymlinksFails() {
	target := "/abs/file.txt"

	s.sys.Override("EvalSymlinks", target).Return(target, nil)
	s.sys.On("EvalSymlinks", "/missing-root").Return("", fmt.Errorf("injected eval err"))
	s.sys.Override("Stat", target).Return(mockFileInfoSizeBytes(0), nil)
	s.srv.sys = s.sys

	exists, _, _ := s.srv.resolveAndStat([]string{"/missing-root"}, target)
	require.False(s.T(), exists)
}

func (s *ServerSuite) TestFilesExists_RelativeTraversal() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body := `{"paths":["../etc/passwd"]}`
	rec := s.testRequest("POST", "/api/channels/ch-1/files/exists", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp filesExistsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.False(s.T(), resp.Results[0].Exists)
}

func (s *ServerSuite) TestResolveAndStat_AbsoluteFile() {
	target := "/A/file.txt"
	root := "/A"

	s.sys.Override("EvalSymlinks", target).Return(target, nil)
	s.sys.On("EvalSymlinks", root).Return(root, nil)
	s.sys.Override("Stat", target).Return(mockFileInfoSizeBytes(0), nil)
	s.srv.sys = s.sys

	exists, idx, rel := s.srv.resolveAndStat([]string{root}, target)
	require.True(s.T(), exists)
	require.Equal(s.T(), 0, idx)
	require.Equal(s.T(), "file.txt", rel)
}

// mockFileInfoSizeBytes returns a non-dir file info for tests.
func mockFileInfoSizeBytes(size int64) fs.FileInfo {
	return &mockFileInfo{name: "x", size: size, modTime: time.Time{}}
}
