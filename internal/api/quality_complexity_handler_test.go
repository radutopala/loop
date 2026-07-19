package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
)

// complexityGraph is a helper that builds a graph with a hot function so
// the complexity handler exercises the over-threshold + paging paths.
func complexityGraph() *graph.Graph {
	return graph.Build([]*parser.FileFacts{
		{
			Path: "a.go",
			Functions: []parser.Function{
				{Name: "Small", StartLine: 1, EndLine: 5, Body: &parser.FunctionBody{
					LOC: 5, ParamCount: 1, MaxNesting: 1,
					DecisionPoints: 1, CognitiveLoad: 1,
				}},
				{Name: "Hot", StartLine: 10, EndLine: 200, Body: &parser.FunctionBody{
					LOC: 200, ParamCount: 9, MaxNesting: 8,
					DecisionPoints: 30, CognitiveLoad: 40,
				}},
			},
		},
	})
}

// clonesGraph builds two near-identical functions so the clones handler
// returns a non-trivial cluster.
func clonesGraph() *graph.Graph {
	body := func() *parser.FunctionBody {
		return &parser.FunctionBody{
			LOC:      10,
			Shingles: []uint64{0xAAAA, 0xBBBB, 0xCCCC, 0xDDDD, 0xEEEE, 0xFFFF},
		}
	}
	return graph.Build([]*parser.FileFacts{
		{Path: "a.go", Functions: []parser.Function{{Name: "F1", StartLine: 1, EndLine: 10, Body: body()}}},
		{Path: "b.go", Functions: []parser.Function{{Name: "F2", StartLine: 1, EndLine: 10, Body: body()}}},
	})
}

// ─── GET /quality/complexity ───

func (s *ServerSuite) TestHandleQualityComplexityGraphProviderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityComplexityNoCachedGraph() {
	s.srv.quality.setGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestHandleQualityComplexityReturnsHotspots() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 2, resp.TotalFunctions)
	require.GreaterOrEqual(s.T(), resp.OverThreshold, 1)
	require.NotEmpty(s.T(), resp.Functions)
	// Worst-first sort: Hot dominates the listing.
	require.Equal(s.T(), "Hot", resp.Functions[0].Name)
	require.NotEmpty(s.T(), resp.Histogram)
	require.Less(s.T(), resp.Score, 1.0)
}

func (s *ServerSuite) TestHandleQualityComplexityRespectsLimit() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity?limit=1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 1, resp.Limit)
	require.Equal(s.T(), 1, resp.Returned)
	require.Len(s.T(), resp.Functions, 1)
}

func (s *ServerSuite) TestHandleQualityComplexityRespectsOffset() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity?offset=1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 1, resp.Offset)
	// Sorted worst-first; offset=1 skips Hot, leaves at most Small (which
	// is below all thresholds, but the listing still includes it because
	// the handler returns the full sorted slice, capped by the metric).
	require.LessOrEqual(s.T(), resp.Returned, 1)
}

func (s *ServerSuite) TestHandleQualityComplexityOffsetBeyondReturnsEmpty() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity?offset=100", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 0, resp.Returned)
	require.NotNil(s.T(), resp.Functions, "empty must encode as []")
}

func (s *ServerSuite) TestHandleQualityComplexityClampsLimitToMax() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity?limit=9999", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), complexityMaxLimit, resp.Limit)
}

func (s *ServerSuite) TestHandleQualityComplexityRejectsBadLimit() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity?limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityComplexityRejectsNegativeOffset() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity?offset=-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityComplexityUsesProjectThresholds() {
	s.channelWithDir("ch-1", s.T().TempDir())
	s.srv.quality.setGraphProvider(&graphProviderHit{g: complexityGraph()})
	// Crank cyclomatic threshold up so even the "Hot" function scores 1.0
	// — proves the handler actually consults the resolveMetricsConfig
	// path rather than falling back to defaults silently.
	s.srv.quality.setMetricsLoader(func(string, string) metrics.Config {
		return metrics.Config{
			Complexity: metrics.ComplexityConfig{
				CyclomaticT: 1000, CognitiveT: 1000, NestingT: 100,
				ParamsT: 100, LOCT: 1000,
			},
		}
	})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/complexity", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityComplexityResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 0, resp.OverThreshold)
	require.Equal(s.T(), 1.0, resp.Score)
}

