package metrics

import (
	"slices"
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// detectCommunities runs the Leiden algorithm (Traag, Waltman, van Eck
// 2019) on the graph's import edges (treated as undirected, unit-
// weighted) and returns community[i] — the community label for node i.
// Communities are renumbered 0..K-1 with the node-of-smallest-index in
// each community winning the lower id, so output is deterministic
// regardless of map iteration order.
//
// Empty / no-edge graphs short-circuit to one community per node.
//
// Leiden is a strict improvement over Louvain on three fronts:
//
//  1. Phase 1 (FastLocalMove) re-evaluates only nodes whose neighbours
//     have moved instead of doing repeated full sweeps — converges
//     faster and tighter on large graphs.
//  2. Phase 2 (refinement) re-partitions each phase-1 community into
//     well-connected sub-communities, starting from singletons and
//     allowing only moves where the moving node and the destination
//     sub-community both meet a connectivity threshold against the
//     parent community. This eliminates Louvain's well-known disconnected
//     -community pathology.
//  3. Aggregation seeds the next level's partition from the phase-1
//     community labels, not the refined labels — letting subsequent
//     phase-1 passes split apart clusters Louvain would have been stuck
//     with as monoliths.
//
// Determinism: ascending node-index order for queue initialisation and
// candidate enumeration, lower-community-id tie-break, sort.Ints over
// every map iteration. With these in place the algorithm is reproducible
// across runs and OS map-iteration orderings — required by the snapshot
// test stability contract.
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
	// each endpoint's neighbour list — modularity is symmetric so we walk
	// an undirected projection. graph.Build drops self-loops; the input
	// adjacency contains none.
	adj0 := make([][]neighbour, n)
	for _, e := range g.Edges {
		adj0[e.FromIndex] = append(adj0[e.FromIndex], neighbour{e.ToIndex, 1})
		adj0[e.ToIndex] = append(adj0[e.ToIndex], neighbour{e.FromIndex, 1})
	}

	// twoM = total edge weight × 2 (each edge contributes 2 to adj0).
	// Total weight is conserved across aggregation, so this stays
	// constant across levels.
	twoM := float64(2 * len(g.Edges))

	// partition[i] is the current community of original node i, projected
	// through every level's renumbering so it always indexes the current
	// level's adjacency.
	partition := make([]int, n)
	for i := range partition {
		partition[i] = i
	}
	adj := adj0
	// levelPartition[i] is the starting community label for level-node i
	// in the next phase-1 pass. Initially singletons; reseeded after
	// every aggregation from phase-1 community labels.
	levelPartition := make([]int, n)
	for i := range levelPartition {
		levelPartition[i] = i
	}

	for {
		moved, phase1 := leidenLocalMoves(adj, levelPartition, twoM)
		if !moved {
			break
		}
		refined := refinePartition(adj, phase1, twoM)
		denseRef, k := densify(refined)
		for i := range partition {
			partition[i] = denseRef[partition[i]]
		}
		adj = aggregateDense(adj, denseRef, k)
		// Seed next level: each refined super-node inherits its parent
		// phase-1 label. Two refined sub-communities sharing a phase-1
		// parent start in the same community next level — phase 1 will
		// keep them together unless splitting raises Q.
		next := make([]int, k)
		seen := make([]bool, k)
		// Iterate by ascending original-level index so the
		// representative chosen for each refined community is
		// deterministic.
		for orig := range denseRef {
			dr := denseRef[orig]
			if !seen[dr] {
				next[dr] = phase1[orig]
				seen[dr] = true
			}
		}
		// Densify levelPartition labels so subsequent passes see
		// contiguous ids — keeps determinism stable across map iteration.
		levelPartition, _ = densify(next)
		if k <= 1 {
			break
		}
	}
	return renumber(partition)
}

// leidenLocalMoves runs Leiden's phase-1 FastLocalMove: starts from the
// supplied initial partition, pushes every node onto a FIFO queue, and
// for each popped node picks the neighbouring community that maximises
// ΔQ. When a node moves, its neighbours not already in the destination
// community (and not currently queued) get re-queued — this is the Leiden
// optimisation that avoids re-evaluating settled nodes.
//
// FIFO order with ascending initial push order makes the convergence
// path deterministic; identical inputs always produce identical outputs.
func leidenLocalMoves(adj [][]neighbour, initial []int, twoM float64) (bool, []int) {
	n := len(adj)
	community := make([]int, n)
	copy(community, initial)

	nodeDegree := make([]float64, n)
	for i, ns := range adj {
		for _, nb := range ns {
			nodeDegree[i] += nb.weight
		}
	}
	kTot := make(map[int]float64)
	for i, c := range community {
		kTot[c] += nodeDegree[i]
	}

	queue := make([]int, n)
	for i := range n {
		queue[i] = i
	}
	inQueue := make([]bool, n)
	for i := range inQueue {
		inQueue[i] = true
	}

	moved := false
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		inQueue[v] = false

		kV := nodeDegree[v]
		toComm := make(map[int]float64)
		for _, nb := range adj[v] {
			if nb.to == v {
				continue
			}
			toComm[community[nb.to]] += nb.weight
		}

		from := community[v]
		kTot[from] -= kV

		// Evaluate ΔQ ∝ k_{v,C} - kTot[C] · k_v / 2m for every
		// candidate community C in toComm, plus the implicit "stay
		// alone" baseline at gain 0. Lower id wins ties so the path
		// stays deterministic.
		bestComm := from
		bestGain := 0.0
		for _, c := range sortedKeys(toComm) {
			gain := toComm[c] - kTot[c]*kV/twoM
			if gain > bestGain || (gain == bestGain && c < bestComm) {
				bestGain = gain
				bestComm = c
			}
		}

		kTot[bestComm] += kV
		if bestComm != from {
			community[v] = bestComm
			moved = true
			for _, nb := range adj[v] {
				if nb.to == v || community[nb.to] == bestComm || inQueue[nb.to] {
					continue
				}
				queue = append(queue, nb.to)
				inQueue[nb.to] = true
			}
		}
	}
	return moved, community
}

