package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

// --- Fakes ---

// fakeQualityScanner records the most recent Scan call and returns a
// preset response. Tests inject success, error, and InProgress paths
// without spinning up a real engine.
type fakeQualityScanner struct {
	mu            sync.Mutex
	calls         int32
	dirPath       string
	parentDirPath string
	branch        string
	channelID     string
	result        engine.ScanResult
	err           error
	delay         time.Duration
	cancelOn      chan struct{}
}

func (f *fakeQualityScanner) Scan(ctx context.Context, channelID, branch, dirPath, parentDirPath string) (engine.ScanResult, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	f.dirPath = dirPath
	f.parentDirPath = parentDirPath
	f.branch = branch
	f.channelID = channelID
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return engine.ScanResult{}, ctx.Err()
		}
	}
	if f.cancelOn != nil {
		close(f.cancelOn)
		f.cancelOn = nil
	}
	return f.result, f.err
}

// fakeGraphProvider returns a hand-built graph for one channelID.
type fakeGraphProvider struct {
	g *graph.Graph
}

func (f *fakeGraphProvider) Get(_ string) (*graph.Graph, bool) {
	return f.g, false
}

// fakeSnapshotReader serves Get/GetLatest from in-memory rows keyed by branch.
type fakeSnapshotReader struct {
	byBranch map[string]*snapshot.Snapshot
	latest   *snapshot.Snapshot
	getErr   error
}

func (f *fakeSnapshotReader) Get(_ context.Context, _, branch string) (*snapshot.Snapshot, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if s, ok := f.byBranch[branch]; ok {
		return s, nil
	}
	return nil, snapshot.ErrNotFound
}

func (f *fakeSnapshotReader) GetLatest(_ context.Context, _ string) (*snapshot.Snapshot, error) {
	if f.latest == nil {
		return nil, snapshot.ErrNotFound
	}
	return f.latest, nil
}

// captureBroadcaster pulls events from EventsHub.Broadcast by registering
// a fake subscriber. We can't easily inspect the hub's outbound writes,
// so tests use a custom hub built around a callback.
type captureHub struct {
	mu     sync.Mutex
	events []capturedEvent
	wg     sync.WaitGroup
	expect int
}

type capturedEvent struct {
	Type      string
	ChannelID string
	Data      any
}

// waitForEvent attaches an EventsHub-shaped capture to s.srv. The hub
// counts broadcasts and the test waits via WaitForEvents to avoid
// flake-prone time.Sleep calls.
func (c *captureHub) record(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, capturedEvent{Type: e.Type, ChannelID: e.ChannelID, Data: e.Data})
	if c.expect > 0 && len(c.events) >= c.expect {
		c.wg.Done()
		c.expect = 0
	}
}

func (c *captureHub) snapshot() []capturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEvent, len(c.events))
	copy(out, c.events)
	return out
}

