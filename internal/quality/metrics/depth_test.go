package metrics

import (
	"strconv"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DepthSuite struct {
	suite.Suite
}

func TestDepthSuite(t *testing.T) {
	suite.Run(t, new(DepthSuite))
}

func (s *DepthSuite) TestNilGraph() {
	r := Depth(nil)
	require.Equal(s.T(), DepthName, r.Name)
	require.Equal(s.T(), 0.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
	require.Equal(s.T(), DepthDetail{}, r.Detail)
}

func (s *DepthSuite) TestEmptyGraph() {
	g := graph.Build(nil)
	r := Depth(g)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *DepthSuite) TestSingleFileNoEdgesDepthOne() {
	g := graph.Build([]*parser.FileFacts{{Path: "a/x.go"}})
	r := Depth(g)
	require.Equal(s.T(), 1.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(DepthDetail)
	require.Equal(s.T(), 1, d.MaxDepth)
	require.Equal(s.T(), [][]string{{"a/x.go"}}, d.Layers)
	require.False(s.T(), d.SCCCollapsed)
}

func (s *DepthSuite) TestLinearChainCountsLayers() {
	// a → b → c → d → e: 5 layers.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "a/b.go", Imports: []parser.Import{{Path: "./c"}}},
		{Path: "a/c.go", Imports: []parser.Import{{Path: "./d"}}},
		{Path: "a/d.go", Imports: []parser.Import{{Path: "./e"}}},
		{Path: "a/e.go"},
	})
	r := Depth(g)
	require.Equal(s.T(), 5.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score, "5 ≤ lakosTarget=6")
	d := r.Detail.(DepthDetail)
	require.Len(s.T(), d.Layers, 5)
	for _, l := range d.Layers {
		require.Len(s.T(), l, 1)
	}
}

func (s *DepthSuite) TestSCCCollapsesToOneLayer() {
	// 4-node cycle: a → b → c → d → a. Should collapse to depth 1.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "a/b.go", Imports: []parser.Import{{Path: "./c"}}},
		{Path: "a/c.go", Imports: []parser.Import{{Path: "./d"}}},
		{Path: "a/d.go", Imports: []parser.Import{{Path: "./a"}}},
	})
	r := Depth(g)
	require.Equal(s.T(), 1.0, r.Raw)
	d := r.Detail.(DepthDetail)
	require.Len(s.T(), d.Layers, 1)
	require.Len(s.T(), d.Layers[0], 4)
	require.True(s.T(), d.SCCCollapsed)
}

func (s *DepthSuite) TestSCCWithTailExtendsChain() {
	// Cycle (a↔b) → c → d. Three layers: {a,b}, {c}, {d}.
	g := graph.Build([]*parser.FileFacts{
		{Path: "x/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "x/b.go", Imports: []parser.Import{{Path: "./a"}, {Path: "./c"}}},
		{Path: "x/c.go", Imports: []parser.Import{{Path: "./d"}}},
		{Path: "x/d.go"},
	})
	r := Depth(g)
	require.Equal(s.T(), 3.0, r.Raw)
	d := r.Detail.(DepthDetail)
	require.Len(s.T(), d.Layers, 3)
	require.ElementsMatch(s.T(), []string{"x/a.go", "x/b.go"}, d.Layers[0])
	require.Equal(s.T(), []string{"x/c.go"}, d.Layers[1])
	require.Equal(s.T(), []string{"x/d.go"}, d.Layers[2])
	require.True(s.T(), d.SCCCollapsed)
}

func (s *DepthSuite) TestDeepChainScoreDecays() {
	// 12-layer chain. Score = 1 - (12-6)/24 = 0.75.
	files := make([]*parser.FileFacts, 12)
	for i := range 12 {
		f := &parser.FileFacts{Path: nodePath(i)}
		if i+1 < 12 {
			f.Imports = []parser.Import{{Path: "./" + nodeName(i+1)}}
		}
		files[i] = f
	}
	g := graph.Build(files)
	r := Depth(g)
	require.Equal(s.T(), 12.0, r.Raw)
	require.InDelta(s.T(), 0.75, r.Score, 1e-9)
}

func (s *DepthSuite) TestExtremeChainFloorsAtZero() {
	// 35-layer chain → past lakosTarget+lakosScale (=30) → score 0.
	files := make([]*parser.FileFacts, 35)
	for i := range 35 {
		f := &parser.FileFacts{Path: nodePath(i)}
		if i+1 < 35 {
			f.Imports = []parser.Import{{Path: "./" + nodeName(i+1)}}
		}
		files[i] = f
	}
	g := graph.Build(files)
	r := Depth(g)
	require.Equal(s.T(), 35.0, r.Raw)
	require.Equal(s.T(), 0.0, r.Score)
}

func (s *DepthSuite) TestBranchingPicksLongestArm() {
	// a → b → c
	//      → d → e → f
	// Longest chain is a-b-d-e-f: 5 layers.
	g := graph.Build([]*parser.FileFacts{
		{Path: "x/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "x/b.go", Imports: []parser.Import{{Path: "./c"}, {Path: "./d"}}},
		{Path: "x/c.go"},
		{Path: "x/d.go", Imports: []parser.Import{{Path: "./e"}}},
		{Path: "x/e.go", Imports: []parser.Import{{Path: "./f"}}},
		{Path: "x/f.go"},
	})
	r := Depth(g)
	require.Equal(s.T(), 5.0, r.Raw)
}

func (s *DepthSuite) TestDuplicateCondensationEdgesDeduped() {
	// Two cycles share an edge to a common downstream file.
	// SCC1: {a, b}; SCC2: {c}; a → c, b → c (both yield SCC1 → SCC2).
	g := graph.Build([]*parser.FileFacts{
		{Path: "x/a.go", Imports: []parser.Import{{Path: "./b"}, {Path: "./c"}}},
		{Path: "x/b.go", Imports: []parser.Import{{Path: "./a"}, {Path: "./c"}}},
		{Path: "x/c.go"},
	})
	r := Depth(g)
	require.Equal(s.T(), 2.0, r.Raw)
	d := r.Detail.(DepthDetail)
	require.Len(s.T(), d.Layers, 2)
}

func (s *DepthSuite) TestLakosScoreBoundary() {
	require.Equal(s.T(), 1.0, lakosScore(0))
	require.Equal(s.T(), 1.0, lakosScore(lakosTarget))
	require.Equal(s.T(), 0.0, lakosScore(lakosTarget+lakosScale))
	require.Equal(s.T(), 0.0, lakosScore(lakosTarget+lakosScale+10))
	require.InDelta(s.T(), 0.5, lakosScore(lakosTarget+lakosScale/2), 1e-9)
}

// nodeName / nodePath build deterministic linear-chain test paths.
func nodeName(i int) string { return "n" + strconv.Itoa(i) }
func nodePath(i int) string { return "x/" + nodeName(i) + ".go" }
