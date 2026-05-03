// Package mcpserver: quality_diagnostics.go exposes the diagnostics/insight
// tier of the quality engine to MCP agents — cycles, metrics breakdown,
// per-file deficits, rule outcomes, what-if simulation, evolution analysis,
// C4 component diagram, bus-factor risk. Each tool is a thin wrapper around
// the corresponding HTTP endpoint, formatting the response for the agent's
// text channel and returning a structured payload alongside.
//
// All tools are per-channel (s.channelID, no WorkDir arg). They assume the
// channel has been scanned at least once — the cycles/metrics/diagnostics/
// rules/whatif/c4 endpoints return 503 (no graph) until then.

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type qualityCyclesInput struct{}

type qualityCyclesPayload struct {
	Cycles             [][]string `json:"cycles"`
	LargestCycleSize   int        `json:"largest_cycle_size"`
	TotalNodesInCycles int        `json:"total_nodes_in_cycles"`
}

func (s *Server) handleQualityCycles(_ context.Context, _ *mcp.CallToolRequest, _ qualityCyclesInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_cycles", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_cycles requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/cycles", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityCyclesPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	return textResult(formatQualityCycles(resp)), resp, nil
}

func formatQualityCycles(p *qualityCyclesPayload) string {
	if len(p.Cycles) == 0 {
		return "No import cycles detected."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d cycle(s); %d files involved (largest cycle has %d files):\n",
		len(p.Cycles), p.TotalNodesInCycles, p.LargestCycleSize)
	for i, cyc := range p.Cycles {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, strings.Join(cyc, " → "))
	}
	return b.String()
}

type qualityMetricsInput struct{}

type qualityMetricsListPayload struct {
	Branch    string                  `json:"branch"`
	Signal    int                     `json:"signal"`
	GeoMean   float64                 `json:"geo_mean"`
	ScannedAt string                  `json:"scanned_at"`
	Metrics   []qualityMetricsPayload `json:"metrics"`
}

func (s *Server) handleQualityMetrics(_ context.Context, _ *mcp.CallToolRequest, _ qualityMetricsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_metrics", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_metrics requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/metrics", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityMetricsListPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Quality signal: %d (geo-mean %.3f) on %q (scanned %s)\n",
		resp.Signal, resp.GeoMean, resp.Branch, resp.ScannedAt)
	for _, m := range resp.Metrics {
		fmt.Fprintf(&b, "  - %s: score=%.3f raw=%.3f\n", m.Name, m.Score, m.Raw)
	}
	return textResult(b.String()), resp, nil
}

type qualityDiagnosticsInput struct {
	Limit int `json:"limit,omitempty"`
}

type qualityFileTilePayload struct {
	Path           string             `json:"path"`
	LOC            int                `json:"loc"`
	Deficit        float64            `json:"deficit"`
	MetricDeficits map[string]float64 `json:"metric_deficits"`
	TopReason      string             `json:"top_reason"`
}

type qualityDiagnosticsPayload struct {
	Tiles []qualityFileTilePayload `json:"tiles"`
}

func (s *Server) handleQualityDiagnostics(_ context.Context, _ *mcp.CallToolRequest, input qualityDiagnosticsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_diagnostics", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_diagnostics requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/diagnostics", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityDiagnosticsPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > len(resp.Tiles) {
		limit = len(resp.Tiles)
	}
	tiles := resp.Tiles[:limit]
	var b strings.Builder
	if len(tiles) == 0 {
		return textResult("No per-file deficits — every file is contributing positively to the signal."), resp, nil
	}
	fmt.Fprintf(&b, "Top %d files by score deficit:\n", len(tiles))
	for i, t := range tiles {
		fmt.Fprintf(&b, "  %d. %s (LOC %d, deficit %.3f, top reason: %s)\n",
			i+1, t.Path, t.LOC, t.Deficit, t.TopReason)
	}
	return textResult(b.String()), &qualityDiagnosticsPayload{Tiles: tiles}, nil
}

