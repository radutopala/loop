package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- ui_state, ui_run tools ---

func (s *MCPServerSuite) TestUIState() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Equal(s.T(), "/api/ui/state", req.URL.Path)
		return jsonResponse(http.StatusOK, `{"version":3,"clients":[]}`), nil
	}
	text, isError := s.callTool("ui_state", map[string]any{})
	require.False(s.T(), isError)
	require.Equal(s.T(), `{"version":3,"clients":[]}`, text)
}

func (s *MCPServerSuite) TestUIStateErrors() {
	tests := []struct {
		name string
		do   func(*http.Request) (*http.Response, error)
		want string
	}{
		{"network error", func(*http.Request) (*http.Response, error) { return nil, errors.New("refused") }, "calling API: "},
		{"api error", func(*http.Request) (*http.Response, error) { return stringResponse(http.StatusForbidden, "no"), nil }, "API error (status 403): no"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.do
			text, isError := s.callTool("ui_state", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.want)
		})
	}
}

func (s *MCPServerSuite) TestUIRun() {
	tests := []struct {
		name      string
		steps     []any
		wantSteps string
	}{
		{"in this channel", []any{map[string]any{"op": "set_tab", "tab": "Git"}}, `[{"op":"select_channel","channel_id":"test-channel"},{"op":"set_tab","tab":"Git"}]`},
		{"in the channel it opens", []any{map[string]any{"op": "select_channel", "channel_id": "th-1"}}, `[{"op":"select_channel","channel_id":"th-1"}]`},
		{
			"keeping a false and a 0",
			[]any{
				map[string]any{"op": "send_input", "pane": "docker-shell", "text": "ls", "submit": false},
				map[string]any{"op": "wait_for", "pane": "docker-shell", "quiet_ms": 0, "lines": 5},
			},
			`[{"op":"select_channel","channel_id":"test-channel"},{"op":"send_input","pane":"docker-shell","text":"ls","submit":false},{"op":"wait_for","pane":"docker-shell","quiet_ms":0,"lines":5}]`,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "POST", req.Method)
				require.Equal(s.T(), "/api/ui/commands", req.URL.Path)
				body, _ := io.ReadAll(req.Body)
				var payload struct {
					Steps    json.RawMessage `json:"steps"`
					ClientID string          `json:"client_id"`
					Timeout  string          `json:"timeout"`
				}
				require.NoError(s.T(), json.Unmarshal(body, &payload))
				require.JSONEq(s.T(), tt.wantSteps, string(payload.Steps))
				require.Equal(s.T(), "w1", payload.ClientID)
				require.Equal(s.T(), "30s", payload.Timeout)
				return jsonResponse(http.StatusOK, `{"client_id":"w1","results":[{"op":"set_tab","ok":true}]}`), nil
			}
			text, isError := s.callTool("ui_run", map[string]any{"steps": tt.steps, "client_id": "w1", "timeout": "30s"})
			require.False(s.T(), isError)
			require.JSONEq(s.T(), `{"client_id":"w1","results":[{"op":"set_tab","ok":true}]}`, text)
		})
	}
}

func (s *MCPServerSuite) TestUIRunErrors() {
	tests := []struct {
		name string
		args map[string]any
		do   func(*http.Request) (*http.Response, error)
		want string
	}{
		{name: "no steps", args: map[string]any{"steps": []any{}}, want: "minItems"},
		{name: "unknown op", args: map[string]any{"steps": []any{map[string]any{"op": "close_window"}}}, want: "/op"},
		{name: "unknown panel", args: map[string]any{"steps": []any{map[string]any{"op": "add_pane", "panel": "terminal"}}}, want: "/panel"},
		{name: "unknown open_mode", args: map[string]any{"steps": []any{map[string]any{"op": "add_pane", "panel": "docker-agent", "open_mode": "new"}}}, want: "/open_mode"},
		{name: "unknown field", args: map[string]any{"steps": []any{map[string]any{"op": "set_tab", "tabb": "Git"}}}, want: "tabb"},
		{
			name: "network error",
			do:   func(*http.Request) (*http.Response, error) { return nil, errors.New("refused") },
			want: "calling API: ",
		},
		{
			name: "api error",
			do: func(*http.Request) (*http.Response, error) {
				return stringResponse(http.StatusNotFound, "no app window"), nil
			},
			want: "API error (status 404): no app window",
		},
		{
			name: "bad response",
			do:   func(*http.Request) (*http.Response, error) { return stringResponse(http.StatusOK, "nope"), nil },
			want: "decoding response: ",
		},
		{
			name: "a step fails",
			do: func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"client_id":"w1","results":[{"op":"set_tab","ok":false,"error":"no tab"}]}`), nil
			},
			want: `"error":"no tab"`,
		},
		{
			name: "the window fails",
			do: func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"client_id":"w1","results":[],"error":"boom"}`), nil
			},
			want: `"error":"boom"`,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.do
			args := tt.args
			if args == nil {
				args = map[string]any{"steps": []any{map[string]any{"op": "restore_pane"}}}
			}
			text, isError := s.callTool("ui_run", args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.want)
		})
	}
}
