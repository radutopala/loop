package metrics

import (
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// EqualityName is the canonical key for the file-size-equality metric.
const EqualityName = "equality"

// EqualityDetail is the panel-facing payload: the Gini coefficient
// over file LOC plus the largest files (god-file candidates the
// diagnostics surface flags for refactor).
type EqualityDetail struct {
	// Gini is the raw coefficient — 0 means every file has the same
	// LOC, 1 means one file holds everything.
	Gini float64

	// Hotspots lists up to equalityHotspotCap largest files, sorted
	// by LOC desc, lex tiebreak. Each entry includes the file's share
	// of total LOC for the panel to render proportionally.
	Hotspots []EqualityHotspot

	// TotalLOC and FileCount are reported alongside so the panel can
	// render absolute numbers without re-iterating the graph.
	TotalLOC  int
	FileCount int
}

// EqualityHotspot is one entry in the diagnostics list for the
// equality metric.
type EqualityHotspot struct {
	Path  string
	LOC   int
	Share float64
}

// equalityHotspotCap bounds the panel's diagnostics list. Ten lines
// is enough to surface god files; deeper ranking is what the treemap
// is for.
const equalityHotspotCap = 10

// Equality scores how evenly LOC is distributed across files. The
// inverse of the Gini coefficient: 1.0 means every file is the same
// size, 0.0 means a single file dominates.
//
// Formula (sorted, equivalent to the Σ|x_i - x_j| double sum):
//
//	G = (Σ_i (2i - n - 1) · x_i) / (n · Σ_i x_i)   for sorted x_1 ≤ ... ≤ x_n
//
// Empty graphs and graphs whose total LOC is zero return Gini 0 and
// Score 1.0 — there's no inequality to measure.
func Equality(g *graph.Graph) Result {
	if g == nil || len(g.Nodes) == 0 {
		return Result{Name: EqualityName, Raw: 0, Score: 1.0, Detail: EqualityDetail{}}
	}

	locs := make([]int, len(g.Nodes))
	totalLOC := 0
	for i, n := range g.Nodes {
		locs[i] = n.LOC
		totalLOC += n.LOC
	}
	if totalLOC == 0 {
		return Result{
			Name:   EqualityName,
			Raw:    0,
			Score:  1.0,
			Detail: EqualityDetail{FileCount: len(g.Nodes)},
		}
	}

	sort.Ints(locs)
	var weightedSum float64
	n := len(locs)
	for i, x := range locs {
		// Sorted form: (2*(i+1) - n - 1) · x_i ; index in formula is 1-based.
		weightedSum += float64(2*(i+1)-n-1) * float64(x)
	}
	gini := weightedSum / (float64(n) * float64(totalLOC))

	hotspots := buildEqualityHotspots(g, totalLOC)

	return Result{
		Name:  EqualityName,
		Raw:   gini,
		Score: clamp01(1.0 - gini),
		Detail: EqualityDetail{
			Gini:      gini,
			Hotspots:  hotspots,
			TotalLOC:  totalLOC,
			FileCount: n,
		},
	}
}

func buildEqualityHotspots(g *graph.Graph, totalLOC int) []EqualityHotspot {
	picked := make([]EqualityHotspot, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.LOC == 0 {
			continue
		}
		picked = append(picked, EqualityHotspot{
			Path:  n.Path,
			LOC:   n.LOC,
			Share: float64(n.LOC) / float64(totalLOC),
		})
	}
	sort.Slice(picked, func(i, j int) bool {
		if picked[i].LOC != picked[j].LOC {
			return picked[i].LOC > picked[j].LOC
		}
		return picked[i].Path < picked[j].Path
	})
	if len(picked) > equalityHotspotCap {
		picked = picked[:equalityHotspotCap]
	}
	return picked
}
