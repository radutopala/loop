package c4

import (
	"strings"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type C4Suite struct {
	suite.Suite
}

func TestC4Suite(t *testing.T) {
	suite.Run(t, new(C4Suite))
}

func (s *C4Suite) TestEmitNilGraphReturnsEmptyDiagram() {
	d := Emit(nil)
	require.Contains(s.T(), d.Mermaid, "flowchart LR")
	require.Contains(s.T(), d.Mermaid, "no components")
	require.Equal(s.T(), 0, d.ComponentCount)
	require.Equal(s.T(), 0, d.EdgeCount)
}

func (s *C4Suite) TestEmitEmptyGraphReturnsEmptyDiagram() {
	g := graph.Build(nil)
	d := Emit(g)
	require.Contains(s.T(), d.Mermaid, "flowchart LR")
	require.Contains(s.T(), d.Mermaid, "no components")
}

func (s *C4Suite) TestEmitClustersByPackageWithSubgraphs() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "cmd/loop/main.go", Language: "go", Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/api"}}},
		{Path: "internal/api/handler.go", Language: "go"},
		{Path: "internal/api/util.go", Language: "go"},
		{Path: "internal/quality/c4/c4.go", Language: "go"},
	})

	d := Emit(g)

	require.Equal(s.T(), 3, d.ComponentCount)
	require.Equal(s.T(), 1, d.EdgeCount)
	require.Contains(s.T(), d.Mermaid, "flowchart LR")
	require.Contains(s.T(), d.Mermaid, `subgraph g_cmd["cmd"]`)
	require.Contains(s.T(), d.Mermaid, `subgraph g_internal["internal"]`)
	require.Contains(s.T(), d.Mermaid, `cmd_loop["cmd/loop"]`)
	require.Contains(s.T(), d.Mermaid, `internal_api["internal/api"]`)
	require.Contains(s.T(), d.Mermaid, `internal_quality_c4["internal/quality/c4"]`)
	require.Contains(s.T(), d.Mermaid, "cmd_loop --> internal_api")
}

func (s *C4Suite) TestEmitDedupesParallelEdgesBetweenSamePackages() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "cmd/a.go", Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/x"}}},
		{Path: "cmd/b.go", Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/x"}}},
		{Path: "internal/x/x.go"},
	})

	d := Emit(g)
	require.Equal(s.T(), 1, d.EdgeCount, "two cross-package imports collapse to one component arrow")
}

func (s *C4Suite) TestEmitDropsIntraPackageEdgesFromComponentDiagram() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "internal/api/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "internal/api/b.go"},
	})

	d := Emit(g)
	require.Equal(s.T(), 1, d.ComponentCount)
	require.Equal(s.T(), 0, d.EdgeCount)
}

func (s *C4Suite) TestEmitOrdersEdgesByFromThenTo() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "z/pkg/a.go", Imports: []parser.Import{{Path: "github.com/radutopala/loop/m/x"}}},
		{Path: "a/pkg/a.go", Imports: []parser.Import{{Path: "github.com/radutopala/loop/m/y"}}},
		{Path: "m/x/x.go"},
		{Path: "m/y/y.go"},
	})

	d := Emit(g)
	idxA := strings.Index(d.Mermaid, "a_pkg --> m_y")
	idxZ := strings.Index(d.Mermaid, "z_pkg --> m_x")
	require.True(s.T(), idxA > 0)
	require.True(s.T(), idxZ > 0)
	require.True(s.T(), idxA < idxZ, "edges sort by from-package first")
}

func (s *C4Suite) TestEmitOrdersComponentEdgesDeterministically() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "cmd/loop/a.go", Imports: []parser.Import{{Path: "github.com/radutopala/loop/internal/api"}, {Path: "github.com/radutopala/loop/external/x"}}},
		{Path: "internal/api/x.go"},
		{Path: "external/x/x.go"},
	})

	d := Emit(g)
	idxAPI := strings.Index(d.Mermaid, "cmd_loop --> internal_api")
	idxExt := strings.Index(d.Mermaid, "cmd_loop --> external_x")
	require.True(s.T(), idxAPI > 0)
	require.True(s.T(), idxExt > 0)
	require.True(s.T(), idxExt < idxAPI, "external sorts before internal alphabetically")
}

func (s *C4Suite) TestEmitTopLevelFileBecomesBareNode() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "main.go"},
	})

	d := Emit(g)
	require.Equal(s.T(), 1, d.ComponentCount)
	require.Contains(s.T(), d.Mermaid, `main["main"]`)
	require.NotContains(s.T(), d.Mermaid, "subgraph")
}

func (s *C4Suite) TestEmitTopLevelFileWithoutExtension() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "Makefile"},
	})

	d := Emit(g)
	require.Contains(s.T(), d.Mermaid, `Makefile["Makefile"]`)
}

func (s *C4Suite) TestComponentIDStripsNonLetterCharsAndPrefixesNumeric() {
	require.Equal(s.T(), "anon", componentID(""))
	require.Equal(s.T(), "internal", componentID("internal"))
	require.Equal(s.T(), "node_modules", componentID("node-modules"))
	require.Equal(s.T(), "m_3rdparty", componentID("3rdparty"))
	require.Equal(s.T(), "___", componentID("---"))
	require.Equal(s.T(), "src1", componentID("src1"))
	require.Equal(s.T(), "internal_quality_c4", componentID("internal/quality/c4"))
}
