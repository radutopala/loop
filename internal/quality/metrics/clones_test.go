package metrics

import (
	"hash/fnv"
	"strconv"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClonesSuite struct {
	suite.Suite
}

func TestClonesSuite(t *testing.T) {
	suite.Run(t, new(ClonesSuite))
}

func (s *ClonesSuite) TestNilGraph() {
	r := ComputeClones(nil, DefaultClonesConfig())
	require.Equal(s.T(), ClonesName, r.Name)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ClonesSuite) TestEmptyGraph() {
	g := graph.Build(nil)
	r := ComputeClones(g, DefaultClonesConfig())
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ClonesSuite) TestNoEligibleFunctionsScoresOne() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "Tiny", Body: &parser.FunctionBody{LOC: 2, Shingles: []uint64{1, 2}}},
			},
		},
	})
	r := ComputeClones(g, DefaultClonesConfig())
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ClonesSuite) TestExactDuplicatesCluster() {
	body := func() *parser.FunctionBody {
		return &parser.FunctionBody{
			LOC:      10,
			Shingles: []uint64{0xAAAA, 0xBBBB, 0xCCCC, 0xDDDD, 0xEEEE, 0xFFFF},
		}
	}
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{Name: "F1", StartLine: 1, EndLine: 10, Body: body()}}},
		{Path: "b.go", Functions: []parser.Function{{Name: "F2", StartLine: 1, EndLine: 10, Body: body()}}},
	})
	r := ComputeClones(g, DefaultClonesConfig())
	d := r.Detail.(ClonesDetail)
	require.Len(s.T(), d.Clusters, 1)
	require.Len(s.T(), d.Clusters[0].Members, 2)
	require.Equal(s.T(), 0, d.Clusters[0].MaxDistance)
	require.Less(s.T(), r.Score, 1.0)
	require.Equal(s.T(), 10, d.DuplicatedLOC)
}

func (s *ClonesSuite) TestDistinctFunctionsDoNotCluster() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{
			Name: "F1", Body: &parser.FunctionBody{LOC: 10, Shingles: []uint64{0x1111, 0x2222, 0x3333, 0x4444, 0x5555}},
		}}},
		{Path: "b.go", Functions: []parser.Function{{
			Name: "F2", Body: &parser.FunctionBody{LOC: 10, Shingles: []uint64{0xAAAAAAAAAAAAAAAA, 0xBBBBBBBBBBBBBBBB, 0xCCCCCCCCCCCCCCCC, 0xDDDDDDDDDDDDDDDD, 0xEEEEEEEEEEEEEEEE}},
		}}},
	})
	r := ComputeClones(g, DefaultClonesConfig())
	d := r.Detail.(ClonesDetail)
	require.Empty(s.T(), d.Clusters)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ClonesSuite) TestMaxDistanceTuning() {
	// Two functions whose shingles differ by ~1 bit per shingle. With
	// MaxDistance=3 they cluster; with MaxDistance=0 they don't.
	body1 := &parser.FunctionBody{LOC: 10, Shingles: []uint64{0xAAAA, 0xBBBB, 0xCCCC, 0xDDDD, 0xEEEE}}
	body2 := &parser.FunctionBody{LOC: 10, Shingles: []uint64{0xAAAB, 0xBBBC, 0xCCCD, 0xDDDE, 0xEEEF}}
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{Name: "F1", Body: body1}}},
		{Path: "b.go", Functions: []parser.Function{{Name: "F2", Body: body2}}},
	})

	loose := ComputeClones(g, ClonesConfig{MinLOC: 5, MaxDistance: 32})
	require.NotEmpty(s.T(), loose.Detail.(ClonesDetail).Clusters)

	strict := ComputeClones(g, ClonesConfig{MinLOC: 5, MaxDistance: 0})
	require.Empty(s.T(), strict.Detail.(ClonesDetail).Clusters)
}

func (s *ClonesSuite) TestMinLOCFilter() {
	body := &parser.FunctionBody{LOC: 3, Shingles: []uint64{0x1, 0x2, 0x3}}
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{Name: "F1", Body: body}}},
		{Path: "b.go", Functions: []parser.Function{{Name: "F2", Body: body}}},
	})
	r := ComputeClones(g, ClonesConfig{MinLOC: 10, MaxDistance: 3})
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ClonesSuite) TestEmptyShinglesSkipped() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{Name: "F", Body: &parser.FunctionBody{LOC: 10, Shingles: nil}}}},
	})
	r := ComputeClones(g, DefaultClonesConfig())
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ClonesSuite) TestClusterCap() {
	// Build 60 distinct duplicate pairs. ClusterCount surfaces the full
	// 60; Clusters is capped at 50.
	facts := make([]*parser.FileFacts, 0, 120)
	for i := range 60 {
		shingles := make([]uint64, 32)
		for j := range shingles {
			h := fnv.New64a()
			_, _ = h.Write([]byte(strconv.Itoa(i)))
			_, _ = h.Write([]byte{'/'})
			_, _ = h.Write([]byte(strconv.Itoa(j)))
			shingles[j] = h.Sum64()
		}
		body := &parser.FunctionBody{LOC: 10, Shingles: shingles}
		facts = append(facts,
			&parser.FileFacts{Path: "a" + strconv.Itoa(i) + ".go", Functions: []parser.Function{{Name: "F1_" + strconv.Itoa(i), Body: body}}},
			&parser.FileFacts{Path: "b" + strconv.Itoa(i) + ".go", Functions: []parser.Function{{Name: "F2_" + strconv.Itoa(i), Body: body}}},
		)
	}
	g := graph.Build(facts)
	r := ComputeClones(g, DefaultClonesConfig())
	d := r.Detail.(ClonesDetail)
	require.Equal(s.T(), 60, d.ClusterCount)
	require.Len(s.T(), d.Clusters, clonesClusterCap)
}