func (c *captureHub) waitFor(n int) {
	c.mu.Lock()
	c.expect = n
	c.wg.Add(1)
	if len(c.events) >= n {
		c.expect = 0
		c.wg.Done()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// hookHub returns an EventsHub whose Broadcast is intercepted into cap.
// Implemented by wrapping a real hub and overriding via a small adapter.
func (s *ServerSuite) hookHub() *captureHub {
	cap := &captureHub{}
	s.srv.eventsHub = newCapturingHub(cap)
	return cap
}

// newCapturingHub returns an EventsHub whose Broadcast feeds a captureHub
// instead of writing to websockets. Reuses the real *EventsHub allocator
// because Broadcast is a method on the concrete type — wrapping is the
// simplest way to short-circuit network I/O in tests.
func newCapturingHub(cap *captureHub) *EventsHub {
	hub := NewEventsHub(testLogger())
	hub.subscribers = nil
	// Register a single capture subscriber that replays Broadcast as a
	// callback. We wrap by swapping in a side-channel via capture hook.
	hub.captureHook = cap.record
	return hub
}

// --- Helpers used by both handlers ---

func (s *ServerSuite) channelWithDir(channelID, dirPath string) {
	s.store.On("GetChannel", mock.Anything, channelID).Return(&db.Channel{ChannelID: channelID, DirPath: dirPath}, nil)
}

// --- handleQualityScan ---

func (s *ServerSuite) TestHandleQualityScanNotConfigured() {
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityScanResolveDirError() {
	scanner := &fakeQualityScanner{}
	s.srv.SetQualityScanner(scanner)
	// No DirPath, no LoopDir → resolveDirPath returns an error.
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "no dir_path")
	require.Equal(s.T(), int32(0), atomic.LoadInt32(&scanner.calls))
}

func (s *ServerSuite) TestHandleQualityScanSuccessBroadcastsEvents() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	scanner := &fakeQualityScanner{
		result: engine.ScanResult{
			Signal: metrics.Signal{
				Value:   8000,
				GeoMean: 0.8,
				Metrics: []metrics.Result{{Name: "modularity", Score: 0.9, Raw: 0.9}},
			},
			FileCount: 7,
			ScannedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	s.srv.SetQualityScanner(scanner)
	// Provide a graph so rules eval emits real results.
	healthyGraph := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Language: "go", LOC: 10, Imports: []parser.Import{{Path: "b.go"}}},
		{Path: "b.go", Language: "go", LOC: 10},
	})
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: healthyGraph})
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)
	var resp scanResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "started", resp.Status)

	// session_started fires synchronously before the goroutine launches;
	// scanned + session_ended fire from the goroutine.
	cap.waitFor(3)
	events := cap.snapshot()
	require.Equal(s.T(), EventQualitySessionStarted, events[0].Type)
	require.Equal(s.T(), EventQualityScanned, events[1].Type)
	require.Equal(s.T(), EventQualitySessionEnded, events[2].Type)

	report, ok := events[1].Data.(QualityScanReport)
	require.True(s.T(), ok)
	require.Equal(s.T(), 8000, report.Signal)
	require.Equal(s.T(), dir, report.DirPath)
	require.NotEmpty(s.T(), report.Rules.Passed)
	require.Empty(s.T(), report.Rules.Failed)
}

func (s *ServerSuite) TestHandleQualityScanWorktreePassesParentDirPath() {
	dir := s.T().TempDir()
	parentDir := s.T().TempDir()
	// First GetChannel("ch-1") in resolveDirPath returns the worktree
	// channel; second call in resolveParentDirPath returns it again;
	// then GetChannel("parent-1") returns the parent.
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", DirPath: dir, Worktree: true, ParentID: "parent-1",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "parent-1").Return(&db.Channel{
		ChannelID: "parent-1", DirPath: parentDir,
	}, nil)

	scanner := &fakeQualityScanner{result: engine.ScanResult{Signal: metrics.Signal{Value: 8000}}}
	s.srv.SetQualityScanner(scanner)
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: graph.Build(nil)})
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(3)
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	require.Equal(s.T(), dir, scanner.dirPath)
	require.Equal(s.T(), parentDir, scanner.parentDirPath)
}

func (s *ServerSuite) TestHandleQualityScanNonWorktreeOmitsParentDirPath() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	scanner := &fakeQualityScanner{result: engine.ScanResult{Signal: metrics.Signal{Value: 8000}}}
	s.srv.SetQualityScanner(scanner)
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: graph.Build(nil)})
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(3)
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	require.Equal(s.T(), dir, scanner.dirPath)
	require.Empty(s.T(), scanner.parentDirPath)
}

func (s *ServerSuite) TestHandleQualityScanWorktreeWithMissingParentSilentlyDrops() {
	// Worktree channel references a parent that GetChannel can't find:
	// resolveParentDirPath must swallow the error and return "" rather
	// than failing the scan — the scan still runs with no parent
	// override.
	dir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1", DirPath: dir, Worktree: true, ParentID: "missing",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "missing").Return((*db.Channel)(nil), errors.New("not found"))

	scanner := &fakeQualityScanner{result: engine.ScanResult{Signal: metrics.Signal{Value: 8000}}}
	s.srv.SetQualityScanner(scanner)
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: graph.Build(nil)})
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(3)
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	require.Empty(s.T(), scanner.parentDirPath)
}

func (s *ServerSuite) TestHandleQualityScanRulesViolatedFires() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	// Signal below the floor → signal_floor rule fails.
	scanner := &fakeQualityScanner{
		result: engine.ScanResult{
			Signal:    metrics.Signal{Value: 100, GeoMean: 0.01},
			FileCount: 1,
		},
	}
	s.srv.SetQualityScanner(scanner)
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: graph.Build(nil)})
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(4)
	events := cap.snapshot()
	require.Equal(s.T(), EventQualityScanned, events[1].Type)
	require.Equal(s.T(), EventQualityRulesViolated, events[2].Type)
	require.Equal(s.T(), EventQualitySessionEnded, events[3].Type)
}

