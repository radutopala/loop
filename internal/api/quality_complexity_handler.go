// Package api: quality_complexity_handler.go exposes the per-function
// complexity hotspots and clone clusters surfaced by the metrics package
// to HTTP consumers (panel + MCP). Both endpoints recompute the metric
// from the cached graph using the same project-level Complexity / Clones
// thresholds the engine used during scan, so the numbers stay consistent
// across the whatif / rules / panel surfaces.

package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
)

// QualityComplexityFunction is the wire shape for one function in the
// complexity hotspots list. Mirrors metrics.FuncComplexity with explicit
// JSON tags so the panel can read snake_case fields.
type QualityComplexityFunction struct {
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

// QualityComplexityResponse is the full GET /quality/complexity payload.
// Score / Raw mirror the metric-level numbers; the per-function list is
// paged via offset/limit so the panel can lazy-load. Histogram is the
// per-dimension ok/warn/crit breakdown.
type QualityComplexityResponse struct {
	Score          float64                     `json:"score"`
	Raw            float64                     `json:"raw"`
	TotalFunctions int                         `json:"total_functions"`
	OverThreshold  int                         `json:"over_threshold"`
	Histogram      map[string]map[string]int   `json:"histogram"`
	Functions      []QualityComplexityFunction `json:"functions"`
	Offset         int                         `json:"offset"`
	Limit          int                         `json:"limit"`
	Returned       int                         `json:"returned"`
}

// QualityCloneMember mirrors metrics.CloneMember with JSON tags.
type QualityCloneMember struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	LOC       int    `json:"loc"`
}

// QualityCloneCluster mirrors metrics.CloneCluster with JSON tags.
type QualityCloneCluster struct {
	Members     []QualityCloneMember `json:"members"`
	LOC         int                  `json:"loc"`
	MaxDistance int                  `json:"max_distance"`
}

// QualityClonesResponse is the full GET /quality/clones payload. As with
// complexity, the cluster list is paged so the panel can reveal more on
// demand.
type QualityClonesResponse struct {
	Score         float64               `json:"score"`
	Raw           float64               `json:"raw"`
	DuplicatedLOC int                   `json:"duplicated_loc"`
	TotalLOC      int                   `json:"total_loc"`
	ClusterCount  int                   `json:"cluster_count"`
	Clusters      []QualityCloneCluster `json:"clusters"`
	Offset        int                   `json:"offset"`
	Limit         int                   `json:"limit"`
	Returned      int                   `json:"returned"`
}

const (
	complexityDefaultLimit = 50
	complexityMaxLimit     = 100

	clonesDefaultLimit = 25
	clonesMaxLimit     = 50
)

// pagedQualityRequest carries the inputs every paged-quality handler
// needs: the cached graph, the resolved metrics config, and the
// validated paging window. preparePagedQualityRequest writes the HTTP
// response itself on any error and returns ok=false so callers can bail
// out cleanly.
type pagedQualityRequest struct {
	graph  *graph.Graph
	cfg    metrics.Config
	limit  int
	offset int
}

func (s *Server) preparePagedQualityRequest(w http.ResponseWriter, r *http.Request, defaultLimit, maxLimit int) (pagedQualityRequest, bool) {
	channelID := r.PathValue("id")
	g, ok := s.lookupCachedGraph(w, channelID)
	if !ok {
		return pagedQualityRequest{}, false
	}
	limit, ok := parseQueryInt(w, r, "limit", defaultLimit, maxLimit)
	if !ok {
		return pagedQualityRequest{}, false
	}
	offset, ok := parseQueryNonNegInt(w, r, "offset")
	if !ok {
		return pagedQualityRequest{}, false
	}
	return pagedQualityRequest{
		graph:  g,
		cfg:    s.resolveMetricsConfigForChannel(r.Context(), channelID),
		limit:  limit,
		offset: offset,
	}, true
}

