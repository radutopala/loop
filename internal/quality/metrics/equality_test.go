package metrics

import (
	"strconv"
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EqualitySuite struct {
	suite.Suite
}

func TestEqualitySuite(t *testing.T) {
	suite.Run(t, new(EqualitySuite))
}

func (s *EqualitySuite) TestNilGraph() {
	r := Equality(nil)
	require.Equal(s.T(), EqualityName, r.Name)
	require.Equal(s.T(), 0.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
	require.Equal(s.T(), EqualityDetail{}, r.Detail)
}

func (s *EqualitySuite) TestEmptyGraph() {
	g := graph.Build(nil)
	r := Equality(g)
	require.Equal(s.T(), 1.0, r.Score)
}

func (s *EqualitySuite) TestZeroLOCAcrossAllFilesScoresOne() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"},
	})
	r := Equality(g)
	require.Equal(s.T(), 0.0, r.Raw)
	require.Equal(s.T(), 1.0, r.Score)
	d := r.Detail.(EqualityDetail)
	require.Equal(s.T(), 3, d.FileCount)
	require.Empty(s.T(), d.Hotspots)
}

func (s *EqualitySuite) TestPerfectEqualityScoresOne() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", LOC: 100},
		{Path: "b.go", LOC: 100},
		{Path: "c.go", LOC: 100},
		{Path: "d.go", LOC: 100},
	})
	r := Equality(g)
	require.InDelta(s.T(), 0.0, r.Raw, 1e-9)
	require.InDelta(s.T(), 1.0, r.Score, 1e-9)
	d := r.Detail.(EqualityDetail)
	require.Equal(s.T(), 400, d.TotalLOC)
	require.Len(s.T(), d.Hotspots, 4)
}

func (s *EqualitySuite) TestGodFileHasHighGini() {
	// One 1000-LOC file, ten 1-LOC files — extreme imbalance.
	files := []*parser.FileFacts{{Path: "god.go", LOC: 1000}}
	for i := range 10 {
		files = append(files, &parser.FileFacts{
			Path: "small_" + strconv.Itoa(i) + ".go",
			LOC:  1,
		})
	}
	g := graph.Build(files)
	r := Equality(g)
	require.Greater(s.T(), r.Raw, 0.8, "extreme imbalance should give Gini > 0.8")
	d := r.Detail.(EqualityDetail)
	require.Equal(s.T(), "god.go", d.Hotspots[0].Path)
	require.Equal(s.T(), 1000, d.Hotspots[0].LOC)
	require.InDelta(s.T(), 1000.0/1010.0, d.Hotspots[0].Share, 1e-9)
}

func (s *EqualitySuite) TestKnownTwoFileGini() {
	// 2 files, LOC 25 and 75.
	// Sorted: x_1=25, x_2=75 ; total = 100 ; n = 2.
	// Σ (2i-n-1)·x_i = (2-3)·25 + (4-3)·75 = -25 + 75 = 50.
	// G = 50 / (2 · 100) = 0.25.
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", LOC: 25},
		{Path: "b.go", LOC: 75},
	})
	r := Equality(g)
	require.InDelta(s.T(), 0.25, r.Raw, 1e-9)
	require.InDelta(s.T(), 0.75, r.Score, 1e-9)
}

func (s *EqualitySuite) TestHotspotsCapAtTen() {
	files := make([]*parser.FileFacts, 25)
	for i := range 25 {
		files[i] = &parser.FileFacts{
			Path: "f_" + strconv.Itoa(i) + ".go",
			LOC:  100 - i, // varying sizes, all positive
		}
	}
	g := graph.Build(files)
	r := Equality(g)
	d := r.Detail.(EqualityDetail)
	require.Len(s.T(), d.Hotspots, 10)
	// Largest first.
	for i := 1; i < len(d.Hotspots); i++ {
		require.GreaterOrEqual(s.T(), d.Hotspots[i-1].LOC, d.Hotspots[i].LOC)
	}
}

func (s *EqualitySuite) TestHotspotsSkipZeroLOC() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "a.go", LOC: 50},
		{Path: "empty.go", LOC: 0},
		{Path: "c.go", LOC: 50},
	})
	r := Equality(g)
	d := r.Detail.(EqualityDetail)
	require.Len(s.T(), d.Hotspots, 2)
	for _, h := range d.Hotspots {
		require.NotEqual(s.T(), "empty.go", h.Path)
	}
}

func (s *EqualitySuite) TestHotspotsLexTiebreakOnEqualLOC() {
	g := graph.Build([]*parser.FileFacts{
		{Path: "z.go", LOC: 10},
		{Path: "a.go", LOC: 10},
		{Path: "m.go", LOC: 10},
	})
	r := Equality(g)
	d := r.Detail.(EqualityDetail)
	require.Equal(s.T(), []string{"a.go", "m.go", "z.go"},
		[]string{d.Hotspots[0].Path, d.Hotspots[1].Path, d.Hotspots[2].Path})
}
