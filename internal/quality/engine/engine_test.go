package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/snapshot"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EngineSuite struct {
	suite.Suite
	parser *fakeParser
	store  *fakeStore
	cache  *graph.Cache
	fs     *fakeFS
	clock  Clock
	now    time.Time
	engine *Engine
}

func TestEngineSuite(t *testing.T) {
	suite.Run(t, new(EngineSuite))
}

func (s *EngineSuite) SetupTest() {
	s.parser = newFakeParser()
	s.store = &fakeStore{}
	s.cache = graph.NewCache()
	s.fs = newFakeFS()
	s.now = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return s.now }
	s.engine = New(s.parser, s.store, s.cache, s.fs, Config{}, nil, s.clock)
	// Replace the production enumerator with a fake by default; tests
	// that need real walk semantics override this via a temp dir.
	s.engine.enumerate = listEnumerator(nil)
}

// --- happy path ---

func (s *EngineSuite) TestScanPersistsSnapshotAndCachesGraph() {
	s.engine.enumerate = listEnumerator([]string{"a.go", "b.go"})
	s.fs.put("/work/a.go", []byte("package a\n"))
	s.fs.put("/work/b.go", []byte("package b\n"))
	s.parser.facts["a.go"] = &parser.FileFacts{Path: "a.go", Language: "go", LOC: 1}
	s.parser.facts["b.go"] = &parser.FileFacts{Path: "b.go", Language: "go", LOC: 1}

	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.False(s.T(), res.InProgress)
	require.Equal(s.T(), 2, res.FileCount)
	require.Equal(s.T(), 0, res.ParseFailed)
	require.Equal(s.T(), s.now, res.ScannedAt)
	require.Greater(s.T(), res.Signal.Value, 0)

	require.Len(s.T(), s.store.saves, 1)
	require.Equal(s.T(), "ch1", s.store.saves[0].channel)
	require.Equal(s.T(), "main", s.store.saves[0].branch)
	require.Equal(s.T(), s.now, s.store.saves[0].at)

	g, dirty := s.cache.Get("ch1")
	require.NotNil(s.T(), g)
	require.False(s.T(), dirty)
	require.Len(s.T(), g.Nodes, 2)
}

func (s *EngineSuite) TestScanSkipsUnsupportedFiles() {
	s.engine.enumerate = listEnumerator([]string{"a.go", "README.md"})
	s.fs.put("/work/a.go", []byte("package a\n"))
	s.parser.facts["a.go"] = &parser.FileFacts{Path: "a.go", Language: "go", LOC: 1}
	// README.md is not in s.parser.exts → Supports returns false.

	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, res.FileCount)
	// fakeFS was never asked for README.md.
	require.NotContains(s.T(), s.fs.reads, "/work/README.md")
}

func (s *EngineSuite) TestSetProgressFiresStartMidAndTerminalTicks() {
	s.engine.enumerate = listEnumerator([]string{"a.go", "b.go"})
	s.fs.put("/work/a.go", []byte("package a\n"))
	s.fs.put("/work/b.go", []byte("package b\n"))
	s.parser.facts["a.go"] = &parser.FileFacts{Path: "a.go", Language: "go", LOC: 1}
	s.parser.facts["b.go"] = &parser.FileFacts{Path: "b.go", Language: "go", LOC: 1}

	type tick struct {
		channel     string
		done, total int
	}
	var ticks []tick
	s.engine.SetProgress(func(channelID string, done, total int) {
		ticks = append(ticks, tick{channel: channelID, done: done, total: total})
	})

	_, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.GreaterOrEqual(s.T(), len(ticks), 3)
	require.Equal(s.T(), tick{"ch1", 0, 2}, ticks[0])
	require.Equal(s.T(), tick{"ch1", 2, 2}, ticks[len(ticks)-1])
}

// --- defaults ---

func (s *EngineSuite) TestNewDefaultsClockToTimeNow() {
	e := New(s.parser, s.store, s.cache, s.fs, Config{}, nil, nil)
	require.NotNil(s.T(), e.clock)
	got := e.clock()
	require.WithinDuration(s.T(), time.Now(), got, time.Second)
}

// --- error / fallback paths ---

