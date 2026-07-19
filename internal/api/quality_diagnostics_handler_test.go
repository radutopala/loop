package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

// graphProviderHit returns the graph for any channelID. The base
// fakeGraphProvider returns ok=false; this variant returns ok=true so
// the handlers' lookupCachedGraph path proceeds.
type graphProviderHit struct {
	g *graph.Graph
}

func (f *graphProviderHit) Get(_ string) (*graph.Graph, bool) {
	return f.g, true
}

// fakeHistoryReader satisfies the QualityHistoryReader interface for
// the evolution / bug-factor tests.
type fakeHistoryReader struct {
	commits []evolution.CommitFiles
	err     error
}

func (f *fakeHistoryReader) Read(_ context.Context, _ string, _, _ int) ([]evolution.CommitFiles, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.commits, nil
}

// helper: a small connected graph used by cycles/whatif/c4/rules tests.
func smallGraph() *graph.Graph {
	return graph.Build([]*parser.FileFacts{
		{Path: "cmd/main.go", Language: "go", LOC: 30, Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/api"}}},
		{Path: "internal/api/h.go", Language: "go", LOC: 50},
		{Path: "internal/api/u.go", Language: "go", LOC: 20},
	})
}

// ─── DELETE /quality/scan ─── (handleQualityScanCancel)

func (s *ServerSuite) TestHandleQualityScanCancelNoInflightReturns204() {
	rec := s.testRequest("DELETE", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestHandleQualityScanCancelInflightCancelsAndBroadcasts() {
	cap := s.hookHub()
	_, cancel := context.WithCancel(context.Background())
	s.srv.quality.mu.Lock()
	if s.srv.quality.cancellers == nil {
		s.srv.quality.cancellers = map[string]context.CancelFunc{}
	}
	s.srv.quality.cancellers["ch-1"] = cancel
	s.srv.quality.mu.Unlock()

	rec := s.testRequest("DELETE", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)

	cap.waitFor(1)
	events := cap.snapshot()
	require.Equal(s.T(), EventQualityScanCancelled, events[0].Type)
	payload, ok := events[0].Data.(map[string]string)
	require.True(s.T(), ok)
	require.Equal(s.T(), "user_requested", payload["reason"])
}

func (s *ServerSuite) TestHandleQualityScanCancelNilHubStillSucceeds() {
	_, cancel := context.WithCancel(context.Background())
	s.srv.quality.mu.Lock()
	s.srv.quality.cancellers = map[string]context.CancelFunc{"ch-1": cancel}
	s.srv.quality.mu.Unlock()
	s.srv.eventsHub = nil

	rec := s.testRequest("DELETE", "/api/channels/ch-1/quality/scan", "")
	require.Equal(s.T(), http.StatusAccepted, rec.Code)
}

// ─── GET /quality/cycles ───

func (s *ServerSuite) TestHandleQualityCyclesGraphProviderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/cycles", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityCyclesNoCachedGraph() {
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/cycles", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestHandleQualityCyclesReturnsDetail() {
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/cycles", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityCyclesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(s.T(), resp.Cycles, "nil-to-empty must produce non-nil slice")
}

// ─── GET /quality/metrics ───

func (s *ServerSuite) TestHandleQualityMetricsSnapshotReaderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/metrics", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityMetricsStoreUnset() {
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	s.srv.store = nil
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/metrics", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityMetricsResolveDirError() {
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/metrics", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityMetricsNoSnapshot() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/metrics", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestHandleQualityMetricsSnapshotErrorReturns500() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{getErr: errors.New("io fail")})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/metrics", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestHandleQualityMetricsReturnsBreakdown() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {
				ChannelID:       "ch-1",
				Branch:          "main",
				Value:           7000,
				GeoMean:         0.7,
				ScannedAt:       time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				MetricBreakdown: json.RawMessage(`[{"name":"modularity","score":0.9,"raw":0.9}]`),
			},
		},
	})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/metrics", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityMetricsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 7000, resp.Signal)
	require.Equal(s.T(), "main", resp.Branch)
	require.Len(s.T(), resp.Metrics, 1)
}

