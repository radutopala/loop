package metrics

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TilesSuite struct {
	suite.Suite
}

func TestTilesSuite(t *testing.T) {
	suite.Run(t, new(TilesSuite))
}

func (s *TilesSuite) TestNilGraphReturnsNil() {
	require.Nil(s.T(), AttributeFiles(nil, nil))
}

func (s *TilesSuite) TestEmptyGraphReturnsNil() {
	require.Nil(s.T(), AttributeFiles(graph.Build(nil), nil))
}

func (s *TilesSuite) TestSingleFileNoEdgesYieldsZeroDeficit() {
	g := graph.Build([]*parser.FileFacts{{Path: "a/x.go", LOC: 10}})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	require.Len(s.T(), tiles, 1)
	require.Equal(s.T(), "a/x.go", tiles[0].Path)
	require.Equal(s.T(), 10, tiles[0].LOC)
	require.Equal(s.T(), 0.0, tiles[0].Deficit)
	require.Equal(s.T(), "", tiles[0].TopReason)
	require.NotNil(s.T(), tiles[0].MetricDeficits)
}

func (s *TilesSuite) TestCycleMembersFlaggedAsCyclesDrag() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", LOC: 1, Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", LOC: 1, Imports: []parser.Import{{Path: "./x"}}},
	})
	results := []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)}
	tiles := AttributeFiles(g, results)
	require.Len(s.T(), tiles, 2)
	for _, t := range tiles {
		require.Equal(s.T(), 1.0, t.Deficit)
		require.Equal(s.T(), 1.0, t.MetricDeficits[CyclesName])
		require.Equal(s.T(), CyclesName, t.TopReason)
	}
}

func (s *TilesSuite) TestModularityCrossClusterFraction() {
	// Two modules: "internal" and "cmd". Cross edge from cmd→internal/x.
	g := graph.Build([]*parser.FileFacts{
		{Path: "cmd/main.go", LOC: 1, Imports: []parser.Import{{Path: "internal/x"}}},
		{Path: "internal/x.go", LOC: 1},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	byPath := map[string]FileTile{}
	for _, t := range tiles {
		byPath[t.Path] = t
	}
	// Both files have one edge each, all crossing module boundaries.
	require.Equal(s.T(), 1.0, byPath["cmd/main.go"].MetricDeficits[ModularityName])
	require.Equal(s.T(), 1.0, byPath["internal/x.go"].MetricDeficits[ModularityName])
}

func (s *TilesSuite) TestModularityIntraClusterIsHealthy() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", LOC: 1, Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", LOC: 1},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	for _, t := range tiles {
		require.Equal(s.T(), 0.0, t.MetricDeficits[ModularityName])
	}
}

func (s *TilesSuite) TestEqualityFlagsLargestFile() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/small.go", LOC: 10},
		{Path: "a/medium.go", LOC: 50},
		{Path: "a/big.go", LOC: 1000},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	byPath := map[string]FileTile{}
	for _, t := range tiles {
		byPath[t.Path] = t
	}
	// big.go is the largest; spread = max - mean. (1000 - 1060/3) / (1000 - 1060/3) = 1.
	require.Equal(s.T(), 1.0, byPath["a/big.go"].MetricDeficits[EqualityName])
	// Below-mean files contribute 0.
	require.Equal(s.T(), 0.0, byPath["a/small.go"].MetricDeficits[EqualityName])
}

func (s *TilesSuite) TestEqualityZeroLOCAllFiles() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", LOC: 0},
		{Path: "a/y.go", LOC: 0},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	for _, t := range tiles {
		require.Equal(s.T(), 0.0, t.MetricDeficits[EqualityName])
	}
}

func (s *TilesSuite) TestEqualityAllSameLOC() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", LOC: 100},
		{Path: "a/y.go", LOC: 100},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	for _, t := range tiles {
		require.Equal(s.T(), 0.0, t.MetricDeficits[EqualityName])
	}
}

func (s *TilesSuite) TestRedundancyFlagsDeadFunctions() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a/x.go",
			LOC:       10,
			Functions: []parser.Function{{Name: "Used", StartLine: 1}, {Name: "deadFn", StartLine: 5}},
			Calls:     []parser.Call{{Name: "Used"}},
		},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	require.Len(s.T(), tiles, 1)
	require.InDelta(s.T(), 0.5, tiles[0].MetricDeficits[RedundancyName], 1e-9)
	require.Equal(s.T(), RedundancyName, tiles[0].TopReason)
}

func (s *TilesSuite) TestRedundancyConventionExclusions() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a/x.go",
			LOC:       10,
			Functions: []parser.Function{{Name: "main"}, {Name: "init"}, {Name: "TestX"}, {Name: "MarshalJSON"}},
		},
	})
	tiles := AttributeFiles(g, []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)})
	require.Equal(s.T(), 0.0, tiles[0].MetricDeficits[RedundancyName])
}

