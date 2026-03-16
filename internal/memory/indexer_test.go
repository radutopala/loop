package memory

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockEmbedder mocks the embeddings.Embedder interface.
type mockEmbedder struct {
	mock.Mock
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	args := m.Called(ctx, texts)
	return args.Get(0).([][]float32), args.Error(1)
}

func (m *mockEmbedder) Dimensions() int {
	return m.Called().Int(0)
}

type IndexerSuite struct {
	suite.Suite
	embedder *mockEmbedder
	store    *testutil.MockStore
	sys      *testutil.MockSystem
	indexer  *Indexer
}

func TestIndexerSuite(t *testing.T) {
	suite.Run(t, new(IndexerSuite))
}

func (s *IndexerSuite) SetupTest() {
	s.embedder = new(mockEmbedder)
	s.store = new(testutil.MockStore)
	s.sys = new(testutil.MockSystem)
	s.indexer = NewIndexer(s.embedder, s.store, slog.New(slog.NewTextHandler(os.Stderr, nil)), 0)
	s.indexer.sys = s.sys
	// Default: Stat returns os.ErrNotExist (tests that need it override).
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	// Default: EvalSymlinks returns the input path unchanged.
	s.setupEvalSymlinksPassthrough()
}

// setupEvalSymlinksPassthrough sets up EvalSymlinks to return the input path unchanged.
func (s *IndexerSuite) setupEvalSymlinksPassthrough() {
	call := s.sys.On("EvalSymlinks", mock.Anything).Return("", nil)
	call.Run(func(args mock.Arguments) {
		call.ReturnArguments = mock.Arguments{args.String(0), nil}
	})
}

// mockWalkDir sets up WalkDir to visit the given file paths.
func (s *IndexerSuite) mockWalkDir(paths []string) {
	call := s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(nil)
	call.Run(func(args mock.Arguments) {
		root := args.String(0)
		fn := args.Get(1).(fs.WalkDirFunc)
		_ = fn(root, &fakeDirEntry{name: root, isDir: true}, nil)
		for _, p := range paths {
			name := p[len(p)-1:]
			if idx := lastIndexByte(p, '/'); idx >= 0 {
				name = p[idx+1:]
			}
			_ = fn(p, &fakeDirEntry{name: name}, nil)
		}
	})
}

// stubFS overrides WalkDir, ReadFile, and Stat on the mock system for a test.
// It sets Stat to return a directory, WalkDir to visit the given paths,
// and ReadFile to return the given content for every file.
func (s *IndexerSuite) stubFS(paths []string, content string) {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "dir", isDir: true}, nil)
	s.mockWalkDir(paths)
	s.sys.Override("ReadFile", mock.Anything).Return([]byte(content), nil)
}

// stubFSFunc is like stubFS but accepts a custom readFile function.
func (s *IndexerSuite) stubFSFunc(paths []string, readFn func(string) ([]byte, error)) {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "dir", isDir: true}, nil)
	s.mockWalkDir(paths)
	call := s.sys.Override("ReadFile", mock.Anything).Return([]byte(nil), nil)
	call.Run(func(args mock.Arguments) {
		data, err := readFn(args.String(0))
		call.ReturnArguments = mock.Arguments{data, err}
	})
}

// expectEmbed sets up embedder and store mocks for a successful index of small files.
func (s *IndexerSuite) expectEmbed(ctx context.Context) {
	s.embedder.On("Embed", ctx, mock.Anything).Return([][]float32{{0.1}}, nil)
	s.embedder.On("Dimensions").Return(768)
	s.store.On("UpsertMemoryFile", ctx, mock.AnythingOfType("*db.MemoryFile")).Return(nil)
}

// --- Index tests ---