func (s *EngineSuite) TestScanReadFileErrorFlagsParseFailed() {
	s.engine.enumerate = listEnumerator([]string{"a.go", "b.go"})
	s.fs.put("/work/a.go", []byte("package a\n"))
	// b.go is missing from fakeFS → ReadFile returns error.
	s.parser.facts["a.go"] = &parser.FileFacts{Path: "a.go", Language: "go", LOC: 1}

	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 2, res.FileCount)
	require.Equal(s.T(), 1, res.ParseFailed)
}

func (s *EngineSuite) TestScanParseErrorFlagsParseFailed() {
	s.engine.enumerate = listEnumerator([]string{"bad.go"})
	s.fs.put("/work/bad.go", []byte("package bad\n"))
	s.parser.errs["bad.go"] = errors.New("boom")

	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, res.FileCount)
	require.Equal(s.T(), 1, res.ParseFailed)
}

func (s *EngineSuite) TestScanParserReturnsParseFailedFacts() {
	// Parser returned without error but with ParseFailed=true (the
	// production tree-sitter path). Engine must count it.
	s.engine.enumerate = listEnumerator([]string{"glr.go"})
	s.fs.put("/work/glr.go", []byte("package glr\n"))
	s.parser.facts["glr.go"] = &parser.FileFacts{Path: "glr.go", ParseFailed: true}

	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, res.FileCount)
	require.Equal(s.T(), 1, res.ParseFailed)
}

func (s *EngineSuite) TestScanRepoTooLargePropagatesStructuredError() {
	s.engine.enumerate = func(_ string, _ graph.EnumerateOptions) ([]string, error) {
		return nil, &graph.RepoTooLargeError{FileCount: 100, Limit: 50}
	}
	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.Error(s.T(), err)
	require.Equal(s.T(), ScanResult{}, res)
	var tooLarge *graph.RepoTooLargeError
	require.True(s.T(), errors.As(err, &tooLarge))
	require.Equal(s.T(), 100, tooLarge.FileCount)
	require.Empty(s.T(), s.store.saves)
}

func (s *EngineSuite) TestScanEnumerateOtherErrorWrapped() {
	s.engine.enumerate = func(_ string, _ graph.EnumerateOptions) ([]string, error) {
		return nil, errors.New("disk gone")
	}
	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.Error(s.T(), err)
	require.Equal(s.T(), ScanResult{}, res)
	require.Contains(s.T(), err.Error(), "enumerate files")
}

func (s *EngineSuite) TestScanContextCancelled() {
	s.engine.enumerate = listEnumerator([]string{"a.go"})
	s.fs.put("/work/a.go", []byte("package a\n"))
	s.parser.facts["a.go"] = &parser.FileFacts{Path: "a.go", Language: "go", LOC: 1}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := s.engine.Scan(ctx, "ch1", "main", "/work")
	require.ErrorIs(s.T(), err, context.Canceled)
	require.Equal(s.T(), ScanResult{}, res)
	require.Empty(s.T(), s.store.saves)
}

func (s *EngineSuite) TestScanStoreSaveErrorWrapped() {
	s.engine.enumerate = listEnumerator([]string{"a.go"})
	s.fs.put("/work/a.go", []byte("package a\n"))
	s.parser.facts["a.go"] = &parser.FileFacts{Path: "a.go", Language: "go", LOC: 1}
	s.store.saveErr = errors.New("disk full")

	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.Error(s.T(), err)
	require.Equal(s.T(), ScanResult{}, res)
	require.Contains(s.T(), err.Error(), "save snapshot")
}

// --- coalescing ---

func (s *EngineSuite) TestScanCoalescesConcurrentCallsForSameChannel() {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	s.engine.enumerate = func(_ string, _ graph.EnumerateOptions) ([]string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil, nil
	}

	var (
		wg       sync.WaitGroup
		firstRes ScanResult
		firstErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstRes, firstErr = s.engine.Scan(context.Background(), "ch1", "main", "/work")
	}()
	<-started

	// Second concurrent call coalesces.
	res, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.True(s.T(), res.InProgress)

	close(release)
	wg.Wait()
	require.NoError(s.T(), firstErr)
	require.False(s.T(), firstRes.InProgress)
	require.Equal(s.T(), int32(1), calls.Load())

	// After release the in-flight slot is cleared, so another scan runs.
	res2, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.False(s.T(), res2.InProgress)
	require.Equal(s.T(), int32(2), calls.Load())
}

