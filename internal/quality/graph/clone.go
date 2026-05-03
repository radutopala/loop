package graph

import "maps"

// Clone returns a deep-enough copy of g for the whatif simulator to mutate.
// Slice headers are duplicated so the caller's mutations don't bleed into
// the cached graph; Node.Functions/Types/Calls are NOT cloned because the
// metrics package treats them as read-only and never reassigns the slices.
//
// Index, Edges and Modules are rebuilt from scratch when whatif applies a
// mutation — see graph.RebuildAfterMutation. This Clone is the fast path
// for "I just want to inspect a copy without rebuilding"; mutations should
// always go through RebuildAfterMutation to keep the invariants.
func (g *Graph) Clone() *Graph {
	if g == nil {
		return nil
	}
	out := &Graph{
		Nodes:       make([]*Node, len(g.Nodes)),
		Index:       make(map[string]int, len(g.Index)),
		Edges:       make([]Edge, len(g.Edges)),
		Modules:     make([]*Module, len(g.Modules)),
		ParseFailed: g.ParseFailed,
	}
	for i, n := range g.Nodes {
		nn := *n
		out.Nodes[i] = &nn
	}
	maps.Copy(out.Index, g.Index)
	copy(out.Edges, g.Edges)
	for i, m := range g.Modules {
		indices := make([]int, len(m.NodeIndices))
		copy(indices, m.NodeIndices)
		out.Modules[i] = &Module{Name: m.Name, NodeIndices: indices}
	}
	return out
}
