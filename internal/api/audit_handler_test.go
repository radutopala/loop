package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockAuditDirResolver struct {
	mock.Mock
}

func (m *mockAuditDirResolver) AuditDir(channelID string) string {
	return m.Called(channelID).String(0)
}

func (s *ServerSuite) TestHandleListAuditFilesNotConfigured() {
	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 501, w.Code)
}

func (s *ServerSuite) TestHandleListAuditFilesResolverReturnsEmpty() {
	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return("")
	s.srv.SetAuditDirResolver(resolver)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Files)
	require.Equal(s.T(), 0, resp.Total)
}

func (s *ServerSuite) TestHandleListAuditFilesDirMissingReturnsEmpty() {
	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return("/does/not/exist")
	s.srv.SetAuditDirResolver(resolver)

	s.sys.Override("ReadDir", "/does/not/exist").Return(nil, os.ErrNotExist)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Files)
}

func (s *ServerSuite) TestHandleListAuditFilesSuccessSortedNewestFirst() {
	tmpDir := s.T().TempDir()
	// Three audit files on different days + noise files that must be skipped.
	for _, name := range []string{
		"agentgate-2026-04-22.jsonl",
		"agentgate-2026-04-24.jsonl",
		"agentgate-2026-04-23.jsonl",
		"agentgate-bogus.jsonl",    // bad date
		"other-2026-04-24.jsonl",   // wrong prefix
		"agentgate-2026-04-22.log", // wrong suffix
	} {
		require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, name), []byte("{}\n"), 0644))
	}
	// A subdir that must be skipped.
	require.NoError(s.T(), os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755))

	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return(tmpDir)
	s.srv.SetAuditDirResolver(resolver)

	// Real ReadDir through osutil.RealSystem.
	entries, err := os.ReadDir(tmpDir)
	require.NoError(s.T(), err)
	s.sys.Override("ReadDir", tmpDir).Return(entries, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Files, 3)
	require.Equal(s.T(), 3, resp.Total)
	require.Equal(s.T(), "2026-04-24", resp.Files[0].Date)
	require.Equal(s.T(), "2026-04-23", resp.Files[1].Date)
	require.Equal(s.T(), "2026-04-22", resp.Files[2].Date)
}

func (s *ServerSuite) TestHandleListAuditFilesPagination() {
	tmpDir := s.T().TempDir()
	dates := []string{"2026-04-20", "2026-04-21", "2026-04-22", "2026-04-23", "2026-04-24"}
	for _, d := range dates {
		require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "agentgate-"+d+".jsonl"), []byte("{}\n"), 0644))
	}

	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return(tmpDir)
	s.srv.SetAuditDirResolver(resolver)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(s.T(), err)
	s.sys.Override("ReadDir", tmpDir).Return(entries, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit?offset=1&limit=2", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), 5, resp.Total)
	require.Len(s.T(), resp.Files, 2)
	// Offset 1 skips 2026-04-24 (newest) and returns 23, 22.
	require.Equal(s.T(), "2026-04-23", resp.Files[0].Date)
	require.Equal(s.T(), "2026-04-22", resp.Files[1].Date)
}

func (s *ServerSuite) TestHandleListAuditFilesOffsetBeyondTotal() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "agentgate-2026-04-24.jsonl"), []byte("{}\n"), 0644))

	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return(tmpDir)
	s.srv.SetAuditDirResolver(resolver)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(s.T(), err)
	s.sys.Override("ReadDir", tmpDir).Return(entries, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit?offset=99&limit=10", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), 1, resp.Total)
	require.Empty(s.T(), resp.Files)
}

// Covers the path where ReadDir succeeds with only non-matching entries, so
// `files` stays nil and the `page == nil` fallback replaces it with an empty
// slice.
func (s *ServerSuite) TestHandleListAuditFilesAllNoiseReturnsEmpty() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("x"), 0644))

	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return(tmpDir)
	s.srv.SetAuditDirResolver(resolver)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(s.T(), err)
	s.sys.Override("ReadDir", tmpDir).Return(entries, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(s.T(), resp.Files)
	require.Empty(s.T(), resp.Files)
	require.Equal(s.T(), 0, resp.Total)
}

// Covers the branch where DirEntry.Info() returns an error — the entry is
// skipped rather than failing the whole request.
func (s *ServerSuite) TestHandleListAuditFilesInfoError() {
	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return("/audit/dir")
	s.srv.SetAuditDirResolver(resolver)

	entries := []fs.DirEntry{fakeDirEntry{name: "agentgate-2026-04-24.jsonl"}}
	s.sys.Override("ReadDir", "/audit/dir").Return(entries, nil)
	s.srv.sys = s.sys

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Files)
	require.Equal(s.T(), 0, resp.Total)
}

func (s *ServerSuite) TestHandleDeleteAuditFileNotConfigured() {
	req := httptest.NewRequest("DELETE", "/api/channels/ch-1/audit/2026-04-24", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 501, w.Code)
}

func (s *ServerSuite) TestHandleDeleteAuditFileInvalidDate() {
	resolver := new(mockAuditDirResolver)
	s.srv.SetAuditDirResolver(resolver)

	req := httptest.NewRequest("DELETE", "/api/channels/ch-1/audit/not-a-date", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 400, w.Code)
}

func (s *ServerSuite) TestHandleDeleteAuditFileAuditNotConfigured() {
	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return("")
	s.srv.SetAuditDirResolver(resolver)

	req := httptest.NewRequest("DELETE", "/api/channels/ch-1/audit/2026-04-24", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 404, w.Code)
}

func (s *ServerSuite) TestHandleDeleteAuditFileRemoveError() {
	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return("/audit/dir")
	s.srv.SetAuditDirResolver(resolver)

	s.sys.Override("Remove", "/audit/dir/agentgate-2026-04-24.jsonl").Return(errors.New("boom"))
	s.srv.sys = s.sys

	req := httptest.NewRequest("DELETE", "/api/channels/ch-1/audit/2026-04-24", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 500, w.Code)
}

func (s *ServerSuite) TestHandleDeleteAuditFileSuccess() {
	tmpDir := s.T().TempDir()
	path := filepath.Join(tmpDir, "agentgate-2026-04-24.jsonl")
	require.NoError(s.T(), os.WriteFile(path, []byte("{}\n"), 0644))

	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return(tmpDir)
	s.srv.SetAuditDirResolver(resolver)

	s.sys.Override("Remove", path).Return(nil)
	s.srv.sys = s.sys

	req := httptest.NewRequest("DELETE", "/api/channels/ch-1/audit/2026-04-24", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 204, w.Code)
}

// Covers the `n > 500` branch in parsePaging — values above the cap are
// clamped to 500.
func (s *ServerSuite) TestHandleListAuditFilesLimitCappedAt500() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "agentgate-2026-04-24.jsonl"), []byte("{}\n"), 0644))

	resolver := new(mockAuditDirResolver)
	resolver.On("AuditDir", "ch-1").Return(tmpDir)
	s.srv.SetAuditDirResolver(resolver)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(s.T(), err)
	s.sys.Override("ReadDir", tmpDir).Return(entries, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/audit?limit=9999", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), 200, w.Code)

	var resp listAuditFilesResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), 1, resp.Total)
	require.Len(s.T(), resp.Files, 1)
}
