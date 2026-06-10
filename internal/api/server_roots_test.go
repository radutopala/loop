package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// --- handleListRoots tests ---

func (s *ServerSuite) TestListRootsNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/roots", srv.handleListRoots)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/roots", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestListRootsChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("GET", "/api/channels/missing/roots", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- resolveRootDir tests ---

func (s *ServerSuite) TestResolveRootDirDefaultRoot() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/files", nil)
	dirPath, err := s.srv.resolveRootDir(context.Background(), "ch-1", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/user/project", dirPath)
}

func (s *ServerSuite) TestResolveRootDirChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	req := httptest.NewRequest("GET", "/api/channels/missing/files", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "missing", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *ServerSuite) TestResolveRootDirFallbackToLoopDir() {
	s.srv.SetLoopDir("/home/test/.loop")

	s.store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir", DirPath: ""}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-nodir/files", nil)
	dirPath, err := s.srv.resolveRootDir(context.Background(), "ch-nodir", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/test/.loop/ch-nodir/work", dirPath)

	s.srv.SetLoopDir("")
}

// --- allDirPaths tests ---

func (s *ServerSuite) TestAllDirPathsWithExtraDirs() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-multi").
		Return(&db.Channel{ChannelID: "ch-multi", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-multi")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{tmpDir, "/home/user/lib"}, paths)
}

// TestAllDirPathsIncludesGlobalExtraDirs covers the global-config seed: a
// global-level extra_dirs (no project config) surfaces as a file-tree root,
// matching the mounts the agent container gets from the same merge.
func (s *ServerSuite) TestAllDirPathsIncludesGlobalExtraDirs() {
	tmpDir := s.T().TempDir() // no .loop/config.json — project sets nothing
	s.srv.loadConfig = func() (*config.Config, error) {
		return &config.Config{ExtraDirs: []string{"/home/user/global-lib"}}, nil
	}

	s.store.On("GetChannel", mock.Anything, "ch-global").
		Return(&db.Channel{ChannelID: "ch-global", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-global")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{tmpDir, "/home/user/global-lib"}, paths)
}

// TestAllDirPathsNilLoadConfigFallsBackToConfigLoad covers the nil-loader
// branch (production default): allDirPaths falls back to config.Load. The
// host's real global config may or may not define extra_dirs, so only the
// primary root is asserted.
func (s *ServerSuite) TestAllDirPathsNilLoadConfigFallsBackToConfigLoad() {
	tmpDir := s.T().TempDir()
	s.srv.loadConfig = nil

	s.store.On("GetChannel", mock.Anything, "ch-nilload").
		Return(&db.Channel{ChannelID: "ch-nilload", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-nilload")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), paths)
	require.Equal(s.T(), tmpDir, paths[0])
}

// TestAllDirPathsGlobalConfigLoadError falls back to an empty base when the
// global config can't be loaded — project extra_dirs still resolve.
func (s *ServerSuite) TestAllDirPathsGlobalConfigLoadError() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))
	s.srv.loadConfig = func() (*config.Config, error) { return nil, fmt.Errorf("boom") }

	s.store.On("GetChannel", mock.Anything, "ch-loaderr").
		Return(&db.Channel{ChannelID: "ch-loaderr", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-loaderr")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{tmpDir, "/home/user/lib"}, paths)
}

// TestAllDirPathsWorktreeUnionsParentExtraDirs covers the worktree branch:
// a worktree channel's file-tree roots include the parent channel's
// extra_dirs (three-layer merge), not just the parent dir the worktree
// config seeds — matching the roots the agent container gets.
func (s *ServerSuite) TestAllDirPathsWorktreeUnionsParentExtraDirs() {
	parentDir := s.T().TempDir()
	parentLoop := filepath.Join(parentDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(parentLoop, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(parentLoop, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/shared-lib"]}`),
		0644,
	))

	worktreeDir := s.T().TempDir()
	wtLoop := filepath.Join(worktreeDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(wtLoop, 0755))
	// Seeded form written by worktree.Creator.Create: extra_dirs -> parent dir.
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(wtLoop, "config.json"),
		fmt.Appendf(nil, `{"extra_dirs": [%q]}`, parentDir),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-wt").
		Return(&db.Channel{ChannelID: "ch-wt", DirPath: worktreeDir, Worktree: true, ParentID: "ch-parent"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-parent").
		Return(&db.Channel{ChannelID: "ch-parent", DirPath: parentDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-wt")
	require.NoError(s.T(), err)
	// worktree dir, then parent's own extra_dirs, then the seeded parent dir.
	require.Equal(s.T(), []string{worktreeDir, "/home/user/shared-lib", parentDir}, paths)
}

func (s *ServerSuite) TestAllDirPathsNoExtraDirs() {
	tmpDir := s.T().TempDir()
	// No .loop/config.json — extra dirs should be empty.

	s.store.On("GetChannel", mock.Anything, "ch-noextra").
		Return(&db.Channel{ChannelID: "ch-noextra", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-noextra")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{tmpDir}, paths)
}

func (s *ServerSuite) TestAllDirPathsChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return((*db.Channel)(nil), nil)

	_, err := s.srv.allDirPaths(context.Background(), "ch-err")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *ServerSuite) TestAllDirPathsExpandsTilde() {
	s.srv.sys = s.sys

	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["~/dev/foo", "/abs/path", "~/dev/bar"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-tilde").
		Return(&db.Channel{ChannelID: "ch-tilde", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-tilde")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{
		tmpDir,
		"/home/testuser/dev/foo",
		"/abs/path",
		"/home/testuser/dev/bar",
	}, paths)
}

func (s *ServerSuite) TestExpandHomePathNoTilde() {
	s.srv.sys = s.sys
	require.Equal(s.T(), "/abs/path", s.srv.expandHomePath("/abs/path"))
	require.Equal(s.T(), "relative/path", s.srv.expandHomePath("relative/path"))
	require.Equal(s.T(), "~user/dev", s.srv.expandHomePath("~user/dev"))
}

func (s *ServerSuite) TestExpandHomePathHomeError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", fmt.Errorf("no home"))
	s.srv.sys = sys

	require.Equal(s.T(), "~/dev/foo", s.srv.expandHomePath("~/dev/foo"))
}

func (s *ServerSuite) TestExpandHomePathEmptyHome() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", nil)
	s.srv.sys = sys

	require.Equal(s.T(), "~/dev/foo", s.srv.expandHomePath("~/dev/foo"))
}

// --- resolveRootDir with extra dirs ---

func (s *ServerSuite) TestResolveRootDirWithExtraDir() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib", "/home/user/common"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-extra").
		Return(&db.Channel{ChannelID: "ch-extra", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-extra/files?root=1", nil)
	dirPath, err := s.srv.resolveRootDir(context.Background(), "ch-extra", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/user/lib", dirPath)
}

func (s *ServerSuite) TestResolveRootDirInvalidIndex() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-badidx").
		Return(&db.Channel{ChannelID: "ch-badidx", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-badidx/files?root=5", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "ch-badidx", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid root index")
}

func (s *ServerSuite) TestResolveRootDirAllDirPathsError() {
	s.store.On("GetChannel", mock.Anything, "ch-missing").
		Return((*db.Channel)(nil), nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-missing/files?root=1", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "ch-missing", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *ServerSuite) TestResolveRootDirNegativeIndex() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-neg").
		Return(&db.Channel{ChannelID: "ch-neg", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-neg/files?root=-1", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "ch-neg", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid root index")
}

// --- handleListRoots success ---

func (s *ServerSuite) TestListRootsSuccess() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-roots").
		Return(&db.Channel{ChannelID: "ch-roots", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-roots/roots", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp listRootsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Roots, 2)
	require.Equal(s.T(), 0, resp.Roots[0].Index)
	require.Equal(s.T(), tmpDir, resp.Roots[0].Path)
	require.Equal(s.T(), filepath.Base(tmpDir), resp.Roots[0].Name)
	require.Equal(s.T(), 1, resp.Roots[1].Index)
	require.Equal(s.T(), "/home/user/lib", resp.Roots[1].Path)
	require.Equal(s.T(), "lib", resp.Roots[1].Name)
}

func (s *ServerSuite) TestListRootsSkipsEmptyPaths() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["", "/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-empty").
		Return(&db.Channel{ChannelID: "ch-empty", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-empty/roots", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp listRootsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	// Empty path should be skipped, so only tmpDir and /home/user/lib remain.
	require.Len(s.T(), resp.Roots, 2)
	require.Equal(s.T(), 0, resp.Roots[0].Index)
	require.Equal(s.T(), 2, resp.Roots[1].Index) // index 1 was empty, so index 2 is lib
}
