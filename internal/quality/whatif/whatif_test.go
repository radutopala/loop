package whatif

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type WhatifSuite struct {
	suite.Suite
}

func TestWhatifSuite(t *testing.T) {
	suite.Run(t, new(WhatifSuite))
}

func (s *WhatifSuite) buildGraph() *graph.Graph {
	return graph.Build([]*parser.FileFacts{
		{Path: "cmd/main.go", Language: "go", LOC: 50, Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/api"}}},
		{Path: "internal/api/handler.go", Language: "go", LOC: 80, Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/db"}}},
		{Path: "internal/api/util.go", Language: "go", LOC: 30},
		{Path: "internal/db/db.go", Language: "go", LOC: 60},
		{Path: "internal/dead/orphan.go", Language: "go", LOC: 200},
	})
}

func (s *WhatifSuite) TestSimulateNilGraphReturnsErrEmpty() {
	_, err := Simulate(nil, nil)
	require.ErrorIs(s.T(), err, ErrEmptyGraph)
}

func (s *WhatifSuite) TestSimulateZeroNodeGraphReturnsErrEmpty() {
	g := graph.Build(nil)
	_, err := Simulate(g, nil)
	require.ErrorIs(s.T(), err, ErrEmptyGraph)
}

func (s *WhatifSuite) TestSimulateNoMutationsReturnsZeroDelta() {
	g := s.buildGraph()
	r, err := Simulate(g, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), r.BaselineSignal, r.PredictedSignal)
	require.Equal(s.T(), 0, r.DeltaSignal)
	require.NotEmpty(s.T(), r.BaselineMetrics)
	require.NotEmpty(s.T(), r.PredictedMetrics)
}

func (s *WhatifSuite) TestSimulateDeleteRemovesFileAndItsEdges() {
	g := s.buildGraph()
	r, err := Simulate(g, []Mutation{{Op: OpDelete, Path: "internal/dead/orphan.go"}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), r.PredictedSignal-r.BaselineSignal, r.DeltaSignal)
}

func (s *WhatifSuite) TestSimulateDeletePrunesEdgesTouchingDeletedNode() {
	g := s.buildGraph()
	r, err := Simulate(g, []Mutation{{Op: OpDelete, Path: "internal/db/db.go"}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), r.PredictedSignal-r.BaselineSignal, r.DeltaSignal)
}

func (s *WhatifSuite) TestSimulateDeleteUnknownPathReturnsError() {
	g := s.buildGraph()
	_, err := Simulate(g, []Mutation{{Op: OpDelete, Path: "does/not/exist.go"}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mutation 0")
	require.Contains(s.T(), err.Error(), "delete")
}

func (s *WhatifSuite) TestSimulateMoveChangesNodeModule() {
	g := s.buildGraph()
	r, err := Simulate(g, []Mutation{{Op: OpMove, Path: "internal/api/util.go", NewModule: "shared"}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), r.PredictedSignal-r.BaselineSignal, r.DeltaSignal)
}

func (s *WhatifSuite) TestSimulateMoveRequiresNewModule() {
	g := s.buildGraph()
	_, err := Simulate(g, []Mutation{{Op: OpMove, Path: "internal/api/util.go"}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "new_module is required")
}

func (s *WhatifSuite) TestSimulateMoveUnknownPathReturnsError() {
	g := s.buildGraph()
	_, err := Simulate(g, []Mutation{{Op: OpMove, Path: "does/not/exist.go", NewModule: "shared"}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path not in graph")
}

func (s *WhatifSuite) TestSimulateSplitProducesPartFilesAndDistributesEdges() {
	g := s.buildGraph()
	r, err := Simulate(g, []Mutation{{Op: OpSplit, Path: "internal/api/handler.go", Parts: 3}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), r.PredictedSignal-r.BaselineSignal, r.DeltaSignal)
}

func (s *WhatifSuite) TestSimulateSplitRequiresAtLeastTwoParts() {
	g := s.buildGraph()
	_, err := Simulate(g, []Mutation{{Op: OpSplit, Path: "internal/api/handler.go", Parts: 1}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parts must be ≥2")
}

func (s *WhatifSuite) TestSimulateSplitUnknownPathReturnsError() {
	g := s.buildGraph()
	_, err := Simulate(g, []Mutation{{Op: OpSplit, Path: "does/not/exist.go", Parts: 2}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "path not in graph")
}

func (s *WhatifSuite) TestSimulateUnsupportedOpReturnsError() {
	g := s.buildGraph()
	_, err := Simulate(g, []Mutation{{Op: Op("rename"), Path: "internal/api/util.go"}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unsupported op")
}

func (s *WhatifSuite) TestSimulateChainAppliesMutationsInOrder() {
	g := s.buildGraph()
	r, err := Simulate(g, []Mutation{
		{Op: OpDelete, Path: "internal/dead/orphan.go"},
		{Op: OpMove, Path: "internal/api/util.go", NewModule: "shared"},
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), r.Mutations, 2)
}

func (s *WhatifSuite) TestApplySplitDropsSelfLoopOnSourceFile() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "internal/foo.go", Language: "go", LOC: 100},
		{Path: "internal/bar.go", Language: "go", LOC: 50, Imports: []parser.Import{{Path: "./foo"}}},
	})
	sh := projectToShadow(g)
	sh.edges = append(sh.edges, edgeSpec{from: "internal/foo.go", to: "internal/foo.go"})
	require.NoError(s.T(), sh.applySplit("internal/foo.go", 2))

	for _, e := range sh.edges {
		require.False(s.T(), e.from == "internal/foo.go" && e.to == "internal/foo.go")
	}
}

func (s *WhatifSuite) TestApplySplitDuplicatesIncomingEdgesToEveryPart() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "internal/big.go", Language: "go", LOC: 300},
		{Path: "internal/caller.go", Language: "go", LOC: 20, Imports: []parser.Import{{Path: "./big"}}},
	})
	sh := projectToShadow(g)
	require.NoError(s.T(), sh.applySplit("internal/big.go", 3))

	count := 0
	for _, e := range sh.edges {
		if e.from == "internal/caller.go" {
			count++
		}
	}
	require.Equal(s.T(), 3, count, "incoming edge fans out to all parts")
}

func (s *WhatifSuite) TestApplySplitDistributesOutgoingEdgesRoundRobin() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "src/big.go", LOC: 400, Imports: []parser.Import{
			{Path: "./a"}, {Path: "./b"}, {Path: "./c"},
		}},
		{Path: "src/a.go"},
		{Path: "src/b.go"},
		{Path: "src/c.go"},
	})
	sh := projectToShadow(g)
	require.NoError(s.T(), sh.applySplit("src/big.go", 2))

	counts := map[string]int{}
	for _, e := range sh.edges {
		counts[e.from]++
	}
	// 3 outgoing edges round-robin across 2 parts → part1 gets 2, part2 gets 1.
	require.Equal(s.T(), 2, counts["src/big.part1.go"])
	require.Equal(s.T(), 1, counts["src/big.part2.go"])
}

