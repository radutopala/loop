// Package api: quality_handler.go exposes the structural-quality scan engine
// over HTTP. Endpoints are nested under /api/channels/{id}/quality/... and
// are intended for local-Electron consumption only — there is no auth on
// any /api/* route today, so the surface assumes a trusted-localhost zone.
//
// Wire shape mirrors the CLI's scanReport so a future refactor can fold the
// two into a shared package without breaking either consumer.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

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

// QualityScanReport is the JSON contract for both the POST scan response
// and the quality.scanned event payload. Matches the CLI's scanReport
// shape so the panel can render either source identically.
//
// PreviousSignal mirrors QualitySnapshotResponse.PreviousSignal — the
// prior scan's headline for this (channel, branch), or
// snapshot.NoPreviousValue (-1) on first scan. Lets the panel show
// the Δ chip immediately on quality.scanned without re-fetching the
// snapshot.
type QualityScanReport struct {
	DirPath        string                `json:"dir_path"`
	Branch         string                `json:"branch"`
	Signal         int                   `json:"signal"`
	PreviousSignal int                   `json:"previous_signal"`
	GeoMean        float64               `json:"geo_mean"`
	FileCount      int                   `json:"file_count"`
	ParseFailed    int                   `json:"parse_failed"`
	ScannedAt      time.Time             `json:"scanned_at"`
	Metrics        []QualityMetricReport `json:"metrics"`
	Tiles          []QualityFileTile     `json:"tiles"`
	Rules          QualityRulesReport    `json:"rules"`
}

// QualityFileTile is the wire shape for one file's deficit attribution.
// The treemap renders tile size from LOC and color from Deficit;
// MetricDeficits drives the popover; TopReason picks the worst metric
// for the tile's one-word label.
type QualityFileTile struct {
	Path           string             `json:"path"`
	LOC            int                `json:"loc"`
	Deficit        float64            `json:"deficit"`
	MetricDeficits map[string]float64 `json:"metric_deficits"`
	TopReason      string             `json:"top_reason"`
}

// QualityMetricReport is one metric's value within a scan report. Detail
// is omitted from the wire shape — the panel does not need per-metric
// internals at this milestone.
type QualityMetricReport struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Raw   float64 `json:"raw"`
}

// QualityRulesReport splits rule outcomes by severity for the panel —
// passed and failed are rendered in different sections, so the API
// pre-bucketed shape is what the consumer wants.
type QualityRulesReport struct {
	Passed []QualityRuleReport `json:"passed"`
	Failed []QualityRuleReport `json:"failed"`
}

// QualityRuleReport is one rule's outcome. Citations only populate for
// failures; passed rules emit an empty list and the field is omitted.
type QualityRuleReport struct {
	Name      string                  `json:"name"`
	Severity  string                  `json:"severity"`
	Message   string                  `json:"message"`
	Citations []QualityCitationReport `json:"citations,omitempty"`
}

