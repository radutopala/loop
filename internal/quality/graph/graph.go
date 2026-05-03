package graph

import (
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
			toIdx, ok := resolveImport(imp.Path, f.Path, index, dirIndex)
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
// Returns (-1, false) for external imports — engine drops them.
func resolveImport(impPath, fromPath string, index map[string]int, dirIndex map[string]int) (int, bool) {
	imp := strings.TrimSpace(impPath)
	if imp == "" {
		return -1, false
	}
	if idx, ok := index[imp]; ok {
		return idx, true
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

	for dir, idx := range dirIndex {
		if dir == imp || strings.HasSuffix(imp, "/"+dir) {
			return idx, true
		}
	}

	for p, idx := range index {
		// p == imp is covered by the early index[imp] lookup above; the
		// remaining cases here are tail-suffix matches without and with
		// a tacked-on extension.
		if strings.HasSuffix(p, "/"+imp) {
			return idx, true
		}
		for _, ext := range candidateExts {
			if p == imp+ext || strings.HasSuffix(p, "/"+imp+ext) {
				return idx, true
			}
		}
	}
	return -1, false
}

// buildDirIndex maps each unique directory of a scanned file to the
// lowest-indexed file in that directory. Used by resolveImport to back
// Go-style package imports (which name a directory, not a file).
// "Lowest index" is deterministic because the input is pre-sorted.
func buildDirIndex(sorted []*parser.FileFacts) map[string]int {
	out := make(map[string]int)
	for i, f := range sorted {
		dir := path.Dir(f.Path)
		if dir == "." || dir == "/" {
			continue
		}
		if _, exists := out[dir]; exists {
			continue
		}
		out[dir] = i
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
