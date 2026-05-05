package metrics

import (
	"github.com/radutopala/loop/internal/quality/graph"
)

// ModularityName is the canonical key used in MCP responses, the panel
// metric cards, and the rules engine. Stable across releases.
const ModularityName = "modularity"

// ModularityDetail carries the per-snapshot Leiden partition so the
// diagnostics layer (tile drag, what-if simulation) can reuse exactly
// the same clustering the headline Q was computed on. Communities is
// 1:1 with g.Nodes, indexed identically; NumCommunities is derived but
// surfaced for the panel's "K modules detected" badge.
type ModularityDetail struct {
	Communities    []int `json:"communities"`
	NumCommunities int   `json:"num_communities"`
}

// Modularity computes Newman's Q for the given graph against communities
// the Leiden algorithm discovers from the import structure itself —
// rather than against the directory layout. The directory-based
// clustering treats e.g. all of internal/* as one giant cluster, which
// gives Q ≈ 0 for any single-language Go project regardless of how
// well-organised it is internally; Leiden finds the import-coupled
// sub-modules and Q reflects their actual cohesion.
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
// degree, and δ is 1 when nodes i and j are in the same community.
//
// Empty graphs and graphs with no edges return Q = 0 (perfectly modular
// in the trivial sense — there are no edges to violate boundaries).
func Modularity(g *graph.Graph) Result {
	if g == nil || len(g.Nodes) == 0 || len(g.Edges) == 0 {
		return Result{Name: ModularityName, Raw: 0, Score: 1.0, Detail: ModularityDetail{}}
	}

	communities := detectCommunities(g)
	q := modularityQ(g, communities)
	numCommunities := 0
	for _, c := range communities {
		if c+1 > numCommunities {
			numCommunities = c + 1
		}
	}

	return Result{
		Name:  ModularityName,
		Raw:   q,
		Score: clamp01(q),
		Detail: ModularityDetail{
			Communities:    communities,
			NumCommunities: numCommunities,
		},
	}
}

// modularityQ evaluates Newman's Q for the supplied node→community
// labelling using the closed-form decomposition (Newman 2004):
//
//	Q = Σ_c [ (L_c / m) - (D_c / 2m)^2 ]
//
// where L_c = edges fully inside cluster c and D_c = sum of node
// degrees in c. Equivalent to the per-pair definition but runs in
// O(communities + edges) without an n^2 pair sweep.
func modularityQ(g *graph.Graph, community []int) float64 {
	degree := make([]float64, len(g.Nodes))
	for _, e := range g.Edges {
		degree[e.FromIndex]++
		degree[e.ToIndex]++
	}
	twoM := float64(2 * len(g.Edges))
	clusterEdges := make(map[int]int)
	clusterDegree := make(map[int]float64)
	for i, c := range community {
		clusterDegree[c] += degree[i]
	}
	for _, e := range g.Edges {
		if community[e.FromIndex] == community[e.ToIndex] {
			clusterEdges[community[e.FromIndex]]++
		}
	}
	m := float64(len(g.Edges))
	var q float64
	for c, lcCount := range clusterEdges {
		lc := float64(lcCount) / m
		dc := clusterDegree[c] / twoM
		q += lc - dc*dc
	}
	// Clusters with no internal edges still subtract their degree term.
	for c, dcVal := range clusterDegree {
		if _, ok := clusterEdges[c]; ok {
			continue
		}
		dc := dcVal / twoM
		q -= dc * dc
	}
	return q
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
