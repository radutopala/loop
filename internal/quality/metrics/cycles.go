package metrics

import (
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// CyclesName is the canonical key for the cycle-pressure metric.
const CyclesName = "cycles"

// CyclesDetail is the human-readable payload the diagnostics surface and
// the rules engine consume. Cycles are the strongly connected components
// of size > 1 found in the import graph; each cycle lists its member
// file paths.
type CyclesDetail struct {
	// Cycles is one entry per non-trivial SCC. Sorted by descending size,
	// tie-broken by lexicographic path order — deterministic across runs.
	Cycles [][]string

	// LargestCycleSize is len(Cycles[0]) when Cycles is non-empty, else 0.
	LargestCycleSize int

	// TotalNodesInCycles sums all members across all non-trivial SCCs.
	TotalNodesInCycles int
}

// Cycles runs Tarjan's SCC algorithm against the directed import graph
// and returns the cycle pressure as a Result. Score is 1.0 for an
// acyclic graph, decaying linearly with the share of nodes that live
// inside any non-trivial SCC.
//
// Tarjan is O(V+E) and iterative — implemented with an explicit stack
// so deep import chains (tens of thousands of files) don't blow the
// goroutine stack. Single-node SCCs (no self-loop because we drop those
// at build time) don't count as cycles.
func Cycles(g *graph.Graph) Result {
	if g == nil || len(g.Nodes) == 0 {
		return Result{
			Name:   CyclesName,
			Raw:    0,
			Score:  1.0,
			Detail: CyclesDetail{},
		}
	}

	adj := buildAdjacency(g)
	sccs := tarjanSCC(len(g.Nodes), adj)

	var nontrivial [][]string
	totalInCycles := 0
	for _, comp := range sccs {
		if len(comp) < 2 {
			continue
		}
		paths := make([]string, len(comp))
		for i, idx := range comp {
			paths[i] = g.Nodes[idx].Path
		}
		sort.Strings(paths)
		nontrivial = append(nontrivial, paths)
		totalInCycles += len(paths)
	}
	sort.Slice(nontrivial, func(i, j int) bool {
		if len(nontrivial[i]) != len(nontrivial[j]) {
			return len(nontrivial[i]) > len(nontrivial[j])
		}
		return nontrivial[i][0] < nontrivial[j][0]
	})

	largest := 0
	if len(nontrivial) > 0 {
		largest = len(nontrivial[0])
	}

	// Each node is in at most one SCC, so totalInCycles ≤ |Nodes| and score ≥ 0
	// without an explicit clamp.
	score := 1.0 - float64(totalInCycles)/float64(len(g.Nodes))
	return Result{
		Name:  CyclesName,
		Raw:   float64(totalInCycles),
		Score: score,
		Detail: CyclesDetail{
			Cycles:             nontrivial,
			LargestCycleSize:   largest,
			TotalNodesInCycles: totalInCycles,
		},
	}
}

func buildAdjacency(g *graph.Graph) [][]int {
	adj := make([][]int, len(g.Nodes))
	for _, e := range g.Edges {
		adj[e.FromIndex] = append(adj[e.FromIndex], e.ToIndex)
	}
	return adj
}

// tarjanSCC returns the strongly connected components of a directed
// graph with n nodes and adjacency list adj. Each component is a slice
// of node indices. Components are returned in reverse-topological order
// (leaves first), which is incidental — the caller doesn't depend on
// it because Cycles re-sorts.
func tarjanSCC(n int, adj [][]int) [][]int {
	index := make([]int, n)
	lowlink := make([]int, n)
	onStack := make([]bool, n)
	inComp := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	var components [][]int
	nextIndex := 0

	type frame struct {
		v     int
		child int
	}

	for start := range n {
		if index[start] != -1 {
			continue
		}
		callStack := []frame{{v: start, child: 0}}
		index[start] = nextIndex
		lowlink[start] = nextIndex
		nextIndex++
		stack = append(stack, start)
		onStack[start] = true

		for len(callStack) > 0 {
			top := &callStack[len(callStack)-1]
			v := top.v
			if top.child < len(adj[v]) {
				w := adj[v][top.child]
				top.child++
				if index[w] == -1 {
					index[w] = nextIndex
					lowlink[w] = nextIndex
					nextIndex++
					stack = append(stack, w)
					onStack[w] = true
					callStack = append(callStack, frame{v: w, child: 0})
				} else if onStack[w] && lowlink[v] > index[w] {
					lowlink[v] = index[w]
				}
				continue
			}
			// All neighbours processed; finalise v.
			if lowlink[v] == index[v] {
				var comp []int
				for {
					top := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[top] = false
					inComp[top] = true
					comp = append(comp, top)
					if top == v {
						break
					}
				}
				components = append(components, comp)
			}
			callStack = callStack[:len(callStack)-1]
			if len(callStack) > 0 {
				parent := &callStack[len(callStack)-1]
				if lowlink[parent.v] > lowlink[v] {
					lowlink[parent.v] = lowlink[v]
				}
			}
		}
	}
	return components
}