func (s *IndexerSuite) TestIndexNewFile() {
	s.stubFS([]string{"/memory/test.md"}, "## Docker\n\nCleanup info")

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return("", nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	s.store.AssertExpectations(s.T())
}

func (s *IndexerSuite) TestIndexSkipsUpToDate() {
	s.stubFS([]string{"/memory/test.md"}, "## Docker\n\nCleanup info")

	ctx := context.Background()
	expectedHash := contentHash("## Docker\n\nCleanup info")
	s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return(expectedHash, nil)

	n, err := s.indexer.Index(ctx, "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexNonExistentDir() {
	n, err := s.indexer.Index(context.Background(), "/nonexistent-path-that-does-not-exist", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexRecursesSubdirectories() {
	s.stubFSFunc([]string{"/memory/a.md", "/memory/sub/b.md"}, func(name string) ([]byte, error) {
		return []byte("content of " + name), nil
	})

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, mock.Anything, "").Return("", nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 2, n)
}

func (s *IndexerSuite) TestIndexSkipsNonMdFiles() {
	s.stubFS([]string{"/memory/notes.txt", "/memory/.vectors.db"}, "")

	n, err := s.indexer.Index(context.Background(), "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexErrorPaths() {
	tests := []struct {
		name  string
		setup func(ctx context.Context)
	}{
		{
			name: "read file error",
			setup: func(ctx context.Context) {
				s.sys.Override("ReadFile", mock.Anything).Return([]byte(nil), errors.New("read error"))
			},
		},
		{
			name: "embed error",
			setup: func(ctx context.Context) {
				s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return("", nil)
				s.embedder.On("Embed", ctx, mock.Anything).Return([][]float32(nil), errors.New("embed failed"))
			},
		},
		{
			name: "hash check error",
			setup: func(ctx context.Context) {
				s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return("", errors.New("db error"))
			},
		},
		{
			name: "upsert error",
			setup: func(ctx context.Context) {
				s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return("", nil)
				s.embedder.On("Embed", ctx, mock.Anything).Return([][]float32{{0.1}}, nil)
				s.embedder.On("Dimensions").Return(768)
				s.store.On("UpsertMemoryFile", ctx, mock.Anything).Return(errors.New("db error"))
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.stubFS([]string{"/memory/test.md"}, "content")
			ctx := context.Background()
			tt.setup(ctx)
			n, err := s.indexer.Index(ctx, "/memory", "", nil)
			require.NoError(s.T(), err) // logged, not returned
			require.Equal(s.T(), 0, n)
		})
	}
}

func (s *IndexerSuite) TestIndexWalkDirEntryError() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "dir", isDir: true}, nil)
	call := s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(nil)
	call.Run(func(args mock.Arguments) {
		root := args.String(0)
		fn := args.Get(1).(fs.WalkDirFunc)
		_ = fn(root, &fakeDirEntry{name: root, isDir: true}, nil)
		_ = fn("/memory/bad", nil, errors.New("permission denied"))
	})

	n, err := s.indexer.Index(context.Background(), "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexChunksLargeFile() {
	line := strings.Repeat("x", 100) + "\n"
	largeContent := strings.Repeat(line, (defaultMaxChunkChars/101)+2)
	require.Greater(s.T(), len(largeContent), defaultMaxChunkChars)

	s.stubFS([]string{"/memory/large.md"}, largeContent)

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/large.md", "").Return("", nil)
	// Header row (chunk_index=0, no embedding)
	s.store.On("UpsertMemoryFile", ctx, mock.MatchedBy(func(f *db.MemoryFile) bool {
		return f.ChunkIndex == 0 && f.ContentHash != "" && f.Dimensions == 0
	})).Return(nil)
	// Chunk rows (chunk_index > 0, with embedding)
	s.embedder.On("Embed", ctx, mock.Anything).Return([][]float32{{0.1}}, nil)
	s.embedder.On("Dimensions").Return(768)
	s.store.On("UpsertMemoryFile", ctx, mock.MatchedBy(func(f *db.MemoryFile) bool {
		return f.ChunkIndex > 0 && f.Dimensions == 768
	})).Return(nil)

	n, err := s.indexer.Index(ctx, "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	s.store.AssertExpectations(s.T())
}

func (s *IndexerSuite) TestIndexChunksErrorPaths() {
	largeContent := strings.Repeat("x\n", defaultMaxChunkChars)

	tests := []struct {
		name  string
		setup func(ctx context.Context)
	}{
		{
			name: "header upsert error",
			setup: func(ctx context.Context) {
				s.store.On("UpsertMemoryFile", ctx, mock.MatchedBy(func(f *db.MemoryFile) bool {
					return f.ChunkIndex == 0
				})).Return(errors.New("db error"))
			},
		},
		{
			name: "chunk upsert error",
			setup: func(ctx context.Context) {
				s.store.On("UpsertMemoryFile", ctx, mock.MatchedBy(func(f *db.MemoryFile) bool {
					return f.ChunkIndex == 0
				})).Return(nil)
				s.embedder.On("Embed", ctx, mock.Anything).Return([][]float32{{0.1}}, nil)
				s.embedder.On("Dimensions").Return(768)
				s.store.On("UpsertMemoryFile", ctx, mock.MatchedBy(func(f *db.MemoryFile) bool {
					return f.ChunkIndex > 0
				})).Return(errors.New("db error"))
			},
		},
		{
			name: "chunk embed error",
			setup: func(ctx context.Context) {
				s.store.On("UpsertMemoryFile", ctx, mock.MatchedBy(func(f *db.MemoryFile) bool {
					return f.ChunkIndex == 0
				})).Return(nil)
				s.embedder.On("Embed", ctx, mock.Anything).Return([][]float32(nil), errors.New("embed error"))
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.stubFS([]string{"/memory/large.md"}, largeContent)
			ctx := context.Background()
			s.store.On("GetMemoryFileHash", ctx, "/memory/large.md", "").Return("", nil)
			tt.setup(ctx)
			n, err := s.indexer.Index(ctx, "/memory", "", nil)
			require.NoError(s.T(), err) // logged, not returned
			require.Equal(s.T(), 0, n)
		})
	}
}

func (s *IndexerSuite) TestIndexWalkDirError() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "dir", isDir: true}, nil)
	s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(errors.New("walk error"))

	n, err := s.indexer.Index(context.Background(), "/memory", "", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "walking memory dir")
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexEmptyFileContent() {
	s.stubFS([]string{"/memory/empty.md"}, "   \n  \n  ")

	n, err := s.indexer.Index(context.Background(), "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexStaleDeletesOldEntry() {
	s.stubFS([]string{"/memory/test.md"}, "updated content")

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return("old-hash", nil)
	s.store.On("DeleteMemoryFile", ctx, "/memory/test.md", "").Return(nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	s.store.AssertCalled(s.T(), "DeleteMemoryFile", ctx, "/memory/test.md", "")
}

func (s *IndexerSuite) TestIndexResolvesSymlinks() {
	s.stubFS([]string{"/real/memory/test.md"}, "content")

	call := s.sys.Override("EvalSymlinks", mock.Anything).Return("", nil)
	call.Run(func(args mock.Arguments) {
		path := args.String(0)
		if path == "/project/memory" {
			call.ReturnArguments = mock.Arguments{"/real/memory", nil}
		} else {
			call.ReturnArguments = mock.Arguments{path, nil}
		}
	})

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/real/memory/test.md", "").Return("", nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/project/memory", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
}

func (s *IndexerSuite) TestIndexEvalSymlinksError() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "dir", isDir: true}, nil)
	s.sys.Override("EvalSymlinks", mock.Anything).Return("", errors.New("symlink error"))

	_, err := s.indexer.Index(context.Background(), "/memory", "", nil)
	require.ErrorContains(s.T(), err, "resolving symlinks")
}