func (s *EngineSuite) TestScanDifferentChannelsRunInParallel() {
	release := make(chan struct{})
	var inFlight atomic.Int32
	var maxParallel atomic.Int32
	s.engine.enumerate = func(_ string, _ graph.EnumerateOptions) ([]string, error) {
		n := inFlight.Add(1)
		// Track max observed; ok if not perfectly tight, we just need ≥2.
		for {
			cur := maxParallel.Load()
			if n <= cur || maxParallel.CompareAndSwap(cur, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
		return nil, nil
	}

	var wg sync.WaitGroup
	for _, ch := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(channelID string) {
			defer wg.Done()
			_, _ = s.engine.Scan(context.Background(), channelID, "main", "/work")
		}(ch)
	}
	// Wait until all three are in flight.
	require.Eventually(s.T(), func() bool { return inFlight.Load() == 3 }, time.Second, 5*time.Millisecond)
	require.GreaterOrEqual(s.T(), maxParallel.Load(), int32(3))
	close(release)
	wg.Wait()
}

// --- hot-reload of MaxFiles / ExcludePaths ---

func (s *EngineSuite) TestScanRefreshesConfigFromLoader() {
	// Capture the EnumerateOptions the engine hands the enumerator so we
	// can assert the loader's values were used (not the seed cfg).
	var seenOpts []graph.EnumerateOptions
	s.engine.enumerate = func(_ string, opts graph.EnumerateOptions) ([]string, error) {
		seenOpts = append(seenOpts, opts)
		return nil, nil
	}
	calls := 0
	s.engine.configLoad = func() (Config, error) {
		calls++
		return Config{
			MaxFiles:     100 + calls,
			ExcludePaths: []string{fmt.Sprintf("p%d/", calls)},
		}, nil
	}

	_, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	_, err = s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)

	require.Equal(s.T(), 2, calls)
	require.Len(s.T(), seenOpts, 2)
	require.Equal(s.T(), 101, seenOpts[0].MaxFiles)
	require.Equal(s.T(), []string{"p1/"}, seenOpts[0].ExtraExcludePatterns)
	require.Equal(s.T(), 102, seenOpts[1].MaxFiles)
	require.Equal(s.T(), []string{"p2/"}, seenOpts[1].ExtraExcludePatterns)
}

func (s *EngineSuite) TestScanFallsBackToCachedCfgOnLoaderError() {
	// First scan: loader succeeds, engine caches the fresh value.
	// Second scan: loader errors, engine reuses the cached value.
	var seenOpts []graph.EnumerateOptions
	s.engine.enumerate = func(_ string, opts graph.EnumerateOptions) ([]string, error) {
		seenOpts = append(seenOpts, opts)
		return nil, nil
	}
	calls := 0
	s.engine.configLoad = func() (Config, error) {
		calls++
		if calls == 1 {
			return Config{MaxFiles: 42, ExcludePaths: []string{"good/"}}, nil
		}
		return Config{}, errors.New("read config: boom")
	}

	_, err := s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	_, err = s.engine.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)

	require.Equal(s.T(), 2, calls)
	require.Equal(s.T(), 42, seenOpts[0].MaxFiles)
	require.Equal(s.T(), []string{"good/"}, seenOpts[0].ExtraExcludePatterns)
	// Loader errored on the second call; cached cfg from the first
	// successful reload must still drive enumeration.
	require.Equal(s.T(), 42, seenOpts[1].MaxFiles)
	require.Equal(s.T(), []string{"good/"}, seenOpts[1].ExtraExcludePatterns)
}

func (s *EngineSuite) TestScanInitialLoaderErrorFallsBackToSeedCfg() {
	// Loader errors on the very first call; engine must use the seed
	// Config that was passed to New (cfg cache starts there).
	seed := Config{MaxFiles: 7, ExcludePaths: []string{"seed/"}}
	e := New(s.parser, s.store, s.cache, s.fs, seed, func() (Config, error) {
		return Config{}, errors.New("nope")
	}, s.clock)
	var got graph.EnumerateOptions
	e.enumerate = func(_ string, opts graph.EnumerateOptions) ([]string, error) {
		got = opts
		return nil, nil
	}

	_, err := e.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 7, got.MaxFiles)
	require.Equal(s.T(), []string{"seed/"}, got.ExtraExcludePatterns)
}

