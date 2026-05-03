package metrics

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CyclesSuite struct {
	suite.Suite
}

func TestCyclesSuite(t *testing.T) {
	suite.Run(t, new(CyclesSuite))
}

func (s *CyclesSuite) TestNilGraphReturnsScoreOne() {
	r := Cycles(nil)
	require.Equal(s.T(), CyclesName, r.Name)
	require.Equal(s.T(), 0.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
	require.Equal(s.T(), CyclesDetail{}, r.Detail)
}

func (s *CyclesSuite) TestEmptyGraphReturnsScoreOne() {
	g := graph.Build(nil)
	r := Cycles(g)
	require.Equal(s.T(), 1.0, r.Score)
	require.Equal(s.T(), CyclesDetail{}, r.Detail)
}

func (s *CyclesSuite) TestAcyclicGraphReturnsScoreOne() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go"},
	})
	r := Cycles(g)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(CyclesDetail)
	require.Empty(s.T(), d.Cycles)
	require.Equal(s.T(), 0, d.LargestCycleSize)
	require.Equal(s.T(), 0, d.TotalNodesInCycles)
}

func (s *CyclesSuite) TestTwoNodeCycleDetected() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", Imports: []parser.Import{{Path: "./x"}}},
	})
	r := Cycles(g)
	d := r.Detail.(CyclesDetail)
	require.Len(s.T(), d.Cycles, 1)
	require.Equal(s.T(), []string{"a/x.go", "a/y.go"}, d.Cycles[0])
	require.Equal(s.T(), 2, d.LargestCycleSize)
	require.Equal(s.T(), 2, d.TotalNodesInCycles)
	require.Equal(s.T(), 2.0, r.Raw)
	require.Equal(s.T(), 0.0, r.Score) // 1 - 2/2 = 0
}

func (s *CyclesSuite) TestThreeNodeCycleDetected() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", Imports: []parser.Import{{Path: "./z"}}},
		{Path: "a/z.go", Imports: []parser.Import{{Path: "./x"}}},
	})
	r := Cycles(g)
	d := r.Detail.(CyclesDetail)
	require.Len(s.T(), d.Cycles, 1)
	require.Equal(s.T(), []string{"a/x.go", "a/y.go", "a/z.go"}, d.Cycles[0])
	require.Equal(s.T(), 3, d.LargestCycleSize)
	require.Equal(s.T(), 3, d.TotalNodesInCycles)
}

func (s *CyclesSuite) TestDisjointCyclesSortedBySizeDescending() {
	// Cycle 1: 2 nodes; Cycle 2: 3 nodes. Largest first.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", Imports: []parser.Import{{Path: "./x"}}},
		{Path: "b/p.go", Imports: []parser.Import{{Path: "./q"}}},
		{Path: "b/q.go", Imports: []parser.Import{{Path: "./r"}}},
		{Path: "b/r.go", Imports: []parser.Import{{Path: "./p"}}},
	})
	r := Cycles(g)
	d := r.Detail.(CyclesDetail)
	require.Len(s.T(), d.Cycles, 2)
	require.Len(s.T(), d.Cycles[0], 3, "largest cycle first")
	require.Equal(s.T(), []string{"b/p.go", "b/q.go", "b/r.go"}, d.Cycles[0])
	require.Equal(s.T(), []string{"a/x.go", "a/y.go"}, d.Cycles[1])
	require.Equal(s.T(), 3, d.LargestCycleSize)
	require.Equal(s.T(), 5, d.TotalNodesInCycles)
}

func (s *CyclesSuite) TestEqualSizedCyclesSortedLexicographically() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "z/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "z/b.go", Imports: []parser.Import{{Path: "./a"}}},
		{Path: "a/m.go", Imports: []parser.Import{{Path: "./n"}}},
		{Path: "a/n.go", Imports: []parser.Import{{Path: "./m"}}},
	})
	r := Cycles(g)
	d := r.Detail.(CyclesDetail)
	require.Len(s.T(), d.Cycles, 2)
	require.Equal(s.T(), "a/m.go", d.Cycles[0][0], "lex tiebreak: 'a/' < 'z/'")
	require.Equal(s.T(), "z/a.go", d.Cycles[1][0])
}

