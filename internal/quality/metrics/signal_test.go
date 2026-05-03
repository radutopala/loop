package metrics

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SignalSuite struct {
	suite.Suite
}

func TestSignalSuite(t *testing.T) {
	suite.Run(t, new(SignalSuite))
}

func (s *SignalSuite) TestAggregateEmpty() {
	sig := Aggregate(nil)
	require.Equal(s.T(), SignalScale, sig.Value)
	require.Equal(s.T(), 1.0, sig.GeoMean)
	require.Empty(s.T(), sig.Metrics)
}

func (s *SignalSuite) TestAggregateAllPerfectScoresMax() {
	results := []Result{
		{Name: ModularityName, Score: 1.0},
		{Name: CyclesName, Score: 1.0},
		{Name: DepthName, Score: 1.0},
		{Name: EqualityName, Score: 1.0},
		{Name: RedundancyName, Score: 1.0},
	}
	sig := Aggregate(results)
	require.InDelta(s.T(), 1.0, sig.GeoMean, 1e-9)
	require.Equal(s.T(), SignalScale, sig.Value)
	require.Len(s.T(), sig.Metrics, 5)
}

func (s *SignalSuite) TestAggregateUniformScoresGeometricMeanEqualsScore() {
	results := []Result{
		{Score: 0.5}, {Score: 0.5}, {Score: 0.5}, {Score: 0.5}, {Score: 0.5},
	}
	sig := Aggregate(results)
	require.InDelta(s.T(), 0.5, sig.GeoMean, 1e-9)
	require.Equal(s.T(), 5000, sig.Value)
}

func (s *SignalSuite) TestAggregateZeroScoreDominatesToZero() {
	results := []Result{
		{Score: 0.0}, {Score: 1.0}, {Score: 1.0}, {Score: 1.0}, {Score: 1.0},
	}
	sig := Aggregate(results)
	require.Equal(s.T(), 0.0, sig.GeoMean)
	require.Equal(s.T(), 0, sig.Value)
}

func (s *SignalSuite) TestAggregateMixedScoresCorrectGeometricMean() {
	// 5√(0.8 · 0.6 · 0.9 · 0.7 · 0.5) = 5√0.1512 ≈ 0.6853.
	results := []Result{
		{Score: 0.8}, {Score: 0.6}, {Score: 0.9}, {Score: 0.7}, {Score: 0.5},
	}
	sig := Aggregate(results)
	require.InDelta(s.T(), 0.6853, sig.GeoMean, 1e-3)
	require.InDelta(s.T(), 6853, sig.Value, 5)
}

func (s *SignalSuite) TestAggregateRoundsHalfUp() {
	// Two scores 0.5 and 1.0 average to √0.5 ≈ 0.707106... → 7071.
	results := []Result{{Score: 0.5}, {Score: 1.0}}
	sig := Aggregate(results)
	require.Equal(s.T(), 7071, sig.Value)
}

func (s *SignalSuite) TestAggregatePreservesMetricsSlice() {
	results := []Result{{Name: "x", Score: 0.5}}
	sig := Aggregate(results)
	require.Equal(s.T(), results, sig.Metrics)
}

func (s *SignalSuite) TestComputeOnNilGraph() {
	sig := Compute(nil)
	require.Equal(s.T(), SignalScale, sig.Value)
	require.InDelta(s.T(), 1.0, sig.GeoMean, 1e-9)
	require.Len(s.T(), sig.Metrics, 5)
	for _, r := range sig.Metrics {
		require.Equal(s.T(), 1.0, r.Score)
	}
}

func (s *SignalSuite) TestComputeOnEmptyGraph() {
	sig := Compute(graph.Build(nil))
	require.Equal(s.T(), SignalScale, sig.Value)
	require.Len(s.T(), sig.Metrics, 5)
	require.Equal(s.T(), ModularityName, sig.Metrics[0].Name)
	require.Equal(s.T(), CyclesName, sig.Metrics[1].Name)
	require.Equal(s.T(), DepthName, sig.Metrics[2].Name)
	require.Equal(s.T(), EqualityName, sig.Metrics[3].Name)
	require.Equal(s.T(), RedundancyName, sig.Metrics[4].Name)
}

func (s *SignalSuite) TestComputeOnHealthyGraph() {
	// Two-module graph, edges only inside modules (high modularity),
	// no cycles, depth 2, balanced LOC, every function called.
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a/x.go",
			LOC:       50,
			Imports:   []parser.Import{{Path: "./y"}},
			Functions: []parser.Function{{Name: "Alpha"}},
			Calls:     []parser.Call{{Name: "Beta"}},
		},
		{
			Path:      "a/y.go",
			LOC:       50,
			Functions: []parser.Function{{Name: "Beta"}},
			Calls:     []parser.Call{{Name: "Alpha"}},
		},
		{
			Path:      "b/x.go",
			LOC:       50,
			Imports:   []parser.Import{{Path: "./y"}},
			Functions: []parser.Function{{Name: "Gamma"}},
			Calls:     []parser.Call{{Name: "Delta"}},
		},
		{
			Path:      "b/y.go",
			LOC:       50,
			Functions: []parser.Function{{Name: "Delta"}},
			Calls:     []parser.Call{{Name: "Gamma"}},
		},
	})
	sig := Compute(g)
	require.Greater(s.T(), sig.Value, 7000, "healthy graph should sit in green band")
}

func (s *SignalSuite) TestComputeOnUnhealthyGraph() {
	// Cross-cluster cycles + extreme imbalance + dead code.
	g := graph.Build([]*parser.FileFacts{
		{
			Path:      "a/x.go",
			LOC:       1000,
			Imports:   []parser.Import{{Path: "../b/x"}, {Path: "../c/x"}},
			Functions: []parser.Function{{Name: "Dead1"}, {Name: "Dead2"}},
		},
		{
			Path:    "b/x.go",
			LOC:     10,
			Imports: []parser.Import{{Path: "../a/x"}, {Path: "../c/x"}},
		},
		{
			Path:    "c/x.go",
			LOC:     10,
			Imports: []parser.Import{{Path: "../a/x"}, {Path: "../b/x"}},
		},
	})
	sig := Compute(g)
	require.Less(s.T(), sig.Value, 3000, "unhealthy graph should be deep in the red band")
}