func (s *ServerSuite) TestHandleQualityScanCustomRulesConfig() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	scanner := &fakeQualityScanner{
		result: engine.ScanResult{Signal: metrics.Signal{Value: 100}, FileCount: 1},
	}
	s.srv.SetQualityScanner(scanner)
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: graph.Build(nil)})

	// Disable the floor rule entirely — even at signal=100 it should pass.
	cfg := rules.Config{Rules: map[string]rules.RuleConfig{
		rules.SignalFloor:    {Enabled: false},
		rules.NoImportCycles: {Enabled: true},
		rules.ParseFail:      {Enabled: true, Threshold: rules.ParseFailMaxDefault},
	}}
	s.srv.SetQualityRulesLoader(func(string, string) *rules.Config { return &cfg })
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(3)
	events := cap.snapshot()
	require.Equal(s.T(), EventQualityScanned, events[1].Type)
	report := events[1].Data.(QualityScanReport)
	for _, r := range report.Rules.Failed {
		require.NotEqual(s.T(), rules.SignalFloor, r.Name)
	}
}

func (s *ServerSuite) TestHandleQualityScanInProgressOnSecondCall() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	cancelOn := make(chan struct{})
	scanner := &fakeQualityScanner{
		result:   engine.ScanResult{Signal: metrics.Signal{Value: 8000, GeoMean: 0.8}, FileCount: 1},
		delay:    100 * time.Millisecond,
		cancelOn: cancelOn,
	}
	s.srv.SetQualityScanner(scanner)
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: graph.Build(nil)})
	cap := s.hookHub()

	// Kick off the first scan asynchronously — it'll occupy the in-flight slot.
	rec1 := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec1.Code)
	var first scanResponse
	require.NoError(s.T(), json.Unmarshal(rec1.Body.Bytes(), &first))
	require.Equal(s.T(), "started", first.Status)

	// While it's still running, second call should coalesce.
	rec2 := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec2.Code)
	var second scanResponse
	require.NoError(s.T(), json.Unmarshal(rec2.Body.Bytes(), &second))
	require.Equal(s.T(), "in_progress", second.Status)

	cap.waitFor(3)
	require.Equal(s.T(), int32(1), atomic.LoadInt32(&scanner.calls))
}

func (s *ServerSuite) TestHandleQualityScanEngineError() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	scanner := &fakeQualityScanner{err: errors.New("disk full")}
	s.srv.SetQualityScanner(scanner)
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(2) // session_started + session_ended (with error)
	events := cap.snapshot()
	ended := events[1]
	require.Equal(s.T(), EventQualitySessionEnded, ended.Type)
	payload := ended.Data.(map[string]any)
	require.Equal(s.T(), false, payload["ok"])
	require.Equal(s.T(), "disk full", payload["error"])
}

func (s *ServerSuite) TestHandleQualityScanRepoTooLargeIncludesDetail() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	scanner := &fakeQualityScanner{err: &graph.RepoTooLargeError{FileCount: 30000, Limit: 25000}}
	s.srv.SetQualityScanner(scanner)
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(2)
	events := cap.snapshot()
	payload := events[1].Data.(map[string]any)
	tooLarge, ok := payload["repo_too_large"].(map[string]int)
	require.True(s.T(), ok)
	require.Equal(s.T(), 30000, tooLarge["file_count"])
	require.Equal(s.T(), 25000, tooLarge["limit"])
}

func (s *ServerSuite) TestHandleQualityScanInProgressFromEngine() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)

	scanner := &fakeQualityScanner{result: engine.ScanResult{InProgress: true}}
	s.srv.SetQualityScanner(scanner)
	cap := s.hookHub()

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(1) // only session_started — engine reported coalesce
	require.Equal(s.T(), 1, len(cap.snapshot()))
}

func (s *ServerSuite) TestHandleQualityScanEventsHubNilStillSucceeds() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	scanner := &fakeQualityScanner{result: engine.ScanResult{Signal: metrics.Signal{Value: 8000}}}
	s.srv.SetQualityScanner(scanner)
	s.srv.eventsHub = nil

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	// Wait for the goroutine to finish — no events to wait on, so poll
	// the scanner call count.
	require.Eventually(s.T(), func() bool {
		return atomic.LoadInt32(&scanner.calls) == 1
	}, time.Second, 10*time.Millisecond)
}

