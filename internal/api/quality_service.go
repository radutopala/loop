package api

import (
	"context"
	"sync"
	"time"
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
