package metrics

import (
	"math"
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// FileTile is the per-file deficit projection the panel renders as a
// treemap rectangle. Deficit is the worst-case value across all metrics
// (the colour the tile paints), MetricDeficits carries the full breakdown
// for the diagnostics popover, and TopReason names the worst metric so
// the panel can label tiles with a one-word "why".
type FileTile struct {
	Path           string             `json:"path"`
	LOC            int                `json:"loc"`
	Deficit        float64            `json:"deficit"`
	MetricDeficits map[string]float64 `json:"metric_deficits"`
	TopReason      string             `json:"top_reason"`
}

// TileCap bounds the per-snapshot tile slice. Far more than the panel
// can render usefully but enough that repos under the file cap stay
// fully attributable. Sort order is deficit-desc, LOC-desc, path-asc —
// the worst offenders never get clipped.
const TileCap = 500

// AttributeFiles produces the per-file tile slice from the graph plus
// the just-computed metric results. Empty / nil input returns nil; the
// callers (engine, snapshot store, API) all treat nil as "no tiles".
//
// The function reads each metric's Detail to identify the files that
// drag it down; for modularity (which has no per-file Detail today) it
// recomputes the cross-cluster ratio directly from the graph.
func AttributeFiles(g *graph.Graph, results []Result) []FileTile {
	if g == nil || len(g.Nodes) == 0 {
		return nil
	}
	mod := modularityFileDrag(g)
	cyc := cyclesFileDrag(findResult(results, CyclesName))
	dep := depthFileDrag(findResult(results, DepthName))
	eq := equalityFileDrag(g)
	red := redundancyFileDrag(g)

	out := make([]FileTile, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		deficits := map[string]float64{
			ModularityName: mod[n.Path],
			CyclesName:     cyc[n.Path],
			DepthName:      dep[n.Path],
			EqualityName:   eq[n.Path],
			RedundancyName: red[n.Path],
		}
		worst, reason := worstDeficit(deficits)
		out = append(out, FileTile{
			Path:           n.Path,
			LOC:            n.LOC,
			Deficit:        worst,
			MetricDeficits: deficits,
			TopReason:      reason,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Deficit != out[j].Deficit {
			return out[i].Deficit > out[j].Deficit
		}
		if out[i].LOC != out[j].LOC {
			return out[i].LOC > out[j].LOC
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > TileCap {
		out = out[:TileCap]
	}
	return out
}

// worstDeficit returns the highest-valued (metric, deficit) pair. Iteration
// order over Go maps is random; for ties we deterministically prefer the
// canonical metric order so the panel shows a stable TopReason.
func worstDeficit(d map[string]float64) (float64, string) {
	order := []string{ModularityName, CyclesName, DepthName, EqualityName, RedundancyName}
	worst := 0.0
	reason := ""
	for _, k := range order {
		if d[k] > worst {
			worst = d[k]
			reason = k
		}
	}
	return worst, reason
}

func findResult(results []Result, name string) *Result {
	for i := range results {
		if results[i].Name == name {
			return &results[i]
		}
	}
	return nil
}

// modularityFileDrag returns the per-file fraction of edges that cross
// the file's cluster boundary. A file with all-internal edges has drag 0;
// one whose every edge points outside its module has drag 1. Files with
// no edges are absent from the map (drag 0).
func modularityFileDrag(g *graph.Graph) map[string]float64 {
	moduleByNode := make([]int, len(g.Nodes))
	for mi, m := range g.Modules {
		for _, ni := range m.NodeIndices {
			moduleByNode[ni] = mi
		}
	}
	cross := make([]int, len(g.Nodes))
	total := make([]int, len(g.Nodes))
	for _, e := range g.Edges {
		total[e.FromIndex]++
		total[e.ToIndex]++
		if moduleByNode[e.FromIndex] != moduleByNode[e.ToIndex] {
			cross[e.FromIndex]++
			cross[e.ToIndex]++
		}
	}
	out := make(map[string]float64, len(g.Nodes))
	for i, n := range g.Nodes {
		if total[i] == 0 {
			continue
		}
		out[n.Path] = float64(cross[i]) / float64(total[i])
	}
	return out
}

// cyclesFileDrag flags any file that participates in a non-trivial SCC
// with full drag (1.0). The metric itself penalises the share of nodes
// in cycles, so per-file the drag is binary — a file is either in a
// cycle or it isn't.
func cyclesFileDrag(r *Result) map[string]float64 {
	out := make(map[string]float64)
	if r == nil {
		return out
	}
	d, ok := r.Detail.(CyclesDetail)
	if !ok {
		return out
	}
	for _, cycle := range d.Cycles {
		for _, p := range cycle {
			out[p] = 1.0
		}
	}
	return out
}

// depthFileDrag attributes deep-DAG drag to the files in the longest
// chain. Layer index L (1-based) drag = clamp01((L − target) / scale),
// matching the lakosScore curve at the per-layer level.
func depthFileDrag(r *Result) map[string]float64 {
	out := make(map[string]float64)
	if r == nil {
		return out
	}
	d, ok := r.Detail.(DepthDetail)
	if !ok {
		return out
	}
	for li, layer := range d.Layers {
		layerDepth := li + 1
		if layerDepth <= lakosTarget {
			continue
		}
		w := math.Min(1.0, float64(layerDepth-lakosTarget)/float64(lakosScale))
		for _, p := range layer {
			if w > out[p] {
				out[p] = w
			}
		}
	}
	return out
}

// equalityFileDrag attributes Gini drag to files larger than the mean
// LOC, scaled by their distance from the mean toward the largest file.
// A file at the mean has drag 0; the largest file has drag 1; files
// below the mean have drag 0 (they pull inequality down, not up).
func equalityFileDrag(g *graph.Graph) map[string]float64 {
	out := make(map[string]float64)
	total := 0
	maxLOC := 0
	for _, n := range g.Nodes {
		total += n.LOC
		if n.LOC > maxLOC {
			maxLOC = n.LOC
		}
	}
	if total == 0 {
		return out
	}
	mean := float64(total) / float64(len(g.Nodes))
	spread := float64(maxLOC) - mean
	if spread <= 0 {
		return out
	}
	for _, n := range g.Nodes {
		if float64(n.LOC) <= mean {
			continue
		}
		out[n.Path] = math.Min(1.0, (float64(n.LOC)-mean)/spread)
	}
	return out
}

// redundancyFileDrag computes the per-file dead-function ratio without
// the 20-entry cap the metric Detail applies. Drag is dead / total
// functions in the file, in [0, 1]. Files with no functions are absent.
func redundancyFileDrag(g *graph.Graph) map[string]float64 {
	out := make(map[string]float64)
	callSet := make(map[string]struct{})
	for _, n := range g.Nodes {
		for _, c := range n.Calls {
			callSet[c.Name] = struct{}{}
		}
	}
	for _, n := range g.Nodes {
		if len(n.Functions) == 0 {
			continue
		}
		dead := 0
		for _, f := range n.Functions {
			if isReachableByConvention(f.Name) {
				continue
			}
			if _, called := callSet[f.Name]; called {
				continue
			}
			dead++
		}
		if dead > 0 {
			out[n.Path] = float64(dead) / float64(len(n.Functions))
		}
	}
	return out
}
