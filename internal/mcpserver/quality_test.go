package mcpserver

import (
	"errors"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- quality_scan ---

func (s *MCPServerSuite) TestQualityScanStarted() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/quality/scan", req.URL.String())
		return jsonResponse(http.StatusAccepted, `{"status":"started"}`), nil
	}

	text, isError := s.callTool("quality_scan", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Scan started")
}

func (s *MCPServerSuite) TestQualityScanInProgress() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusAccepted, `{"status":"in_progress"}`), nil
	}

	text, isError := s.callTool("quality_scan", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "already in progress")
}

func (s *MCPServerSuite) TestQualityScanAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusBadRequest, "no dir_path"), nil
	}

	text, isError := s.callTool("quality_scan", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
	require.Contains(s.T(), text, "no dir_path")
}

func (s *MCPServerSuite) TestQualityScanTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	}

	text, isError := s.callTool("quality_scan", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualityScanRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_scan", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_snapshot ---

func (s *MCPServerSuite) TestQualitySnapshotSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/quality/snapshot", req.URL.String())
		return jsonResponse(http.StatusOK, `{
			"dir_path": "/work",
			"branch": "main",
			"current_branch": "main",
			"branch_mismatch": false,
			"signal": 7000,
			"geo_mean": 0.7,
			"scanned_at": "2026-05-01T12:00:00Z",
			"metrics": [
				{"name":"modularity","score":0.9,"raw":0.85},
				{"name":"cycles","score":1.0,"raw":1.0}
			]
		}`), nil
	}

	text, isError := s.callTool("quality_snapshot", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Quality signal: 7000")
	require.Contains(s.T(), text, "modularity")
	require.Contains(s.T(), text, "cycles")
	require.Contains(s.T(), text, "score=0.900")
	require.NotContains(s.T(), text, "Snapshot is from")
}

func (s *MCPServerSuite) TestQualitySnapshotBranchMismatchBanner() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"branch": "feature-x",
			"current_branch": "main",
			"branch_mismatch": true,
			"signal": 6000,
			"geo_mean": 0.6
		}`), nil
	}

	text, isError := s.callTool("quality_snapshot", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Snapshot is from \"feature-x\"")
	require.Contains(s.T(), text, "current branch is \"main\"")
}

func (s *MCPServerSuite) TestQualitySnapshotNoMetricsNoTimestamp() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"branch":"main","current_branch":"main","signal":1000,"geo_mean":0.1}`), nil
	}

	text, isError := s.callTool("quality_snapshot", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Quality signal: 1000")
	require.NotContains(s.T(), text, "Metrics:")
	require.NotContains(s.T(), text, "Scanned at")
}

func (s *MCPServerSuite) TestQualitySnapshotNotFound() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusNotFound, "no snapshot yet"), nil
	}

	text, isError := s.callTool("quality_snapshot", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error (status 404)")
}

func (s *MCPServerSuite) TestQualitySnapshotTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	}

	text, isError := s.callTool("quality_snapshot", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualitySnapshotRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_snapshot", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}