func (s *TilesSuite) TestDepthDragOnDeepChain() {
	// Build a chain longer than lakosTarget (6) to provoke depth drag.
	// Modules cluster by top-level segment so we use distinct dirs to
	// avoid SCC collapse confounding the test.
	files := []*parser.FileFacts{}
	for i := range 10 {
		f := &parser.FileFacts{Path: layerPath(i), LOC: 1}
		if i+1 < 10 {
			f.Imports = []parser.Import{{Path: layerPath(i + 1)}}
		}
		files = append(files, f)
	}
	g := graph.Build(files)
	results := []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)}
	tiles := AttributeFiles(g, results)

	byPath := map[string]FileTile{}
	for _, t := range tiles {
		byPath[t.Path] = t
	}
	// Files in layers > lakosTarget should have positive depth drag.
	require.Greater(s.T(), byPath[layerPath(9)].MetricDeficits[DepthName], 0.0)
	// Files at depth ≤ lakosTarget should have zero depth drag.
	require.Equal(s.T(), 0.0, byPath[layerPath(0)].MetricDeficits[DepthName])
}

func (s *TilesSuite) TestSortDeficitDescThenLOCDesc() {
	// Three files of equal LOC so equality drag stays 0; cycle members
	// have deficit 1.0 and the third file has deficit 0.
	g := graph.Build([]*parser.FileFacts{
		{Path: "z/clean.go", LOC: 1},
		{Path: "a/x.go", LOC: 1, Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", LOC: 1, Imports: []parser.Import{{Path: "./x"}}},
	})
	results := []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)}
	tiles := AttributeFiles(g, results)
	require.Len(s.T(), tiles, 3)
	require.Equal(s.T(), 1.0, tiles[0].Deficit)
	require.Equal(s.T(), 1.0, tiles[1].Deficit)
	require.Equal(s.T(), "z/clean.go", tiles[2].Path)
	require.Equal(s.T(), 0.0, tiles[2].Deficit)
}

func (s *TilesSuite) TestTileCapTruncates() {
	files := make([]*parser.FileFacts, TileCap+5)
	for i := range files {
		files[i] = &parser.FileFacts{Path: pathN(i), LOC: i + 1}
	}
	g := graph.Build(files)
	results := []Result{Modularity(g), Cycles(g), Depth(g), Equality(g), Redundancy(g)}
	tiles := AttributeFiles(g, results)
	require.Len(s.T(), tiles, TileCap)
}

func (s *TilesSuite) TestFindResultMissingReturnsNil() {
	require.Nil(s.T(), findResult(nil, ModularityName))
	require.Nil(s.T(), findResult([]Result{{Name: "other"}}, ModularityName))
	r := findResult([]Result{{Name: ModularityName, Score: 0.5}}, ModularityName)
	require.NotNil(s.T(), r)
	require.Equal(s.T(), 0.5, r.Score)
}

func (s *TilesSuite) TestCyclesFileDragHandlesNilAndBadDetail() {
	require.Empty(s.T(), cyclesFileDrag(nil))
	require.Empty(s.T(), cyclesFileDrag(&Result{Detail: "not a CyclesDetail"}))
}

func (s *TilesSuite) TestDepthFileDragHandlesNilAndBadDetail() {
	require.Empty(s.T(), depthFileDrag(nil))
	require.Empty(s.T(), depthFileDrag(&Result{Detail: 42}))
}

func (s *TilesSuite) TestComputePopulatesTiles() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a/x.go", LOC: 1, Imports: []parser.Import{{Path: "./y"}}},
		{Path: "a/y.go", LOC: 1, Imports: []parser.Import{{Path: "./x"}}},
	})
	sig := Compute(g)
	require.NotEmpty(s.T(), sig.Tiles)
	require.Equal(s.T(), 1.0, sig.Tiles[0].Deficit)
}

func (s *TilesSuite) TestAggregateLeavesTilesEmpty() {
	sig := Aggregate([]Result{{Name: ModularityName, Score: 1, Raw: 0.5}})
	require.Empty(s.T(), sig.Tiles)
}

func (s *TilesSuite) TestWorstDeficitDeterministicTieBreak() {
	// Modularity and cycles both at 0.5 — canonical order picks modularity.
	worst, reason := worstDeficit(map[string]float64{
		ModularityName: 0.5,
		CyclesName:     0.5,
	})
	require.Equal(s.T(), 0.5, worst)
	require.Equal(s.T(), ModularityName, reason)
}

// layerPath spaces files into distinct top-level modules so the depth
// chain isn't collapsed by intra-module clustering.
func layerPath(i int) string {
	return pathN(i)
}

func pathN(i int) string {
	// Pad so lex order matches numeric order; helps the cap-truncation
	// test be deterministic.
	letters := "abcdefghijklmnopqrstuvwxyz"
	first := letters[i%26]
	second := letters[(i/26)%26]
	third := letters[(i/26/26)%26]
	return string([]byte{first, second, third}) + "/x.go"
}
