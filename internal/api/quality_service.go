package api

import (
	"context"
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
