package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/testutil"
)

// --- memoryDir ---

func (s *MainSuite) TestMemoryDir() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/testuser", nil)
	dir, err := s.app.memoryDir("/Users/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/testuser/.claude/projects/-Users-dev-loop/memory", dir)
}

func (s *MainSuite) TestMemoryDirDotPaths() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/testuser", nil)
	dir, err := s.app.memoryDir("/Users/me/.loop/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/testuser/.claude/projects/-Users-me--loop-work/memory", dir)
}

func (s *MainSuite) TestMemoryDirHomeDirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("", errors.New("no home"))
	_, err := s.app.memoryDir("/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

// --- multiDirIndexer ---

type mockMemIndexer struct {
	mock.Mock
}

func (m *mockMemIndexer) Index(ctx context.Context, memoryPath, dirPath string, excludePaths []string) (int, error) {
	args := m.Called(ctx, memoryPath, dirPath, excludePaths)
	return args.Int(0), args.Error(1)
}

func (m *mockMemIndexer) Search(ctx context.Context, dirPath, query string, topK int) ([]memory.SearchResult, error) {
	args := m.Called(ctx, dirPath, query, topK)
	return args.Get(0).([]memory.SearchResult), args.Error(1)
}

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{0.1, 0.2, 0.3}
	}
	return result, nil
}

func (f *fakeEmbedder) Dimensions() int { return 3 }

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPaths() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.False(s.T(), entries[0].global)
	// CLAUDE.md entries: global, project root, project .claude/
	require.Equal(s.T(), "/home/test/.claude/CLAUDE.md", entries[1].path)
	require.True(s.T(), entries[1].global)
	require.Equal(s.T(), "/home/user/project/CLAUDE.md", entries[2].path)
	require.False(s.T(), entries[2].global)
	require.Equal(s.T(), "/home/user/project/.claude/CLAUDE.md", entries[3].path)
	require.False(s.T(), entries[3].global)
	require.Equal(s.T(), "/home/user/project/memory", entries[4].path)
	require.False(s.T(), entries[4].global) // relative config path
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsHomeDirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("", errors.New("no home"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	entries, excludePaths := mdi.resolveMemoryPaths("/path")
	require.Len(s.T(), entries, 3)
	require.Empty(s.T(), excludePaths)
	// No auto-memory or global CLAUDE.md when home dir fails.
	require.Equal(s.T(), "/path/CLAUDE.md", entries[0].path)
	require.False(s.T(), entries[0].global)
	require.Equal(s.T(), "/path/.claude/CLAUDE.md", entries[1].path)
	require.False(s.T(), entries[1].global)
	require.Equal(s.T(), "/path/memory", entries[2].path)
	require.False(s.T(), entries[2].global)
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsWithGlobalAndProject() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)
	s.app.loadProjectMemoryPaths = func(_ string) []string { return []string{"./docs/arch.md"} }

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"/shared/knowledge"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 6)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.False(s.T(), entries[0].global)
	// CLAUDE.md entries
	require.Equal(s.T(), "/home/test/.claude/CLAUDE.md", entries[1].path)
	require.True(s.T(), entries[1].global)
	require.Equal(s.T(), "/home/user/project/CLAUDE.md", entries[2].path)
	require.False(s.T(), entries[2].global)
	require.Equal(s.T(), "/home/user/project/.claude/CLAUDE.md", entries[3].path)
	require.False(s.T(), entries[3].global)
	require.Equal(s.T(), "/shared/knowledge", entries[4].path)
	require.True(s.T(), entries[4].global) // absolute config path
	require.Equal(s.T(), "/home/user/project/docs/arch.md", entries[5].path)
	require.False(s.T(), entries[5].global) // relative project path
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsDedup() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)
	// Project config returns paths that duplicate a global path.
	s.app.loadProjectMemoryPaths = func(_ string) []string {
		return []string{"./memory", "/shared/knowledge"}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "/shared/knowledge"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	// Should be deduplicated: auto-memory, CLAUDE.md x3, project/memory, /shared/knowledge — no duplicates.
	require.Len(s.T(), entries, 6)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.Equal(s.T(), "/home/test/.claude/CLAUDE.md", entries[1].path)
	require.Equal(s.T(), "/home/user/project/CLAUDE.md", entries[2].path)
	require.Equal(s.T(), "/home/user/project/.claude/CLAUDE.md", entries[3].path)
	require.Equal(s.T(), "/home/user/project/memory", entries[4].path)
	require.Equal(s.T(), "/shared/knowledge", entries[5].path)
	require.True(s.T(), entries[5].global)
}

func (s *MainSuite) TestResolveMemoryPathsWithExclusions() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "!./memory/drafts"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5) // auto-memory + CLAUDE.md x3 + ./memory
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/home/user/project/memory/drafts", excludePaths[0])
}

func (s *MainSuite) TestResolveMemoryPathsAbsoluteExclusion() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "!/shared/secret"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5) // auto-memory + CLAUDE.md x3 + ./memory
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/shared/secret", excludePaths[0])
}

