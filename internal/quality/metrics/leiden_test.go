package metrics

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LeidenSuite struct {
	suite.Suite
}

func TestLeidenSuite(t *testing.T) {
	suite.Run(t, new(LeidenSuite))
}

// Empty / degenerate inputs return trivial partitions instead of
// crashing — callers (Modularity, modularityFileDrag) rely on never
// having to nil-check the result.
func (s *LeidenSuite) TestEmptyGraphReturnsNil() {
	require.Nil(s.T(), detectCommunities(graph.Build(nil)))
}

func (s *LeidenSuite) TestNoEdgesPutsEachNodeInOwnCommunity() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"},
	})
	got := detectCommunities(g)
	require.Equal(s.T(), []int{0, 1, 2}, got)
}

// Two disjoint cliques: Leiden must put each clique in its own
// community. This is the algorithm's defining behaviour — recovering
// communities from edge structure alone.
func (s *LeidenSuite) TestTwoDisjointTrianglesSplitCleanly() {
	g := graph.Build([]*parser.FileFacts{
		// Triangle 1: a, b, c
		{Path: "a.go", Imports: []parser.Import{{Path: "./b"}, {Path: "./c"}}},
		{Path: "b.go", Imports: []parser.Import{{Path: "./a"}, {Path: "./c"}}},
		{Path: "c.go", Imports: []parser.Import{{Path: "./a"}, {Path: "./b"}}},
		// Triangle 2: d, e, f
		{Path: "d.go", Imports: []parser.Import{{Path: "./e"}, {Path: "./f"}}},
		{Path: "e.go", Imports: []parser.Import{{Path: "./d"}, {Path: "./f"}}},
		{Path: "f.go", Imports: []parser.Import{{Path: "./d"}, {Path: "./e"}}},
	})
	got := detectCommunities(g)
	require.Len(s.T(), got, 6)
	// Same community within each triangle.
	require.Equal(s.T(), got[0], got[1])
	require.Equal(s.T(), got[0], got[2])
	require.Equal(s.T(), got[3], got[4])
	require.Equal(s.T(), got[3], got[5])
	// Different community across triangles.
	require.NotEqual(s.T(), got[0], got[3])
}

// Single-language project laid out as one top-level dir with two
// import-coupled sub-clusters: directory-based clustering would lump
// everything into "internal", giving Q ≈ 0; Leiden ignores the path
// and recovers the import structure.
func (s *LeidenSuite) TestRecoversSubModulesUnderSingleTopLevelDir() {
	g := graph.Build([]*parser.FileFacts{
		// Cluster "auth"
		{Path: "internal/auth/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "internal/auth/b.go", Imports: []parser.Import{{Path: "./a"}}},
		// Cluster "store"
		{Path: "internal/store/x.go", Imports: []parser.Import{{Path: "./y"}}},
		{Path: "internal/store/y.go", Imports: []parser.Import{{Path: "./x"}}},
	})
	r := Modularity(g)
	require.Greater(s.T(), r.Raw, 0.3, "Leiden should recover the two coupled sub-modules")
	d, ok := r.Detail.(ModularityDetail)
	require.True(s.T(), ok, "Detail should be ModularityDetail")
	require.Len(s.T(), d.Communities, 4)
	require.Equal(s.T(), 2, d.NumCommunities)
}

// Determinism: the same graph fed to Leiden twice must produce
// identical partitions. Without stable iteration order over candidate
// communities and ties broken by lower id, this fails on Go's
// randomised map iteration.
func (s *LeidenSuite) TestDeterministicAcrossRuns() {
	build := func() *graph.Graph {
		return graph.Build([]*parser.FileFacts{
			{Path: "a.go", Imports: []parser.Import{{Path: "./b"}, {Path: "./c"}}},
			{Path: "b.go", Imports: []parser.Import{{Path: "./a"}, {Path: "./c"}}},
			{Path: "c.go", Imports: []parser.Import{{Path: "./a"}, {Path: "./b"}}},
			{Path: "d.go", Imports: []parser.Import{{Path: "./e"}}},
			{Path: "e.go", Imports: []parser.Import{{Path: "./d"}}},
		})
	}
	first := detectCommunities(build())
	for range 20 {
		require.Equal(s.T(), first, detectCommunities(build()))
	}
}

// Renumbering: communities must be 0..K-1 with the lowest-index node
// in each community winning the lower id. The diagnostics view depends
// on this for stable colour assignment across rescans.
func (s *LeidenSuite) TestCommunitiesAreRenumberedFromZero() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "b.go", Imports: []parser.Import{{Path: "./a"}}},
		{Path: "c.go", Imports: []parser.Import{{Path: "./d"}}},
		{Path: "d.go", Imports: []parser.Import{{Path: "./c"}}},
	})
	got := detectCommunities(g)
	// First node always lands in community 0.
	require.Equal(s.T(), 0, got[0])
	// Second community appears as 1 (if any), and so on — never skip.
	maxSeen := 0
	for _, c := range got {
		require.LessOrEqual(s.T(), c, maxSeen+1, "community ids must be contiguous from 0")
		if c > maxSeen {
			maxSeen = c
		}
	}
}

