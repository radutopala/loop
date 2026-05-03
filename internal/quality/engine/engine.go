package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

// FileSystem abstracts the parser-side I/O the engine needs. Production
// uses a thin os.ReadFile-backed adapter; tests inject fakes that can
// provoke read errors without touching the disk.
type FileSystem interface {
	ReadFile(path string) ([]byte, error)
}

// Clock returns the timestamp the engine stamps onto a finished snapshot.
// Held as a struct field so tests can pin a deterministic value rather
// than letting time.Now() drift through assertions.
type Clock func() time.Time

// Config carries the engine-tunable knobs the project config maps to. The
// engine does not read config itself; the daemon constructs an engine
// once at startup with values resolved from the config-merge layer.
type Config struct {
	// MaxFiles overrides graph.DefaultMaxFiles. Zero uses the default;
	// negative disables the cap (intended for tests, never production).
	MaxFiles int

	// ExcludePaths appends to graph.DefaultExcludePatterns and the
	// repo's .gitignore. Same syntax as DefaultExcludePatterns.
	ExcludePaths []string
}

// ScanResult is the engine-level return shape. Signal is the just-computed
// 0–10000 quality_signal plus per-metric breakdown; the rest of the fields
// describe the scan itself so callers (MCP, HTTP, CLI) can render the
// right state without inspecting the snapshot store.
type ScanResult struct {
	// Signal is the aggregated metric signal. Zero-valued when InProgress.
	Signal metrics.Signal

	// FileCount is the number of files the parser was handed (after
	// exclusions, before parse failures).
	FileCount int

	// ParseFailed is the number of files the parser could not handle.
	// Surfaced via the parse_fail rule and the panel's "files skipped"
	// counter.
	ParseFailed int

	// ScannedAt is the timestamp the snapshot was stamped with. Zero
	// when InProgress.
	ScannedAt time.Time

	// InProgress is true when this call coalesced into an already-running
	// scan for the same channel. Caller should treat this as "wait for
	// the quality.scanned event"; no Signal/FileCount/etc. is filled.
	InProgress bool
}

// ProgressFunc is the optional progress reporter the daemon wires in
// to drive the panel's "Scanning… N/M files" UI. Called from the
// parse loop on a per-channel goroutine; implementations must be
// non-blocking (a channel send with drop-if-full is the prescribed
// pattern). Reported (done, total) are post-exclusion file counts.
//
// nil is fine — the engine simply runs without emitting progress.
type ProgressFunc func(channelID string, done, total int)

// Engine is the synchronous orchestrator. One instance is shared across
// all channels; per-channel state lives in the snapshot.Store and the
// in-flight map.
type Engine struct {
	parser parser.Parser
	store  snapshot.Store
	cache  *graph.Cache
	fs     FileSystem
	cfg    Config
	clock  Clock

	// enumerate is held as a struct field so tests can substitute a
	// fake that doesn't require a real directory tree on disk.
	enumerate enumerateFunc

	// progress is the optional progress hook the daemon wires in;
	// nil disables progress reporting (the default for the CLI path).
	progress ProgressFunc

	mu       sync.Mutex
	inFlight map[string]struct{}
}

type enumerateFunc func(rootDir string, opts graph.EnumerateOptions) ([]string, error)

// New wires a production Engine. The caller owns parser, store, cache and
// fs; the engine never closes them. Clock defaults to time.Now if nil.
func New(p parser.Parser, store snapshot.Store, cache *graph.Cache, fs FileSystem, cfg Config, clock Clock) *Engine {
	if clock == nil {
		clock = time.Now
	}
	return &Engine{
		parser:    p,
		store:     store,
		cache:     cache,
		fs:        fs,
		cfg:       cfg,
		clock:     clock,
		enumerate: graph.Enumerate,
		inFlight:  make(map[string]struct{}),
	}
}

// SetProgress installs the progress hook. Pass nil to disable. Safe to
// call before any Scan; calling concurrently with an in-flight Scan is
// not supported (the daemon wires this once at startup).
func (e *Engine) SetProgress(p ProgressFunc) {
	e.progress = p
}

// Scan runs a full scan for (channelID, branch) rooted at dirPath and
// persists the resulting snapshot. Concurrent calls for the same channel
// return InProgress=true without re-running. Concurrent calls for
// different channels run in parallel.
//
// dirPath is the workspace root the parser is handed; relative paths in
// the returned snapshot are slash-separated and rooted at dirPath.
func (e *Engine) Scan(ctx context.Context, channelID, branch, dirPath string) (ScanResult, error) {
	if !e.acquire(channelID) {
		return ScanResult{InProgress: true}, nil
	}
	defer e.release(channelID)

	files, err := e.enumerate(dirPath, graph.EnumerateOptions{
		MaxFiles:             e.cfg.MaxFiles,
		ExtraExcludePatterns: e.cfg.ExcludePaths,
	})
	if err != nil {
		var tooLarge *graph.RepoTooLargeError
		if errors.As(err, &tooLarge) {
			return ScanResult{}, err
		}
		return ScanResult{}, fmt.Errorf("enumerate files: %w", err)
	}

	facts := make([]*parser.FileFacts, 0, len(files))
	parseFailed := 0
	total := len(files)
	if e.progress != nil {
		e.progress(channelID, 0, total)
	}
	for i, rel := range files {
		if err := ctx.Err(); err != nil {
			return ScanResult{}, err
		}
		if e.progress != nil {
			e.progress(channelID, i, total)
		}
		if !e.parser.Supports(rel) {
			continue
		}
		abs := filepath.Join(dirPath, filepath.FromSlash(rel))
		source, err := e.fs.ReadFile(abs)
		if err != nil {
			facts = append(facts, &parser.FileFacts{Path: rel, ParseFailed: true})
			parseFailed++
			continue
		}
		f, err := e.parser.Parse(rel, source)
		if err != nil {
			facts = append(facts, &parser.FileFacts{Path: rel, ParseFailed: true})
			parseFailed++
			continue
		}
		if f.ParseFailed {
			parseFailed++
		}
		facts = append(facts, f)
	}

	g := graph.Build(facts)
	e.cache.Set(channelID, g)
	if e.progress != nil {
		e.progress(channelID, total, total)
	}

	sig := metrics.Compute(g)
	scannedAt := e.clock().UTC()
	if err := e.store.Save(ctx, channelID, branch, sig, scannedAt); err != nil {
		return ScanResult{}, fmt.Errorf("save snapshot: %w", err)
	}

	return ScanResult{
		Signal:      sig,
		FileCount:   len(facts),
		ParseFailed: parseFailed,
		ScannedAt:   scannedAt,
	}, nil
}

func (e *Engine) acquire(channelID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.inFlight[channelID]; ok {
		return false
	}
	e.inFlight[channelID] = struct{}{}
	return true
}

func (e *Engine) release(channelID string) {
	e.mu.Lock()
	delete(e.inFlight, channelID)
	e.mu.Unlock()
}
