package metrics

import (
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// detectCommunities runs the Louvain method on the graph's import edges
// (treated as undirected, unit-weighted) and returns community[i] — the
// community label for node i. Communities are renumbered 0..K-1 with the
// node-of-smallest-index in each community winning the lower id, so the
// output is deterministic regardless of map iteration order.
//
// Empty / no-edge graphs short-circuit to one community per node.
//
// The implementation is multi-level: we run local moves until no node
// improves Q, aggregate communities into super-nodes, and repeat — the
// canonical Blondel et al. (2008) formulation. Iteration order over
// nodes is ascending index; this is what makes the result reproducible.
func detectCommunities(g *graph.Graph) []int {
	n := len(g.Nodes)
	if n == 0 {
		return nil
	}
	if len(g.Edges) == 0 {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}

	// Original undirected adjacency. Each Edge contributes one entry to
	// each endpoint's neighbour list — the Q math is symmetric.
	// graph.Build already drops self-loops, so we don't re-check here.
	adj0 := make([][]neighbour, n)
	for _, e := range g.Edges {
		adj0[e.FromIndex] = append(adj0[e.FromIndex], neighbour{e.ToIndex, 1})
		adj0[e.ToIndex] = append(adj0[e.ToIndex], neighbour{e.FromIndex, 1})
	}

	// twoM = total edge weight × 2. Each Edge contributes 2 (one per
	// endpoint in adj0); since len(g.Edges) > 0 here, twoM > 0.
	// Aggregation across levels conserves total weight, so this stays
	// constant.
	twoM := float64(2 * len(g.Edges))

	// partition[i] = current community of original node i. Updated at
	// the end of every Louvain level so callers always see a partition
	// referenced to original node indices.
	partition := make([]int, n)
	for i := range partition {
		partition[i] = i
	}
	adj := adj0

	for {
		moved, levelComm := louvainLocalMoves(adj, twoM)
		if !moved {
			break
		}
		// Densify levelComm to 0..K-1 so partition (which keys into the
		// next-level adjacency) and aggregate() share an index space. The
		// raw output of louvainLocalMoves can be sparse (e.g. [3,3,3,7,7])
		// and projecting that onto partition would collide with adj[]
		// bounds on the next pass.
		dense, k := densify(levelComm)
		// Project the level's community labels back onto the original
		// nodes: each original node was in level-node `partition[i]`
		// before this pass; that level-node now sits in community
		// `dense[partition[i]]`.
		for i := range partition {
			partition[i] = dense[partition[i]]
		}
		adj = aggregateDense(adj, dense, k)
		if k <= 1 {
			break
		}
	}
	return renumber(partition)
}

// densify rekeys an arbitrary community array (values may be sparse,
// e.g. [3, 3, 7, 7, 7]) to the dense 0..K-1 range. Sort order is
// ascending by original id so the result is deterministic, which the
// snapshot-test stability contract relies on. Returns (dense, K).
func densify(comm []int) ([]int, int) {
	idx := make(map[int]int)
	for _, c := range uniqueSorted(comm) {
		idx[c] = len(idx)
	}
	out := make([]int, len(comm))
	for i, c := range comm {
		out[i] = idx[c]
	}
	return out, len(idx)
}

// neighbour is one weighted adjacency entry. Weights are float64 because
// aggregation across levels can produce non-integer totals if the input
// ever uses fractional weights — keeping the type uniform avoids a level
// boundary that surprises readers.
type neighbour struct {
	to     int
	weight float64
}

// louvainLocalMoves runs phase 1 of Louvain on adj: repeatedly walks the
// node list in ascending order, moving each node to whichever neighbour
// community yields the largest positive ΔQ. Returns (anyMoveHappened,
// finalCommunityPerNode). Halts when a full sweep produces no moves —
// the canonical convergence criterion.
func louvainLocalMoves(adj [][]neighbour, twoM float64) (bool, []int) {
	n := len(adj)
	community := make([]int, n)
	for i := range community {
		community[i] = i
	}

	// Per-community totals: kIn = 2× internal edge weight, kTot = sum
	// of degrees of nodes in community. Initialised for the trivial
	// "each node alone" partition; updated incrementally as nodes move.
	kTot := make([]float64, n)
	for i, ns := range adj {
		for _, nb := range ns {
			kTot[i] += nb.weight
		}
	}

	anyMove := false
	for {
		passMoved := false
		for i := range n {
			ki := kTot[i] // pre-removal degree of node i
			// Edge-weight sum from i into each neighbour community.
			// Counted with i still in its current community; we'll
			// subtract i's self-contribution to its own community when
			// computing the "remove i" baseline.
			toComm := make(map[int]float64)
			selfLoop := 0.0
			for _, nb := range adj[i] {
				if nb.to == i {
					selfLoop += nb.weight
					continue
				}
				toComm[community[nb.to]] += nb.weight
			}

			from := community[i]
			// Remove i from its community: kTot[from] drops by ki.
			kTot[from] -= ki

			// Evaluate ΔQ for each candidate community (including the
			// original — staying put is one of the options). The
			// formula (Blondel 2008, eq. 2) for moving an isolated
			// node i into community C, ignoring constants common to
			// every candidate, is:
			//
			//   ΔQ ∝ k_i,C - kTot[C] * ki / twoM
			//
			// We pick the C maximising this, breaking ties by lower
			// community id so the result is deterministic.
			bestComm := from
			bestGain := 0.0 // the "stay isolated" baseline
			candidates := sortedKeys(toComm)
			for _, c := range candidates {
				kic := toComm[c]
				gain := kic - kTot[c]*ki/twoM
				if gain > bestGain || (gain == bestGain && c < bestComm) {
					bestGain = gain
					bestComm = c
				}
			}

			// Add i back into bestComm.
			kTot[bestComm] += ki
			if bestComm != from {
				community[i] = bestComm
				passMoved = true
				anyMove = true
			}
			_ = selfLoop // self-loops don't change ΔQ ordering; kept for clarity
		}
		if !passMoved {
			break
		}
	}
	return anyMove, community
}

// aggregateDense builds the next-level adjacency: each community
// becomes a single super-node, edges between communities aggregate by
// summing weights, and intra-community edges become self-loops on the
// super-node. Caller supplies a dense (0..K-1) community labelling so
// the result indexes consistently with downstream partition projection.
func aggregateDense(adj [][]neighbour, denseComm []int, k int) [][]neighbour {
	out := make([][]neighbour, k)
	tmp := make([]map[int]float64, k)
	for i := range tmp {
		tmp[i] = make(map[int]float64)
	}
	for from, ns := range adj {
		ci := denseComm[from]
		for _, nb := range ns {
			cj := denseComm[nb.to]
			tmp[ci][cj] += nb.weight
		}
	}
	for i, m := range tmp {
		keys := sortedKeys(m)
		for _, j := range keys {
			out[i] = append(out[i], neighbour{j, m[j]})
		}
	}
	return out
}

// renumber relabels community ids to dense 0..K-1, with the smallest
// node-id in each community winning the lower label. Without this step,
// ids would carry over from internal Louvain bookkeeping and depend on
// graph size, breaking snapshot-test stability.
func renumber(partition []int) []int {
	n := len(partition)
	out := make([]int, n)
	seen := make(map[int]int)
	next := 0
	for i := range n {
		c := partition[i]
		if id, ok := seen[c]; ok {
			out[i] = id
			continue
		}
		seen[c] = next
		out[i] = next
		next++
	}
	return out
}

func sortedKeys(m map[int]float64) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func uniqueSorted(xs []int) []int {
	seen := make(map[int]struct{}, len(xs))
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	sort.Ints(out)
	return out
}
