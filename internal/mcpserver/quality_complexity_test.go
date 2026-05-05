package mcpserver

import (
	"errors"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- quality_complexity ---

func (s *MCPServerSuite) TestQualityComplexitySuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/quality/complexity", req.URL.String())
		return jsonResponse(http.StatusOK, `{
			"score": 0.42,
			"raw": 3,
			"total_functions": 5,
			"over_threshold": 2,
			"histogram": {"cyclomatic": {"ok": 3, "warn": 1, "crit": 1}},
			"functions": [
				{"path":"a.go","name":"Hot","start_line":10,"cyclomatic":25,"cognitive":30,"max_nesting":6,"param_count":7,"loc":120,"score":0.0},
				{"path":"b.go","name":"Warm","start_line":3,"cyclomatic":12,"cognitive":18,"max_nesting":3,"param_count":4,"loc":40,"score":0.6}
			],
			"offset": 0,
			"limit": 50,
			"returned": 2
		}`), nil
	}
	text, isError := s.callTool("quality_complexity", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Complexity score: 0.420")
	require.Contains(s.T(), text, "(2/5 functions over threshold)")
	require.Contains(s.T(), text, "a.go:10 Hot")
	require.Contains(s.T(), text, "b.go:3 Warm")
	require.Contains(s.T(), text, "cyc 25, cog 30")
}

func (s *MCPServerSuite) TestQualityComplexityWithPaging() {
	var capturedURL string
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return jsonResponse(http.StatusOK, `{
			"score":1.0,"raw":0,"total_functions":1,"over_threshold":0,
			"histogram":{},"functions":[],"offset":5,"limit":10,"returned":0
		}`), nil
	}
	text, isError := s.callTool("quality_complexity", map[string]any{
		"limit":  float64(10),
		"offset": float64(5),
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), capturedURL, "limit=10")
	require.Contains(s.T(), capturedURL, "offset=5")
	require.Contains(s.T(), text, "No hotspots in this page")
}

func (s *MCPServerSuite) TestQualityComplexityAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_complexity", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityComplexityTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("net down")
	}
	text, isError := s.callTool("quality_complexity", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualityComplexityRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_complexity", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_clones ---

func (s *MCPServerSuite) TestQualityClonesSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/quality/clones", req.URL.String())
		return jsonResponse(http.StatusOK, `{
			"score": 0.7,
			"raw": 25,
			"duplicated_loc": 25,
			"total_loc": 100,
			"cluster_count": 1,
			"clusters": [
				{
					"members": [
						{"path":"a.go","name":"F1","start_line":1,"end_line":10,"loc":10},
						{"path":"b.go","name":"F2","start_line":1,"end_line":10,"loc":10}
					],
					"loc": 20,
					"max_distance": 0
				}
			],
			"offset": 0,
			"limit": 25,
			"returned": 1
		}`), nil
	}
	text, isError := s.callTool("quality_clones", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Clones score: 0.700")
	require.Contains(s.T(), text, "duplicated 25 / total 100 LOC across 1 clusters")
	require.Contains(s.T(), text, "1. 2 members, 20 LOC, max-distance 0")
	require.Contains(s.T(), text, "a.go:1 F1")
	require.Contains(s.T(), text, "b.go:1 F2")
}

func (s *MCPServerSuite) TestQualityClonesWithPaging() {
	var capturedURL string
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return jsonResponse(http.StatusOK, `{
			"score":1.0,"raw":0,"duplicated_loc":0,"total_loc":0,"cluster_count":0,
			"clusters":[],"offset":2,"limit":5,"returned":0
		}`), nil
	}
	text, isError := s.callTool("quality_clones", map[string]any{
		"limit":  float64(5),
		"offset": float64(2),
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), capturedURL, "limit=5")
	require.Contains(s.T(), capturedURL, "offset=2")
	require.Contains(s.T(), text, "No clusters in this page")
}

func (s *MCPServerSuite) TestQualityClonesAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_clones", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityClonesTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("net down")
	}
	text, isError := s.callTool("quality_clones", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualityClonesRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_clones", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- buildPagingQuery ---

func (s *MCPServerSuite) TestBuildPagingQueryEmpty() {
	require.Equal(s.T(), "", buildPagingQuery(0, 0))
}

func (s *MCPServerSuite) TestBuildPagingQueryLimitOnly() {
	require.Equal(s.T(), "limit=10", buildPagingQuery(10, 0))
}

func (s *MCPServerSuite) TestBuildPagingQueryOffsetOnly() {
	require.Equal(s.T(), "offset=5", buildPagingQuery(0, 5))
}

func (s *MCPServerSuite) TestBuildPagingQueryBoth() {
	require.Equal(s.T(), "limit=10&offset=5", buildPagingQuery(10, 5))
}