func (s *IndexerSuite) TestIndexStatPermissionError() {
	s.sys.Override("Stat", mock.Anything).Return(nil, errors.New("permission denied"))

	_, err := s.indexer.Index(context.Background(), "/memory", "", nil)
	require.ErrorContains(s.T(), err, "stat memory path")
}

func (s *IndexerSuite) TestIndexFileReadNotExist() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "file", isDir: false}, nil)
	s.sys.Override("ReadFile", mock.Anything).Return([]byte(nil), os.ErrNotExist)

	n, err := s.indexer.Index(context.Background(), "/docs/notes.md", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexStaleDeleteError() {
	s.stubFS([]string{"/memory/test.md"}, "updated content")

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/test.md", "").Return("old-hash", nil)
	s.store.On("DeleteMemoryFile", ctx, "/memory/test.md", "").Return(errors.New("db error"))

	n, err := s.indexer.Index(ctx, "/memory", "", nil)
	require.NoError(s.T(), err) // logged, not returned
	require.Equal(s.T(), 0, n)
}

// --- Index file path tests ---

func (s *IndexerSuite) TestIndexSingleMdFile() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "file", isDir: false}, nil)
	s.sys.Override("ReadFile", mock.Anything).Return([]byte("## Topic\nSome content"), nil)

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/docs/notes.md", "").Return("", nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/docs/notes.md", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	s.store.AssertExpectations(s.T())
}