// ─── GET /quality/clones ───

func (s *ServerSuite) TestHandleQualityClonesGraphProviderUnset() {
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleQualityClonesNoCachedGraph() {
	s.srv.quality.setGraphProvider(&fakeGraphProvider{g: nil})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestHandleQualityClonesReturnsClusters() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityClonesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 1, resp.ClusterCount)
	require.Len(s.T(), resp.Clusters, 1)
	require.Len(s.T(), resp.Clusters[0].Members, 2)
	require.Less(s.T(), resp.Score, 1.0)
	require.Greater(s.T(), resp.DuplicatedLOC, 0)
}

func (s *ServerSuite) TestHandleQualityClonesRespectsLimit() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones?limit=1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityClonesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 1, resp.Limit)
	require.LessOrEqual(s.T(), len(resp.Clusters), 1)
}

func (s *ServerSuite) TestHandleQualityClonesOffsetBeyondReturnsEmpty() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones?offset=100", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityClonesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 0, resp.Returned)
	require.NotNil(s.T(), resp.Clusters)
}

func (s *ServerSuite) TestHandleQualityClonesClampsLimitToMax() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones?limit=9999", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityClonesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), clonesMaxLimit, resp.Limit)
}

func (s *ServerSuite) TestHandleQualityClonesRejectsBadOffset() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones?offset=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityClonesRejectsBadLimit() {
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones?limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleQualityClonesUsesProjectThresholds() {
	s.channelWithDir("ch-1", s.T().TempDir())
	s.srv.quality.setGraphProvider(&graphProviderHit{g: clonesGraph()})
	// MinLOC=999 disqualifies every function so no cluster is detected.
	s.srv.quality.setMetricsLoader(func(string, string) metrics.Config {
		return metrics.Config{
			Clones: metrics.ClonesConfig{MinLOC: 999, MaxDistance: 0},
		}
	})
	rec := s.testRequest("GET", "/api/channels/ch-1/quality/clones", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp QualityClonesResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 0, resp.ClusterCount)
	require.Equal(s.T(), 1.0, resp.Score)
}

// ─── helper coverage ───

func (s *ServerSuite) TestResolveMetricsConfigForChannelNoLoader() {
	// No loader configured — handler shouldn't even consult the channel
	// store, so the test deliberately leaves the mock unregistered.
	cfg := s.srv.quality.resolveMetricsConfigForChannel(s.T().Context(), "ch-1")
	require.Equal(s.T(), metrics.DefaultConfig(), cfg)
}

func (s *ServerSuite) TestResolveMetricsConfigForChannelLoaderReturnsZero() {
	s.srv.quality.setMetricsLoader(func(string, string) metrics.Config { return metrics.Config{} })
	s.channelWithDir("ch-1", s.T().TempDir())
	cfg := s.srv.quality.resolveMetricsConfigForChannel(s.T().Context(), "ch-1")
	require.Equal(s.T(), metrics.DefaultConfig(), cfg)
}

func (s *ServerSuite) TestResolveMetricsConfigForChannelStoreUnset() {
	s.srv.store = nil
	custom := metrics.Config{Complexity: metrics.ComplexityConfig{CyclomaticT: 99}}
	var capturedDir, capturedParent string
	s.srv.quality.setMetricsLoader(func(d, p string) metrics.Config {
		capturedDir, capturedParent = d, p
		return custom
	})
	cfg := s.srv.quality.resolveMetricsConfigForChannel(s.T().Context(), "ch-1")
	require.Equal(s.T(), custom, cfg)
	require.Equal(s.T(), "", capturedDir)
	require.Equal(s.T(), "", capturedParent)
}

func (s *ServerSuite) TestResolveMetricsConfigForChannelDirResolveError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return((*db.Channel)(nil), errors.New("missing"))
	custom := metrics.Config{Complexity: metrics.ComplexityConfig{CyclomaticT: 42}}
	s.srv.quality.setMetricsLoader(func(string, string) metrics.Config { return custom })
	cfg := s.srv.quality.resolveMetricsConfigForChannel(s.T().Context(), "ch-1")
	require.Equal(s.T(), custom, cfg)
}
