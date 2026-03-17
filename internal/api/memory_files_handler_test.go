package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

func (s *ServerSuite) TestListMemoryFiles_Success() {
	tmpDir := s.T().TempDir()
	aPath := filepath.Join(tmpDir, "a.md")
	bPath := filepath.Join(tmpDir, "b.md")
	require.NoError(s.T(), os.WriteFile(aPath, []byte("a"), 0644))
	require.NoError(s.T(), os.WriteFile(bPath, []byte("b"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, tmpDir).Return([]db.MemoryFileInfo{
		{FilePath: aPath, DirPath: tmpDir},
		{FilePath: bPath, DirPath: tmpDir},
	}, nil)

	rec := s.testRequest("GET", "/api/memory/files?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "a.md")
	require.Contains(s.T(), rec.Body.String(), "b.md")
}

func (s *ServerSuite) TestListMemoryFiles_FiltersNonExisting() {
	tmpDir := s.T().TempDir()
	existPath := filepath.Join(tmpDir, "exists.md")
	require.NoError(s.T(), os.WriteFile(existPath, []byte("ok"), 0644))
	gonePath := filepath.Join(tmpDir, "gone.md")

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, tmpDir).Return([]db.MemoryFileInfo{
		{FilePath: existPath, DirPath: tmpDir},
		{FilePath: gonePath, DirPath: tmpDir},
	}, nil)

	rec := s.testRequest("GET", "/api/memory/files?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "exists.md")
	require.NotContains(s.T(), rec.Body.String(), "gone.md")
}

func (s *ServerSuite) TestListMemoryFiles_Empty() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/projects/foo"}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, "/projects/foo").Return(nil, nil)

	rec := s.testRequest("GET", "/api/memory/files?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"files":[]`)
}

func (s *ServerSuite) TestListMemoryFiles_StoreNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/memory/files", srv.handleListMemoryFiles)

	req := httptest.NewRequest("GET", "/api/memory/files?channel_id=ch1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestListMemoryFiles_MissingParams() {
	rec := s.testRequest("GET", "/api/memory/files", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReadMemoryFile_Success() {
	tmpDir := s.T().TempDir()
	fpath := filepath.Join(tmpDir, "test.md")
	require.NoError(s.T(), os.WriteFile(fpath, []byte("# Hello\nWorld"), 0644))

	rec := s.testRequest("GET", "/api/memory/file?path="+fpath, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "# Hello\nWorld", rec.Body.String())
	require.Contains(s.T(), rec.Result().Header.Get("Content-Type"), "text/plain")
}

func (s *ServerSuite) TestReadMemoryFile_NotFound() {
	rec := s.testRequest("GET", "/api/memory/file?path=/nonexistent/file.md", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestReadMemoryFile_RelativePath() {
	rec := s.testRequest("GET", "/api/memory/file?path=relative/file.md", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReadMemoryFile_NonMdExtension() {
	rec := s.testRequest("GET", "/api/memory/file?path=/tmp/file.txt", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReadMemoryFile_MissingPath() {
	rec := s.testRequest("GET", "/api/memory/file", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMemoryFiles_StoreError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/projects/foo"}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, "/projects/foo").Return(nil, os.ErrPermission)

	rec := s.testRequest("GET", "/api/memory/files?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestReadMemoryFile_ReadError() {
	s.sys.Override("ReadFile", mock.Anything).Return(nil, os.ErrPermission)
	s.srv.sys = s.sys

	rec := s.testRequest("GET", "/api/memory/file?path=/tmp/test.md", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestWriteMemoryFile_Success() {
	tmpDir := s.T().TempDir()
	fpath := filepath.Join(tmpDir, "test.md")
	require.NoError(s.T(), os.WriteFile(fpath, []byte("old"), 0644))

	rec := s.testRequest("PUT", "/api/memory/file?path="+fpath, "# Updated\nContent")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"ok":true`)

	data, err := os.ReadFile(fpath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "# Updated\nContent", string(data))
}

func (s *ServerSuite) TestWriteMemoryFile_CreatesNew() {
	tmpDir := s.T().TempDir()
	fpath := filepath.Join(tmpDir, "new.md")

	rec := s.testRequest("PUT", "/api/memory/file?path="+fpath, "# New file")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	data, err := os.ReadFile(fpath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "# New file", string(data))
}

func (s *ServerSuite) TestWriteMemoryFile_MissingPath() {
	rec := s.testRequest("PUT", "/api/memory/file", "content")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestWriteMemoryFile_RelativePath() {
	rec := s.testRequest("PUT", "/api/memory/file?path=relative/file.md", "content")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestWriteMemoryFile_NonMdExtension() {
	rec := s.testRequest("PUT", "/api/memory/file?path=/tmp/file.txt", "content")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestWriteMemoryFile_WriteError() {
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(os.ErrPermission)
	s.srv.sys = s.sys

	rec := s.testRequest("PUT", "/api/memory/file?path=/tmp/test.md", "content")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSearchMemoryFiles_Success() {
	tmpDir := s.T().TempDir()
	aPath := filepath.Join(tmpDir, "alpha.md")
	bPath := filepath.Join(tmpDir, "beta.md")
	require.NoError(s.T(), os.WriteFile(aPath, []byte("a"), 0644))
	require.NoError(s.T(), os.WriteFile(bPath, []byte("b"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, tmpDir).Return([]db.MemoryFileInfo{
		{FilePath: aPath, DirPath: tmpDir},
		{FilePath: bPath, DirPath: tmpDir},
	}, nil)

	rec := s.testRequest("GET", "/api/memory/files/search?channel_id=ch1&q=alpha", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "alpha.md")
	require.NotContains(s.T(), rec.Body.String(), "beta.md")
}

func (s *ServerSuite) TestSearchMemoryFiles_EmptyQuery() {
	tmpDir := s.T().TempDir()
	aPath := filepath.Join(tmpDir, "alpha.md")
	require.NoError(s.T(), os.WriteFile(aPath, []byte("a"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, tmpDir).Return([]db.MemoryFileInfo{
		{FilePath: aPath, DirPath: tmpDir},
	}, nil)

	rec := s.testRequest("GET", "/api/memory/files/search?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "alpha.md")
}

func (s *ServerSuite) TestSearchMemoryFiles_NoMatch() {
	tmpDir := s.T().TempDir()
	aPath := filepath.Join(tmpDir, "alpha.md")
	require.NoError(s.T(), os.WriteFile(aPath, []byte("a"), 0644))

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, tmpDir).Return([]db.MemoryFileInfo{
		{FilePath: aPath, DirPath: tmpDir},
	}, nil)

	rec := s.testRequest("GET", "/api/memory/files/search?channel_id=ch1&q=zzz", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"files":[]`)
}

func (s *ServerSuite) TestSearchMemoryFiles_StoreNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/memory/files/search", srv.handleSearchMemoryFiles)

	req := httptest.NewRequest("GET", "/api/memory/files/search?channel_id=ch1&q=test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestSearchMemoryFiles_MissingParams() {
	rec := s.testRequest("GET", "/api/memory/files/search", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchMemoryFiles_StoreError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/projects/foo"}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, "/projects/foo").Return(nil, os.ErrPermission)

	rec := s.testRequest("GET", "/api/memory/files/search?channel_id=ch1&q=test", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSearchMemoryFiles_FiltersNonExisting() {
	tmpDir := s.T().TempDir()
	existPath := filepath.Join(tmpDir, "exists.md")
	require.NoError(s.T(), os.WriteFile(existPath, []byte("ok"), 0644))
	gonePath := filepath.Join(tmpDir, "gone.md")

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: tmpDir}, nil)
	s.store.On("ListDistinctMemoryFilePaths", mock.Anything, tmpDir).Return([]db.MemoryFileInfo{
		{FilePath: existPath, DirPath: tmpDir},
		{FilePath: gonePath, DirPath: tmpDir},
	}, nil)

	rec := s.testRequest("GET", "/api/memory/files/search?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "exists.md")
	require.NotContains(s.T(), rec.Body.String(), "gone.md")
}

func (s *ServerSuite) TestWriteMemoryFile_BodyReadError() {
	req, _ := http.NewRequest("PUT", "/api/memory/file?path=/tmp/test.md", &errReader{})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "failed to read request body")
}

func (s *ServerSuite) TestWriteMemoryFile_TooLarge() {
	bigBody := string(make([]byte, maxFileSize+1))
	rec := s.testRequest("PUT", "/api/memory/file?path=/tmp/test.md", bigBody)
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, rec.Code)
}

func (s *ServerSuite) TestWriteMemoryFile_PreservesPermissions() {
	tmpDir := s.T().TempDir()
	fpath := filepath.Join(tmpDir, "perms.md")
	require.NoError(s.T(), os.WriteFile(fpath, []byte("old"), 0755))

	rec := s.testRequest("PUT", "/api/memory/file?path="+fpath, "new content")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	info, err := os.Stat(fpath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), os.FileMode(0755), info.Mode().Perm())
}
