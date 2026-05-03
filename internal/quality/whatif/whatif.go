// Package whatif simulates the effect of a structural mutation on the
// quality_signal without touching the cached graph. The caller hands us
// a list of mutations (delete a file, move a file between modules,
// split a file into N parts); we project the graph into a path-based
// shadow form, apply the mutations, reassemble, recompute all 5
// metrics, and return the baseline-vs-predicted breakdown plus the
// signed delta.
//
// The shadow graph is discarded on return — mutations never touch the
// cache the engine writes to. This keeps the cost predictable (one full
// metric pass per call) and the side-effects nil.
package whatif

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
)

// Op is the supported mutation type. Stable string values back the JSON
// surface (MCP/HTTP/CLI all marshal Mutation as `{"op": "...", ...}`).
type Op string

const (
	// OpDelete removes a single file (and all edges touching it) from
	// the shadow graph. Used to predict "what's the signal if I delete
	// this dead file?".
	OpDelete Op = "delete"

	// OpMove relocates a file's module clustering — useful for "what
	// happens to modularity if I move this file from internal/api to
	// internal/handlers?". Edges and the file itself are preserved;
	// only the Node.Module field changes.
	OpMove Op = "move"

	// OpSplit replaces one file with N synthetic part-files,
	// distributing the original outgoing edges round-robin and
	// duplicating incoming edges to every part. Used to predict
	// "is splitting this 2k-line god-file worth it?".
	OpSplit Op = "split"
)

// Mutation is one structural edit. Json-serializable so the same shape
// flows through MCP arguments, HTTP requests, and CLI flags.
type Mutation struct {
	// Op picks the operation. Required.
	Op Op `json:"op"`

	// Path is the existing file path to mutate. Required for every Op.
	Path string `json:"path"`

	// NewModule is the destination module for OpMove. Empty for other Ops.
	NewModule string `json:"new_module,omitempty"`

	// Parts is the number of synthetic files OpSplit produces. Must be
	// ≥2 for OpSplit; ignored by other Ops.
	Parts int `json:"parts,omitempty"`
}

// Result is the predicted-vs-baseline breakdown the surface returns.
// Both Signals are 0–10000; DeltaSignal is the signed difference
// (positive = healthier after the mutation, negative = worse).
type Result struct {
	Mutations        []Mutation       `json:"mutations"`
	BaselineSignal   int              `json:"baseline_signal"`
	PredictedSignal  int              `json:"predicted_signal"`
	DeltaSignal      int              `json:"delta_signal"`
	BaselineMetrics  []metrics.Result `json:"baseline_metrics"`
	PredictedMetrics []metrics.Result `json:"predicted_metrics"`
}

// ErrEmptyGraph is returned when the caller hands us a nil/zero-node
// graph — there's nothing to simulate against. Callers usually swallow
// this and surface "no snapshot yet; scan first" to the user.
var ErrEmptyGraph = errors.New("whatif: graph is empty")

// Simulate clones g into a path-based shadow, applies muts in order,
// reassembles, and returns the predicted metric breakdown. Errors from
// individual mutations short-circuit and surface to the caller (so a
// typo'd path doesn't silently produce misleading numbers).
func Simulate(g *graph.Graph, muts []Mutation) (Result, error) {
	if g == nil || len(g.Nodes) == 0 {
		return Result{}, ErrEmptyGraph
	}
	baseline := metrics.Compute(g)

	shadow := projectToShadow(g)
	for i, m := range muts {
		if err := shadow.apply(m); err != nil {
			return Result{}, fmt.Errorf("mutation %d (%s %s): %w", i, m.Op, m.Path, err)
		}
	}
	predicted := metrics.Compute(shadow.assemble())

	return Result{
		Mutations:        muts,
		BaselineSignal:   baseline.Value,
		PredictedSignal:  predicted.Value,
		DeltaSignal:      predicted.Value - baseline.Value,
		BaselineMetrics:  baseline.Metrics,
		PredictedMetrics: predicted.Metrics,
	}, nil
}

// shadow is the path-based projection mutations operate on. Edges are
// (fromPath, toPath) so re-sorting nodes never invalidates them.
type shadow struct {
	nodes []*graph.Node
	edges []edgeSpec
}

type edgeSpec struct {
	from string
	to   string
}

func projectToShadow(g *graph.Graph) *shadow {
	out := &shadow{
		nodes: make([]*graph.Node, len(g.Nodes)),
		edges: make([]edgeSpec, 0, len(g.Edges)),
	}
	for i, n := range g.Nodes {
		nn := *n
		out.nodes[i] = &nn
	}
	for _, e := range g.Edges {
		out.edges = append(out.edges, edgeSpec{
			from: g.Nodes[e.FromIndex].Path,
			to:   g.Nodes[e.ToIndex].Path,
		})
	}
	return out
}

