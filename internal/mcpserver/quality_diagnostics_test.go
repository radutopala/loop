package mcpserver

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/stretchr/testify/require"
)

// --- quality_cycles ---

func (s *MCPServerSuite) TestQualityCyclesNoCycles() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/quality/cycles", req.URL.String())
		return jsonResponse(http.StatusOK, `{"cycles":[],"largest_cycle_size":0,"total_nodes_in_cycles":0}`), nil
	}
	text, isError := s.callTool("quality_cycles", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No import cycles")
}

func (s *MCPServerSuite) TestQualityCyclesFound() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"cycles":[["a.go","b.go"]],"largest_cycle_size":2,"total_nodes_in_cycles":2}`), nil
	}
	text, isError := s.callTool("quality_cycles", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Found 1 cycle")
	require.Contains(s.T(), text, "a.go → b.go")
}

func (s *MCPServerSuite) TestQualityCyclesAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_cycles", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityCyclesTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("net down")
	}
	text, isError := s.callTool("quality_cycles", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualityCyclesRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_cycles", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_metrics ---

func (s *MCPServerSuite) TestQualityMetricsSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return jsonResponse(http.StatusOK, `{
			"branch":"main",
			"signal":7000,
			"geo_mean":0.7,
			"scanned_at":"2026-05-01T12:00:00Z",
			"metrics":[
				{"name":"modularity","score":0.9,"raw":0.85},
				{"name":"cycles","score":1.0,"raw":1.0}
			]
		}`), nil
	}
	text, isError := s.callTool("quality_metrics", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Quality signal: 7000")
	require.Contains(s.T(), text, "modularity")
	require.Contains(s.T(), text, "score=0.900")
}

func (s *MCPServerSuite) TestQualityMetricsAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_metrics", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityMetricsTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("net down")
	}
	text, isError := s.callTool("quality_metrics", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualityMetricsRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_metrics", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_diagnostics ---

func (s *MCPServerSuite) TestQualityDiagnosticsSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return jsonResponse(http.StatusOK, `{"tiles":[
			{"path":"a.go","loc":100,"deficit":0.5,"top_reason":"depth"},
			{"path":"b.go","loc":50,"deficit":0.2,"top_reason":"cycles"}
		]}`), nil
	}
	text, isError := s.callTool("quality_diagnostics", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Top 2 files")
	require.Contains(s.T(), text, "a.go")
	require.Contains(s.T(), text, "b.go")
	require.Contains(s.T(), text, "deficit 0.500")
}

func (s *MCPServerSuite) TestQualityDiagnosticsLimit() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"tiles":[
			{"path":"a.go","loc":100,"deficit":0.5,"top_reason":"depth"},
			{"path":"b.go","loc":50,"deficit":0.2,"top_reason":"cycles"}
		]}`), nil
	}
	text, isError := s.callTool("quality_diagnostics", map[string]any{"limit": float64(1)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Top 1 files")
	require.Contains(s.T(), text, "a.go")
	require.NotContains(s.T(), text, "b.go")
}

func (s *MCPServerSuite) TestQualityDiagnosticsLimitOverflowsToAll() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"tiles":[
			{"path":"a.go","loc":100,"deficit":0.5,"top_reason":"depth"}
		]}`), nil
	}
	text, isError := s.callTool("quality_diagnostics", map[string]any{"limit": float64(99)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Top 1 files")
}

func (s *MCPServerSuite) TestQualityDiagnosticsEmpty() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"tiles":[]}`), nil
	}
	text, isError := s.callTool("quality_diagnostics", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No per-file deficits")
}

func (s *MCPServerSuite) TestQualityDiagnosticsAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_diagnostics", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityDiagnosticsRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_diagnostics", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_rules ---

