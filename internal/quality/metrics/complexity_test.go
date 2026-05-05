package metrics

import (
	"strconv"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ComplexitySuite struct {
	suite.Suite
}

func TestComplexitySuite(t *testing.T) {
	suite.Run(t, new(ComplexitySuite))
}

func (s *ComplexitySuite) TestNilGraph() {
	r := ComputeComplexity(nil, DefaultComplexityConfig())
	require.Equal(s.T(), ComplexityName, r.Name)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(ComplexityDetail)
	require.Equal(s.T(), 0, d.TotalFunctions)
	require.NotNil(s.T(), d.Histogram)
}

func (s *ComplexitySuite) TestEmptyGraph() {
	g := graph.Build(nil)
	r := ComputeComplexity(g, DefaultComplexityConfig())
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ComplexitySuite) TestNoFunctionsWithBody() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{Name: "F"}}},
	})
	r := ComputeComplexity(g, DefaultComplexityConfig())
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(ComplexityDetail)
	require.Equal(s.T(), 0, d.TotalFunctions)
}

func (s *ComplexitySuite) TestAllUnderThresholdScoresOne() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "Small", Body: &parser.FunctionBody{
					LOC: 10, ParamCount: 1, MaxNesting: 1, DecisionPoints: 2, CognitiveLoad: 1,
				}},
			},
		},
	})
	r := ComputeComplexity(g, DefaultComplexityConfig())
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(ComplexityDetail)
	require.Equal(s.T(), 1, d.TotalFunctions)
	require.Equal(s.T(), 0, d.OverThreshold)
	require.Equal(s.T(), 1, d.Histogram["cyclomatic"]["ok"])
}

func (s *ComplexitySuite) TestExceededDimensionDragsScore() {
	cfg := DefaultComplexityConfig()
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "Hot", StartLine: 5, Body: &parser.FunctionBody{
					LOC: 20, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 20, // 2x cyclomatic threshold (10) → score 0
					CognitiveLoad:  1,
				}},
			},
		},
	})
	r := ComputeComplexity(g, cfg)
	d := r.Detail.(ComplexityDetail)
	require.Equal(s.T(), 1, d.OverThreshold)
	require.Len(s.T(), d.Functions, 1)
	require.Equal(s.T(), "Hot", d.Functions[0].Name)
	require.InDelta(s.T(), 0.0, d.Functions[0].Score, 1e-9)
	require.Equal(s.T(), 1, d.Histogram["cyclomatic"]["crit"])
}

func (s *ComplexitySuite) TestSoftCurveLinearMidpoint() {
	cfg := DefaultComplexityConfig() // cyclomatic threshold = 10
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "Mid", Body: &parser.FunctionBody{
					LOC: 10, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 15, // halfway between T (1.0) and 2T (0.0) → 0.5
					CognitiveLoad:  1,
				}},
			},
		},
	})
	r := ComputeComplexity(g, cfg)
	d := r.Detail.(ComplexityDetail)
	require.InDelta(s.T(), 0.5, d.Functions[0].Score, 1e-9)
	require.Equal(s.T(), 1, d.Histogram["cyclomatic"]["warn"])
}

func (s *ComplexitySuite) TestLOCWeightedMean() {
	// Big bad function dominates a small good function in the LOC-weighted mean.
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "BigBad", Body: &parser.FunctionBody{
					LOC: 100, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 20, // score 0
					CognitiveLoad:  1,
				}},
				{Name: "TinyGood", Body: &parser.FunctionBody{
					LOC: 1, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 1, // score 1
					CognitiveLoad:  1,
				}},
			},
		},
	})
	r := ComputeComplexity(g, DefaultComplexityConfig())
	// Weighted: (0 * 100 + 1 * 1) / 101 ≈ 0.0099
	require.InDelta(s.T(), 0.0099, r.Score, 1e-3)
}

func (s *ComplexitySuite) TestHotspotCap() {
	funcs := make([]parser.Function, 150)
	for i := range 150 {
		funcs[i] = parser.Function{Name: "F" + strconv.Itoa(i), Body: &parser.FunctionBody{
			LOC: 10, ParamCount: 1, MaxNesting: 1,
			DecisionPoints: 20, CognitiveLoad: 1,
		}}
	}
	g := graph.Build([]*parser.FileFacts{{Path: "a.go", Functions: funcs}})
	r := ComputeComplexity(g, DefaultComplexityConfig())
	d := r.Detail.(ComplexityDetail)
	require.Equal(s.T(), 150, d.TotalFunctions)
	require.Len(s.T(), d.Functions, complexityHotspotCap)
}

func (s *ComplexitySuite) TestZeroThresholdDisablesDimension() {
	cfg := DefaultComplexityConfig()
	cfg.CyclomaticT = 0
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "F", Body: &parser.FunctionBody{
					LOC: 10, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 9999, CognitiveLoad: 1,
				}},
			},
		},
	})
	r := ComputeComplexity(g, cfg)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *ComplexitySuite) TestEqualScoreSortsByPath() {
	body := &parser.FunctionBody{
		LOC: 10, ParamCount: 1, MaxNesting: 1,
		DecisionPoints: 20, CognitiveLoad: 1,
	}
	g := graph.Build([]*parser.FileFacts{
		{Path: "z/last.go", Functions: []parser.Function{{Name: "F", Body: body}}},
		{Path: "a/first.go", Functions: []parser.Function{{Name: "F", Body: body}}},
	})
	r := ComputeComplexity(g, DefaultComplexityConfig())
	d := r.Detail.(ComplexityDetail)
	require.Len(s.T(), d.Functions, 2)
	require.Equal(s.T(), "a/first.go", d.Functions[0].Path)
	require.Equal(s.T(), "z/last.go", d.Functions[1].Path)
}

func (s *ComplexitySuite) TestZeroLOCFunctionStillContributes() {
	g := graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "F", Body: &parser.FunctionBody{
					LOC: 0, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 20, CognitiveLoad: 1,
				}},
			},
		},
	})
	r := ComputeComplexity(g, DefaultComplexityConfig())
	d := r.Detail.(ComplexityDetail)
	require.Equal(s.T(), 1, d.TotalFunctions)
	require.Equal(s.T(), 0.0, d.Functions[0].Score)
}
