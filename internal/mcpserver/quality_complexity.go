// Package mcpserver: quality_complexity.go exposes the per-function
// complexity hotspots and clone clusters surfaced by the metrics
// engine to MCP agents. Both tools paginate their hotspot list via
// limit + offset so an agent can ask for "the next batch of offenders"
// without re-fetching the whole graph.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type qualityComplexityInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type qualityComplexityFunctionPayload struct {
	Path       string  `json:"path"`
	Name       string  `json:"name"`
	StartLine  int     `json:"start_line"`
	Cyclomatic int     `json:"cyclomatic"`
	Cognitive  int     `json:"cognitive"`
	MaxNesting int     `json:"max_nesting"`
	ParamCount int     `json:"param_count"`
	LOC        int     `json:"loc"`
	Score      float64 `json:"score"`
}

type qualityComplexityPayload struct {
	Score          float64                            `json:"score"`
	Raw            float64                            `json:"raw"`
	TotalFunctions int                                `json:"total_functions"`
	OverThreshold  int                                `json:"over_threshold"`
	Histogram      map[string]map[string]int          `json:"histogram"`
	Functions      []qualityComplexityFunctionPayload `json:"functions"`
	Offset         int                                `json:"offset"`
	Limit          int                                `json:"limit"`
	Returned       int                                `json:"returned"`
}

// callPagedQualityTool wraps the channel check + endpoint build + API
// call common to every paged quality tool (complexity, clones). The
// formatter shapes the typed payload into agent-readable text.
func callPagedQualityTool[T any](s *Server, toolName, route string, limit, offset int, formatter func(*T) string) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", toolName, "channel_id", s.channelID)
	if s.channelID == "" {
		return errorResult(toolName + " requires a channel"), nil, nil
	}
	endpoint := fmt.Sprintf("%s/api/channels/%s/quality/%s", s.apiURL, s.channelID, route)
	if q := buildPagingQuery(limit, offset); q != "" {
		endpoint += "?" + q
	}
	resp, errResult, err := doAPICall[T](s, "GET", endpoint, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	return textResult(formatter(resp)), resp, nil
}

func (s *Server) handleQualityComplexity(_ context.Context, _ *mcp.CallToolRequest, input qualityComplexityInput) (*mcp.CallToolResult, any, error) {
	return callPagedQualityTool(s, "quality_complexity", "complexity", input.Limit, input.Offset, formatQualityComplexity)
}

func formatQualityComplexity(p *qualityComplexityPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Complexity score: %.3f (%d/%d functions over threshold)\n",
		p.Score, p.OverThreshold, p.TotalFunctions)
	if len(p.Functions) == 0 {
		b.WriteString("No hotspots in this page.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Showing %d hotspots (offset %d, limit %d):\n", p.Returned, p.Offset, p.Limit)
	for i, f := range p.Functions {
		fmt.Fprintf(&b, "  %d. %s:%d %s — score %.3f (cyc %d, cog %d, nest %d, params %d, LOC %d)\n",
			i+1+p.Offset, f.Path, f.StartLine, f.Name, f.Score,
			f.Cyclomatic, f.Cognitive, f.MaxNesting, f.ParamCount, f.LOC)
	}
	return b.String()
}

type qualityClonesInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

type qualityCloneMemberPayload struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	LOC       int    `json:"loc"`
}

type qualityCloneClusterPayload struct {
	Members     []qualityCloneMemberPayload `json:"members"`
	LOC         int                         `json:"loc"`
	MaxDistance int                         `json:"max_distance"`
}

type qualityClonesPayload struct {
	Score         float64                      `json:"score"`
	Raw           float64                      `json:"raw"`
	DuplicatedLOC int                          `json:"duplicated_loc"`
	TotalLOC      int                          `json:"total_loc"`
	ClusterCount  int                          `json:"cluster_count"`
	Clusters      []qualityCloneClusterPayload `json:"clusters"`
	Offset        int                          `json:"offset"`
	Limit         int                          `json:"limit"`
	Returned      int                          `json:"returned"`
}

func (s *Server) handleQualityClones(_ context.Context, _ *mcp.CallToolRequest, input qualityClonesInput) (*mcp.CallToolResult, any, error) {
	return callPagedQualityTool(s, "quality_clones", "clones", input.Limit, input.Offset, formatQualityClones)
}

func formatQualityClones(p *qualityClonesPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Clones score: %.3f (duplicated %d / total %d LOC across %d clusters)\n",
		p.Score, p.DuplicatedLOC, p.TotalLOC, p.ClusterCount)
	if len(p.Clusters) == 0 {
		b.WriteString("No clusters in this page.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Showing %d clusters (offset %d, limit %d):\n", p.Returned, p.Offset, p.Limit)
	for i, cl := range p.Clusters {
		fmt.Fprintf(&b, "  %d. %d members, %d LOC, max-distance %d\n",
			i+1+p.Offset, len(cl.Members), cl.LOC, cl.MaxDistance)
		for _, m := range cl.Members {
			fmt.Fprintf(&b, "      • %s:%d %s (LOC %d)\n", m.Path, m.StartLine, m.Name, m.LOC)
		}
	}
	return b.String()
}

// buildPagingQuery returns a URL-encoded query string carrying the
// optional limit / offset parameters. Zero values are omitted so the
// HTTP handler applies its own defaults.
func buildPagingQuery(limit, offset int) string {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return q.Encode()
}