func (s *IndexerSuite) TestIndexNonMdFilePath() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "file", isDir: false}, nil)

	n, err := s.indexer.Index(context.Background(), "/docs/notes.txt", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

func (s *IndexerSuite) TestIndexSingleMdFileNotExist() {
	n, err := s.indexer.Index(context.Background(), "/nonexistent/file.md", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

// --- Exclusion tests ---

func (s *IndexerSuite) TestIsExcluded() {
	require.True(s.T(), isExcluded("/memory/drafts", []string{"/memory/drafts"}))
	require.True(s.T(), isExcluded("/memory/drafts/file.md", []string{"/memory/drafts"}))
	require.False(s.T(), isExcluded("/memory/drafts-v2", []string{"/memory/drafts"}))
	require.False(s.T(), isExcluded("/memory/other", []string{"/memory/drafts"}))
	require.False(s.T(), isExcluded("/memory/drafts", nil))
	require.False(s.T(), isExcluded("/memory/drafts", []string{}))
}

func (s *IndexerSuite) TestResolveExcludeSymlinks() {
	call := s.sys.Override("EvalSymlinks", mock.Anything).Return("", nil)
	call.Run(func(args mock.Arguments) {
		path := args.String(0)
		if path == "/project/memory/drafts" {
			call.ReturnArguments = mock.Arguments{"/real/memory/drafts", nil}
		} else {
			call.ReturnArguments = mock.Arguments{"", errors.New("not found")}
		}
	})

	// Resolved path replaces original; non-existent paths kept as-is.
	result := s.indexer.resolveExcludeSymlinks([]string{"/project/memory/drafts", "/nonexistent/path"})
	require.Equal(s.T(), []string{"/real/memory/drafts", "/nonexistent/path"}, result)

	// nil input returns nil.
	require.Nil(s.T(), s.indexer.resolveExcludeSymlinks(nil))
}

func (s *IndexerSuite) TestIndexExcludeWithSymlinks() {
	s.stubFSFunc(nil, func(name string) ([]byte, error) {
		return []byte("content of " + name), nil
	})

	evalCall := s.sys.Override("EvalSymlinks", mock.Anything).Return("", nil)
	evalCall.Run(func(args mock.Arguments) {
		path := args.String(0)
		switch path {
		case "/project/memory":
			evalCall.ReturnArguments = mock.Arguments{"/real/memory", nil}
		case "/project/memory/plans":
			evalCall.ReturnArguments = mock.Arguments{"/real/memory/plans", nil}
		default:
			evalCall.ReturnArguments = mock.Arguments{path, nil}
		}
	})
	walkCall := s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(nil)
	walkCall.Run(func(args mock.Arguments) {
		root := args.String(0)
		fn := args.Get(1).(fs.WalkDirFunc)
		_ = fn(root, &fakeDirEntry{name: root, isDir: true}, nil)
		_ = fn("/real/memory/plans", &fakeDirEntry{name: "plans", isDir: true}, nil)
		_ = fn("/real/memory/notes.md", &fakeDirEntry{name: "notes.md"}, nil)
	})

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/real/memory/notes.md", "").Return("", nil)
	s.expectEmbed(ctx)

	// Exclude /project/memory/plans (pre-symlink path) — should resolve and match /real/memory/plans.
	n, err := s.indexer.Index(ctx, "/project/memory", "", []string{"/project/memory/plans"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n) // Only notes.md indexed, plans/ excluded.
}

func (s *IndexerSuite) TestIndexExcludeDir() {
	s.stubFSFunc(nil, func(name string) ([]byte, error) {
		return []byte("content of " + name), nil
	})

	// Walk visits both a regular file and a file inside an excluded directory.
	walkCall := s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(nil)
	walkCall.Run(func(args mock.Arguments) {
		root := args.String(0)
		fn := args.Get(1).(fs.WalkDirFunc)
		_ = fn(root, &fakeDirEntry{name: root, isDir: true}, nil)
		_ = fn("/memory/drafts", &fakeDirEntry{name: "drafts", isDir: true}, nil)
		_ = fn("/memory/notes.md", &fakeDirEntry{name: "notes.md"}, nil)
	})

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/notes.md", "").Return("", nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/memory", "", []string{"/memory/drafts"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	s.store.AssertNotCalled(s.T(), "GetMemoryFileHash", ctx, "/memory/drafts/file.md", "")
}

func (s *IndexerSuite) TestIndexExcludeFile() {
	s.stubFSFunc([]string{"/memory/keep.md", "/memory/skip.md"}, func(name string) ([]byte, error) {
		return []byte("content of " + name), nil
	})

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/keep.md", "").Return("", nil)
	s.expectEmbed(ctx)

	n, err := s.indexer.Index(ctx, "/memory", "", []string{"/memory/skip.md"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
	s.store.AssertNotCalled(s.T(), "GetMemoryFileHash", ctx, "/memory/skip.md", "")
}

func (s *IndexerSuite) TestIndexExcludeNoMatch() {
	s.stubFS([]string{"/memory/notes.md"}, "content")

	ctx := context.Background()
	s.store.On("GetMemoryFileHash", ctx, "/memory/notes.md", "").Return("", nil)
	s.expectEmbed(ctx)

	// Exclusion that doesn't match any file — all files still indexed.
	n, err := s.indexer.Index(ctx, "/memory", "", []string{"/memory/drafts"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, n)
}

func (s *IndexerSuite) TestIndexSingleFileExcluded() {
	s.sys.Override("Stat", mock.Anything).Return(&fakeFileInfo{name: "file", isDir: false}, nil)

	n, err := s.indexer.Index(context.Background(), "/docs/notes.md", "", []string{"/docs/notes.md"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
}

// --- Search tests ---

func (s *IndexerSuite) TestSearchRankedResults() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"docker cleanup"}).Return([][]float32{{0.9, 0.1}}, nil)
	s.store.On("GetMemoryFilesByDirPath", ctx, "/memory").Return([]*db.MemoryFile{
		{FilePath: "/memory/a.md", Content: "Docker stuff", Embedding: embeddings.SerializeFloat32([]float32{0.95, 0.05})},
		{FilePath: "/memory/b.md", Content: "Slack stuff", Embedding: embeddings.SerializeFloat32([]float32{0.1, 0.9})},
	}, nil)

	results, err := s.indexer.Search(ctx, "/memory", "docker cleanup", 5)
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 2)
	require.Equal(s.T(), "/memory/a.md", results[0].FilePath)
	require.Equal(s.T(), "Docker stuff", results[0].Content)
	require.Equal(s.T(), 0, results[0].ChunkIndex)
	require.Greater(s.T(), results[0].Score, results[1].Score)
}

func (s *IndexerSuite) TestSearchChunkedResultIncludesContent() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"query"}).Return([][]float32{{0.5, 0.5}}, nil)
	s.store.On("GetMemoryFilesByDirPath", ctx, "/memory").Return([]*db.MemoryFile{
		{FilePath: "/memory/large.md", ChunkIndex: 1, Content: "Chunk 1 content", Embedding: embeddings.SerializeFloat32([]float32{0.9, 0.1})},
		{FilePath: "/memory/large.md", ChunkIndex: 2, Content: "Chunk 2 content", Embedding: embeddings.SerializeFloat32([]float32{0.1, 0.9})},
	}, nil)

	results, err := s.indexer.Search(ctx, "/memory", "query", 5)
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 2)
	// Chunk results include content.
	require.Equal(s.T(), "Chunk 1 content", results[0].Content)
	require.Equal(s.T(), 1, results[0].ChunkIndex)
	require.Equal(s.T(), "Chunk 2 content", results[1].Content)
	require.Equal(s.T(), 2, results[1].ChunkIndex)
}

func (s *IndexerSuite) TestSearchTopKLimit() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"query"}).Return([][]float32{{0.5, 0.5}}, nil)
	s.store.On("GetMemoryFilesByDirPath", ctx, "/memory").Return([]*db.MemoryFile{
		{FilePath: "/memory/a.md", Content: "a", Embedding: embeddings.SerializeFloat32([]float32{1.0, 0.0})},
		{FilePath: "/memory/b.md", Content: "b", Embedding: embeddings.SerializeFloat32([]float32{0.0, 1.0})},
		{FilePath: "/memory/c.md", Content: "c", Embedding: embeddings.SerializeFloat32([]float32{0.5, 0.5})},
	}, nil)

	results, err := s.indexer.Search(ctx, "/memory", "query", 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)
}

