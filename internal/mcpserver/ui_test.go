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
		{
			"moving $n pane references past the select_channel step",
			[]any{
				map[string]any{"op": "add_pane", "panel": "chat-side", "next_to": "chat"},
				map[string]any{"op": "add_pane", "panel": "docker-shell", "next_to": "$1"},
				map[string]any{"op": "send_input", "pane": "$2", "text": "ls"},
				map[string]any{"op": "send_input", "pane": "$x", "text": "ls"},
				map[string]any{"op": "send_input", "pane": "$99999999999999999999", "text": "ls"},
			},
			`[{"op":"select_channel","channel_id":"test-channel"},{"op":"add_pane","panel":"chat-side","next_to":"chat"},{"op":"add_pane","panel":"docker-shell","next_to":"$2"},{"op":"send_input","pane":"$3","text":"ls"},{"op":"send_input","pane":"$x","text":"ls"},{"op":"send_input","pane":"$99999999999999999999","text":"ls"}]`,
		},
		{
			"keeping $n pane references after the agent's select_channel",
			[]any{
				map[string]any{"op": "select_channel", "channel_id": "th-1"},
				map[string]any{"op": "add_pane", "panel": "chat-side"},
				map[string]any{"op": "send_input", "pane": "$2", "text": "hi"},
			},
			`[{"op":"select_channel","channel_id":"th-1"},{"op":"add_pane","panel":"chat-side"},{"op":"send_input","pane":"$2","text":"hi"}]`,
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
		{name: "unknown direction", args: map[string]any{"steps": []any{map[string]any{"op": "add_pane", "panel": "git", "direction": "diagonal"}}}, want: "/direction"},
		{name: "unknown side", args: map[string]any{"steps": []any{map[string]any{"op": "add_pane", "panel": "git", "side": "middle"}}}, want: "/side"},
		{name: "unknown scope", args: map[string]any{"steps": []any{map[string]any{"op": "add_pane", "panel": "playground", "item": "p", "scope": "team"}}}, want: "/scope"},
		{name: "unknown view", args: map[string]any{"steps": []any{map[string]any{"op": "open_side", "view": "notes"}}}, want: "/view"},
		{name: "no lines", args: map[string]any{"steps": []any{map[string]any{"op": "read_output", "pane": "chat", "lines": 0}}}, want: "/lines"},
		{name: "too many lines", args: map[string]any{"steps": []any{map[string]any{"op": "read_output", "pane": "chat", "lines": 2001}}}, want: "/lines"},
		{name: "line 0", args: map[string]any{"steps": []any{map[string]any{"op": "open_file", "path": "a.go", "line": 0}}}, want: "/line"},
		{name: "message 0", args: map[string]any{"steps": []any{map[string]any{"op": "show_message", "message_id": 0}}}, want: "/message_id"},
		{name: "negative quiet_ms", args: map[string]any{"steps": []any{map[string]any{"op": "wait_for", "pane": "chat", "quiet_ms": -1}}}, want: "/quiet_ms"},
		{name: "walkthrough 0", args: map[string]any{"steps": []any{map[string]any{"op": "open_walkthrough", "walkthrough_id": 0}}}, want: "/walkthrough_id"},
		{name: "autoplay 0", args: map[string]any{"steps": []any{map[string]any{"op": "open_walkthrough", "walkthrough_id": 1, "autoplay_sec": 0}}}, want: "/autoplay_sec"},
		{name: "walkthrough step 0", args: map[string]any{"steps": []any{map[string]any{"op": "open_walkthrough", "walkthrough_id": 1, "walkthrough_step": 0}}}, want: "/walkthrough_step"},
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

// --- ui_walkthrough tool ---

var walkthroughArgs = map[string]any{
	"title":     "Tour",
	"steps":     []any{map[string]any{"path": "main.go", "start_line": 3, "end_line": 4, "title": "Entry", "body": "Starts here."}},
	"client_id": "w1",
}

// walkthroughDo answers the walkthrough's create with create, then its
// ui command with command.
func (s *MCPServerSuite) walkthroughDo(create, command func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/ui/commands" {
			return command(req)
		}
		return create(req)
	}
}

