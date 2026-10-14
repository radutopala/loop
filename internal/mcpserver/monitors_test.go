package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func (s *MCPServerSuite) TestMonitor() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Equal(s.T(), "/api/channels/test-channel/monitors", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		require.NoError(s.T(), err)
		var got map[string]any
		require.NoError(s.T(), json.Unmarshal(body, &got))
		require.Equal(s.T(), map[string]any{"command": "until false; do sleep 60; done", "description": "pr merge", "timeout_sec": float64(600), "interrupt": true}, got)
		return jsonResponse(http.StatusCreated, `{"id":"mon-a","timeout_sec":600}`), nil
	}
	text, isError := s.callTool("monitor", map[string]any{"command": "until false; do sleep 60; done", "description": "pr merge", "timeout_sec": 600, "interrupt": true})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Monitor mon-a started (times out after 600s). End your turn now")
	require.Contains(s.T(), text, "[monitor mon-a]")
}

func (s *MCPServerSuite) TestMonitorRequiresFields() {
	for _, args := range []map[string]any{
		{"command": "", "description": "pr merge"},
		{"command": "sleep 1", "description": " "},
	} {
		text, isError := s.callTool("monitor", args)
		require.True(s.T(), isError)
		require.Contains(s.T(), text, "command and description are required")
	}
}

func (s *MCPServerSuite) TestMonitorErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "monitor",
		args:         map[string]any{"command": "sleep 1", "description": "wait"},
		apiStatus:    http.StatusConflict,
		apiBody:      "a channel can run at most 10 monitors at once",
		decodeStatus: http.StatusCreated,
	})
}

func (s *MCPServerSuite) TestListMonitors() {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"none", `{"monitors":[]}`, []string{"No monitors running"}},
		{"two", `{"monitors":[` +
			`{"id":"mon-a","description":"pr merge","command":"until merged; do sleep 60; done","started_at":"2026-01-02T03:04:05Z","timeout_sec":3600},` +
			`{"id":"mon-b","description":"deploy","command":"wait-deploy","started_at":"2026-01-02T04:00:00+01:00","timeout_sec":60}]}`,
			[]string{
				"2 monitor(s) running",
				`- mon-a "pr merge" since 2026-01-02T03:04:05Z (timeout 3600s): until merged; do sleep 60; done`,
				`- mon-b "deploy" since 2026-01-02T03:00:00Z (timeout 60s): wait-deploy`,
			}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "GET", req.Method)
				require.Equal(s.T(), "/api/channels/test-channel/monitors", req.URL.Path)
				return jsonResponse(http.StatusOK, tt.body), nil
			}
			text, isError := s.callTool("list_monitors", map[string]any{})
			require.False(s.T(), isError)
			for _, w := range tt.want {
				require.Contains(s.T(), text, w)
			}
		})
	}
}

func (s *MCPServerSuite) TestListMonitorsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "list_monitors",
		args:         map[string]any{},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "list failed",
		decodeStatus: http.StatusOK,
	})
}

func (s *MCPServerSuite) TestCancelMonitor() {
	tests := []struct {
		name      string
		id        string
		code      int
		wantError bool
		wantText  string
	}{
		{"cancels", "mon-a", http.StatusNoContent, false, "Cancelled monitor mon-a."},
		{"unknown", "mon-b", http.StatusNotFound, true, "API error (status 404)"},
		{"id required", "", 0, true, "monitor_id is required"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "DELETE", req.Method)
				require.Equal(s.T(), "/api/channels/test-channel/monitors/"+tt.id, req.URL.Path)
				return jsonResponse(tt.code, "monitor not found"), nil
			}
			text, isError := s.callTool("cancel_monitor", map[string]any{"monitor_id": tt.id})
			require.Equal(s.T(), tt.wantError, isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestCancelMonitorErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:      "cancel_monitor",
		args:      map[string]any{"monitor_id": "mon-a"},
		apiStatus: http.StatusNotFound,
		apiBody:   "monitor not found",
	})
}

// TestMonitorsNoChannel covers the channel-scoped guard: an agent with no
// channel of its own has nothing to wake.
func (s *MCPServerSuite) TestMonitorsNoChannel() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	for _, call := range []func() (*mcp.CallToolResult, any, error){
		func() (*mcp.CallToolResult, any, error) {
			return srv.handleMonitor(context.Background(), nil, monitorInput{Command: "sleep 1", Description: "wait"})
		},
		func() (*mcp.CallToolResult, any, error) {
			return srv.handleListMonitors(context.Background(), nil, listMonitorsInput{})
		},
		func() (*mcp.CallToolResult, any, error) {
			return srv.handleCancelMonitor(context.Background(), nil, cancelMonitorInput{MonitorID: "mon-a"})
		},
	} {
		res, _, err := call()
		require.NoError(s.T(), err)
		require.True(s.T(), res.IsError)
		require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel-scoped")
	}
}