// --- handleQualitySnapshot ---

func (s *ServerSuite) TestHandleQualitySnapshotNotConfigured() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualitySnapshotResolveDirError() {
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualitySnapshotNoneReturns404() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestHandleQualitySnapshotCurrentBranchHit() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {
				ChannelID:       "ch-1",
				Branch:          "main",
				ScannedAt:       time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
				Value:           7000,
				GeoMean:         0.7,
				MetricBreakdown: json.RawMessage(`[{"name":"modularity","score":0.9,"raw":0.9}]`),
			},
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 7000, resp.Signal)
	require.Equal(s.T(), "main", resp.Branch)
	require.False(s.T(), resp.BranchMismatch)
	require.Len(s.T(), resp.Metrics, 1)
	require.Equal(s.T(), "modularity", resp.Metrics[0].Name)
}

func (s *ServerSuite) TestHandleQualitySnapshotBranchMismatchFallsBackToLatest() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{}, // no row on current branch
		latest: &snapshot.Snapshot{
			ChannelID: "ch-1",
			Branch:    "feature-x",
			Value:     6000,
			GeoMean:   0.6,
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "feature-x", resp.Branch)
	require.Equal(s.T(), "main", resp.CurrentBranch)
	require.True(s.T(), resp.BranchMismatch)
	require.Equal(s.T(), 6000, resp.Signal)
}

func (s *ServerSuite) TestHandleQualitySnapshotGetError() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{getErr: errors.New("db unavailable")}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "db unavailable")
}

func (s *ServerSuite) TestHandleQualitySnapshotMissingMetricBreakdown() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {ChannelID: "ch-1", Branch: "main", Value: 1000},
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Metrics)
}

func (s *ServerSuite) TestHandleQualitySnapshotBadMetricBreakdownLogsAndReturnsEmpty() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {
				ChannelID:       "ch-1",
				Branch:          "main",
				Value:           1000,
				MetricBreakdown: json.RawMessage(`not-valid-json`),
			},
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Metrics)
}

func (s *ServerSuite) TestHandleQualitySnapshotPopulatesTiles() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {
				ChannelID: "ch-1",
				Branch:    "main",
				Value:     7500,
				TileData: json.RawMessage(
					`[{"path":"a/x.go","loc":42,"deficit":0.5,"metric_deficits":{"modularity":0.5},"top_reason":"modularity"}]`,
				),
			},
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Tiles, 1)
	require.Equal(s.T(), "a/x.go", resp.Tiles[0].Path)
	require.Equal(s.T(), 42, resp.Tiles[0].LOC)
	require.InDelta(s.T(), 0.5, resp.Tiles[0].Deficit, 1e-9)
	require.Equal(s.T(), "modularity", resp.Tiles[0].TopReason)
	require.InDelta(s.T(), 0.5, resp.Tiles[0].MetricDeficits["modularity"], 1e-9)
}

func (s *ServerSuite) TestHandleQualitySnapshotMissingTileData() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {ChannelID: "ch-1", Branch: "main", Value: 1000},
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Tiles)
}

func (s *ServerSuite) TestHandleQualitySnapshotBadTileDataLogsAndReturnsEmpty() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	reader := &fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {
				ChannelID: "ch-1",
				Branch:    "main",
				Value:     1000,
				TileData:  json.RawMessage(`not-valid-json`),
			},
		},
	}
	s.srv.SetQualitySnapshotReader(reader)

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualitySnapshotResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Tiles)
}

// --- buildQualityReport / collectRules covered indirectly ---