func (s *ClonesSuite) TestMemberCap() {
	// One cluster with 30 members; render slice cuts to clonesMemberCap.
	shingles := []uint64{0x1, 0x2, 0x3, 0x4, 0x5, 0x6}
	facts := make([]*parser.FileFacts, 30)
	for i := range 30 {
		facts[i] = &parser.FileFacts{
			Path:      "f" + strconv.Itoa(i) + ".go",
			Functions: []parser.Function{{Name: "F" + strconv.Itoa(i), Body: &parser.FunctionBody{LOC: 10, Shingles: shingles}}},
		}
	}
	g := graph.Build(facts)
	r := ComputeClones(g, DefaultClonesConfig())
	d := r.Detail.(ClonesDetail)
	require.Len(s.T(), d.Clusters, 1)
	require.Len(s.T(), d.Clusters[0].Members, clonesMemberCap)
}

func (s *ClonesSuite) TestSimHashStability() {
	shingles := []uint64{0xAAAA, 0xBBBB, 0xCCCC}
	require.Equal(s.T(), simHash(shingles), simHash(shingles))
}

func (s *ClonesSuite) TestHammingBasic() {
	require.Equal(s.T(), 0, hamming(0, 0))
	require.Equal(s.T(), 4, hamming(0xF, 0x0))
	require.Equal(s.T(), 64, hamming(0, ^uint64(0)))
}

func (s *ClonesSuite) TestClustersSortedByLOCDesc() {
	smallShingles := []uint64{0x1111, 0x2222, 0x3333, 0x4444, 0x5555}
	bigShingles := []uint64{0xAAAAAAAAAAAAAAAA, 0xBBBBBBBBBBBBBBBB, 0xCCCCCCCCCCCCCCCC, 0xDDDDDDDDDDDDDDDD, 0xEEEEEEEEEEEEEEEE}
	g := graph.Build([]*parser.FileFacts{
		{Path: "small_a.go", Functions: []parser.Function{{Name: "S1", Body: &parser.FunctionBody{LOC: 6, Shingles: smallShingles}}}},
		{Path: "small_b.go", Functions: []parser.Function{{Name: "S2", Body: &parser.FunctionBody{LOC: 6, Shingles: smallShingles}}}},
		{Path: "big_a.go", Functions: []parser.Function{{Name: "B1", Body: &parser.FunctionBody{LOC: 50, Shingles: bigShingles}}}},
		{Path: "big_b.go", Functions: []parser.Function{{Name: "B2", Body: &parser.FunctionBody{LOC: 50, Shingles: bigShingles}}}},
	})
	r := ComputeClones(g, DefaultClonesConfig())
	d := r.Detail.(ClonesDetail)
	require.Len(s.T(), d.Clusters, 2)
	require.Greater(s.T(), d.Clusters[0].LOC, d.Clusters[1].LOC)
}

func (s *ClonesSuite) TestSamePathMembersSortByStartLine() {
	shingles := []uint64{0x1, 0x2, 0x3, 0x4, 0x5}
	g := graph.Build([]*parser.FileFacts{
		{Path: "shared.go", Functions: []parser.Function{
			{Name: "F1", StartLine: 100, EndLine: 110, Body: &parser.FunctionBody{LOC: 10, Shingles: shingles}},
			{Name: "F2", StartLine: 10, EndLine: 20, Body: &parser.FunctionBody{LOC: 10, Shingles: shingles}},
		}},
	})
	r := ComputeClones(g, DefaultClonesConfig())
	d := r.Detail.(ClonesDetail)
	require.Len(s.T(), d.Clusters, 1)
	require.Len(s.T(), d.Clusters[0].Members, 2)
	require.Equal(s.T(), 10, d.Clusters[0].Members[0].StartLine)
	require.Equal(s.T(), 100, d.Clusters[0].Members[1].StartLine)
}

func (s *ClonesSuite) TestUnionFindRankAscendingBranch() {
	parent := []int{0, 1, 2, 3}
	rank := []int{0, 0, 0, 0}
	unionFind(parent, rank, 0, 1)
	require.Equal(s.T(), 1, rank[0])
	unionFind(parent, rank, 2, 0)
	require.Equal(s.T(), 0, parent[2])
	require.Equal(s.T(), findRoot(parent, 0), findRoot(parent, 2))
}
