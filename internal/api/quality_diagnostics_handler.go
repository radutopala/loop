// Package api: quality_diagnostics_handler.go exposes the diagnostics/insight
// tier of the quality engine over HTTP — cycles, metrics breakdown, per-file
// deficits, rules pass/fail, what-if simulation, evolution analysis (git
// history mining), C4 component diagram, and bus-factor risk. All endpoints
// are nested under /api/channels/{id}/quality/... and assume a trusted
// localhost zone (no auth on /api/*).
//
// These handlers read from the cached graph + snapshot — they do NOT trigger
// new scans. POST /scan is the trigger; everything else is read-only or
// pure-function.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/radutopala/loop/internal/quality/c4"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
	"github.com/radutopala/loop/internal/quality/whatif"
)

// handleQualityScanCancel cancels an in-flight scan for the channel. If
// no scan is running, returns 204 (idempotent). Otherwise calls the
// stored CancelFunc and broadcasts quality.scan_cancelled.
func (s *qualityService) handleQualityScanCancel(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	s.mu.Lock()
	cancel, ok := s.cancellers[channelID]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cancel()
	if hub := s.deps.eventsHub; hub != nil {
		hub.BroadcastQualityEvent(EventQualityScanCancelled, channelID, map[string]string{
			"reason": "user_requested",
		})
	}
	w.WriteHeader(http.StatusAccepted)
}

// QualityCyclesResponse is the GET /cycles wire shape — a passthrough
// of the cached graph's CyclesDetail. Empty when no scan has run.
type QualityCyclesResponse struct {
	Cycles             [][]string `json:"cycles"`
	LargestCycleSize   int        `json:"largest_cycle_size"`
	TotalNodesInCycles int        `json:"total_nodes_in_cycles"`
}

func (s *qualityService) handleQualityCycles(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	g, ok := s.lookupCachedGraph(w, channelID)
	if !ok {
		return
	}
	res := metrics.Cycles(g)
	detail, _ := res.Detail.(metrics.CyclesDetail)
	writeHTTPJSON(w, http.StatusOK, QualityCyclesResponse{
		Cycles:             nilToEmpty(detail.Cycles),
		LargestCycleSize:   detail.LargestCycleSize,
		TotalNodesInCycles: detail.TotalNodesInCycles,
	}, s.deps.logger)
}

// QualityMetricsResponse is the GET /metrics wire shape — the full
// per-metric breakdown from the latest snapshot. Driven from the
// snapshot reader (not the live graph) so the values match what the
// panel rendered for the last scan.
type QualityMetricsResponse struct {
	Branch    string                `json:"branch"`
	Signal    int                   `json:"signal"`
	GeoMean   float64               `json:"geo_mean"`
	ScannedAt string                `json:"scanned_at"`
	Metrics   []QualityMetricReport `json:"metrics"`
}

func (s *qualityService) handleQualityMetrics(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.snapshots, "quality snapshot reader not configured") {
		return
	}
	if !requireConfigured(w, s.deps.store, "channel store not configured") {
		return
	}
	channelID := r.PathValue("id")
	dirPath, err := s.deps.workspace.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	branch := gitBranch(r.Context(), dirPath)
	snap, err := s.loadSnapshot(r.Context(), channelID, branch)
	if err != nil {
		writeQualityLookupError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, QualityMetricsResponse{
		Branch:    snap.Branch,
		Signal:    snap.Value,
		GeoMean:   snap.GeoMean,
		ScannedAt: snap.ScannedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Metrics:   unmarshalMetricBreakdown(snap.MetricBreakdown, s.deps.logger),
	}, s.deps.logger)
}

// QualityDiagnosticsResponse exposes the per-file deficit attribution as
// the panel's diagnostics list. Returned in the same descending-deficit
// order metrics.AttributeFiles produces, with TopReason naming the
// worst metric per file.
type QualityDiagnosticsResponse struct {
	Tiles []QualityFileTile `json:"tiles"`
}

func (s *qualityService) handleQualityDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.snapshots, "quality snapshot reader not configured") {
		return
	}
	if !requireConfigured(w, s.deps.store, "channel store not configured") {
		return
	}
	channelID := r.PathValue("id")
	dirPath, err := s.deps.workspace.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	branch := gitBranch(r.Context(), dirPath)
	snap, err := s.loadSnapshot(r.Context(), channelID, branch)
	if err != nil {
		writeQualityLookupError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, QualityDiagnosticsResponse{
		Tiles: unmarshalTileData(snap.TileData, s.deps.logger),
	}, s.deps.logger)
}

// QualityRulesResponse passes through the rules engine's outcome for
// the cached graph. Mirrors the QualityRulesReport shape so the panel
// can render either source identically.
type QualityRulesResponse = QualityRulesReport

func (s *qualityService) handleQualityRules(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	g, ok := s.lookupCachedGraph(w, channelID)
	if !ok {
		return
	}
	// Best-effort dir resolution: if the channel store isn't wired or
	// the channel has no dir_path, fall back to "" — the loader treats
	// that as "use global config only", which is the same behaviour the
	// previous static config field provided.
	var dirPath, parentDirPath string
	if s.deps.store != nil {
		if d, err := s.deps.workspace.resolveDirPath(r.Context(), "", channelID); err == nil {
			dirPath = d
			parentDirPath = s.deps.workspace.resolveParentDirPath(r.Context(), channelID)
		}
	}
	sig := metrics.ComputeWith(g, s.resolveMetricsConfig(dirPath, parentDirPath))
	results := rules.Run(s.resolveRulesConfig(dirPath, parentDirPath), g, sig)

	resp := QualityRulesReport{}
	for _, ru := range results {
		rr := QualityRuleReport{Name: ru.Name, Severity: string(ru.Severity), Message: ru.Message}
		for _, c := range ru.Citations {
			rr.Citations = append(rr.Citations, QualityCitationReport{Path: c.Path, Note: c.Note})
		}
		if ru.Severity == rules.SevFail {
			resp.Failed = append(resp.Failed, rr)
		} else {
			resp.Passed = append(resp.Passed, rr)
		}
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.deps.logger)
}