func (s *MainSuite) TestResolveMemoryPathsProjectExclusion() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)
	s.app.loadProjectMemoryPaths = func(_ string) []string {
		return []string{"./docs", "!./docs/wip"}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5) // auto-memory + CLAUDE.md x3 + ./docs
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/home/user/project/docs/wip", excludePaths[0])
}

func (s *MainSuite) TestResolveRelativePath() {
	require.Equal(s.T(), "/project/memory", resolveRelativePath("/project", "./memory"))
	require.Equal(s.T(), "/project/docs/arch.md", resolveRelativePath("/project", "./docs/arch.md"))
	require.Equal(s.T(), "/project/notes.md", resolveRelativePath("/project", "notes.md"))
	require.Equal(s.T(), "/absolute/path", resolveRelativePath("/project", "/absolute/path"))
}

func (s *MainSuite) TestLoadProjectMemoryPathsDefault() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"memory": {"paths": ["/extra/docs", "./notes.md"]}}`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Equal(s.T(), []string{"/extra/docs", "./notes.md"}, paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsHJSON() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{
			// A comment
			"memory": {"paths": ["/docs"]},
		}`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Equal(s.T(), []string{"/docs"}, paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsMissingFile() {
	paths := s.app.defaultLoadProjectMemoryPaths("/nonexistent")
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsInvalidJSON() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{not valid`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsNoMemoryPaths() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"claude_model": "opus"}`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestMultiDirIndexerSearch() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(s.T().TempDir(), nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return([]*db.MemoryFile{}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/nonexistent/project", "test", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerSearchWithError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(s.T().TempDir(), nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	// GetMemoryFilesByDirPath returning an error triggers the error path
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/nonexistent/project", "test", 5)
	require.Error(s.T(), err)
	require.Nil(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerIndex() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(s.T().TempDir(), nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/nonexistent/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, count)
}

func (s *MainSuite) TestMultiDirIndexerIndexWithError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("stat error"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/some/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, count) // Error was logged, not returned
	mi.AssertExpectations(s.T())
}

func (s *MainSuite) TestMultiDirIndexerIndexWithCount() {
	tmpDir := s.T().TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	require.NoError(s.T(), os.MkdirAll(memDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "notes.md"), []byte("## Topic\nSome content\n"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, tmpDir)
	require.NoError(s.T(), err)
	require.Greater(s.T(), count, 0) // Should have indexed files
}

func (s *MainSuite) TestMultiDirIndexerSearchWithSortAndTopK() {
	tmpDir := s.T().TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	require.NoError(s.T(), os.MkdirAll(memDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "a.md"), []byte("content a"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "b.md"), []byte("content b"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	emb1 := embeddings.SerializeFloat32([]float32{0.1, 0.2, 0.3})
	emb2 := embeddings.SerializeFloat32([]float32{0.3, 0.2, 0.1})
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return([]*db.MemoryFile{
		{FilePath: "a.md", Content: "content a", Embedding: emb1, Dimensions: 3},
		{FilePath: "b.md", Content: "content b", Embedding: emb2, Dimensions: 3},
	}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, tmpDir, "test query", 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1) // topK=1 truncates to 1 result
}

func (s *MainSuite) TestMultiDirIndexerSearchWithGlobalPath() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	// Auto-memory + project CLAUDE.md paths (project-scoped)
	mi.On("Index", mock.Anything, mock.Anything, "/some/project", mock.Anything).Return(0, nil)
	// Global paths (global CLAUDE.md + absolute config path, scope = "")
	mi.On("Index", mock.Anything, mock.Anything, "", mock.Anything).Return(0, nil)
	mi.On("Search", mock.Anything, "/some/project", "test", 5).Return([]memory.SearchResult{}, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"/shared/knowledge"}, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/some/project", "test", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
	mi.AssertExpectations(s.T())
}

func (s *MainSuite) TestMultiDirIndexerSearchWithIndexError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	// Auto-memory path fails
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("index error"))
	mi.On("Search", mock.Anything, "/some/project", "test", 5).Return([]memory.SearchResult{}, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/some/project", "test", 5)
	require.NoError(s.T(), err) // Error was logged, not returned
	require.Empty(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerIndexWithGlobalPath() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	// Auto-memory + project CLAUDE.md paths (project-scoped)
	mi.On("Index", mock.Anything, mock.Anything, "/some/project", mock.Anything).Return(1, nil)
	// Global paths (global CLAUDE.md + absolute config path, scope = "")
	mi.On("Index", mock.Anything, mock.Anything, "", mock.Anything).Return(2, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"/shared/knowledge"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/some/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 7, count) // 3 project-scoped (1 each) + 2 global-scoped (2 each)
	mi.AssertExpectations(s.T())
}

// --- reindexAll ---

type mockChannelLister struct {
	mock.Mock
}

func (m *mockChannelLister) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (s *MainSuite) TestReindexAll() {
	tmpDir := s.T().TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	require.NoError(s.T(), os.MkdirAll(memDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "notes.md"), []byte("## Topic\nSome content\n"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: tmpDir},
		{ChannelID: "ch2", DirPath: ""},             // empty dir_path — skipped
		{ChannelID: "ch3", DirPath: "/nonexistent"}, // no files — 0 indexed
	}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	mdi.reindexAll(context.Background(), store)
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestReindexAllListError() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	store.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	mdi.reindexAll(context.Background(), store)
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestReindexAllCancelledContext() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: "/some/path"},
	}, nil)

	mdi.reindexAll(ctx, cl)
	cl.AssertExpectations(s.T())
	// Index should not be called because ctx is cancelled.
}

// --- reindexLoop ---

func (s *MainSuite) TestReindexLoop() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, app: s.app}

	var callCount atomic.Int32
	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Run(func(_ mock.Arguments) {
		callCount.Add(1)
	}).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: "/some/path"},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mdi.reindexLoop(ctx, cl, 1) // 1-second interval
		close(done)
	}()

	// Wait for at least 2 ListChannels calls (startup + one tick).
	require.Eventually(s.T(), func() bool {
		return callCount.Load() >= 2
	}, 5*time.Second, 100*time.Millisecond)

	cancel()
	<-done
}

