package metrics

import (
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// DepthName is the canonical key for the Lakos depth metric.
const DepthName = "depth"

// DepthDetail is the panel-facing payload: the longest layered chain
// through the (cycle-collapsed) DAG. Each layer is one strongly connected
// component, expanded into its sorted file members so a treemap or list
// can render them.
type DepthDetail struct {
	// Layers is the longest chain, in topological order (root first,
	// leaf last). Each entry is one SCC's sorted file paths.
	Layers [][]string

	// MaxDepth is len(Layers).
	MaxDepth int

	// SCCCollapsed is true if any non-trivial SCC was collapsed in the
	// chain. Tells the panel to mark layers that represent cycles.
	SCCCollapsed bool
}

// Tunables for the score curve. Score is 1.0 up to lakosTarget; decays
// linearly to 0 at lakosTarget + lakosScale. Defaults align with the
// sentrux conventions: 6 layers is healthy, 30 is at-the-floor unhealthy.
const (
	lakosTarget = 6
	lakosScale  = 24
)

// Depth computes the Lakos depth — the longest chain of layered
// dependencies in the import graph. Strongly connected components
// collapse into a single layer (cyclic deps cannot be layered apart).
//
// Score is 1.0 for any DAG within `lakosTarget` layers, decaying
// linearly to 0 once the chain reaches `lakosTarget + lakosScale`.
//
// Empty / nil graphs return depth 0 and Score 1.0.
func Depth(g *graph.Graph) Result {
	if g == nil || len(g.Nodes) == 0 {
		return Result{Name: DepthName, Raw: 0, Score: 1.0, Detail: DepthDetail{}}
	}

	adj := buildAdjacency(g)
	sccs := tarjanSCC(len(g.Nodes), adj)

	sccOf := make([]int, len(g.Nodes))
	for i, comp := range sccs {
		for _, v := range comp {
			sccOf[v] = i
		}
	}

	// Build the condensation: one super-node per SCC, edges deduped,
	// self-loops dropped.
	condAdj := make([][]int, len(sccs))
	seen := make(map[[2]int]struct{})
	for _, e := range g.Edges {
		a, b := sccOf[e.FromIndex], sccOf[e.ToIndex]
		if a == b {
			continue
		}
		k := [2]int{a, b}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		condAdj[a] = append(condAdj[a], b)
	}

	// Tarjan emits components in reverse topological order. Reverse
	// to process sources before their dependents.
	depth := make([]int, len(sccs))
	pred := make([]int, len(sccs))
	for i := range depth {
		depth[i] = 1
		pred[i] = -1
	}
	for i := len(sccs) - 1; i >= 0; i-- {
		u := i
		for _, v := range condAdj[u] {
			if depth[u]+1 > depth[v] {
				depth[v] = depth[u] + 1
				pred[v] = u
			}
		}
	}

	maxIdx := 0
	for i := range depth {
		if depth[i] > depth[maxIdx] {
			maxIdx = i
		}
	}

	var chain []int
	for i := maxIdx; i != -1; i = pred[i] {
		chain = append([]int{i}, chain...)
	}

	layers := make([][]string, len(chain))
	collapsed := false
	for li, si := range chain {
		members := make([]string, len(sccs[si]))
		for j, ni := range sccs[si] {
			members[j] = g.Nodes[ni].Path
		}
		sort.Strings(members)
		layers[li] = members
		if len(members) > 1 {
			collapsed = true
		}
	}

	maxDepth := depth[maxIdx]
	score := lakosScore(maxDepth)

	return Result{
		Name:  DepthName,
		Raw:   float64(maxDepth),
		Score: score,
		Detail: DepthDetail{
			Layers:       layers,
			MaxDepth:     maxDepth,
			SCCCollapsed: collapsed,
		},
	}
}

// lakosScore maps a layer count to a [0, 1] health score using the
// piecewise-linear curve described on the package constants.
func lakosScore(depth int) float64 {
	if depth <= lakosTarget {
		return 1.0
	}
	if depth >= lakosTarget+lakosScale {
		return 0.0
	}
	return 1.0 - float64(depth-lakosTarget)/float64(lakosScale)
}