func (s *ServerSuite) TestBuildQualityReportSplitsFailedRules() {
	res := engine.ScanResult{
		Signal: metrics.Signal{
			Value:   9000,
			Metrics: []metrics.Result{{Name: "modularity", Score: 1, Raw: 1}},
			Tiles: []metrics.FileTile{{
				Path:           "a/x.go",
				LOC:            10,
				Deficit:        0.5,
				MetricDeficits: map[string]float64{"modularity": 0.5},
				TopReason:      "modularity",
			}},
		},
		FileCount: 5,
	}
	rs := []rules.Result{
		{Name: "alpha", Severity: rules.SevPass, Message: "ok"},
		{Name: "beta", Severity: rules.SevFail, Message: "boom", Citations: []rules.Citation{{Path: "x.go", Note: "cycle"}}},
	}
	rep := buildQualityReport("/tmp", "main", res, rs)
	require.Len(s.T(), rep.Rules.Passed, 1)
	require.Len(s.T(), rep.Rules.Failed, 1)
	require.Equal(s.T(), "x.go", rep.Rules.Failed[0].Citations[0].Path)
	require.Equal(s.T(), "main", rep.Branch)
	require.Len(s.T(), rep.Tiles, 1)
	require.Equal(s.T(), "a/x.go", rep.Tiles[0].Path)
	require.Equal(s.T(), "modularity", rep.Tiles[0].TopReason)
}

func (s *ServerSuite) TestCollectRulesNoGraphProviderReturnsNil() {
	out := s.srv.quality.collectRules("ch-1", "", "", metrics.Signal{})
	require.Nil(s.T(), out)
}

func (s *ServerSuite) TestCollectRulesNilGraphReturnsNil() {
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: nil})
	out := s.srv.quality.collectRules("ch-1", "", "", metrics.Signal{})
	require.Nil(s.T(), out)
}

// Loader returning nil should fall through to rules.DefaultConfig — same
// semantic as no loader being wired at all. Ensures the loader can opt
// out without forcing callers to clear it.
func (s *ServerSuite) TestResolveRulesConfigLoaderReturnsNilFallsBackToDefault() {
	s.srv.SetQualityRulesLoader(func(string, string) *rules.Config { return nil })
	got := s.srv.quality.resolveRulesConfig("", "")
	require.Equal(s.T(), rules.DefaultConfig(), got)
}

// --- BroadcastQualityEvent (delivery side) ---

func (s *ServerSuite) TestBroadcastQualityEventDelivers() {
	cap := s.hookHub()
	s.srv.eventsHub.BroadcastQualityEvent(EventQualityScanned, "ch-1", map[string]string{"k": "v"})
	require.Eventually(s.T(), func() bool {
		return len(cap.snapshot()) == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), EventQualityScanned, cap.snapshot()[0].Type)
}

// --- Coverage for the "store not configured" guard branches ---

func (s *ServerSuite) TestHandleQualityScanStoreNotConfigured() {
	s.srv.SetQualityScanner(&fakeQualityScanner{})
	s.srv.store = nil

	rec := s.testRequest("POST", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualitySnapshotStoreNotConfigured() {
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	s.srv.store = nil

	rec := s.testRequest("GET", "/api/channels/ch-1/quality/snapshot", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// broadcastQualityError early-returns when the events hub is unset; cover
// that path explicitly because the engine-error tests always have a hub.
func (s *ServerSuite) TestBroadcastQualityErrorNilHubIsNoop() {
	s.srv.eventsHub = nil
	s.srv.quality.broadcastQualityError("ch-1", "/tmp", "main", errors.New("boom"))
}

func (s *ServerSuite) TestEmitQualityProgressNilHubIsNoop() {
	s.srv.eventsHub = nil
	s.srv.EmitQualityProgress("ch-1", 1, 5)
}

func (s *ServerSuite) TestEmitQualityProgressEmitsFirstAndTerminalAndThrottlesMiddle() {
	cap := s.hookHub()
	s.srv.EmitQualityProgress("ch-1", 0, 10)  // first → emits
	s.srv.EmitQualityProgress("ch-1", 1, 10)  // throttled → drops
	s.srv.EmitQualityProgress("ch-1", 10, 10) // terminal → emits
	got := cap.snapshot()
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), EventQualityScanProgress, got[0].Type)
	require.Equal(s.T(), EventQualityScanProgress, got[1].Type)
}

func (s *ServerSuite) TestEmitQualityProgressMidScanTickAfterWindowEmits() {
	cap := s.hookHub()
	s.srv.EmitQualityProgress("ch-1", 0, 10) // first → emits
	// Backdate the last-emit so the next tick clears the throttle window.
	s.srv.quality.mu.Lock()
	s.srv.quality.progress["ch-1"] = time.Now().Add(-progressThrottle - time.Second)
	s.srv.quality.mu.Unlock()
	s.srv.EmitQualityProgress("ch-1", 5, 10) // window cleared → emits
	require.Len(s.T(), cap.snapshot(), 2)
}