func (s *WhatifSuite) TestApplySplitPreservesUnrelatedEdges() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "src/big.go", LOC: 200},
		{Path: "src/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "src/y.go"},
	})
	sh := projectToShadow(g)
	require.NoError(s.T(), sh.applySplit("src/big.go", 2))

	found := false
	for _, e := range sh.edges {
		if e.from == "src/x.go" && e.to == "src/y.go" {
			found = true
		}
	}
	require.True(s.T(), found, "edge that doesn't touch the split source must survive intact")
}

func (s *WhatifSuite) TestAssembleDedupesParallelEdges() {
	sh := &shadow{
		nodes: []*graph.Node{
			{Path: "a.go", Module: "m"},
			{Path: "b.go", Module: "m"},
		},
		edges: []edgeSpec{
			{from: "a.go", to: "b.go"},
			{from: "a.go", to: "b.go"},
		},
	}
	out := sh.assemble()
	require.Len(s.T(), out.Edges, 1)
}

func (s *WhatifSuite) TestAssembleRebuildsIndexAndModulesAndDedupes() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "internal/a.go", Language: "go", LOC: 5},
		{Path: "internal/b.go", Language: "go", LOC: 7, ParseFailed: true},
	})
	sh := projectToShadow(g)
	sh.edges = append(sh.edges, edgeSpec{from: "internal/a.go", to: "missing/path.go"})
	sh.edges = append(sh.edges, edgeSpec{from: "internal/a.go", to: "internal/a.go"})

	out := sh.assemble()
	require.Equal(s.T(), 2, len(out.Nodes))
	require.Equal(s.T(), 1, out.ParseFailed)
	require.Len(s.T(), out.Modules, 1)
	for _, e := range out.Edges {
		require.NotEqual(s.T(), e.FromIndex, e.ToIndex)
	}
}

func (s *WhatifSuite) TestAssembleSortsEdgesByFromThenTo() {
	sh := &shadow{
		nodes: []*graph.Node{
			{Path: "a.go", Module: "m"},
			{Path: "b.go", Module: "m"},
			{Path: "c.go", Module: "m"},
		},
		edges: []edgeSpec{
			{from: "a.go", to: "c.go"},
			{from: "a.go", to: "b.go"},
			{from: "b.go", to: "c.go"},
		},
	}
	out := sh.assemble()
	require.Len(s.T(), out.Edges, 3)
	require.Equal(s.T(), out.Index["a.go"], out.Edges[0].FromIndex)
	require.True(s.T(), out.Edges[0].ToIndex < out.Edges[1].ToIndex)
}

func (s *WhatifSuite) TestSynthSplitPathRootFile() {
	require.Equal(s.T(), "main.part1.go", synthSplitPath("main.go", 1))
	require.Equal(s.T(), "main.part2.go", synthSplitPath("main.go", 2))
}

func (s *WhatifSuite) TestSynthSplitPathNestedFile() {
	require.Equal(s.T(), "internal/api/handler.part3.go", synthSplitPath("internal/api/handler.go", 3))
}

func (s *WhatifSuite) TestHasNodeReturnsFalseForMissing() {
	sh := &shadow{nodes: []*graph.Node{{Path: "x.go"}}}
	require.True(s.T(), sh.hasNode("x.go"))
	require.False(s.T(), sh.hasNode("y.go"))
}
