package metrics

import (
	"github.com/radutopala/loop/internal/quality/graph"
)

// ModularityName is the canonical key used in MCP responses, the panel
// metric cards, and the rules engine. Stable across releases.
const ModularityName = "modularity"

// Modularity computes Newman's Q for the given graph against the
// pre-computed module clustering. Edges are treated as undirected for
// the Q calculation — modularity is a clustering metric, and the
// undirected form is the standard. Self-loops (already dropped at
// build time) wouldn't contribute either way.
//
// Range: Q sits in [-0.5, 1.0]. Real-world structured codebases land
// between 0.3 and 0.7. We normalise to [0, 1] by clamping below 0 and
// scaling 0..1 → 0..1 (Q above 1 is impossible in practice; we clamp
// for safety).
//
// Formula (undirected, weighted):
//
//	Q = (1 / 2m) Σ_{i,j} [ A_ij - (k_i k_j) / (2m) ] δ(c_i, c_j)
//
// where m is total edges, A is the adjacency matrix, k_i is node i's
// degree, and δ is 1 when nodes i and j are in the same module.
//
// Empty graphs and graphs with no edges return Q = 0 (perfectly modular
// in the trivial sense — there are no edges to violate boundaries).
func Modularity(g *graph.Graph) Result {
	if g == nil || len(g.Nodes) == 0 || len(g.Edges) == 0 {
		return Result{Name: ModularityName, Raw: 0, Score: 1.0}
	}

	moduleByNode := make([]int, len(g.Nodes))
	for mi, m := range g.Modules {
		for _, ni := range m.NodeIndices {
			moduleByNode[ni] = mi
		}
	}

	// Closed-form decomposition (Newman 2004):
	//
	//	Q = Σ_c [ (L_c / m) - (D_c / 2m)^2 ]
	//
	// where L_c = edges fully inside cluster c and D_c = sum of node
	// degrees in c. Equivalent to the per-pair definition but runs in
	// O(modules + edges) without needing an n^2 pair sweep.
	degree := make([]float64, len(g.Nodes))
	for _, e := range g.Edges {
		degree[e.FromIndex]++
		degree[e.ToIndex]++
	}
	twoM := float64(2 * len(g.Edges))
	clusterEdges := make([]int, len(g.Modules))
	clusterDegree := make([]float64, len(g.Modules))
	for i, n := range moduleByNode {
		clusterDegree[n] += degree[i]
	}
	for _, e := range g.Edges {
		if moduleByNode[e.FromIndex] == moduleByNode[e.ToIndex] {
			clusterEdges[moduleByNode[e.FromIndex]]++
		}
	}
	m := float64(len(g.Edges))
	var q float64
	for ci := range g.Modules {
		lc := float64(clusterEdges[ci]) / m
		dc := clusterDegree[ci] / twoM
		q += lc - dc*dc
	}

	return Result{
		Name:  ModularityName,
		Raw:   q,
		Score: clamp01(q),
	}
}

// clamp01 squashes a value into the [0, 1] range — used to bound the
// per-metric Score before the aggregator takes the geometric mean.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