type QualityCitationReport struct {
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

// QualitySnapshotResponse is the GET endpoint shape. BranchMismatch is
// true when no snapshot exists for the requested branch but a snapshot
// for a different branch is returned — the panel renders a banner.
//
// PreviousSignal is the headline value from the prior scan of the same
// (channel, branch) pair, captured by the snapshot UPSERT path. The
// sentinel snapshot.NoPreviousValue (-1) means "first scan ever for
// this row" — the panel hides the Δ chip in that case. Always present
// on the wire (no omitempty) so the UI can distinguish "no delta yet"
// from "delta is exactly zero".
type QualitySnapshotResponse struct {
	DirPath        string                `json:"dir_path"`
	Branch         string                `json:"branch"`
	CurrentBranch  string                `json:"current_branch"`
	BranchMismatch bool                  `json:"branch_mismatch"`
	Signal         int                   `json:"signal"`
	PreviousSignal int                   `json:"previous_signal"`
	GeoMean        float64               `json:"geo_mean"`
	ScannedAt      time.Time             `json:"scanned_at"`
	Metrics        []QualityMetricReport `json:"metrics"`
	Tiles          []QualityFileTile     `json:"tiles"`
}

// scanResponse is the POST endpoint's immediate return when the scan was
// successfully started/coalesced. The full report ships via the
// quality.scanned event so the panel updates without re-fetching.
type scanResponse struct {
	Status string `json:"status"`
}

// SetQualityScanner wires the scanner used by the POST scan endpoint.
// Nil disables the endpoint (501).
func (s *Server) SetQualityScanner(sc QualityScanner) {
	s.qualityScanner = sc
}

// SetQualityGraphProvider wires the graph cache used to evaluate rules
// on the just-completed scan. Nil disables rule evaluation but the scan
// endpoint stays alive (returns empty rule lists).
func (s *Server) SetQualityGraphProvider(gp QualityGraphProvider) {
	s.qualityGraph = gp
}

// SetQualitySnapshotReader wires the snapshot lookup for the GET endpoint.
// Nil disables the endpoint (501).
func (s *Server) SetQualitySnapshotReader(r QualitySnapshotReader) {
	s.qualitySnapshots = r
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

// SetQualityRulesLoader wires the per-scan rules-config resolver. Nil
// disables overrides — handlers fall back to rules.DefaultConfig() at
// evaluation time. Replaces the static SetQualityRulesConfig so changes
// to project-level rule overrides are picked up without restarting the
// daemon (mirrors qualityConfigLoader for the engine config).
func (s *Server) SetQualityRulesLoader(loader QualityRulesLoader) {
	s.qualityRulesLoad = loader
}

// SetQualityMetricsLoader wires the per-scan metrics-config resolver
// used by handlers that recompute the signal from the cached graph
// (rules, whatif). Nil disables overrides — handlers fall back to
// metrics.DefaultConfig() at evaluation time, matching the behaviour
// before per-metric thresholds were configurable.
func (s *Server) SetQualityMetricsLoader(loader QualityMetricsLoader) {
	s.qualityMetricsCfg = loader
}

// resolveMetricsConfig returns the effective metrics.Config for a
// recompute on the cached graph, invoking the loader (if wired) and
// falling back to metrics.DefaultConfig() when the loader is unset or
// returns the zero value.
func (s *Server) resolveMetricsConfig(dirPath, parentDirPath string) metrics.Config {
	if s.qualityMetricsCfg == nil {
		return metrics.DefaultConfig()
	}
	cfg := s.qualityMetricsCfg(dirPath, parentDirPath)
	if cfg == (metrics.Config{}) {
		return metrics.DefaultConfig()
	}
	return cfg
}

// progressThrottle is the minimum gap between successive quality.scan_progress
// events for a single channel. The engine fires progress per file; without a
// throttle a 5000-file scan would emit thousands of events. 250ms keeps panel
// updates smooth.
const progressThrottle = 250 * time.Millisecond

// EmitQualityProgress is the engine's ProgressFunc hook — wired by the daemon
// at startup. Throttles to one event per channel per progressThrottle window
// so the bus doesn't drown in per-file pings. Always emits the terminal
// (done==total) tick so the panel can clear the spinner cleanly.
func (s *Server) EmitQualityProgress(channelID string, done, total int) {
	hub := s.eventsHub
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

func (s *Server) shouldEmitProgress(channelID string, done, total int) bool {
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if s.qualityProgress == nil {
		s.qualityProgress = make(map[string]time.Time)
	}
	now := time.Now()
	// First (done == 0) and terminal (done == total) ticks always pass — the
	// panel needs them to drive the spinner state machine.
	if done == 0 || done >= total {
		s.qualityProgress[channelID] = now
		return true
	}
	if last, ok := s.qualityProgress[channelID]; ok && now.Sub(last) < progressThrottle {
		return false
	}
	s.qualityProgress[channelID] = now
	return true
}

// handleQualityScan kicks an engine scan asynchronously and broadcasts
// quality.session_started + quality.scanned + (optional) quality.rules_violated.
// Returns 202 Accepted with a status hint; the panel waits on the events
// for the full payload.
func (s *Server) handleQualityScan(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.qualityScanner, "quality scanner not configured") {
		return
	}
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	parentDirPath := s.resolveParentDirPath(r.Context(), channelID)
	branch := gitBranch(r.Context(), dirPath)
	if branch == "" {
		branch = "main"
	}

	// Run the scan in the background so the request returns immediately.
	// A separate context detached from r.Context lets the scan outlive
	// the HTTP request — clients only need the eventual quality.scanned
	// event, not the response body.
	scanCtx, cancel := context.WithCancel(context.Background())
	if !s.registerQualityScan(channelID, cancel) {
		cancel()
		writeHTTPJSON(w, http.StatusAccepted, scanResponse{Status: "in_progress"}, s.logger)
		return
	}

	if hub := s.eventsHub; hub != nil {
		hub.BroadcastQualityEvent(EventQualitySessionStarted, channelID, map[string]string{
			"dir_path": dirPath,
			"branch":   branch,
		})
	}

	go s.runQualityScanAsync(scanCtx, channelID, dirPath, parentDirPath, branch)
	writeHTTPJSON(w, http.StatusAccepted, scanResponse{Status: "started"}, s.logger)
}

// runQualityScanAsync runs the engine scan, broadcasts the result, and
// drops the in-flight registration. The scan ctx is detached from the
// HTTP request so this goroutine owns its full lifecycle.
func (s *Server) runQualityScanAsync(ctx context.Context, channelID, dirPath, parentDirPath, branch string) {
	defer s.unregisterQualityScan(channelID)

	res, err := s.qualityScanner.Scan(ctx, channelID, branch, dirPath, parentDirPath)
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

	if hub := s.eventsHub; hub != nil {
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
func (s *Server) broadcastQualityError(channelID, dirPath, branch string, err error) {
	hub := s.eventsHub
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
func (s *Server) collectRules(channelID, dirPath, parentDirPath string, sig metrics.Signal) []rules.Result {
	if s.qualityGraph == nil {
		return nil
	}
	g, _ := s.qualityGraph.Get(channelID)
	if g == nil {
		return nil
	}
	return rules.Run(s.resolveRulesConfig(dirPath, parentDirPath), g, sig)
}

// resolveRulesConfig returns the effective rules.Config for a scan,
// invoking the loader (if wired) and falling back to
// rules.DefaultConfig() when the loader is unset or returns nil.
func (s *Server) resolveRulesConfig(dirPath, parentDirPath string) rules.Config {
	if s.qualityRulesLoad == nil {
		return rules.DefaultConfig()
	}
	if cfg := s.qualityRulesLoad(dirPath, parentDirPath); cfg != nil {
		return *cfg
	}
	return rules.DefaultConfig()
}

// registerQualityScan claims the in-flight slot for channelID. Returns
// false if a scan is already registered (the caller treats that as
// "in_progress" without spawning a new goroutine).
func (s *Server) registerQualityScan(channelID string, cancel context.CancelFunc) bool {
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if s.qualityCancellers == nil {
		s.qualityCancellers = make(map[string]context.CancelFunc)
	}
	if _, ok := s.qualityCancellers[channelID]; ok {
		return false
	}
	s.qualityCancellers[channelID] = cancel
	return true
}

// unregisterQualityScan releases the in-flight slot. Idempotent so
// double-defers from edge cases don't panic.
func (s *Server) unregisterQualityScan(channelID string) {
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	delete(s.qualityCancellers, channelID)
	delete(s.qualityProgress, channelID)
}

// handleQualitySnapshot returns the persisted snapshot for the channel's
// current branch (or the most recent snapshot when no current-branch
// row exists). Returns 404 when no snapshot has ever been recorded —
// the panel uses that as the trigger to render the "Scan now" empty state.
func (s *Server) handleQualitySnapshot(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.qualitySnapshots, "quality snapshot reader not configured") {
		return
	}
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	currentBranch := gitBranch(r.Context(), dirPath)
	if currentBranch == "" {
		currentBranch = "main"
	}

	snap, err := s.qualitySnapshots.Get(r.Context(), channelID, currentBranch)
	branchMismatch := false
	if errors.Is(err, snapshot.ErrNotFound) {
		// Try the most-recent snapshot on any branch — the panel can
		// still render its numbers behind a "snapshot taken on <other>"
		// banner.
		snap, err = s.qualitySnapshots.GetLatest(r.Context(), channelID)
		if errors.Is(err, snapshot.ErrNotFound) {
			http.Error(w, "no snapshot yet", http.StatusNotFound)
			return
		}
		if err == nil {
			branchMismatch = snap.Branch != currentBranch
		}
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("loading snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	resp := QualitySnapshotResponse{
		DirPath:        dirPath,
		Branch:         snap.Branch,
		CurrentBranch:  currentBranch,
		BranchMismatch: branchMismatch,
		Signal:         snap.Value,
		PreviousSignal: snap.PreviousValue,
		GeoMean:        snap.GeoMean,
		ScannedAt:      snap.ScannedAt,
		Metrics:        unmarshalMetricBreakdown(snap.MetricBreakdown, s.logger),
		Tiles:          unmarshalTileData(snap.TileData, s.logger),
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// unmarshalMetricBreakdown decodes the snapshot row's stored metrics
// blob into the wire-shape report. A bad row logs and yields an empty
// list rather than 500 — the snapshot's signal value is still useful.
func unmarshalMetricBreakdown(raw json.RawMessage, logger interface {
	Error(msg string, args ...any)
}) []QualityMetricReport {
	if len(raw) == 0 {
		return nil
	}
	var stored []metrics.Result
	if err := json.Unmarshal(raw, &stored); err != nil {
		logger.Error("quality snapshot: bad metric_breakdown_json", "error", err)
		return nil
	}
	out := make([]QualityMetricReport, 0, len(stored))
	for _, m := range stored {
		out = append(out, QualityMetricReport{Name: m.Name, Score: m.Score, Raw: m.Raw})
	}
	return out
}

// unmarshalTileData decodes the snapshot row's stored tile blob into the
// wire-shape tile slice. Same defensive treatment as the metric breakdown.
func unmarshalTileData(raw json.RawMessage, logger interface {
	Error(msg string, args ...any)
}) []QualityFileTile {
	if len(raw) == 0 {
		return nil
	}
	var stored []metrics.FileTile
	if err := json.Unmarshal(raw, &stored); err != nil {
		logger.Error("quality snapshot: bad tile_data_json", "error", err)
		return nil
	}
	out := make([]QualityFileTile, 0, len(stored))
	for _, t := range stored {
		out = append(out, QualityFileTile{
			Path:           t.Path,
			LOC:            t.LOC,
			Deficit:        t.Deficit,
			MetricDeficits: t.MetricDeficits,
			TopReason:      t.TopReason,
		})
	}
	return out
}

// buildQualityReport assembles the wire-shape report from a finished
// engine ScanResult plus the rule results. Lifted out of the handler so
// tests can exercise the shape independently.
func buildQualityReport(dirPath, branch string, res engine.ScanResult, ruleResults []rules.Result) QualityScanReport {
	rep := QualityScanReport{
		DirPath:        dirPath,
		Branch:         branch,
		Signal:         res.Signal.Value,
		PreviousSignal: res.PreviousSignal,
		GeoMean:        res.Signal.GeoMean,
		FileCount:      res.FileCount,
		ParseFailed:    res.ParseFailed,
		ScannedAt:      res.ScannedAt,
	}
	for _, m := range res.Signal.Metrics {
		rep.Metrics = append(rep.Metrics, QualityMetricReport{
			Name: m.Name, Score: m.Score, Raw: m.Raw,
		})
	}
	for _, t := range res.Signal.Tiles {
		rep.Tiles = append(rep.Tiles, QualityFileTile{
			Path:           t.Path,
			LOC:            t.LOC,
			Deficit:        t.Deficit,
			MetricDeficits: t.MetricDeficits,
			TopReason:      t.TopReason,
		})
	}
	for _, ru := range ruleResults {
		rr := QualityRuleReport{
			Name:     ru.Name,
			Severity: string(ru.Severity),
			Message:  ru.Message,
		}
		for _, c := range ru.Citations {
			rr.Citations = append(rr.Citations, QualityCitationReport{Path: c.Path, Note: c.Note})
		}
		if ru.Severity == rules.SevFail {
			rep.Rules.Failed = append(rep.Rules.Failed, rr)
		} else {
			rep.Rules.Passed = append(rep.Rules.Passed, rr)
		}
	}
	return rep
}