// modularityFileDrag must use the partition from the Modularity result's
// Detail so the tile diagnostics align with the headline metric. If we
// fed it the directory-based clustering instead, a file living in a
// "good" directory but coupled to a different community would report
// drag 0 even though it's hurting Q.
func (s *LeidenSuite) TestFileDragUsesLeidenPartition() {
	g := graph.Build([]*parser.FileFacts{
		// Two coupled pairs — Leiden finds 2 communities.
		{Path: "x/a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "x/b.go", Imports: []parser.Import{{Path: "./a"}}},
		{Path: "y/c.go", Imports: []parser.Import{{Path: "./d"}}},
		{Path: "y/d.go", Imports: []parser.Import{{Path: "./c"}}},
	})
	r := Modularity(g)
	drag := modularityFileDrag(g, &r)
	// All edges are intra-community → every file's drag is 0.
	for _, n := range g.Nodes {
		require.Equal(s.T(), 0.0, drag[n.Path], "file %s should have drag 0", n.Path)
	}
}

// Defensive: a missing or wrong-shaped Detail falls back to "each node
// its own community", which makes every cross-community edge count and
// effectively zeroes modularity's tile contribution rather than panicking.
func (s *LeidenSuite) TestFileDragFallsBackWithoutDetail() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "b.go", Imports: []parser.Import{{Path: "./a"}}},
	})
	drag := modularityFileDrag(g, nil)
	// Without a partition, the trivial "each node its own community"
	// makes every edge cross — drag is 1.0 for both files.
	require.Equal(s.T(), 1.0, drag["a.go"])
	require.Equal(s.T(), 1.0, drag["b.go"])
}

func (s *LeidenSuite) TestFileDragIgnoresWrongShapeDetail() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "b.go", Imports: []parser.Import{{Path: "./a"}}},
	})
	r := &Result{Name: ModularityName, Detail: "not the right type"}
	drag := modularityFileDrag(g, r)
	require.Equal(s.T(), 1.0, drag["a.go"])
}

// Self-loops in the input edge list (defensive — graph.Build drops
// these, but the algorithm should handle them anyway) must not skew Q.
func (s *LeidenSuite) TestSelfLoopsAreIgnored() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Imports: []parser.Import{{Path: "./b"}}},
		{Path: "b.go", Imports: []parser.Import{{Path: "./a"}}},
	})
	got := detectCommunities(g)
	require.Equal(s.T(), got[0], got[1])
}

// Refinement gate (a): a node v is well-connected to C\{v} iff
// edges(v, C\{v}) ≥ k_v·(k_C − k_v) / 2m. The hub-scoped graph below
// puts v=1 (high-degree, mostly external) in phase-1 community 0 with
// just one in-community edge (to v=2); v=1's gate (a) test fails so
// it stays in its singleton refined community. Phase-1 community is
// fed manually because we're white-box-testing the refinement.
//
// The same fixture also triggers gate (b) failure for v=2's evaluation:
// after v=2 is removed from its singleton, the candidate refined
// community {1} (kR = degree(1), edges to C\{1} = 1) flunks the
// connectivity threshold and is filtered out of v=2's move set.
func (s *LeidenSuite) TestRefinementGatesFilterWeaklyConnectedMembers() {
	// Build adjacency by hand to keep edge weights / structure exact:
	//   0 — 2,  0 — 8                       (0 anchored in C, 1 external edge)
	//   1 — 2,  1 — 3,  1 — 4,  1 — 5,  1 — 6  (1 high-degree, only 1 edge in C)
	//   2 — 7                                (2 has 1 external edge)
	// C = {0, 1, 2}; nodes 3..8 sit in their own singleton phase-1 communities.
	adj := make([][]neighbour, 9)
	add := func(a, b int) {
		adj[a] = append(adj[a], neighbour{b, 1})
		adj[b] = append(adj[b], neighbour{a, 1})
	}
	add(0, 2)
	add(0, 8)
	add(1, 2)
	add(1, 3)
	add(1, 4)
	add(1, 5)
	add(1, 6)
	add(2, 7)
	twoM := 2.0 * 8 // 8 edges

	phase1 := []int{0, 0, 0, 1, 2, 3, 4, 5, 6}
	refined := refinePartition(adj, phase1, twoM)

	// v=1's gate (a) failure keeps it in its own refined singleton — it
	// must not have been merged into 0 or 2's communities.
	require.NotEqual(s.T(), refined[1], refined[0], "v=1 should stay isolated after gate (a)")
	require.NotEqual(s.T(), refined[1], refined[2], "v=1 should stay isolated after gate (a)")

	// 0 and 2 are both well-connected to each other (each contributes 1
	// in-C edge to the other), so they should land in the same refined
	// community even though the merger between {0,2} and {1} is rejected.
	require.Equal(s.T(), refined[0], refined[2], "0 and 2 should merge (both pass gate (a))")
}
