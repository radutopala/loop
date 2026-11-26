package graph

import (
	"math"
	"path"
	"sort"
	"strings"

	"github.com/radutopala/loop/internal/quality/parser"
)

// Graph is the structural snapshot the metrics package reduces to numbers.
// Nodes carry the parser's per-file extracts; Edges are file→file import
// relationships resolved against in-repo paths only; Modules cluster Nodes
// by top-level path segment for the modularity metric and the C4 view.
type Graph struct {
	// Nodes is sorted by Path; Index maps Path → slice position.
	Nodes []*Node
	Index map[string]int

	// Edges are deduped, self-loops dropped, sorted by (From, To).
	Edges []Edge

	// Modules cluster Nodes by their top-level segment. Sorted by Name;
	// each module's NodeIndices is ascending.
	Modules []*Module

	// ParseFailed counts files the parser could not handle. Surfaced via
	// the parse_fail rule and /debug/quality/stats.
	ParseFailed int
}

// Node is one scanned file's slot.
type Node struct {
	Path        string
	Module      string
	Language    string
	LOC         int
	Functions   []parser.Function
	Types       []parser.TypeDecl
	Calls       []parser.Call
	ParseFailed bool
}

// Edge is a directed import between two Nodes.
type Edge struct {
	FromIndex int
	ToIndex   int
}

// Module is a coarse cluster of Nodes sharing a top-level path segment.
type Module struct {
	Name        string
	NodeIndices []int
}

// Build assembles a Graph from per-file parser facts. Files with
// ParseFailed=true are kept (the file count and treemap stay accurate)
// but contribute no edges. External imports — those that don't resolve
// to a scanned file — are silently dropped: the graph models in-repo
// dependencies only.
func Build(facts []*parser.FileFacts) *Graph {
	sorted := make([]*parser.FileFacts, len(facts))
	copy(sorted, facts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	g := &Graph{Index: make(map[string]int, len(sorted))}
	for _, f := range sorted {
		n := &Node{
			Path:        f.Path,
			Module:      moduleOf(f.Path),
			Language:    f.Language,
			LOC:         f.LOC,
			Functions:   f.Functions,
			Types:       f.Types,
			Calls:       f.Calls,
			ParseFailed: f.ParseFailed,
		}
		g.Index[f.Path] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
		if f.ParseFailed {
			g.ParseFailed++
		}
	}

	g.Edges = buildEdges(sorted, g.Index)
	g.Modules = buildModules(g.Nodes)
	return g
}

// moduleOf returns the cluster name for a path. Files in a subdirectory
// take the first segment ("internal/api/x.go" → "internal"); top-level
// files cluster under their basename minus extension so the cluster is
// never empty.
func moduleOf(p string) string {
	if head, _, ok := strings.Cut(p, "/"); ok {
		return head
	}
	if ext := path.Ext(p); ext != "" {
		return strings.TrimSuffix(p, ext)
	}
	return p
}

func buildEdges(sorted []*parser.FileFacts, index map[string]int) []Edge {
	type key struct{ from, to int }
	seen := make(map[key]struct{})
	var out []Edge

	dirIndex := buildDirIndex(sorted)

	for _, f := range sorted {
		if f.ParseFailed {
			continue
		}
		fromIdx := index[f.Path]
		for _, imp := range f.Imports {
			toIdx, ok := resolveImport(imp.Path, f.Path, f.Language, index, dirIndex)
			if !ok || toIdx == fromIdx {
				continue
			}
			k := key{from: fromIdx, to: toIdx}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, Edge{FromIndex: fromIdx, ToIndex: toIdx})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].FromIndex != out[j].FromIndex {
			return out[i].FromIndex < out[j].FromIndex
		}
		return out[i].ToIndex < out[j].ToIndex
	})
	return out
}

