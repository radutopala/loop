package graph

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GraphSuite struct {
	suite.Suite
}

func TestGraphSuite(t *testing.T) {
	suite.Run(t, new(GraphSuite))
}

// --- moduleOf ---

func (s *GraphSuite) TestModuleOfSubdirectoryUsesFirstSegment() {
	require.Equal(s.T(), "internal", moduleOf("internal/api/x.go"))
}

func (s *GraphSuite) TestModuleOfTopLevelFileUsesBasenameMinusExt() {
	require.Equal(s.T(), "main", moduleOf("main.go"))
}

func (s *GraphSuite) TestModuleOfTopLevelFileNoExtensionUsesPath() {
	require.Equal(s.T(), "Makefile", moduleOf("Makefile"))
}

// --- Build: nodes, modules, parse-failed counting ---

func (s *GraphSuite) TestBuildSortsNodesByPathAndCountsParseFailures() {
	facts := []*parser.FileFacts{
		{Path: "internal/b.go", Language: "go", LOC: 10},
		{Path: "internal/a.go", Language: "go", LOC: 5},
		{Path: "internal/broken.go", ParseFailed: true},
	}

	g := Build(facts)

	require.Len(s.T(), g.Nodes, 3)
	require.Equal(s.T(), "internal/a.go", g.Nodes[0].Path)
	require.Equal(s.T(), "internal/b.go", g.Nodes[1].Path)
	require.Equal(s.T(), "internal/broken.go", g.Nodes[2].Path)
	require.Equal(s.T(), 0, g.Index["internal/a.go"])
	require.Equal(s.T(), 1, g.ParseFailed)
}

func (s *GraphSuite) TestBuildClustersNodesByModule() {
	facts := []*parser.FileFacts{
		{Path: "cmd/loop/main.go"},
		{Path: "internal/api/x.go"},
		{Path: "internal/api/y.go"},
	}

	g := Build(facts)

	require.Len(s.T(), g.Modules, 2)
	require.Equal(s.T(), "cmd", g.Modules[0].Name)
	require.Equal(s.T(), "internal", g.Modules[1].Name)
	require.Equal(s.T(), []int{0}, g.Modules[0].NodeIndices)
	require.Equal(s.T(), []int{1, 2}, g.Modules[1].NodeIndices)
}

// --- Build: edges (suffix import resolution, dedup, self-loop drop) ---

func (s *GraphSuite) TestBuildResolvesGoStyleSuffixImports() {
	facts := []*parser.FileFacts{
		{
			Path:     "cmd/loop/main.go",
			Language: "go",
			Imports: []parser.Import{
				{Path: "github.com/radutopala/loop/internal/api"},
			},
		},
		{Path: "internal/api/handler.go", Language: "go"},
	}

	g := Build(facts)
	require.Len(s.T(), g.Edges, 1)
	require.Equal(s.T(), g.Index["cmd/loop/main.go"], g.Edges[0].FromIndex)
	require.Equal(s.T(), g.Index["internal/api/handler.go"], g.Edges[0].ToIndex)
}

func (s *GraphSuite) TestBuildResolvesRelativeTypeScriptImports() {
	facts := []*parser.FileFacts{
		{
			Path:     "src/index.ts",
			Language: "typescript",
			Imports: []parser.Import{
				{Path: "./util"},
				{Path: "../shared/types"},
			},
		},
		{Path: "src/util.ts", Language: "typescript"},
		{Path: "shared/types.ts", Language: "typescript"},
	}

	g := Build(facts)
	require.Len(s.T(), g.Edges, 2)
}

func (s *GraphSuite) TestBuildResolvesRelativeImportToFolderIndex() {
	// "./util" should resolve to "src/util.go" by extension fallback.
	facts := []*parser.FileFacts{
		{
			Path:     "src/index.go",
			Language: "go",
			Imports:  []parser.Import{{Path: "./util"}},
		},
		{Path: "src/util.go", Language: "go"},
	}

	g := Build(facts)
	require.Len(s.T(), g.Edges, 1)
}

func (s *GraphSuite) TestBuildResolvesRelativeImportExactPath() {
	// "./util.go" resolves directly via the "exact path" branch in the
	// relative resolver — no extension append needed.
	facts := []*parser.FileFacts{
		{
			Path:     "src/index.go",
			Language: "go",
			Imports:  []parser.Import{{Path: "./util.go"}},
		},
		{Path: "src/util.go", Language: "go"},
	}

	g := Build(facts)
	require.Len(s.T(), g.Edges, 1)
}

func (s *GraphSuite) TestBuildDropsExternalImports() {
	facts := []*parser.FileFacts{
		{
			Path:    "main.go",
			Imports: []parser.Import{{Path: "fmt"}, {Path: "os"}},
		},
	}

	g := Build(facts)
	require.Empty(s.T(), g.Edges)
}

func (s *GraphSuite) TestBuildDropsRelativeImportToMissingFile() {
	facts := []*parser.FileFacts{
		{
			Path:    "src/index.ts",
			Imports: []parser.Import{{Path: "./does-not-exist"}},
		},
	}
	g := Build(facts)
	require.Empty(s.T(), g.Edges)
}