type qualityRulesInput struct{}

type qualityCitationPayload struct {
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

type qualityRulePayload struct {
	Name      string                   `json:"name"`
	Severity  string                   `json:"severity"`
	Message   string                   `json:"message"`
	Citations []qualityCitationPayload `json:"citations,omitempty"`
}

type qualityRulesPayload struct {
	Passed []qualityRulePayload `json:"passed"`
	Failed []qualityRulePayload `json:"failed"`
}

func (s *Server) handleQualityRules(_ context.Context, _ *mcp.CallToolRequest, _ qualityRulesInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_rules", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_rules requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/rules", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityRulesPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Rules: %d passed, %d failed\n", len(resp.Passed), len(resp.Failed))
	for _, ru := range resp.Failed {
		fmt.Fprintf(&b, "  ✗ %s — %s\n", ru.Name, ru.Message)
		for _, c := range ru.Citations {
			fmt.Fprintf(&b, "      • %s%s\n", c.Path, formatCitationNote(c.Note))
		}
	}
	for _, ru := range resp.Passed {
		fmt.Fprintf(&b, "  ✓ %s — %s\n", ru.Name, ru.Message)
	}
	return textResult(b.String()), resp, nil
}

func formatCitationNote(note string) string {
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}

type qualityWhatifMutationInput struct {
	Op        string `json:"op"`
	Path      string `json:"path"`
	NewModule string `json:"new_module,omitempty"`
	Parts     int    `json:"parts,omitempty"`
}

type qualityWhatifInput struct {
	Mutations []qualityWhatifMutationInput `json:"mutations"`
}

type qualityWhatifPayload struct {
	BaselineSignal   int                     `json:"baseline_signal"`
	PredictedSignal  int                     `json:"predicted_signal"`
	DeltaSignal      int                     `json:"delta_signal"`
	BaselineMetrics  []qualityMetricsPayload `json:"baseline_metrics"`
	PredictedMetrics []qualityMetricsPayload `json:"predicted_metrics"`
}

func (s *Server) handleQualityWhatif(_ context.Context, _ *mcp.CallToolRequest, input qualityWhatifInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_whatif", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_whatif requires a channel"), nil, nil
	}
	if len(input.Mutations) == 0 {
		return errorResult("at least one mutation is required (op: delete | move | split)"), nil, nil
	}
	body, _ := json.Marshal(input)
	url := fmt.Sprintf("%s/api/channels/%s/quality/whatif", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityWhatifPayload](s, "POST", url, http.StatusOK, body)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	sign := "+"
	if resp.DeltaSignal < 0 {
		sign = ""
	}
	fmt.Fprintf(&b, "Signal: %d → %d (%s%d)\n",
		resp.BaselineSignal, resp.PredictedSignal, sign, resp.DeltaSignal)
	b.WriteString("Per-metric (predicted):\n")
	for _, m := range resp.PredictedMetrics {
		fmt.Fprintf(&b, "  - %s: score=%.3f raw=%.3f\n", m.Name, m.Score, m.Raw)
	}
	return textResult(b.String()), resp, nil
}

type qualityEvolutionInput struct{}

type qualityCouplingPair struct {
	FileA         string  `json:"file_a"`
	FileB         string  `json:"file_b"`
	CoChangeCount int     `json:"co_change_count"`
	Jaccard       float64 `json:"jaccard"`
	CrossModule   bool    `json:"cross_module"`
}

type qualityChurnHotspot struct {
	File          string `json:"file"`
	ChangeCount   int    `json:"change_count"`
	LastChangedAt string `json:"last_changed_at"`
}

type qualityBusFactor struct {
	File               string  `json:"file"`
	SoleAuthor         string  `json:"sole_author"`
	SoleAuthorRatio    float64 `json:"sole_author_ratio"`
	TotalCommits       int     `json:"total_commits"`
	DaysSinceLastOther int     `json:"days_since_last_other_author"`
}