func (s *IndexerSuite) TestSearchDefaultTopK() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"query"}).Return([][]float32{{0.5}}, nil)
	s.store.On("GetMemoryFilesByDirPath", ctx, "/memory").Return([]*db.MemoryFile{}, nil)

	results, err := s.indexer.Search(ctx, "/memory", "query", 0)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
}

func (s *IndexerSuite) TestSearchEmbedError() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"query"}).Return([][]float32(nil), errors.New("api error"))

	_, err := s.indexer.Search(ctx, "/memory", "query", 5)
	require.ErrorContains(s.T(), err, "embedding query")
}

func (s *IndexerSuite) TestSearchGetFilesError() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"query"}).Return([][]float32{{0.5}}, nil)
	s.store.On("GetMemoryFilesByDirPath", ctx, "/memory").Return(([]*db.MemoryFile)(nil), errors.New("db error"))

	_, err := s.indexer.Search(ctx, "/memory", "query", 5)
	require.ErrorContains(s.T(), err, "loading files")
}

func (s *IndexerSuite) TestSearchSkipsEmptyEmbedding() {
	ctx := context.Background()
	s.embedder.On("Embed", ctx, []string{"query"}).Return([][]float32{{0.5}}, nil)
	s.store.On("GetMemoryFilesByDirPath", ctx, "/memory").Return([]*db.MemoryFile{
		{FilePath: "a.md", Content: "a", Embedding: []byte{1, 2, 3}}, // invalid, len%4 != 0
	}, nil)

	results, err := s.indexer.Search(ctx, "/memory", "query", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
}

func (s *IndexerSuite) TestNewIndexerCustomMaxChunkChars() {
	idx := NewIndexer(s.embedder, s.store, slog.New(slog.NewTextHandler(os.Stderr, nil)), 8000)
	require.Equal(s.T(), 8000, idx.maxChunkChars)
}

func (s *IndexerSuite) TestNewIndexerDefaultMaxChunkChars() {
	idx := NewIndexer(s.embedder, s.store, slog.New(slog.NewTextHandler(os.Stderr, nil)), 0)
	require.Equal(s.T(), defaultMaxChunkChars, idx.maxChunkChars)
}

// --- Content hash test ---

func (s *IndexerSuite) TestContentHash() {
	h1 := contentHash("hello")
	h2 := contentHash("hello")
	h3 := contentHash("world")
	require.Equal(s.T(), h1, h2)
	require.NotEqual(s.T(), h1, h3)
	require.Len(s.T(), h1, 64) // SHA256 hex
}

// --- splitChunks tests ---

func (s *IndexerSuite) TestSplitChunks() {
	tests := []struct {
		name     string
		content  string
		maxChars int
		expected []string
	}{
		{"small", "hello world", 100, []string{"hello world"}},
		{"at newline", "line1\nline2\nline3\nline4", 12, []string{"line1\nline2", "line3\nline4"}},
		{"no newline", strings.Repeat("x", 20), 8, []string{"xxxxxxxx", "xxxxxxxx", "xxxx"}},
		{"exact size", strings.Repeat("x", 10), 10, []string{strings.Repeat("x", 10)}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.expected, splitChunks(tt.content, tt.maxChars))
		})
	}
}

// --- Test helpers ---

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f *fakeDirEntry) Name() string               { return f.name }
func (f *fakeDirEntry) IsDir() bool                { return f.isDir }
func (f *fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f *fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

type fakeFileInfo struct {
	name  string
	isDir bool
}

func (f *fakeFileInfo) Name() string       { return f.name }
func (f *fakeFileInfo) Size() int64        { return 0 }
func (f *fakeFileInfo) Mode() fs.FileMode  { return 0 }
func (f *fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f *fakeFileInfo) IsDir() bool        { return f.isDir }
func (f *fakeFileInfo) Sys() any           { return nil }