// refinePartition is Leiden's phase-2 refinement. Within each phase-1
// community C, re-partition its members starting from singletons, with
// each move gated by two well-connectedness tests against the Newman
// null model:
//
//	(a) v is well-connected to C\{v}:
//	    edges(v, C\{v}) ≥ k_v · (k_C − k_v) / 2m
//	(b) destination refined community R is well-connected to C\R:
//	    edges(R, C\R) ≥ k_R · (k_C − k_R) / 2m
//
// A node failing (a) stays in its singleton; a candidate R failing (b)
// is excluded from the move set. These thresholds are the expected
// number of edges under the same null model modularity scores against —
// so a community that wouldn't survive an honest comparison against
// random doesn't get to merge.
//
// Every refined community emitted by this function is, by construction,
// internally well-connected within its parent phase-1 community —
// Leiden's central correctness guarantee that Louvain lacks.
func refinePartition(adj [][]neighbour, phase1 []int, twoM float64) []int {
	n := len(adj)
	refined := make([]int, n)
	for i := range refined {
		refined[i] = i
	}

	nodeDegree := make([]float64, n)
	for i, ns := range adj {
		for _, nb := range ns {
			nodeDegree[i] += nb.weight
		}
	}

	byC := make(map[int][]int)
	for i, c := range phase1 {
		byC[c] = append(byC[c], i)
	}

	for _, c := range sortedMapKeys(byC) {
		members := byC[c]
		if len(members) <= 1 {
			continue
		}
		var kC float64
		for _, v := range members {
			kC += nodeDegree[v]
		}
		inC := make(map[int]bool, len(members))
		for _, v := range members {
			inC[v] = true
		}

		// Per-refined-community bookkeeping scoped to this C. Membership
		// drives the on-demand R-to-(C\R) edge count for gate (b); kTot
		// drives the ΔQ gain.
		refKtot := make(map[int]float64)
		refMembers := make(map[int][]int)
		for _, v := range members {
			r := refined[v]
			refKtot[r] += nodeDegree[v]
			refMembers[r] = append(refMembers[r], v)
		}

		sort.Ints(members)
		for _, v := range members {
			kV := nodeDegree[v]
			edgesVtoC := 0.0
			toRef := make(map[int]float64)
			for _, nb := range adj[v] {
				if nb.to == v || !inC[nb.to] {
					continue
				}
				edgesVtoC += nb.weight
				toRef[refined[nb.to]] += nb.weight
			}
			// Gate (a): v must be well-connected to C\{v} or it stays
			// in its singleton. This filters out hub spokes that are
			// only weakly tied to the bulk of their phase-1 community.
			if edgesVtoC < kV*(kC-kV)/twoM {
				continue
			}

			from := refined[v]
			refKtot[from] -= kV
			refMembers[from] = removeFirst(refMembers[from], v)
			if len(refMembers[from]) == 0 {
				delete(refMembers, from)
			}

			bestComm := from
			bestGain := 0.0
			for _, r := range sortedKeys(toRef) {
				kR := refKtot[r]
				// Gate (b): R must be well-connected to C\R. Singletons
				// {v} that already passed gate (a) trivially pass this
				// since their edge-count and threshold reduce to the
				// same numbers.
				if !rWellConnectedToC(adj, inC, refMembers[r], kR, kC, twoM) {
					continue
				}
				gain := toRef[r] - kR*kV/twoM
				if gain > bestGain || (gain == bestGain && r < bestComm) {
					bestGain = gain
					bestComm = r
				}
			}

			refKtot[bestComm] += kV
			refMembers[bestComm] = insertSorted(refMembers[bestComm], v)
			if bestComm != from {
				refined[v] = bestComm
			}
		}
	}
	return refined
}

// rWellConnectedToC counts edges from refined community R (= members)
// into C\R and tests whether that count meets the null-model threshold
// k_R · (k_C − k_R) / 2m. Empty R is naturally well-connected: kR is 0,
// edges is 0, and the comparison reduces to 0 ≥ 0. The on-demand
// recomputation is fine for our scan sizes (a few thousand nodes);
// switch to incremental bookkeeping if profiles ever say otherwise.
func rWellConnectedToC(adj [][]neighbour, inC map[int]bool, members []int, kR, kC, twoM float64) bool {
	inR := make(map[int]bool, len(members))
	for _, v := range members {
		inR[v] = true
	}
	edges := 0.0
	for _, v := range members {
		for _, nb := range adj[v] {
			if nb.to == v || !inC[nb.to] || inR[nb.to] {
				continue
			}
			edges += nb.weight
		}
	}
	return edges >= kR*(kC-kR)/twoM
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
// node-id in each community winning the lower label. Without this step
// ids would carry over from internal Leiden bookkeeping and depend on
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

func sortedMapKeys(m map[int][]int) []int {
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

// removeFirst removes the first occurrence of target from s and returns
// the trimmed slice. Used for refined-community membership tracking
// during phase-2 moves; callers always pass a target that's present, so
// the slices.Index lookup is invariant-checked by slices.Delete (panics
// on -1) rather than silently masked.
func removeFirst(s []int, target int) []int {
	i := slices.Index(s, target)
	return slices.Delete(s, i, i+1)
}

// insertSorted inserts v into s while preserving ascending order. Used
// by refinement to keep refMembers entries sorted so iteration is
// deterministic.
func insertSorted(s []int, v int) []int {
	i := sort.SearchInts(s, v)
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}