// ─── GET /quality/diagnostics ───

func (s *ServerSuite) TestHandleQualityDiagnosticsSnapshotReaderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/diagnostics", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityDiagnosticsStoreUnset() {
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	s.srv.store = nil
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/diagnostics", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityDiagnosticsResolveDirError() {
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/diagnostics", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityDiagnosticsNoSnapshot() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/diagnostics", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestHandleQualityDiagnosticsSnapshotErrorReturns500() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{getErr: errors.New("io fail")})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/diagnostics", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestHandleQualityDiagnosticsReturnsTiles() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualitySnapshotReader(&fakeSnapshotReader{
		byBranch: map[string]*snapshot.Snapshot{
			"main": {
				ChannelID: "ch-1",
				Branch:    "main",
				Value:     7000,
				TileData: json.RawMessage(
					`[{"path":"a.go","loc":10,"deficit":0.5,"metric_deficits":{"modularity":0.5},"top_reason":"modularity"}]`,
				),
			},
		},
	})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/diagnostics", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityDiagnosticsResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Tiles, 1)
}

// ─── GET /quality/rules ───

func (s *ServerSuite) TestHandleQualityRulesGraphProviderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/rules", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityRulesNoCachedGraph() {
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/rules", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestHandleQualityRulesDefaultConfigRunsAgainstGraph() {
	s.channelWithDir("ch-1", s.T().TempDir())
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/rules", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityRulesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
}

func (s *ServerSuite) TestHandleQualityRulesUsesCustomConfig() {
	s.channelWithDir("ch-1", s.T().TempDir())
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	// All disabled — every rule reports as "pass" with a "disabled" message.
	cfg := rules.Config{Rules: map[string]rules.RuleConfig{
		rules.SignalFloor:    {Enabled: false},
		rules.NoImportCycles: {Enabled: false},
		rules.ParseFail:      {Enabled: false},
	}}
	s.srv.SetQualityRulesLoader(func(string, string) *rules.Config { return &cfg })
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/rules", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityRulesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Failed)
	require.NotEmpty(s.T(), resp.Passed)
}

func (s *ServerSuite) TestHandleQualityRulesEmitsCitationsForFailedRules() {
	s.channelWithDir("ch-1", s.T().TempDir())
	// Build a graph with a cycle so no_import_cycles fires.
	cyc := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "b.go", Imports: []parser.Import{{Path: "./a"}}},
	})
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: cyc})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/rules", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp QualityRulesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(s.T(), resp.Failed)
	var foundWithCitation bool
	for _, r := range resp.Failed {
		if r.Name == rules.NoImportCycles && len(r.Citations) > 0 {
			foundWithCitation = true
		}
	}
	require.True(s.T(), foundWithCitation, "expected at least one cycle citation")
}

// ─── POST /quality/whatif ───