func (s *EngineSuite) TestScanNilLoaderUsesSeedCfg() {
	// Default suite engine has nil configLoad; supply a non-empty seed
	// cfg via New and verify it flows through unchanged across scans.
	seed := Config{MaxFiles: 3, ExcludePaths: []string{"a/", "b/"}}
	e := New(s.parser, s.store, s.cache, s.fs, seed, nil, s.clock)
	var seenOpts []graph.EnumerateOptions
	e.enumerate = func(_ string, opts graph.EnumerateOptions) ([]string, error) {
		seenOpts = append(seenOpts, opts)
		return nil, nil
	}

	_, err := e.Scan(context.Background(), "ch1", "main", "/work")
	require.NoError(s.T(), err)
	_, err = e.Scan(context.Background(), "ch2", "main", "/work")
	require.NoError(s.T(), err)

	require.Len(s.T(), seenOpts, 2)
	for _, o := range seenOpts {
		require.Equal(s.T(), 3, o.MaxFiles)
		require.Equal(s.T(), []string{"a/", "b/"}, o.ExtraExcludePatterns)
	}
}

// --- production helpers ---

func (s *EngineSuite) TestNewWiresProductionEnumerate() {
	// Ensure New() picks graph.Enumerate by default; cover the line.
	e := New(s.parser, s.store, s.cache, s.fs, Config{}, nil, s.clock)
	require.NotNil(s.T(), e.enumerate)
	// Empty temp dir → zero files, no error.
	tmp := s.T().TempDir()
	files, err := e.enumerate(tmp, graph.EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Empty(s.T(), files)
}

func (s *EngineSuite) TestOSFileSystemReadFile() {
	tmp := s.T().TempDir()
	path := filepath.Join(tmp, "x.txt")
	require.NoError(s.T(), writeFile(path, []byte("hi")))
	got, err := OSFileSystem{}.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []byte("hi"), got)
}

// --- fakes ---

type fakeParser struct {
	exts  map[string]struct{}
	facts map[string]*parser.FileFacts
	errs  map[string]error
}

func newFakeParser() *fakeParser {
	return &fakeParser{
		exts:  map[string]struct{}{".go": {}, ".ts": {}, ".js": {}},
		facts: make(map[string]*parser.FileFacts),
		errs:  make(map[string]error),
	}
}

func (p *fakeParser) Supports(path string) bool {
	ext := filepath.Ext(path)
	_, ok := p.exts[ext]
	return ok
}

func (p *fakeParser) Parse(path string, _ []byte) (*parser.FileFacts, error) {
	if err, ok := p.errs[path]; ok {
		return nil, err
	}
	if f, ok := p.facts[path]; ok {
		return f, nil
	}
	return &parser.FileFacts{Path: path}, nil
}

type savedSnapshot struct {
	channel, branch string
	sig             metrics.Signal
	at              time.Time
}

type fakeStore struct {
	mu      sync.Mutex
	saves   []savedSnapshot
	saveErr error
}

func (s *fakeStore) Save(_ context.Context, channelID, branch string, sig metrics.Signal, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves = append(s.saves, savedSnapshot{channelID, branch, sig, at})
	return nil
}

func (s *fakeStore) Get(_ context.Context, _, _ string) (*snapshot.Snapshot, error) {
	return nil, snapshot.ErrNotFound
}

func (s *fakeStore) GetLatest(_ context.Context, _ string) (*snapshot.Snapshot, error) {
	return nil, snapshot.ErrNotFound
}

func (s *fakeStore) DeleteForChannel(_ context.Context, _ string) error { return nil }

type fakeFS struct {
	mu    sync.Mutex
	files map[string][]byte
	reads []string
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: make(map[string][]byte)}
}

func (f *fakeFS) put(path string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = data
}

func (f *fakeFS) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, path)
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("missing: %s", path)
}

func listEnumerator(files []string) enumerateFunc {
	return func(_ string, _ graph.EnumerateOptions) ([]string, error) {
		return files, nil
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