// candidateExts is the set of source extensions resolveImport will append
// when an import has no extension (TypeScript and JavaScript both omit
// extensions in import strings; Go imports use package paths without an
// extension by definition).
var candidateExts = []string{".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}

// resolveImport maps an import string to a Node index. Strategy:
//  1. exact path hit (rare but cheap);
//  2. relative imports ("./x", "../y") resolve against the importer's
//     directory, with extension fallback;
//  3. directory-suffix match against the dirIndex (Go package imports
//     like "github.com/radutopala/loop/internal/api" → some file in
//     internal/api/);
//  4. file-suffix match (module-style TS/JS imports without extension).
//
// fromLang is the importer's language ("go", "typescript", "javascript",
// "python", "rust", or ""). Python and Rust have their own resolvers,
// since their import paths follow module rules rather than file paths. Go bare imports (no slash) are always stdlib (e.g. "embed",
// "fmt") and never resolve to a local file — without that guard, the
// fuzzy file-suffix match in steps 3–4 would falsely tie any local
// `*/embed.go` to every Go file that imports the stdlib `embed` package
// (and similarly for any other stdlib name shadowed by a same-basename
// local file), producing phantom edges and phantom cycles.
//
// Returns (-1, false) for external imports — engine drops them.
func resolveImport(impPath, fromPath, fromLang string, index map[string]int, dirIndex map[string]int) (int, bool) {
	imp := strings.TrimSpace(impPath)
	if imp == "" {
		return -1, false
	}
	if idx, ok := index[imp]; ok {
		return idx, true
	}
	switch fromLang {
	case "python":
		return resolvePythonImport(imp, fromPath, index)
	case "rust":
		return resolveRustImport(imp, fromPath, index)
	}

	if strings.HasPrefix(imp, "./") || strings.HasPrefix(imp, "../") {
		joined := path.Clean(path.Join(path.Dir(fromPath), imp))
		if idx, ok := index[joined]; ok {
			return idx, true
		}
		for _, ext := range candidateExts {
			if idx, ok := index[joined+ext]; ok {
				return idx, true
			}
		}
		return -1, false
	}

	if fromLang == "go" && !strings.Contains(imp, "/") {
		return -1, false
	}

	// Several entries can match, so the pick is ranked and ties go to the
	// lowest index; the edge doesn't depend on map order. A package dir
	// match prefers the longest dir (internal/api over api for an import
	// ending in /internal/api); a file match prefers the shortest path
	// (src/util.ts over packages/foo/src/util.ts for "src/util").
	best, bestRank := -1, math.MinInt
	pick := func(idx, rank int) {
		if rank > bestRank || (rank == bestRank && idx < best) {
			best, bestRank = idx, rank
		}
	}
	for dir, idx := range dirIndex {
		if dir == imp || strings.HasSuffix(imp, "/"+dir) {
			pick(idx, len(dir))
		}
	}
	if best >= 0 {
		return best, true
	}

	for p, idx := range index {
		// p == imp is covered by the early index[imp] lookup above; the
		// remaining cases here are tail-suffix matches without and with
		// a tacked-on extension.
		if strings.HasSuffix(p, "/"+imp) {
			pick(idx, -len(p))
			continue
		}
		for _, ext := range candidateExts {
			if p == imp+ext || strings.HasSuffix(p, "/"+imp+ext) {
				pick(idx, -len(p))
				break
			}
		}
	}
	return best, best >= 0
}

// resolvePythonImport resolves a module path from parser.pythonImportPaths
// ("a/b", "./sib", "../pkg/__init__") to a module file or a package's
// __init__.py. A relative path resolves against the importer's directory.
// An absolute one matches by path suffix, but only from a directory that
// isn't itself a package (no __init__.py): Python imports a module by its
// full name from a source root, so "util" never names "pkg/util.py". The
// shortest match wins, ties going to the lowest index.
func resolvePythonImport(imp, fromPath string, index map[string]int) (int, bool) {
	candidates := []string{imp + ".py", imp + "/__init__.py"}
	if strings.HasPrefix(imp, "./") || strings.HasPrefix(imp, "../") {
		for _, c := range candidates {
			if idx, ok := index[path.Join(path.Dir(fromPath), c)]; ok {
				return idx, true
			}
		}
		return -1, false
	}
	best, bestLen := -1, math.MaxInt
	for p, idx := range index {
		for _, c := range candidates {
			root, ok := strings.CutSuffix(p, c)
			if !ok || (root != "" && !strings.HasSuffix(root, "/")) {
				continue
			}
			if _, pkg := index[root+"__init__.py"]; pkg && root != "" {
				continue
			}
			if len(p) < bestLen || (len(p) == bestLen && idx < best) {
				best, bestLen = idx, len(p)
			}
		}
	}
	return best, best >= 0
}

// resolveRustImport resolves a `use` or `mod` path from
// parser.rustImportPaths to the file of the deepest module it names.
// "crate::" starts at the crate root (the nearest directory up with a
// lib.rs or main.rs), "self::" at the importer's own module, and each
// "super::" one module up. Any other path is tried from the importer's
// own module, for a child declared with `mod`; external crates such as
// std find no file and are dropped.
func resolveRustImport(imp, fromPath string, index map[string]int) (int, bool) {
	segs := strings.Split(imp, "::")
	base := rustModuleDir(fromPath)
	minDepth := 1
	switch segs[0] {
	case "crate":
		root, ok := rustCrateRoot(fromPath, index)
		if !ok {
			return -1, false
		}
		base, segs, minDepth = root, segs[1:], 0
	case "self", "super":
		for len(segs) > 0 && segs[0] == "super" {
			base = path.Dir(base)
			segs = segs[1:]
		}
		if len(segs) > 0 && segs[0] == "self" {
			segs = segs[1:]
		}
		minDepth = 0
	}
	// The path's tail can name an item (a type or fn) rather than a
	// module, so try the longest module prefix first.
	for depth := len(segs); depth >= minDepth; depth-- {
		dir := path.Join(append([]string{base}, segs[:depth]...)...)
		for _, c := range []string{dir + ".rs", dir + "/mod.rs", dir + "/lib.rs", dir + "/main.rs"} {
			if idx, ok := index[c]; ok {
				return idx, true
			}
		}
	}
	return -1, false
}

// rustModuleDir is the directory holding a Rust file's child modules:
// a mod.rs, lib.rs or main.rs file's own directory, and "x/foo" for any
// other "x/foo.rs".
func rustModuleDir(p string) string {
	switch path.Base(p) {
	case "mod.rs", "lib.rs", "main.rs":
		return path.Dir(p)
	}
	return strings.TrimSuffix(p, ".rs")
}

// rustCrateRoot walks up from p's directory to the first one with a
// lib.rs or main.rs.
func rustCrateRoot(p string, index map[string]int) (string, bool) {
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		for _, root := range []string{"lib.rs", "main.rs"} {
			if _, ok := index[path.Join(dir, root)]; ok {
				return dir, true
			}
		}
		if dir == "." || dir == "/" {
			return "", false
		}
	}
}