func (s *ServerSuite) TestHandleQualityWhatifGraphProviderUnset() {
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", `{"mutations":[{"op":"delete","path":"x.go"}]}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityWhatifNoCachedGraph() {
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", `{"mutations":[{"op":"delete","path":"x.go"}]}`)
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestHandleQualityWhatifInvalidJSON() {
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", `not-json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "decoding")
}

func (s *ServerSuite) TestHandleQualityWhatifEmptyMutations() {
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", `{"mutations":[]}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "at least one mutation")
}

func (s *ServerSuite) TestHandleQualityWhatifEmptyBodyTreatedAsNoMutations() {
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityWhatifBodyReadError() {
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	req, _ := http.NewRequest("POST", "/api/channels/ch-1/quality/whatif", &errReader{})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "reading body")
}

func (s *ServerSuite) TestHandleQualityWhatifSimulateError() {
	s.channelWithDir("ch-1", s.T().TempDir())
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", `{"mutations":[{"op":"delete","path":"missing.go"}]}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "path not in graph")
}

func (s *ServerSuite) TestHandleQualityWhatifDeleteSucceeds() {
	s.channelWithDir("ch-1", s.T().TempDir())
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("POST", "/api/channels/ch-1/quality/whatif", `{"mutations":[{"op":"delete","path":"internal/api/u.go"}]}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityWhatifResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(s.T(), resp.BaselineMetrics, "expected baseline metrics")
	require.NotEmpty(s.T(), resp.PredictedMetrics, "expected predicted metrics")

	// Guard the wire shape: per-metric breakdown must use lowercase
	// name/score/raw keys (the panel reads bm.score). metrics.Result
	// has no JSON tags, so a direct passthrough would emit Name/Score/
	// Raw and crash the WhatifTab on toFixed.
	var raw struct {
		BaselineMetrics []map[string]any `json:"baseline_metrics"`
	}
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &raw))
	require.NotEmpty(s.T(), raw.BaselineMetrics)
	first := raw.BaselineMetrics[0]
	require.Contains(s.T(), first, "name")
	require.Contains(s.T(), first, "score")
	require.Contains(s.T(), first, "raw")
	require.NotContains(s.T(), first, "Name")
	require.NotContains(s.T(), first, "Score")
}

// ─── GET /quality/evolution ───

func (s *ServerSuite) TestHandleQualityEvolutionHistoryReaderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/evolution", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityEvolutionStoreUnset() {
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{})
	s.srv.store = nil
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/evolution", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityEvolutionResolveDirError() {
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/evolution", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityEvolutionNoHistoryReturns404() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/evolution", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestHandleQualityEvolutionAnalysisErrorReturns500() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{err: errors.New("git failed")})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/evolution", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestHandleQualityEvolutionReturnsResult() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{
		commits: []evolution.CommitFiles{
			{Author: "alice", Timestamp: ts, Files: []string{"a.go", "b.go"}},
			{Author: "alice", Timestamp: ts, Files: []string{"a.go", "b.go"}},
		},
	})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/evolution", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp evolution.Result
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 2, resp.CommitsScanned)
}

// ─── GET /quality/bugfactor ───

func (s *ServerSuite) TestHandleQualityBugFactorHistoryReaderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/bugfactor", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityBugFactorStoreUnset() {
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{})
	s.srv.store = nil
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/bugfactor", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityBugFactorResolveDirError() {
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/bugfactor", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityBugFactorNoHistoryReturns404() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/bugfactor", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestHandleQualityBugFactorAnalysisErrorReturns500() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{err: errors.New("git failed")})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/bugfactor", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestHandleQualityBugFactorReturnsRiskList() {
	dir := s.T().TempDir()
	s.channelWithDir("ch-1", dir)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.srv.SetQualityHistoryReader(&fakeHistoryReader{
		commits: []evolution.CommitFiles{
			{Author: "alice", Timestamp: ts, Files: []string{"solo.go"}},
			{Author: "alice", Timestamp: ts, Files: []string{"solo.go"}},
		},
	})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/bugfactor", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "bus_factor")
}

// ─── GET /quality/c4 ───

func (s *ServerSuite) TestHandleQualityC4GraphProviderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/c4", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityC4NoCachedGraph() {
	s.srv.SetQualityGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/c4", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestHandleQualityC4ReturnsDiagram() {
	s.srv.SetQualityGraphProvider(&graphProviderHit{g: smallGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/c4", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "flowchart LR")
}

// ─── lookupCachedGraph + loadSnapshot helpers covered indirectly above ───

// ─── nilToEmpty unit ───

func (s *ServerSuite) TestNilToEmptyConvertsNil() {
	require.Equal(s.T(), [][]string{}, nilToEmpty(nil))
}

func (s *ServerSuite) TestNilToEmptyPassesThroughNonNil() {
	in := [][]string{{"a", "b"}}
	require.Equal(s.T(), in, nilToEmpty(in))
}