// QualityWhatifRequest is the POST /whatif body — a list of mutations
// to project against the cached graph.
type QualityWhatifRequest struct {
	Mutations []whatif.Mutation `json:"mutations"`
}

// QualityWhatifResponse is the wire shape for /whatif. metrics.Result
// has no JSON tags, so we project to QualityMetricReport here to keep
// the per-metric breakdown lowercase for the panel.
type QualityWhatifResponse struct {
	Mutations        []whatif.Mutation     `json:"mutations"`
	BaselineSignal   int                   `json:"baseline_signal"`
	PredictedSignal  int                   `json:"predicted_signal"`
	DeltaSignal      int                   `json:"delta_signal"`
	BaselineMetrics  []QualityMetricReport `json:"baseline_metrics"`
	PredictedMetrics []QualityMetricReport `json:"predicted_metrics"`
}

func (s *qualityService) handleQualityWhatif(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	g, ok := s.lookupCachedGraph(w, channelID)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading body: %v", err), http.StatusBadRequest)
		return
	}
	var req QualityWhatifRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, fmt.Sprintf("decoding request: %v", err), http.StatusBadRequest)
			return
		}
	}
	if len(req.Mutations) == 0 {
		http.Error(w, "mutations: at least one mutation required", http.StatusBadRequest)
		return
	}
	var dirPath, parentDirPath string
	if s.deps.store != nil {
		if d, derr := s.deps.workspace.resolveDirPath(r.Context(), "", channelID); derr == nil {
			dirPath = d
			parentDirPath = s.deps.workspace.resolveParentDirPath(r.Context(), channelID)
		}
	}
	res, err := whatif.SimulateWith(g, req.Mutations, s.resolveMetricsConfig(dirPath, parentDirPath))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeHTTPJSON(w, http.StatusOK, QualityWhatifResponse{
		Mutations:        res.Mutations,
		BaselineSignal:   res.BaselineSignal,
		PredictedSignal:  res.PredictedSignal,
		DeltaSignal:      res.DeltaSignal,
		BaselineMetrics:  metricResultsToReport(res.BaselineMetrics),
		PredictedMetrics: metricResultsToReport(res.PredictedMetrics),
	}, s.deps.logger)
}

func metricResultsToReport(in []metrics.Result) []QualityMetricReport {
	out := make([]QualityMetricReport, 0, len(in))
	for _, m := range in {
		out = append(out, QualityMetricReport{Name: m.Name, Score: m.Score, Raw: m.Raw})
	}
	return out
}

func (s *qualityService) handleQualityEvolution(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.history, "quality history reader not configured") {
		return
	}
	if !requireConfigured(w, s.deps.store, "channel store not configured") {
		return
	}
	channelID := r.PathValue("id")
	dirPath, err := s.deps.workspace.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := evolution.Analyze(r.Context(), s.history, dirPath, evolution.Options{})
	if err != nil {
		if errors.Is(err, evolution.ErrNoHistory) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, res, s.deps.logger)
}

func (s *qualityService) handleQualityBugFactor(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.history, "quality history reader not configured") {
		return
	}
	if !requireConfigured(w, s.deps.store, "channel store not configured") {
		return
	}
	channelID := r.PathValue("id")
	dirPath, err := s.deps.workspace.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := evolution.Analyze(r.Context(), s.history, dirPath, evolution.Options{})
	if err != nil {
		if errors.Is(err, evolution.ErrNoHistory) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{
		"bus_factor":      res.BusFactor,
		"commits_scanned": res.CommitsScanned,
		"shallow_warning": res.ShallowWarning,
	}, s.deps.logger)
}

func (s *qualityService) handleQualityC4(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	g, ok := s.lookupCachedGraph(w, channelID)
	if !ok {
		return
	}
	writeHTTPJSON(w, http.StatusOK, c4.Emit(g), s.deps.logger)
}

// lookupCachedGraph resolves the cached graph for channelID and writes
// a 503 (no graph) or 501 (graph provider unset) on miss. Returns the
// graph + true when the caller should proceed; false means the response
// has already been written.
func (s *qualityService) lookupCachedGraph(w http.ResponseWriter, channelID string) (*graph.Graph, bool) {
	if s.graph == nil {
		http.Error(w, "quality graph provider not configured", http.StatusNotImplemented)
		return nil, false
	}
	g, _ := s.graph.Get(channelID)
	if g == nil {
		http.Error(w, "no graph cached; trigger a scan first", http.StatusServiceUnavailable)
		return nil, false
	}
	return g, true
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

func writeQualityLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, snapshot.ErrNotFound) {
		http.Error(w, "no snapshot yet", http.StatusNotFound)
		return
	}
	http.Error(w, fmt.Sprintf("loading snapshot: %v", err), http.StatusInternalServerError)
}

func nilToEmpty(in [][]string) [][]string {
	if in == nil {
		return [][]string{}
	}
	return in
}

// SetQualityHistoryReader wires the git-history reader for the evolution
// and bug-factor endpoints. Nil disables those endpoints (501).
func (s *Server) SetQualityHistoryReader(r QualityHistoryReader) {
	s.quality.history = r
}