func (s *shadow) apply(m Mutation) error {
	switch m.Op {
	case OpDelete:
		return s.applyDelete(m.Path)
	case OpMove:
		return s.applyMove(m.Path, m.NewModule)
	case OpSplit:
		return s.applySplit(m.Path, m.Parts)
	default:
		return fmt.Errorf("unsupported op %q", m.Op)
	}
}

func (s *shadow) applyDelete(p string) error {
	if !s.hasNode(p) {
		return fmt.Errorf("path not in graph: %s", p)
	}
	keptNodes := s.nodes[:0]
	for _, n := range s.nodes {
		if n.Path == p {
			continue
		}
		keptNodes = append(keptNodes, n)
	}
	s.nodes = keptNodes

	keptEdges := s.edges[:0]
	for _, e := range s.edges {
		if e.from == p || e.to == p {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	s.edges = keptEdges
	return nil
}

func (s *shadow) applyMove(p, newModule string) error {
	if newModule == "" {
		return errors.New("move: new_module is required")
	}
	for _, n := range s.nodes {
		if n.Path == p {
			n.Module = newModule
			return nil
		}
	}
	return fmt.Errorf("path not in graph: %s", p)
}

func (s *shadow) applySplit(p string, parts int) error {
	if parts < 2 {
		return errors.New("split: parts must be ≥2")
	}
	var src *graph.Node
	for _, n := range s.nodes {
		if n.Path == p {
			src = n
			break
		}
	}
	if src == nil {
		return fmt.Errorf("path not in graph: %s", p)
	}
	loc := src.LOC / parts
	newPaths := make([]string, parts)
	newNodes := make([]*graph.Node, parts)
	for i := range parts {
		newPaths[i] = synthSplitPath(p, i+1)
		nn := *src
		nn.Path = newPaths[i]
		nn.LOC = loc
		newNodes[i] = &nn
	}

	keptNodes := s.nodes[:0]
	for _, n := range s.nodes {
		if n.Path == p {
			continue
		}
		keptNodes = append(keptNodes, n)
	}
	keptNodes = append(keptNodes, newNodes...)
	s.nodes = keptNodes

	out := make([]edgeSpec, 0, len(s.edges)+parts)
	outgoingFromSrc := 0
	for _, e := range s.edges {
		switch {
		case e.from == p && e.to == p:
			continue
		case e.from == p:
			out = append(out, edgeSpec{from: newPaths[outgoingFromSrc%parts], to: e.to})
			outgoingFromSrc++
		case e.to == p:
			for _, np := range newPaths {
				out = append(out, edgeSpec{from: e.from, to: np})
			}
		default:
			out = append(out, e)
		}
	}
	s.edges = out
	return nil
}

func (s *shadow) hasNode(p string) bool {
	for _, n := range s.nodes {
		if n.Path == p {
			return true
		}
	}
	return false
}

// assemble reconstructs a graph.Graph from the mutated shadow form. The
// output mirrors graph.Build's invariants: Nodes sorted by Path, Index
// maps Path → slot, Edges deduped + sorted by (From, To), Modules
// clustered by top-level segment with NodeIndices ascending.
func (s *shadow) assemble() *graph.Graph {
	sort.Slice(s.nodes, func(i, j int) bool { return s.nodes[i].Path < s.nodes[j].Path })

	g := &graph.Graph{
		Nodes: s.nodes,
		Index: make(map[string]int, len(s.nodes)),
	}
	for i, n := range s.nodes {
		g.Index[n.Path] = i
		if n.ParseFailed {
			g.ParseFailed++
		}
	}

	type key struct{ from, to int }
	seen := make(map[key]struct{}, len(s.edges))
	out := make([]graph.Edge, 0, len(s.edges))
	for _, e := range s.edges {
		fi, fok := g.Index[e.from]
		ti, tok := g.Index[e.to]
		if !fok || !tok || fi == ti {
			continue
		}
		k := key{from: fi, to: ti}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, graph.Edge{FromIndex: fi, ToIndex: ti})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromIndex != out[j].FromIndex {
			return out[i].FromIndex < out[j].FromIndex
		}
		return out[i].ToIndex < out[j].ToIndex
	})
	g.Edges = out

	byName := make(map[string][]int)
	for i, n := range g.Nodes {
		byName[n.Module] = append(byName[n.Module], i)
	}
	mods := make([]*graph.Module, 0, len(byName))
	for name, idxs := range byName {
		mods = append(mods, &graph.Module{Name: name, NodeIndices: idxs})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Name < mods[j].Name })
	g.Modules = mods
	return g
}

// synthSplitPath produces a deterministic part filename for OpSplit.
// "internal/foo/bar.go" with i=1 → "internal/foo/bar.part1.go".
func synthSplitPath(orig string, i int) string {
	dir := path.Dir(orig)
	base := path.Base(orig)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if dir == "." {
		return fmt.Sprintf("%s.part%d%s", stem, i, ext)
	}
	return fmt.Sprintf("%s/%s.part%d%s", dir, stem, i, ext)
}
