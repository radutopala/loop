package metrics

import (
	"math"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ModularitySuite struct {
	suite.Suite
}

func TestModularitySuite(t *testing.T) {
	suite.Run(t, new(ModularitySuite))
}

func (s *ModularitySuite) TestNilGraphReturnsScoreOne() {
	r := Modularity(nil)
	require.Equal(s.T(), ModularityName, r.Name)
	require.Equal(s.T(), 0.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ModularitySuite) TestEmptyGraphReturnsScoreOne() {
	g := graph.Build(nil)
	r := Modularity(g)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ModularitySuite) TestNoEdgesReturnsScoreOne() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go"}, {Path: "b/y.go"},
	})
	r := Modularity(g)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ModularitySuite) TestPerfectClusteringScoresHigh() {
	// Two modules, edges only inside each module → high Q.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go"},
		{Path: "b/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "b/y.go"},
	})
	r := Modularity(g)
	require.Greater(s.T(), r.Raw, 0.3, "expected modular structure to produce Q > 0.3")
	require.Equal(s.T(), r.Raw, r.Score, "Q in [0,1] should pass through to Score unchanged")
}

func (s *ModularitySuite) TestCrossClusterEdgesScoreLow() {
	// Three top-level modules connected pairwise → no in-cluster edges.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "../b/x"}, {Path: "../c/x"}}},
		{Path: "b/x.go", Imports: []parser.Import{{Path: "../a/x"}, {Path: "../c/x"}}},
		{Path: "c/x.go", Imports: []parser.Import{{Path: "../a/x"}, {Path: "../b/x"}}},
	})
	r := Modularity(g)
	require.LessOrEqual(s.T(), r.Raw, 0.0, "purely cross-cluster edges yield non-positive Q")
	require.Equal(s.T(), 0.0, r.Score, "negative Q clamps to Score 0")
}

func (s *ModularitySuite) TestQValuePassThroughWithinUnitInterval() {
	// Sanity: a known small graph hits an analytically computable Q.
	// 4 nodes, 2 modules, 2 in-cluster edges, 0 cross-cluster.
	// Each cluster has L = 1, D = 2, m = 2 → per-cluster: 1/2 - (2/4)^2 = 0.25
	// Total Q = 0.5.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go"},
		{Path: "b/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "b/y.go"},
	})
	r := Modularity(g)
	require.InDelta(s.T(), 0.5, r.Raw, 1e-9)
}

func (s *ModularitySuite) TestClampLowerBound() {
	require.Equal(s.T(), 0.0, clamp01(-0.5))
}

func (s *ModularitySuite) TestClampUpperBound() {
	require.Equal(s.T(), 1.0, clamp01(1.5))
}

func (s *ModularitySuite) TestClampMidValuePassesThrough() {
	require.Equal(s.T(), 0.42, clamp01(0.42))
	require.False(s.T(), math.IsNaN(clamp01(0.42)))
}
