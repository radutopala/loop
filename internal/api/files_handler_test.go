package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// ── validateFilePath ──

func (s *ServerSuite) TestValidateFilePath_Success() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "src"), 0755))

	abs, err := validateFilePath(tmpDir, "src")
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join(tmpDir, "src"), abs)
}

func (s *ServerSuite) TestValidateFilePath_Root() {
	tmpDir := s.T().TempDir()

	abs, err := validateFilePath(tmpDir, ".")
	require.NoError(s.T(), err)
	require.Equal(s.T(), tmpDir, abs)
}

func (s *ServerSuite) TestValidateFilePath_EmptyPath() {
	_, err := validateFilePath("/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path is required")
}

func (s *ServerSuite) TestValidateFilePath_AbsolutePath() {
	_, err := validateFilePath("/tmp", "/etc/passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "absolute paths are not allowed")
}

func (s *ServerSuite) TestValidateFilePath_Traversal() {
	tmpDir := s.T().TempDir()

	_, err := validateFilePath(tmpDir, "../etc/passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_DotDot() {
	tmpDir := s.T().TempDir()

	_, err := validateFilePath(tmpDir, "..")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_NewFile() {
	tmpDir := s.T().TempDir()

	abs, err := validateFilePath(tmpDir, "newfile.txt")
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join(tmpDir, "newfile.txt"), abs)
}

func (s *ServerSuite) TestValidateFilePath_InvalidRoot() {
	_, err := validateFilePath("/nonexistent-root-dir-12345", "file.txt")
	require.Error(s.T(), err)
}

func (s *ServerSuite) TestValidateFilePath_SymlinkTraversal() {
	tmpDir := s.T().TempDir()
	outside := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))

	require.NoError(s.T(), os.Symlink(outside, filepath.Join(tmpDir, "escape")))

	_, err := validateFilePath(tmpDir, "escape/secret.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_NewFileParentMissing() {
	tmpDir := s.T().TempDir()

	_, err := validateFilePath(tmpDir, "nosuchdir/file.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path not found")
}

func (s *ServerSuite) TestValidateFilePath_NullByte() {
	tmpDir := s.T().TempDir()
	_, err := validateFilePath(tmpDir, "file\x00.txt")
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

	_, err := validateFilePath(root, "link/secret.txt")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path traversal not allowed")
}

func (s *ServerSuite) TestValidateFilePath_NewFileParentSymlinkOutside() {
	tmpDir := s.T().TempDir()
	outside := s.T().TempDir()

	require.NoError(s.T(), os.Symlink(outside, filepath.Join(tmpDir, "escape")))

	_, err := validateFilePath(tmpDir, "escape/newfile.txt")
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
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=.", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListFiles_MockReadDir() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("ReadDir", mock.Anything).Return([]fs.DirEntry{fakeDirEntry{name: "mock.go"}}, nil)
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/channels/ch-1/files?path=.", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"name":"mock.go"`)
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
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x00}
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "image.png"), data, 0644))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=image.png", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "true", rec.Header().Get("X-File-Binary"))
	require.Empty(s.T(), rec.Body.String())
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
	subDir := filepath.Join(tmpDir, "locked")
	require.NoError(s.T(), os.MkdirAll(subDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(subDir, "secret.txt"), []byte("x"), 0644))
	require.NoError(s.T(), os.Chmod(subDir, 0000))
	s.T().Cleanup(func() { require.NoError(s.T(), os.Chmod(subDir, 0755)) })

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=locked/secret.txt", "")
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
	s.srv.sys = s.sys

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
	require.NoError(s.T(), os.Chmod(filepath.Join(tmpDir, "readonly.txt"), 0000))
	s.T().Cleanup(func() { require.NoError(s.T(), os.Chmod(filepath.Join(tmpDir, "readonly.txt"), 0644)) })

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=readonly.txt", "content")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestWriteFile_WriteFileVarError() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("injected write error"))
	s.srv.sys = s.sys

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
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=subdir", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "cannot delete directories")
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
	s.srv.sys = s.sys

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=test.txt", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to delete file")
}

func (s *ServerSuite) TestDeleteFile_StatError() {
	tmpDir := s.T().TempDir()
	subDir := filepath.Join(tmpDir, "locked")
	require.NoError(s.T(), os.MkdirAll(subDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(subDir, "secret.txt"), []byte("x"), 0644))
	require.NoError(s.T(), os.Chmod(subDir, 0000))
	s.T().Cleanup(func() { require.NoError(s.T(), os.Chmod(subDir, 0755)) })

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("DELETE", "/api/channels/ch-1/file?path=locked/secret.txt", "")
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

func TestValidateFilePathUnit(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateFilePath(tt.root, tt.path)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
