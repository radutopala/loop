package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// quality tool inputs are intentionally empty: the server reads channelID
// from its struct field (per-channel pattern, no WorkDir arg).
type qualityScanInput struct{}
type qualitySnapshotInput struct{}

// qualityScanResponse mirrors api.scanResponse — status hint only; the
// real payload arrives via the quality.scanned event.
type qualityScanResponse struct {
	Status string `json:"status"`
}

// qualitySnapshotPayload mirrors api.QualitySnapshotResponse to keep the
// MCP wire shape stable as the API evolves.
type qualitySnapshotPayload struct {
	DirPath        string                  `json:"dir_path"`
	Branch         string                  `json:"branch"`
	CurrentBranch  string                  `json:"current_branch"`
	BranchMismatch bool                    `json:"branch_mismatch"`
	Signal         int                     `json:"signal"`
	GeoMean        float64                 `json:"geo_mean"`
	ScannedAt      string                  `json:"scanned_at"`
	Metrics        []qualityMetricsPayload `json:"metrics"`
}

type qualityMetricsPayload struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Raw   float64 `json:"raw"`
}

func (s *Server) handleQualityScan(_ context.Context, _ *mcp.CallToolRequest, _ qualityScanInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_scan", "channel_id", s.channelID)

	if s.channelID == "" {
		return errorResult("quality_scan requires a channel"), nil, nil
	}

	url := fmt.Sprintf("%s/api/channels/%s/quality/scan", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualityScanResponse](s, "POST", url, http.StatusAccepted, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	if resp.Status == "in_progress" {
		return textResult("Scan already in progress for this channel; quality.scanned event will fire when it completes."), nil, nil
	}
	return textResult("Scan started; quality.scanned event will fire with the full payload when complete."), nil, nil
}

func (s *Server) handleQualitySnapshot(_ context.Context, _ *mcp.CallToolRequest, _ qualitySnapshotInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "quality_snapshot", "channel_id", s.channelID)

	if s.channelID == "" {
		return errorResult("quality_snapshot requires a channel"), nil, nil
	}

	url := fmt.Sprintf("%s/api/channels/%s/quality/snapshot", s.apiURL, s.channelID)
	resp, errResult, err := doAPICall[qualitySnapshotPayload](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return textResult(formatQualitySnapshot(resp)), nil, nil
}

// formatQualitySnapshot renders the snapshot payload as a compact
// human-readable text block. The agent reads this directly; the panel
// uses the structured event payload instead.
func formatQualitySnapshot(p *qualitySnapshotPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Quality signal: %d (geo-mean %.3f) on branch %q\n", p.Signal, p.GeoMean, p.Branch)
	if p.BranchMismatch {
		fmt.Fprintf(&b, "  ⚠️ Snapshot is from %q; current branch is %q. Run quality_scan to refresh.\n", p.Branch, p.CurrentBranch)
	}
	if p.ScannedAt != "" {
		fmt.Fprintf(&b, "Scanned at: %s\n", p.ScannedAt)
	}
	if len(p.Metrics) > 0 {
		b.WriteString("Metrics:\n")
		for _, m := range p.Metrics {
			fmt.Fprintf(&b, "  - %s: score=%.3f raw=%.3f\n", m.Name, m.Score, m.Raw)
		}
	}
	return b.String()
}