func (s *MCPServerSuite) TestQualityRulesSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return jsonResponse(http.StatusOK, `{
			"passed":[{"name":"signal_floor","severity":"pass","message":"signal=7000 ≥ 5000"}],
			"failed":[{"name":"no_import_cycles","severity":"fail","message":"1 cycle detected","citations":[{"path":"a.go","note":"part of cycle"}]}]
		}`), nil
	}
	text, isError := s.callTool("quality_rules", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "1 passed, 1 failed")
	require.Contains(s.T(), text, "no_import_cycles")
	require.Contains(s.T(), text, "signal_floor")
	require.Contains(s.T(), text, "a.go (part of cycle)")
}

func (s *MCPServerSuite) TestQualityRulesNoCitationNote() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"passed":[],
			"failed":[{"name":"no_import_cycles","severity":"fail","message":"x","citations":[{"path":"a.go"}]}]
		}`), nil
	}
	text, isError := s.callTool("quality_rules", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "• a.go\n")
}

func (s *MCPServerSuite) TestQualityRulesAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_rules", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityRulesRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_rules", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_whatif ---

func (s *MCPServerSuite) TestQualityWhatifSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/quality/whatif", req.URL.String())
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"op":"delete"`)
		require.Contains(s.T(), string(body), `"path":"a.go"`)
		return jsonResponse(http.StatusOK, `{
			"baseline_signal":6000,
			"predicted_signal":6500,
			"delta_signal":500,
			"baseline_metrics":[],
			"predicted_metrics":[{"name":"modularity","score":0.95,"raw":0.9}]
		}`), nil
	}
	text, isError := s.callTool("quality_whatif", map[string]any{
		"mutations": []any{map[string]any{"op": "delete", "path": "a.go"}},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "6000 → 6500")
	require.Contains(s.T(), text, "+500")
	require.Contains(s.T(), text, "modularity")
}

func (s *MCPServerSuite) TestQualityWhatifNegativeDelta() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"baseline_signal":7000,
			"predicted_signal":6800,
			"delta_signal":-200,
			"baseline_metrics":[],
			"predicted_metrics":[]
		}`), nil
	}
	text, isError := s.callTool("quality_whatif", map[string]any{
		"mutations": []any{map[string]any{"op": "move", "path": "a.go", "new_module": "x"}},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "7000 → 6800")
	require.Contains(s.T(), text, "-200")
}

func (s *MCPServerSuite) TestQualityWhatifNoMutations() {
	text, isError := s.callTool("quality_whatif", map[string]any{
		"mutations": []any{},
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "at least one mutation")
}

func (s *MCPServerSuite) TestQualityWhatifAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusBadRequest, "unknown path"), nil
	}
	text, isError := s.callTool("quality_whatif", map[string]any{
		"mutations": []any{map[string]any{"op": "delete", "path": "missing.go"}},
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityWhatifTransportError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("conn refused")
	}
	text, isError := s.callTool("quality_whatif", map[string]any{
		"mutations": []any{map[string]any{"op": "delete", "path": "a.go"}},
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestQualityWhatifRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_whatif", map[string]any{
		"mutations": []any{map[string]any{"op": "delete", "path": "a.go"}},
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_evolution ---

func (s *MCPServerSuite) TestQualityEvolutionSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return jsonResponse(http.StatusOK, `{
			"commits_scanned":250,
			"shallow_warning":false,
			"coupling_pairs":[{"file_a":"a.go","file_b":"b.go","co_change_count":12,"jaccard":0.85,"cross_module":true}],
			"churn_hotspots":[{"file":"a.go","change_count":42,"last_changed_at":"2026-04-01T00:00:00Z"}],
			"bus_factor":[{"file":"x.go","sole_author":"alice","sole_author_ratio":0.95,"total_commits":20,"days_since_last_other_author":120}]
		}`), nil
	}
	text, isError := s.callTool("quality_evolution", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Scanned 250 commits")
	require.Contains(s.T(), text, "Top coupling pairs")
	require.Contains(s.T(), text, "a.go ⇄ b.go")
	require.Contains(s.T(), text, "[cross-module]")
	require.Contains(s.T(), text, "Churn hotspots")
	require.Contains(s.T(), text, "Bus-factor risks")
	require.Contains(s.T(), text, "alice owns 95%")
}

func (s *MCPServerSuite) TestQualityEvolutionShallow() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"commits_scanned":3,"shallow_warning":true,"coupling_pairs":[],"churn_hotspots":[],"bus_factor":[]}`), nil
	}
	text, isError := s.callTool("quality_evolution", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "shallow clone")
}