type qualityEvolutionPayload struct {
	CommitsScanned int                   `json:"commits_scanned"`
	ShallowWarning bool                  `json:"shallow_warning"`
	CouplingPairs  []qualityCouplingPair `json:"coupling_pairs"`
	ChurnHotspots  []qualityChurnHotspot `json:"churn_hotspots"`
	BusFactor      []qualityBusFactor    `json:"bus_factor"`
}

func (s *Server) handleQualityEvolution(_ context.Context, _ *mcp.CallToolRequest, _ qualityEvolutionInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_evolution", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_evolution requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/evolution", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityEvolutionPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Scanned %d commits", resp.CommitsScanned)
	if resp.ShallowWarning {
		b.WriteString(" (shallow clone — fewer than expected; consider git fetch --unshallow)")
	}
	b.WriteString(".\n")
	if len(resp.CouplingPairs) > 0 {
		b.WriteString("Top coupling pairs (Jaccard ≥ threshold):\n")
		for i, p := range resp.CouplingPairs {
			if i >= 10 {
				break
			}
			cross := ""
			if p.CrossModule {
				cross = " [cross-module]"
			}
			fmt.Fprintf(&b, "  %d. %s ⇄ %s — j=%.2f, %d co-changes%s\n",
				i+1, p.FileA, p.FileB, p.Jaccard, p.CoChangeCount, cross)
		}
	}
	if len(resp.ChurnHotspots) > 0 {
		b.WriteString("Churn hotspots:\n")
		for i, h := range resp.ChurnHotspots {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "  %d. %s — %d changes\n", i+1, h.File, h.ChangeCount)
		}
	}
	if len(resp.BusFactor) > 0 {
		b.WriteString("Bus-factor risks:\n")
		for i, r := range resp.BusFactor {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "  %d. %s — %s owns %.0f%% (%d commits)\n",
				i+1, r.File, r.SoleAuthor, r.SoleAuthorRatio*100, r.TotalCommits)
		}
	}
	return textResult(b.String()), resp, nil
}

type qualityBugFactorInput struct{}

type qualityBugFactorPayload struct {
	BusFactor      []qualityBusFactor `json:"bus_factor"`
	CommitsScanned int                `json:"commits_scanned"`
	ShallowWarning bool               `json:"shallow_warning"`
}

func (s *Server) handleQualityBugFactor(_ context.Context, _ *mcp.CallToolRequest, _ qualityBugFactorInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_bugfactor", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_bugfactor requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/bugfactor", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityBugFactorPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	if len(resp.BusFactor) == 0 {
		return textResult(fmt.Sprintf("Scanned %d commits — no concentrated bus-factor risks above threshold.", resp.CommitsScanned)), resp, nil
	}
	fmt.Fprintf(&b, "Scanned %d commits; %d files have concentrated authorship:\n",
		resp.CommitsScanned, len(resp.BusFactor))
	for i, r := range resp.BusFactor {
		fmt.Fprintf(&b, "  %d. %s — %s owns %.0f%% (%d commits, %d days since last other author)\n",
			i+1, r.File, r.SoleAuthor, r.SoleAuthorRatio*100, r.TotalCommits, r.DaysSinceLastOther)
	}
	return textResult(b.String()), resp, nil
}

type qualityC4Input struct{}

type qualityC4Payload struct {
	Mermaid        string `json:"mermaid"`
	ComponentCount int    `json:"component_count"`
	EdgeCount      int    `json:"edge_count"`
}

func (s *Server) handleQualityC4(_ context.Context, _ *mcp.CallToolRequest, _ qualityC4Input) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_c4", "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult("quality_c4 requires a channel"), nil, nil
	}
	url := fmt.Sprintf("%s/api/channels/%s/quality/c4", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityC4Payload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	body := fmt.Sprintf("C4 component diagram (%d components, %d cross-component edges):\n```mermaid\n%s\n```",
		resp.ComponentCount, resp.EdgeCount, resp.Mermaid)
	return textResult(body), resp, nil
}
