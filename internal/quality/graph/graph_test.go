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
	idx, ok := resolveImport("", "from.go", "", map[string]int{"from.go": 0}, nil)
	require.False(s.T(), ok)
	require.Equal(s.T(), -1, idx)
}

func (s *GraphSuite) TestResolveImportExactPathHit() {
	idx, ok := resolveImport("foo/bar.go", "from.go", "", map[string]int{"foo/bar.go": 7}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 7, idx)
}

func (s *GraphSuite) TestResolveImportSuffixWithoutLeadingSlashEqualsExtTarget() {
	// Covers the "p == imp+ext" branch.
	idx, ok := resolveImport("foo", "from.ts", "typescript", map[string]int{"foo.go": 5}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 5, idx)
}

func (s *GraphSuite) TestResolveImportParentRelativeSuccess() {
	idx, ok := resolveImport("../shared/x", "src/index.ts", "typescript",
		map[string]int{"shared/x.ts": 4}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 4, idx)
}

// TestResolveImportPicksStableMatch checks that, when several entries
// match, the longest dir or the shortest file wins and ties go to the
// lowest index, on every run whatever the map order.
func (s *GraphSuite) TestResolveImportPicksStableMatch() {
	tests := []struct {
		name     string
		imp      string
		fromLang string
		index    map[string]int
		dirIndex map[string]int
		want     int
	}{
		{
			name:     "longest dir suffix",
			imp:      "github.com/x/internal/api",
			fromLang: "go",
			dirIndex: map[string]int{"api": 1, "internal/api": 2, "x/internal/api": 3, "other": 4},
			want:     3,
		},
		{
			name:     "exact file beats a deeper suffix",
			imp:      "lib/util",
			fromLang: "typescript",
			index:    map[string]int{"lib/util.ts": 4, "src/lib/util.ts": 2, "a/b/lib/util": 5},
			want:     4,
		},
		{
			name:     "shortest file suffix",
			imp:      "lib/util",
			fromLang: "typescript",
			index:    map[string]int{"packages/foo/lib/util.ts": 1, "src/lib/util.ts": 6},
			want:     6,
		},
		{
			name:     "tie goes to the lowest index",
			imp:      "x",
			fromLang: "typescript",
			index:    map[string]int{"a/x.ts": 3, "b/x.ts": 1, "c/x.ts": 2},
			want:     1,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			for range 20 {
				idx, ok := resolveImport(tc.imp, "from.ts", tc.fromLang, tc.index, tc.dirIndex)
				require.True(s.T(), ok)
				require.Equal(s.T(), tc.want, idx)
			}
		})
	}
}

func (s *GraphSuite) TestResolveImportNoMatchReturnsFalse() {
	idx, ok := resolveImport("does-not-exist", "from.go", "", map[string]int{"foo.go": 0}, nil)
	require.False(s.T(), ok)
	require.Equal(s.T(), -1, idx)
}

func (s *GraphSuite) TestResolveImportGoBareImportIsStdlib() {
	// `import "embed"` from a Go file must NOT resolve to a local
	// `*/embed.go` via the file-suffix match — that produced phantom
	// edges and phantom cycles between any two same-basename stdlib
	// shadows in the codebase (the bug that this guard fixes).
	idx, ok := resolveImport("embed", "internal/foo/x.go", "go",
		map[string]int{"internal/bar/embed.go": 3}, map[string]int{"internal/bar": 3})
	require.False(s.T(), ok)
	require.Equal(s.T(), -1, idx)
}

