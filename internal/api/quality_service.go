package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

// qualityService owns the structural-quality-scan domain: the scanner,
// graph/snapshot/history readers, per-scan config loaders, and the
// in-flight scan registry. It was extracted from Server so quality state
// is reachable only through this struct; shared daemon deps are accessed
// via srv.
//
// All fields are nil by default — handlers return 501 until the daemon
// wires concrete implementations. Tests can opt-in via the Set*Quality*
// setters without spinning up a real engine.
type qualityService struct {
	deps *serverDeps // shared infrastructure; see serverDeps

	scanner    QualityScanner
	graph      QualityGraphProvider
	snapshots  QualitySnapshotReader
	rulesLoad  QualityRulesLoader
	metricsCfg QualityMetricsLoader
	history    QualityHistoryReader
	mu         sync.Mutex
	cancellers map[string]context.CancelFunc
	progress   map[string]time.Time // per-channel throttle for quality.scan_progress
}

// newQualityService creates the quality domain with its scan registry ready.
// The engine deps (scanner, graph, snapshots, loaders, history) arrive later
// via the Server setters — the daemon wires them conditionally when the
// quality engine is enabled.
func newQualityService(deps *serverDeps) *qualityService {
	return &qualityService{deps: deps, cancellers: map[string]context.CancelFunc{}}
}

// QualityScanner is the slice of *engine.Engine the HTTP handler depends on.
// Held as an interface so tests can inject a fake without spinning up a
// real parser + cache + store stack.
type QualityScanner interface {
	Scan(ctx context.Context, channelID, branch, dirPath, parentDirPath string) (engine.ScanResult, error)
}

// QualityGraphProvider supplies the post-scan graph for rule evaluation.
// Satisfied by *graph.Cache; carved out so tests can inject a stub that
// returns a hand-built graph without going through a scan.
type QualityGraphProvider interface {
	Get(channelID string) (*graph.Graph, bool)
}

// QualitySnapshotReader is the read-side of snapshot.Store, narrowed for
// the GET handler. The full Store also covers writes (engine path).
type QualitySnapshotReader interface {
	Get(ctx context.Context, channelID, branch string) (*snapshot.Snapshot, error)
	GetLatest(ctx context.Context, channelID string) (*snapshot.Snapshot, error)
}

// QualityRulesLoader resolves the rules.Config for a scan, given the
// scan's dirPath and (for worktrees) the parent project's dirPath.
// Returning nil means "use rules.DefaultConfig()" — same semantic the
// previous static SetQualityRulesConfig(nil) carried.
type QualityRulesLoader func(dirPath, parentDirPath string) *rules.Config

// QualityMetricsLoader resolves the metrics.Config for a scan, given the
// same (dirPath, parentDirPath) pair. Returning the zero Config means
// "use metrics.DefaultConfig()" so the metric paths stay consistent
// between the scanner and the post-scan diagnostics endpoints.
type QualityMetricsLoader func(dirPath, parentDirPath string) metrics.Config

// QualityHistoryReader is the slice of evolution.HistoryReader the HTTP
// handler depends on. Held as an interface so tests can inject a fake
// without requiring a real git repo on disk.
type QualityHistoryReader interface {
	Read(ctx context.Context, dirPath string, sinceMonths, maxCommits int) ([]evolution.CommitFiles, error)
}

// resolveMetricsConfig returns the effective metrics.Config for a
// recompute on the cached graph, invoking the loader (if wired) and
// falling back to metrics.DefaultConfig() when the loader is unset or
// returns the zero value.
func (s *qualityService) resolveMetricsConfig(dirPath, parentDirPath string) metrics.Config {
	if s.metricsCfg == nil {
		return metrics.DefaultConfig()
	}
	cfg := s.metricsCfg(dirPath, parentDirPath)
	if cfg == (metrics.Config{}) {
		return metrics.DefaultConfig()
	}
	return cfg
}

func (s *qualityService) emitProgress(channelID string, done, total int) {
	hub := s.deps.eventsHub
	if hub == nil {
		return
	}
	if !s.shouldEmitProgress(channelID, done, total) {
		return
	}
	hub.BroadcastQualityEvent(EventQualityScanProgress, channelID, map[string]int{
		"done":  done,
		"total": total,
	})
}

func (s *qualityService) shouldEmitProgress(channelID string, done, total int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.progress == nil {
		s.progress = make(map[string]time.Time)
	}
	now := time.Now()
	// First (done == 0) and terminal (done == total) ticks always pass — the
	// panel needs them to drive the spinner state machine.
	if done == 0 || done >= total {
		s.progress[channelID] = now
		return true
	}
	if last, ok := s.progress[channelID]; ok && now.Sub(last) < progressThrottle {
		return false
	}
	s.progress[channelID] = now
	return true
}