// jsExts are the JS/TS source extensions whose test naming conventions
// IsTestPath applies.
var jsExts = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".mts": true, ".cts": true,
}

// IsTestPath reports whether the slash-separated path p is a test file.
// Each naming convention applies to its own language: Go's *_test.go;
// JS/TS *.test.*, *.spec.* and files under a __tests__ dir; Python's
// test_*.py, *_test.py, conftest.py and files under a tests dir; and
// Rust files under a tests dir (integration tests). Anything
// under a testdata dir counts whatever its language, since it holds
// fixtures (Go tooling skips the dir).
func IsTestPath(p string) bool {
	if underDir(p, "testdata") {
		return true
	}
	base := path.Base(p)
	ext := path.Ext(base)
	switch {
	case ext == ".go":
		return strings.HasSuffix(base, "_test.go")
	case jsExts[ext]:
		return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || underDir(p, "__tests__")
	case ext == ".py":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "conftest.py" || underDir(p, "tests")
	case ext == ".rs":
		return underDir(p, "tests")
	}
	return false
}

// underDir reports whether p sits below a directory named dir.
func underDir(p, dir string) bool {
	return strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/")
}

// buildDirIndex maps each unique directory of a scanned file to the
// lowest-indexed non-test file in that directory (falling back to test
// files only when a directory has no production files). Used by
// resolveImport to back Go-style package imports (which name a directory,
// not a file), so an import of ".../internal/plugin" resolves to a
// production file rather than an external *_test.go file whose own
// subpackage imports would fabricate a cycle.
// "Lowest index" is deterministic because the input is pre-sorted.
func buildDirIndex(sorted []*parser.FileFacts) map[string]int {
	out := make(map[string]int)
	for pass := range 2 {
		for i, f := range sorted {
			dir := path.Dir(f.Path)
			if dir == "." || dir == "/" {
				continue
			}
			if _, exists := out[dir]; exists {
				continue
			}
			if pass == 0 && IsTestPath(f.Path) {
				continue
			}
			out[dir] = i
		}
	}
	return out
}

func buildModules(nodes []*Node) []*Module {
	byName := make(map[string]*Module)
	for i, n := range nodes {
		m, ok := byName[n.Module]
		if !ok {
			m = &Module{Name: n.Module}
			byName[n.Module] = m
		}
		m.NodeIndices = append(m.NodeIndices, i)
	}
	out := make([]*Module, 0, len(byName))
	for _, m := range byName {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
