// Package c4 emits a Mermaid flowchart from a quality graph, clustering
// nodes by their containing directory (Go package boundary) and drawing
// cross-package import edges. Top-level segments (e.g. `internal`,
// `cmd`) wrap their packages in Mermaid `subgraph` blocks so the layout
// preserves the repository's tree shape.
//
// The plan deliberately scopes this to the Component layer only —
// Context (external systems) and Container (deployment) require domain
// knowledge the engine doesn't have, and the Code layer at panel scale
// duplicates the treemap.
//
// We use Mermaid's `flowchart LR` syntax (left-to-right). Mermaid does
// not have a `componentDiagram` primitive — `flowchart` is the standard
// way to render labelled nodes with directed edges, which is exactly
// the C4 Component layer's shape.
package c4

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/radutopala/loop/internal/quality/graph"
)

// Diagram is the JSON-serializable result. Mermaid carries the diagram
// text; ComponentCount and EdgeCount let surfaces show a one-line
// summary ("3 components, 5 edges") without re-parsing the body.
type Diagram struct {
	Mermaid        string `json:"mermaid"`
	ComponentCount int    `json:"component_count"`
	EdgeCount      int    `json:"edge_count"`
}

// Emit produces a flowchart for g. An empty or nil graph yields a
// single "no components" placeholder node so the surface always has
// something to render.
func Emit(g *graph.Graph) Diagram {
	if g == nil || len(g.Nodes) == 0 {
		return emptyDiagram()
	}

	pkgOf := make([]string, len(g.Nodes))
	pkgSet := make(map[string]struct{})
	for i, n := range g.Nodes {
		p := packageOf(n.Path)
		pkgOf[i] = p
		pkgSet[p] = struct{}{}
	}

	pkgs := make([]string, 0, len(pkgSet))
	for p := range pkgSet {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	groups := make(map[string][]string)
	for _, p := range pkgs {
		head, _, _ := strings.Cut(p, "/")
		groups[head] = append(groups[head], p)
	}
	heads := make([]string, 0, len(groups))
	for h := range groups {
		heads = append(heads, h)
	}
	sort.Strings(heads)

	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for _, h := range heads {
		members := groups[h]
		if len(members) == 1 && members[0] == h {
			fmt.Fprintf(&b, "  %s[%q]\n", componentID(members[0]), members[0])
			continue
		}
		fmt.Fprintf(&b, "  subgraph %s[%q]\n", componentID("g_"+h), h)
		for _, m := range members {
			fmt.Fprintf(&b, "    %s[%q]\n", componentID(m), m)
		}
		b.WriteString("  end\n")
	}

	type pair struct{ from, to string }
	seen := make(map[pair]struct{})
	pairs := make([]pair, 0)
	for _, e := range g.Edges {
		from := pkgOf[e.FromIndex]
		to := pkgOf[e.ToIndex]
		if from == "" || to == "" || from == to {
			continue
		}
		k := pair{from: from, to: to}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		pairs = append(pairs, k)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].from != pairs[j].from {
			return pairs[i].from < pairs[j].from
		}
		return pairs[i].to < pairs[j].to
	})
	for _, p := range pairs {
		fmt.Fprintf(&b, "  %s --> %s\n", componentID(p.from), componentID(p.to))
	}

	return Diagram{
		Mermaid:        b.String(),
		ComponentCount: len(pkgs),
		EdgeCount:      len(pairs),
	}
}

func emptyDiagram() Diagram {
	return Diagram{Mermaid: "flowchart LR\n  empty[\"no components\"]\n"}
}

// packageOf returns the cluster name for a path. Subdirectory files use
// their containing directory ("internal/quality/c4/c4.go" →
// "internal/quality/c4"); top-level files cluster under their basename
// minus extension so the cluster is never empty.
func packageOf(p string) string {
	dir := path.Dir(p)
	if dir == "." || dir == "/" {
		if ext := path.Ext(p); ext != "" {
			return strings.TrimSuffix(p, ext)
		}
		return p
	}
	return dir
}

// componentID maps a package name to a Mermaid-safe identifier. Slashes,
// hyphens, and other separators collapse to underscores; identifiers
// starting with a digit are prefixed with "m_" so Mermaid accepts them.
func componentID(name string) string {
	if name == "" {
		return "anon"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	id := b.String()
	if len(id) == 0 || (id[0] >= '0' && id[0] <= '9') {
		id = "m_" + id
	}
	return id
}