func (s *MCPServerSuite) TestQualityEvolutionCappedAtTen() {
	repeatJSON := func(item string, n int) string {
		var b strings.Builder
		for i := range n {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(item)
		}
		return b.String()
	}
	pairs := repeatJSON(`{"file_a":"a","file_b":"b","co_change_count":1,"jaccard":0.5,"cross_module":false}`, 12)
	hotspots := repeatJSON(`{"file":"f","change_count":1,"last_changed_at":""}`, 12)
	bf := repeatJSON(`{"file":"f","sole_author":"x","sole_author_ratio":0.9,"total_commits":1,"days_since_last_other_author":0}`, 12)
	body := `{"commits_scanned":1,"shallow_warning":false,"coupling_pairs":[` + pairs + `],"churn_hotspots":[` + hotspots + `],"bus_factor":[` + bf + `]}`
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, body), nil
	}
	text, isError := s.callTool("quality_evolution", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "10. ")
	require.NotContains(s.T(), text, "11. ")
}

func (s *MCPServerSuite) TestQualityEvolutionAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusInternalServerError, "git error"), nil
	}
	text, isError := s.callTool("quality_evolution", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityEvolutionRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_evolution", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_bugfactor ---

func (s *MCPServerSuite) TestQualityBugFactorSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return jsonResponse(http.StatusOK, `{
			"commits_scanned":250,
			"shallow_warning":false,
			"bus_factor":[{"file":"x.go","sole_author":"alice","sole_author_ratio":0.95,"total_commits":20,"days_since_last_other_author":120}]
		}`), nil
	}
	text, isError := s.callTool("quality_bugfactor", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Scanned 250 commits")
	require.Contains(s.T(), text, "x.go")
	require.Contains(s.T(), text, "alice owns 95%")
	require.Contains(s.T(), text, "120 days")
}

func (s *MCPServerSuite) TestQualityBugFactorEmpty() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"commits_scanned":250,"shallow_warning":false,"bus_factor":[]}`), nil
	}
	text, isError := s.callTool("quality_bugfactor", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Scanned 250 commits")
	require.Contains(s.T(), text, "no concentrated bus-factor risks")
}

func (s *MCPServerSuite) TestQualityBugFactorAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusInternalServerError, "git error"), nil
	}
	text, isError := s.callTool("quality_bugfactor", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityBugFactorRequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_bugfactor", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}

// --- quality_c4 ---

func (s *MCPServerSuite) TestQualityC4Success() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return jsonResponse(http.StatusOK, `{
			"mermaid":"flowchart LR\n  cmd --> internal_api",
			"component_count":2,
			"edge_count":1
		}`), nil
	}
	text, isError := s.callTool("quality_c4", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "C4 component diagram")
	require.Contains(s.T(), text, "2 components")
	require.Contains(s.T(), text, "1 cross-component edges")
	require.Contains(s.T(), text, "```mermaid")
	require.Contains(s.T(), text, "flowchart LR")
}

func (s *MCPServerSuite) TestQualityC4APIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusServiceUnavailable, "no graph"), nil
	}
	text, isError := s.callTool("quality_c4", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "API error")
}

func (s *MCPServerSuite) TestQualityC4RequiresChannel() {
	s.srv.channelID = ""
	text, isError := s.callTool("quality_c4", map[string]any{})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "requires a channel")
}
