package metrics

import (
	"strconv"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RedundancySuite struct {
	suite.Suite
}

func TestRedundancySuite(t *testing.T) {
	suite.Run(t, new(RedundancySuite))
}

func (s *RedundancySuite) TestNilGraph() {
	r := Redundancy(nil)
	require.Equal(s.T(), RedundancyName, r.Name)
	require.Equal(s.T(), 0.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(RedundancyDetail)
	require.Equal(s.T(), DeadCodeDetail{}, d.DeadCode)
	require.Equal(s.T(), ClonesDetail{}, d.Clones)
}

func (s *RedundancySuite) TestEmptyGraph() {
	g := graph.Build(nil)
	r := Redundancy(g)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *RedundancySuite) TestNoFunctionsScoresOne() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go"}, {Path: "b.go"},
	})
	r := Redundancy(g)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(RedundancyDetail)
	require.Equal(s.T(), 0, d.DeadCode.TotalFunctions)
}

func (s *RedundancySuite) TestAllFunctionsCalledScoresOne() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a.go",
			Functions: []parser.Function{{Name: "Alpha"}, {Name: "Beta"}},
			Calls:     []parser.Call{{Name: "Beta"}},
		},
		{
			Path:  "b.go",
			Calls: []parser.Call{{Name: "Alpha"}},
		},
	})
	r := Redundancy(g)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(RedundancyDetail)
	require.Empty(s.T(), d.DeadCode.DeadFunctions)
	require.Equal(s.T(), 2, d.DeadCode.TotalFunctions)
}

func (s *RedundancySuite) TestUncalledFunctionFlagged() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "Used", StartLine: 5, EndLine: 10},
				{Name: "Unused", StartLine: 20, EndLine: 25},
			},
			Calls: []parser.Call{{Name: "Used"}},
		},
	})
	r := Redundancy(g)
	// dead-half score = 0.5; clones-half score = 1.0 → mean 0.75.
	require.InDelta(s.T(), 0.75, r.Score, 1e-9)
	d := r.Detail.(RedundancyDetail)
	require.Len(s.T(), d.DeadCode.DeadFunctions, 1)
	require.Equal(s.T(), "Unused", d.DeadCode.DeadFunctions[0].Name)
	require.Equal(s.T(), 20, d.DeadCode.DeadFunctions[0].StartLine)
}

func (s *RedundancySuite) TestEntryPointsNotFlagged() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "main.go",
			Functions: []parser.Function{
				{Name: "main"},
				{Name: "init"},
			},
		},
	})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.Empty(s.T(), d.DeadCode.DeadFunctions)
}

func (s *RedundancySuite) TestTestHarnessNamesNotFlagged() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "x_test.go",
			Functions: []parser.Function{
				{Name: "TestFoo"},
				{Name: "BenchmarkBar"},
				{Name: "ExampleBaz"},
				{Name: "FuzzQux"},
			},
		},
	})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.Empty(s.T(), d.DeadCode.DeadFunctions)
}

func (s *RedundancySuite) TestInterfaceMethodsNotFlagged() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "iface.go",
			Functions: []parser.Function{
				{Name: "String"},
				{Name: "Error"},
				{Name: "ServeHTTP"},
				{Name: "MarshalJSON"},
			},
		},
	})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.Empty(s.T(), d.DeadCode.DeadFunctions)
}

func (s *RedundancySuite) TestCrossFileCallsCount() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a.go",
			Functions: []parser.Function{{Name: "Worker"}},
		},
		{
			Path:  "caller.go",
			Calls: []parser.Call{{Name: "Worker"}},
		},
	})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.Empty(s.T(), d.DeadCode.DeadFunctions)
}

func (s *RedundancySuite) TestDeadHotspotCap() {
	// 30 unused functions, listed cap is 20 but DeadCount reflects all 30.
	funcs := make([]parser.Function, 30)
	for i := range 30 {
		funcs[i] = parser.Function{Name: "Unused" + strconv.Itoa(i)}
	}
	g := graph.Build([]*parser.FileFacts{{Path: "a.go", Functions: funcs}})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.Equal(s.T(), 30, d.DeadCode.DeadCount)
	require.Len(s.T(), d.DeadCode.DeadFunctions, redundancyHotspotCap)
}

func (s *RedundancySuite) TestDeadListSortedByPathThenLine() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "z.go",
			Functions: []parser.Function{
				{Name: "Z2", StartLine: 100},
				{Name: "Z1", StartLine: 10},
			},
		},
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "A1", StartLine: 50},
			},
		},
	})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.Len(s.T(), d.DeadCode.DeadFunctions, 3)
	require.Equal(s.T(), "a.go", d.DeadCode.DeadFunctions[0].Path)
	require.Equal(s.T(), "z.go", d.DeadCode.DeadFunctions[1].Path)
	require.Equal(s.T(), 10, d.DeadCode.DeadFunctions[1].StartLine)
	require.Equal(s.T(), 100, d.DeadCode.DeadFunctions[2].StartLine)
}

func (s *RedundancySuite) TestIsReachableByConvention() {
	require.True(s.T(), isReachableByConvention("main"))
	require.True(s.T(), isReachableByConvention("init"))
	require.True(s.T(), isReachableByConvention("TestSomething"))
	require.True(s.T(), isReachableByConvention("BenchmarkLoop"))
	require.True(s.T(), isReachableByConvention("ExampleX"))
	require.True(s.T(), isReachableByConvention("FuzzInput"))
	require.True(s.T(), isReachableByConvention("String"))
	require.True(s.T(), isReachableByConvention("Error"))
	require.False(s.T(), isReachableByConvention("ordinaryHelper"))
	require.True(s.T(), isReachableByConvention("Test")) // bare "Test" still matches the prefix rule
}

func (s *RedundancySuite) TestClonesDragScore() {
	// Two duplicate functions, no dead code. Dead score 1.0, clone
	// score < 1.0 → mean drops below 1.0.
	body := func() *parser.FunctionBody {
		return &parser.FunctionBody{
			LOC:      10,
			Shingles: []uint64{0xAAAA, 0xBBBB, 0xCCCC, 0xDDDD, 0xEEEE},
		}
	}
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a.go",
			Functions: []parser.Function{{Name: "A", Body: body()}},
			Calls:     []parser.Call{{Name: "A"}, {Name: "B"}},
		},
		{
			Path:      "b.go",
			Functions: []parser.Function{{Name: "B", Body: body()}},
		},
	})
	r := Redundancy(g)
	d := r.Detail.(RedundancyDetail)
	require.NotEmpty(s.T(), d.Clones.Clusters)
	require.Less(s.T(), r.Score, 1.0)
}