func (s *GraphSuite) TestResolveImportGoSlashedImportStillResolves() {
	// Sanity check: the stdlib guard must not affect normal Go module
	// imports (which always have at least one slash).
	idx, ok := resolveImport("github.com/example/lib/internal/api", "internal/foo/x.go", "go",
		map[string]int{"internal/api/handler.go": 8}, map[string]int{"internal/api": 8})
	require.True(s.T(), ok)
	require.Equal(s.T(), 8, idx)
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

func (s *GraphSuite) TestBuildDirIndexPrefersNonTestFile() {
	// Even when integration_test.go sorts before manifest.go, the
	// directory representative must be manifest.go so external test
	// imports don't create false cycles. Test-only dirs still resolve.
	sorted := []*parser.FileFacts{
		{Path: "internal/plugin/integration_test.go"},
		{Path: "internal/plugin/manifest.go"},
		{Path: "internal/testonly/only_test.go"},
	}
	dirIdx := buildDirIndex(sorted)
	require.Equal(s.T(), 1, dirIdx["internal/plugin"])
	require.Equal(s.T(), 2, dirIdx["internal/testonly"])
}

func (s *GraphSuite) TestResolveImportDirectoryExactMatch() {
	// Covers the "dir == imp" branch in the directory-suffix scan. Use
	// a TS importer so the Go-stdlib guard doesn't reject the bare
	// import.
	dirIdx := map[string]int{"foo": 9}
	idx, ok := resolveImport("foo", "from.ts", "typescript", map[string]int{}, dirIdx)
	require.True(s.T(), ok)
	require.Equal(s.T(), 9, idx)
}

func (s *GraphSuite) TestResolveImportFileSuffixWithExtension() {
	// Covers the bare 'strings.HasSuffix(p, "/"+imp)' branch — the
	// import already includes the extension, so no ext-loop append is
	// needed for the match.
	idx, ok := resolveImport("util.go", "from.go", "",
		map[string]int{"src/util.go": 7}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 7, idx)
}

func (s *GraphSuite) TestIsTestPath() {
	tests := []struct {
		path string
		want bool
	}{
		{"internal/api/server_test.go", true},
		{"server_test.go", true},
		{"internal/api/server.go", false},
		{"internal/api/testdata.go", false},
		{"app/src/utils/format.test.ts", true},
		{"app/src/Chat.spec.tsx", true},
		{"app/src/utils/format.ts", false},
		{"app/src/__tests__/helpers.ts", true},
		{"__tests__/setup.js", true},
		{"app/src/my__tests__dir/x.ts", false},
		{"internal/foo/testdata/gen.go", true},
		{"testdata/fixture.ts", true},
		{"internal/mytestdata/x.go", false},
		{"internal/api/openapi.spec.go", false},
		{"internal/api/x.test.go", false},
		{"pkg/__tests__/helper.go", false},
		{"app/src/x_test.go.ts", false},
		{"app/src/mod.test.mjs", true},
		{"app/testdata/page.html", true},
		{"docs/guide.spec.md", false},
		{"pkg/test_parser.py", true},
		{"pkg/parser_test.py", true},
		{"conftest.py", true},
		{"tests/helpers.py", true},
		{"pkg/parser.py", false},
		{"pkg/latest_x.py", false},
		{"crate/tests/integration.rs", true},
		{"src/lib.rs", false},
		{"src/test_utils.rs", false},
		{"tests/helpers.go", false},
	}
	for _, tc := range tests {
		s.Run(tc.path, func() {
			require.Equal(s.T(), tc.want, IsTestPath(tc.path))
		})
	}
}

func (s *GraphSuite) TestResolvePythonImport() {
	index := map[string]int{
		"app/main.py":         0,
		"app/__init__.py":     1,
		"app/util.py":         2,
		"app/sub/__init__.py": 3,
		"app/sub/leaf.py":     4,
		"src/lib/core.py":     5,
		"lib/core.py":         6,
		"pkg/json.py":         7,
		"pkg/__init__.py":     8,
		"json.py":             9,
		"xlib/core.py":        10,
	}
	tests := []struct {
		name, imp, from string
		want            int
	}{
		{"sibling module", "./util", "app/main.py", 2},
		{"own package", "./__init__", "app/main.py", 1},
		{"parent package module", "../util", "app/sub/leaf.py", 2},
		{"relative package dir", "./sub", "app/main.py", 3},
		{"missing relative", "./nope", "app/main.py", -1},
		{"absolute module", "app/util", "src/lib/core.py", 2},
		{"absolute package", "app/sub", "lib/core.py", 3},
		{"shortest root wins", "lib/core", "app/main.py", 6},
		{"module inside a package isn't a root", "util", "app/main.py", -1},
		{"top-level module", "json", "pkg/json.py", 9},
		{"stdlib", "os", "app/main.py", -1},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			idx, ok := resolveImport(tc.imp, tc.from, "python", index, nil)
			require.Equal(s.T(), tc.want, idx)
			require.Equal(s.T(), tc.want >= 0, ok)
		})
	}
}

