package graph

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CloneSuite struct {
	suite.Suite
}

func TestCloneSuite(t *testing.T) {
	suite.Run(t, new(CloneSuite))
}

func (s *CloneSuite) TestCloneNilReturnsNil() {
	var g *Graph
	require.Nil(s.T(), g.Clone())
}

func (s *CloneSuite) TestCloneCopiesNodesIndexEdgesModulesAndParseFailed() {
	src := Build([]*parser.FileFacts{
		{Path: "internal/a.go", Language: "go", LOC: 5, Imports: []parser.Import{{Path: "./b"}}},
		{Path: "internal/b.go", Language: "go", LOC: 7},
		{Path: "internal/broken.go", ParseFailed: true},
	})

	out := src.Clone()

	require.NotSame(s.T(), src, out)
	require.Equal(s.T(), src.ParseFailed, out.ParseFailed)
	require.Len(s.T(), out.Nodes, len(src.Nodes))
	require.Len(s.T(), out.Edges, len(src.Edges))
	require.Equal(s.T(), src.Index, out.Index)
	require.Len(s.T(), out.Modules, len(src.Modules))

	// Mutating the clone must not leak into the source — verifies deep copy
	// of Nodes, Modules.NodeIndices, Edges, and Index.
	out.Nodes[0].LOC = 999
	require.NotEqual(s.T(), 999, src.Nodes[0].LOC)
	out.Modules[0].NodeIndices[0] = 42
	require.NotEqual(s.T(), 42, src.Modules[0].NodeIndices[0])
	out.Index["mutated"] = 1
	_, ok := src.Index["mutated"]
	require.False(s.T(), ok)
	out.Edges[0].ToIndex = 999
	require.NotEqual(s.T(), 999, src.Edges[0].ToIndex)
}