func (s *MCPServerSuite) TestUIWalkthrough() {
	created := func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Equal(s.T(), "/api/channels/test-channel/walkthroughs", req.URL.Path)
		body, _ := io.ReadAll(req.Body)
		require.JSONEq(s.T(), `{"title":"Tour","steps":[{"path":"main.go","start_line":3,"end_line":4,"title":"Entry","body":"Starts here."}]}`, string(body))
		return jsonResponse(http.StatusCreated, `{"id":7,"title":"Tour"}`), nil
	}
	tests := []struct {
		name    string
		args    map[string]any
		command func(*http.Request) (*http.Response, error)
		want    string
	}{
		{
			name: "opened",
			command: func(req *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(req.Body)
				require.JSONEq(s.T(), `{"client_id":"w1","steps":[{"op":"select_channel","channel_id":"test-channel"},{"op":"open_walkthrough","walkthrough_id":7}]}`, string(body))
				return jsonResponse(http.StatusOK, `{"client_id":"w1","results":[{"ok":true},{"ok":true}]}`), nil
			},
			want: "Saved as walkthrough 7, open in the app's editor.",
		},
		{
			name: "played",
			args: map[string]any{"title": "Tour", "steps": walkthroughArgs["steps"], "autoplay_sec": 5},
			command: func(req *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(req.Body)
				require.JSONEq(s.T(), `{"client_id":"","steps":[{"op":"select_channel","channel_id":"test-channel"},{"op":"open_walkthrough","walkthrough_id":7,"autoplay_sec":5}]}`, string(body))
				return jsonResponse(http.StatusOK, `{"results":[{"ok":true},{"ok":true}]}`), nil
			},
			want: "Saved as walkthrough 7, open in the app's editor.",
		},
		{
			name: "no window",
			command: func(*http.Request) (*http.Response, error) {
				return stringResponse(http.StatusNotFound, "no app window is connected"), nil
			},
			want: "Saved as walkthrough 7; no app window is open",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			args := tt.args
			if args == nil {
				args = walkthroughArgs
			}
			s.httpClient.doFunc = s.walkthroughDo(created, tt.command)
			text, isError := s.callTool("ui_walkthrough", args)
			require.False(s.T(), isError, text)
			require.Contains(s.T(), text, tt.want)
		})
	}
}

func (s *MCPServerSuite) TestUIWalkthroughErrors() {
	created := func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, `{"id":7}`), nil
	}
	ok := func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[{"ok":true}]}`), nil
	}
	refused := func(*http.Request) (*http.Response, error) { return nil, errors.New("refused") }
	tests := []struct {
		name            string
		args            map[string]any
		create, command func(*http.Request) (*http.Response, error)
		want            string
	}{
		{name: "no steps", args: map[string]any{"title": "T", "steps": []any{}}, want: "minItems"},
		{name: "autoplay 0", args: map[string]any{"title": "T", "steps": walkthroughArgs["steps"], "autoplay_sec": 0}, want: "/autoplay_sec"},
		{name: "autoplay too slow", args: map[string]any{"title": "T", "steps": walkthroughArgs["steps"], "autoplay_sec": 601}, want: "/autoplay_sec"},
		{name: "line 0", args: map[string]any{"title": "T", "steps": []any{map[string]any{"path": "a", "start_line": 0, "body": "b"}}}, want: "/start_line"},
		{name: "create network error", create: refused, command: ok, want: "calling API: "},
		{
			name: "create api error", command: ok, want: "API error (status 400): step 1: no file",
			create: func(*http.Request) (*http.Response, error) {
				return stringResponse(http.StatusBadRequest, "step 1: no file"), nil
			},
		},
		{
			name: "create bad response", command: ok, want: "decoding response: ",
			create: func(*http.Request) (*http.Response, error) { return stringResponse(http.StatusCreated, "nope"), nil },
		},
		{name: "command network error", create: created, command: refused, want: "Saved as walkthrough 7, but opening it failed: calling API: "},
		{
			name: "command api error", create: created, want: "Saved as walkthrough 7, but opening it failed: API error (status 403): no",
			command: func(*http.Request) (*http.Response, error) { return stringResponse(http.StatusForbidden, "no"), nil },
		},
		{
			name: "command bad response", create: created, want: "but opening it failed: nope",
			command: func(*http.Request) (*http.Response, error) { return stringResponse(http.StatusOK, "nope"), nil },
		},
		{
			name: "step fails", create: created, want: `but opening it failed: {"results":[{"ok":false,"error":"no walkthrough 7"}]}`,
			command: func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"results":[{"ok":false,"error":"no walkthrough 7"}]}`), nil
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = s.walkthroughDo(tt.create, tt.command)
			args := tt.args
			if args == nil {
				args = walkthroughArgs
			}
			text, isError := s.callTool("ui_walkthrough", args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.want)
		})
	}
}