func (s *Server) handleQualityComplexity(w http.ResponseWriter, r *http.Request) { //nolint:dupl
	req, ok := s.preparePagedQualityRequest(w, r, complexityDefaultLimit, complexityMaxLimit)
	if !ok {
		return
	}
	res := metrics.ComputeComplexity(req.graph, req.cfg.Complexity)
	detail, _ := res.Detail.(metrics.ComplexityDetail)

	page := pageFunctions(detail.Functions, req.offset, req.limit)
	writeHTTPJSON(w, http.StatusOK, QualityComplexityResponse{
		Score:          res.Score,
		Raw:            res.Raw,
		TotalFunctions: detail.TotalFunctions,
		OverThreshold:  detail.OverThreshold,
		Histogram:      detail.Histogram,
		Functions:      page,
		Offset:         req.offset,
		Limit:          req.limit,
		Returned:       len(page),
	}, s.logger)
}

func (s *Server) handleQualityClones(w http.ResponseWriter, r *http.Request) { //nolint:dupl
	req, ok := s.preparePagedQualityRequest(w, r, clonesDefaultLimit, clonesMaxLimit)
	if !ok {
		return
	}
	res := metrics.ComputeClones(req.graph, req.cfg.Clones)
	detail, _ := res.Detail.(metrics.ClonesDetail)

	page := pageClusters(detail.Clusters, req.offset, req.limit)
	writeHTTPJSON(w, http.StatusOK, QualityClonesResponse{
		Score:         res.Score,
		Raw:           res.Raw,
		DuplicatedLOC: detail.DuplicatedLOC,
		TotalLOC:      detail.TotalLOC,
		ClusterCount:  detail.ClusterCount,
		Clusters:      page,
		Offset:        req.offset,
		Limit:         req.limit,
		Returned:      len(page),
	}, s.logger)
}

// resolveMetricsConfigForChannel returns the effective metrics.Config
// for the channel's recompute path. Skips the channel-store lookup when
// no loader is configured — handlers that don't need overrides shouldn't
// pay for a GetChannel call (and tests shouldn't need to register the
// mock).
func (s *Server) resolveMetricsConfigForChannel(ctx context.Context, channelID string) metrics.Config {
	if s.qualityMetricsCfg == nil {
		return metrics.DefaultConfig()
	}
	var dir, parent string
	if s.store != nil {
		if d, err := s.resolveDirPath(ctx, "", channelID); err == nil {
			dir = d
			parent = s.resolveParentDirPath(ctx, channelID)
		}
	}
	return s.resolveMetricsConfig(dir, parent)
}

// pageFunctions slices the (already-sorted) hotspot list by offset/limit
// and projects to the wire shape.
func pageFunctions(in []metrics.FuncComplexity, offset, limit int) []QualityComplexityFunction {
	if offset >= len(in) {
		return []QualityComplexityFunction{}
	}
	end := min(offset+limit, len(in))
	out := make([]QualityComplexityFunction, 0, end-offset)
	for _, f := range in[offset:end] {
		out = append(out, QualityComplexityFunction{
			Path:       f.Path,
			Name:       f.Name,
			StartLine:  f.StartLine,
			Cyclomatic: f.Cyclomatic,
			Cognitive:  f.Cognitive,
			MaxNesting: f.MaxNesting,
			ParamCount: f.ParamCount,
			LOC:        f.LOC,
			Score:      f.Score,
		})
	}
	return out
}

// pageClusters slices the cluster list by offset/limit and projects to
// the wire shape.
func pageClusters(in []metrics.CloneCluster, offset, limit int) []QualityCloneCluster {
	if offset >= len(in) {
		return []QualityCloneCluster{}
	}
	end := min(offset+limit, len(in))
	out := make([]QualityCloneCluster, 0, end-offset)
	for _, cl := range in[offset:end] {
		members := make([]QualityCloneMember, 0, len(cl.Members))
		for _, m := range cl.Members {
			members = append(members, QualityCloneMember{
				Path:      m.Path,
				Name:      m.Name,
				StartLine: m.StartLine,
				EndLine:   m.EndLine,
				LOC:       m.LOC,
			})
		}
		out = append(out, QualityCloneCluster{
			Members:     members,
			LOC:         cl.LOC,
			MaxDistance: cl.MaxDistance,
		})
	}
	return out
}

// parseQueryNonNegInt parses an optional non-negative int query
// parameter (default 0). On parse error or a negative value it writes
// 400 and returns (0, false). Used by the complexity / clones offset
// argument where 0 is meaningful ("first page").
func parseQueryNonNegInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return 0, false
	}
	return v, true
}