// runQualityScanAsync runs the engine scan, broadcasts the result, and
// drops the in-flight registration. The scan ctx is detached from the
// HTTP request so this goroutine owns its full lifecycle.
func (s *qualityService) runQualityScanAsync(ctx context.Context, channelID, dirPath, parentDirPath, branch string) {
	defer s.unregisterQualityScan(channelID)

	res, err := s.scanner.Scan(ctx, channelID, branch, dirPath, parentDirPath)
	if err != nil {
		s.broadcastQualityError(channelID, dirPath, branch, err)
		return
	}
	if res.InProgress {
		// Another scan was already running for this channel — the engine
		// coalesced. We've already cleaned up our cancel registration via
		// the defer; nothing more to broadcast (the in-flight scan owns
		// the events for this channel).
		return
	}

	report := buildQualityReport(dirPath, branch, res, s.collectRules(channelID, dirPath, parentDirPath, res.Signal))

	if hub := s.deps.eventsHub; hub != nil {
		hub.BroadcastQualityEvent(EventQualityScanned, channelID, report)
		if len(report.Rules.Failed) > 0 {
			hub.BroadcastQualityEvent(EventQualityRulesViolated, channelID, report.Rules)
		}
		hub.BroadcastQualityEvent(EventQualitySessionEnded, channelID, map[string]any{
			"branch": branch,
			"ok":     true,
		})
	}
}

// broadcastQualityError emits a session_ended event carrying the error
// message and (when applicable) a structured RepoTooLarge detail. No
// quality.scanned event fires on error — the panel keeps the previous
// snapshot rendered.
func (s *qualityService) broadcastQualityError(channelID, dirPath, branch string, err error) {
	hub := s.deps.eventsHub
	if hub == nil {
		return
	}
	payload := map[string]any{
		"branch":   branch,
		"dir_path": dirPath,
		"ok":       false,
		"error":    err.Error(),
	}
	var tooLarge *graph.RepoTooLargeError
	if errors.As(err, &tooLarge) {
		payload["repo_too_large"] = map[string]int{
			"file_count": tooLarge.FileCount,
			"limit":      tooLarge.Limit,
		}
	}
	hub.BroadcastQualityEvent(EventQualitySessionEnded, channelID, payload)
}

// collectRules runs the rules engine against the cached graph for
// channelID. A missing graph (no scan ever completed, or the graph
// provider is unset) yields an empty result list — rules evaluate
// vacuously and the panel renders no rule cards. The rules config is
// resolved via the loader so per-project overrides (rules in the
// project's .loop/config.json) reach this path on every scan.
func (s *qualityService) collectRules(channelID, dirPath, parentDirPath string, sig metrics.Signal) []rules.Result {
	if s.graph == nil {
		return nil
	}
	g, _ := s.graph.Get(channelID)
	if g == nil {
		return nil
	}
	return rules.Run(s.resolveRulesConfig(dirPath, parentDirPath), g, sig)
}

// resolveRulesConfig returns the effective rules.Config for a scan,
// invoking the loader (if wired) and falling back to
// rules.DefaultConfig() when the loader is unset or returns nil.
func (s *qualityService) resolveRulesConfig(dirPath, parentDirPath string) rules.Config {
	if s.rulesLoad == nil {
		return rules.DefaultConfig()
	}
	if cfg := s.rulesLoad(dirPath, parentDirPath); cfg != nil {
		return *cfg
	}
	return rules.DefaultConfig()
}

// registerQualityScan claims the in-flight slot for channelID. Returns
// false if a scan is already registered (the caller treats that as
// "in_progress" without spawning a new goroutine).
func (s *qualityService) registerQualityScan(channelID string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cancellers[channelID]; ok {
		return false
	}
	s.cancellers[channelID] = cancel
	return true
}

// unregisterQualityScan releases the in-flight slot. Idempotent so
// double-defers from edge cases don't panic.
func (s *qualityService) unregisterQualityScan(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancellers, channelID)
	delete(s.progress, channelID)
}

// loadSnapshot resolves the snapshot for (channel, branch), falling
// back to the latest snapshot on any branch when the requested branch
// has no row. Wraps the dual lookup the panel needs.
func (s *qualityService) loadSnapshot(ctx context.Context, channelID, branch string) (*snapshot.Snapshot, error) {
	if branch == "" {
		branch = "main"
	}
	snap, err := s.snapshots.Get(ctx, channelID, branch)
	if errors.Is(err, snapshot.ErrNotFound) {
		snap, err = s.snapshots.GetLatest(ctx, channelID)
	}
	return snap, err
}

// resolveMetricsConfigForChannel returns the effective metrics.Config
// for the channel's recompute path. Skips the channel-store lookup when
// no loader is configured — handlers that don't need overrides shouldn't
// pay for a GetChannel call (and tests shouldn't need to register the
// mock).
func (s *qualityService) resolveMetricsConfigForChannel(ctx context.Context, channelID string) metrics.Config {
	if s.metricsCfg == nil {
		return metrics.DefaultConfig()
	}
	var dir, parent string
	if s.deps.store != nil {
		if d, err := s.deps.workspace.resolveDirPath(ctx, "", channelID); err == nil {
			dir = d
			parent = s.deps.workspace.resolveParentDirPath(ctx, channelID)
		}
	}
	return s.resolveMetricsConfig(dir, parent)
}