func (s *MainSuite) TestReindexLoopDefaultInterval() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, app: s.app}

	var callCount atomic.Int32
	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Run(func(_ mock.Arguments) {
		callCount.Add(1)
	}).Return([]*db.Channel{}, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mdi.reindexLoop(ctx, cl, 0) // 0 = default interval
		close(done)
	}()

	// Wait for the startup reindexAll call.
	require.Eventually(s.T(), func() bool {
		return callCount.Load() >= 1
	}, 2*time.Second, 50*time.Millisecond)

	cancel()
	<-done
}

// --- newEmbedder ---

func (s *MainSuite) TestNewEmbedderOllama() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider:  "ollama",
			Model:     "nomic-embed-text",
			OllamaURL: "http://localhost:11434",
		}},
	}
	embedder, err := s.app.defaultNewEmbedder(cfg)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), embedder)
}

func (s *MainSuite) TestNewEmbedderOllamaDefaultModel() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider:  "ollama",
			OllamaURL: "http://localhost:11434",
		}},
	}
	embedder, err := s.app.defaultNewEmbedder(cfg)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), embedder)
}

func (s *MainSuite) TestNewEmbedderUnsupportedProvider() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider: "unknown",
		}},
	}
	_, err := s.app.defaultNewEmbedder(cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unsupported embeddings provider")
}

// --- runMCP with embeddings ---

func (s *MainSuite) TestRunMCPWithMemoryEnabled() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
			Memory: config.MemoryConfig{
				Enabled: true,
				Embeddings: config.EmbeddingsConfig{
					Provider:  "ollama",
					OllamaURL: "http://localhost:11434",
				},
			},
		}, nil
	}

	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "resolved-ch", nil
	}

	var optCount int
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		optCount = len(opts)
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("", "http://localhost:8222", "/home/user/dev/loop", logPath, "", "local", "", false)
	// WithMemoryAPI + WithWorkflowAPI
	require.Equal(s.T(), 2, optCount, "expected WithMemoryAPI + WithWorkflowAPI when config memory is enabled")
}

func (s *MainSuite) TestRunMCPWithMemoryEnabledChannelIDMode() {
	// When dirPath is empty (channel-id mode), memory should still be enabled via channel_id
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
			Memory: config.MemoryConfig{
				Enabled: true,
				Embeddings: config.EmbeddingsConfig{
					Provider: "ollama",
				},
			},
		}, nil
	}

	var optCount int
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		optCount = len(opts)
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "", false)
	// WithMemoryAPI + WithWorkflowAPI
	require.Equal(s.T(), 2, optCount, "expected WithMemoryAPI + WithWorkflowAPI when config memory is enabled in channel-id mode")
}

func (s *MainSuite) TestRunMCPWithMemoryNotEnabled() {
	// When memory is not enabled, memory tools should NOT be wired
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
		}, nil
	}

	var optCount int
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		optCount = len(opts)
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "ch1", nil
	}

	_ = s.app.runMCP("", "http://localhost:8222", "/path", logPath, "", "local", "", false)
	// Only WithWorkflowAPI should be passed; no memory option
	require.Equal(s.T(), 1, optCount, "expected only WithWorkflowAPI when memory is disabled")
}

func (s *MainSuite) TestRunMCPWithMemoryFlag() {
	// When --memory flag is true, memory tools should be enabled regardless of config.
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
		}, nil
	}

	var optCount int
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		optCount = len(opts)
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "", true)
	// WithMemoryAPI + WithWorkflowAPI
	require.Equal(s.T(), 2, optCount, "expected WithMemoryAPI + WithWorkflowAPI when memory flag is true")
}