func (s *GraphSuite) TestResolvePythonImportTieGoesToLowestIndex() {
	// "a/m.py" and "b/m.py" aren't suffixes of each other, but both end in
	// "/m.py" at the same length; neither dir is a package.
	for range 20 {
		idx, ok := resolveImport("m", "x.py", "python", map[string]int{"a/m.py": 3, "b/m.py": 1}, nil)
		require.True(s.T(), ok)
		require.Equal(s.T(), 1, idx)
	}
}

func (s *GraphSuite) TestResolveRustImport() {
	index := map[string]int{
		"src/lib.rs":            0,
		"src/graph.rs":          1,
		"src/graph/edge.rs":     2,
		"src/util/mod.rs":       3,
		"src/util/fmt.rs":       4,
		"tool/src/main.rs":      5,
		"tool/src/cli.rs":       6,
		"loose/orphan.rs":       7,
		"src/graph/node/mod.rs": 8,
	}
	tests := []struct {
		name, imp, from string
		want            int
	}{
		{"crate item in module file", "crate::graph::Node", "src/util/fmt.rs", 1},
		{"crate nested module", "crate::graph::edge::Edge", "src/lib.rs", 2},
		{"crate mod.rs module", "crate::util", "src/graph.rs", 3},
		{"crate root itself", "crate", "src/graph.rs", 0},
		{"main.rs crate root", "crate::cli::run", "tool/src/main.rs", 6},
		{"no crate root", "crate::x", "loose/orphan.rs", -1},
		{"mod from lib.rs", "self::graph", "src/lib.rs", 1},
		{"mod from a module file", "self::edge", "src/graph.rs", 2},
		{"mod from mod.rs", "self::fmt", "src/util/mod.rs", 4},
		{"mod to a mod.rs dir", "self::node", "src/graph.rs", 8},
		{"super from a module file", "super::util", "src/graph.rs", 3},
		{"super from mod.rs", "super::graph", "src/util/mod.rs", 1},
		{"super module itself", "super", "src/graph/edge.rs", 1},
		{"super super", "super::super::util", "src/graph/node/mod.rs", 3},
		{"self then path", "self::edge::Edge", "src/graph.rs", 2},
		{"child module without prefix", "edge::Edge", "src/graph.rs", 2},
		{"external crate", "std::collections::HashMap", "src/lib.rs", -1},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			idx, ok := resolveImport(tc.imp, tc.from, "rust", index, nil)
			require.Equal(s.T(), tc.want, idx)
			require.Equal(s.T(), tc.want >= 0, ok)
		})
	}
}

func (s *GraphSuite) TestRustCrateRootAtTopLevel() {
	idx, ok := resolveImport("crate::a", "b.rs", "rust", map[string]int{"lib.rs": 0, "a.rs": 1, "b.rs": 2}, nil)
	require.True(s.T(), ok)
	require.Equal(s.T(), 1, idx)
}

func (s *GraphSuite) TestBuildResolvesPythonAndRustImports() {
	g := Build([]*parser.FileFacts{
		{Path: "app/__init__.py", Language: "python"},
		{Path: "app/main.py", Language: "python", Imports: []parser.Import{{Path: "./util"}, {Path: "os"}}},
		{Path: "app/util.py", Language: "python"},
		{Path: "src/lib.rs", Language: "rust", Imports: []parser.Import{{Path: "self::graph"}, {Path: "std::fmt"}}},
		{Path: "src/graph.rs", Language: "rust", Imports: []parser.Import{{Path: "crate"}}},
	})
	paths := func(e Edge) [2]string { return [2]string{g.Nodes[e.FromIndex].Path, g.Nodes[e.ToIndex].Path} }
	var got [][2]string
	for _, e := range g.Edges {
		got = append(got, paths(e))
	}
	require.Equal(s.T(), [][2]string{
		{"app/main.py", "app/util.py"},
		{"src/graph.rs", "src/lib.rs"},
		{"src/lib.rs", "src/graph.rs"},
	}, got)
}