func (s *CyclesSuite) TestPartialCycleScoreReflectsShare() {
	// 2-node cycle plus 2 acyclic standalone files → 2/4 = 0.5 in cycles, score 0.5.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", Imports: []parser.Import{{Path: "./x"}}},
		{Path: "b/p.go"},
		{Path: "b/q.go"},
	})
	r := Cycles(g)
	require.InDelta(s.T(), 0.5, r.Score, 1e-9)
	require.Equal(s.T(), 2.0, r.Raw)
}

func (s *CyclesSuite) TestSingleNodeNonCyclicNotReported() {
	// Lone files (no edges, no self-loops) must not appear as 1-element SCCs.
	g := graph.Build([]*parser.FileFacts{
		{Path: "x.go"},
		{Path: "y.go"},
	})
	r := Cycles(g)
	d := r.Detail.(CyclesDetail)
	require.Empty(s.T(), d.Cycles)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *CyclesSuite) TestNestedSCCCollapsedToOne() {
	// 4-node SCC: x → y → z → w → x (full cycle), plus extra x → z chord.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}, {Path: "./z"}}},
		{Path: "a/y.go", Imports: []parser.Import{{Path: "./z"}}},
		{Path: "a/z.go", Imports: []parser.Import{{Path: "./w"}}},
		{Path: "a/w.go", Imports: []parser.Import{{Path: "./x"}}},
	})
	r := Cycles(g)
	d := r.Detail.(CyclesDetail)
	require.Len(s.T(), d.Cycles, 1)
	require.Equal(s.T(), 4, d.LargestCycleSize)
	require.Equal(s.T(), 0.0, r.Score)
}

func (s *CyclesSuite) TestCycleAlongsideAcyclicChainHasMixedScore() {
	// 2-node cycle (a/x ↔ a/y) and a 3-file linear chain (b/u → b/v → b/w).
	// 2 of 5 nodes are in a cycle → score = 1 - 2/5 = 0.6.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", Imports: []parser.Import{{Path: "./x"}}},
		{Path: "b/u.go", Imports: []parser.Import{{Path: "./v"}}},
		{Path: "b/v.go", Imports: []parser.Import{{Path: "./w"}}},
		{Path: "b/w.go"},
	})
	r := Cycles(g)
	require.InDelta(s.T(), 0.6, r.Score, 1e-9)
}

func (s *CyclesSuite) TestBuildAdjacencyDirect() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go"},
	})
	adj := buildAdjacency(g)
	require.Len(s.T(), adj, 2)
	// One edge from x → y; depending on sort order, find the file with one outgoing edge.
	totalEdges := 0
	for _, ns := range adj {
		totalEdges += len(ns)
	}
	require.Equal(s.T(), 1, totalEdges)
}

func (s *CyclesSuite) TestTarjanSCCEmpty() {
	require.Empty(s.T(), tarjanSCC(0, nil))
}

func (s *CyclesSuite) TestTarjanSCCSingletons() {
	// Two disconnected singletons - both get their own component.
	comps := tarjanSCC(2, [][]int{nil, nil})
	require.Len(s.T(), comps, 2)
	for _, c := range comps {
		require.Len(s.T(), c, 1)
	}
}

func (s *CyclesSuite) TestTarjanSCCSelfLoopProducesSingleNodeSCC() {
	// Direct call - graph package drops self-loops, but Tarjan itself should handle them.
	comps := tarjanSCC(1, [][]int{{0}})
	require.Len(s.T(), comps, 1)
	require.Equal(s.T(), []int{0}, comps[0])
}