func (s *GraphSuite) TestBuildDedupesIdenticalEdges() {
	facts := []*parser.FileFacts{
		{
			Path: "a.go",
			Imports: []parser.Import{
				{Path: "./b"},
				{Path: "./b"},
			},
		},
		{Path: "b.go"},
	}
	g := Build(facts)
	require.Len(s.T(), g.Edges, 1)
}

func (s *GraphSuite) TestBuildDropsSelfLoops() {
	// Re-export pattern: file "imports" itself via path suffix.
	facts := []*parser.FileFacts{
		{
			Path:    "a.go",
			Imports: []parser.Import{{Path: "a"}},
		},
	}
	g := Build(facts)
	require.Empty(s.T(), g.Edges)
}

func (s *GraphSuite) TestBuildSkipsImportsFromParseFailedFile() {
	facts := []*parser.FileFacts{
		{
			Path:        "broken.go",
			ParseFailed: true,
			Imports:     []parser.Import{{Path: "./other"}},
		},
		{Path: "other.go"},
	}
	g := Build(facts)
	require.Empty(s.T(), g.Edges)
}

func (s *GraphSuite) TestBuildSortsEdgesByFromThenTo() {
	facts := []*parser.FileFacts{
		{Path: "a.go", Imports: []parser.Import{{Path: "./c"}, {Path: "./b"}}},
		{Path: "b.go"},
		{Path: "c.go"},
		{Path: "d.go", Imports: []parser.Import{{Path: "./b"}}},
	}
	g := Build(facts)
	require.Len(s.T(), g.Edges, 3)
	// Indices: a=0, b=1, c=2, d=3 → expect (0,1), (0,2), (3,1)
	require.Equal(s.T(), Edge{FromIndex: 0, ToIndex: 1}, g.Edges[0])
	require.Equal(s.T(), Edge{FromIndex: 0, ToIndex: 2}, g.Edges[1])
	require.Equal(s.T(), Edge{FromIndex: 3, ToIndex: 1}, g.Edges[2])
}

// --- resolveImport direct cases for branch coverage ---

func (s *GraphSuite) TestResolveImportEmptyString() {
	idx, ok := resolveImport("", "from.go", map[string]int{"from.go": 0}, nil)
	require.False(s.T(), ok)
	require.Equal(s.T(), -1, idx)
}

func (s *GraphSuite) TestResolveImportExactPathHit() {
	idx, ok := resolveImport("foo/bar.go", "from.go", map[string]int{"foo/bar.go": 7}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 7, idx)
}

func (s *GraphSuite) TestResolveImportSuffixWithoutLeadingSlashEqualsExtTarget() {
	// Covers the "p == imp+ext" branch.
	idx, ok := resolveImport("foo", "from.go", map[string]int{"foo.go": 5}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 5, idx)
}

func (s *GraphSuite) TestResolveImportParentRelativeSuccess() {
	idx, ok := resolveImport("../shared/x", "src/index.ts",
		map[string]int{"shared/x.ts": 4}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 4, idx)
}

func (s *GraphSuite) TestResolveImportNoMatchReturnsFalse() {
	idx, ok := resolveImport("does-not-exist", "from.go", map[string]int{"foo.go": 0}, nil)
	require.False(s.T(), ok)
	require.Equal(s.T(), -1, idx)
}

// --- buildDirIndex direct cases ---

func (s *GraphSuite) TestBuildDirIndexSkipsTopLevelFiles() {
	// Files with dir "." (top-level) must not appear in the dir index.
	sorted := []*parser.FileFacts{{Path: "main.go"}, {Path: "internal/x.go"}}
	dirIdx := buildDirIndex(sorted)
	require.NotContains(s.T(), dirIdx, ".")
	require.Equal(s.T(), 1, dirIdx["internal"])
}

func (s *GraphSuite) TestBuildDirIndexKeepsLowestIndexPerDirectory() {
	// Two files under the same dir → only the first one wins.
	sorted := []*parser.FileFacts{
		{Path: "internal/api/a.go"},
		{Path: "internal/api/b.go"},
	}
	dirIdx := buildDirIndex(sorted)
	require.Equal(s.T(), 0, dirIdx["internal/api"])
}

func (s *GraphSuite) TestResolveImportDirectoryExactMatch() {
	// Covers the "dir == imp" branch in the directory-suffix scan.
	dirIdx := map[string]int{"foo": 9}
	idx, ok := resolveImport("foo", "from.go", map[string]int{}, dirIdx)
	require.True(s.T(), ok)
	require.Equal(s.T(), 9, idx)
}

func (s *GraphSuite) TestResolveImportFileSuffixWithExtension() {
	// Covers the bare 'strings.HasSuffix(p, "/"+imp)' branch — the
	// import already includes the extension, so no ext-loop append is
	// needed for the match.
	idx, ok := resolveImport("util.go", "from.go",
		map[string]int{"src/util.go": 7}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 7, idx)
}
