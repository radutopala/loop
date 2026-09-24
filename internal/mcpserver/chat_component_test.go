package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- chat_component tool ---

func (s *MCPServerSuite) TestChatComponentTemplates() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Equal(s.T(), "/api/components", req.URL.Path)
		require.Equal(s.T(), "test-channel", req.URL.Query().Get("channel_id"))
		require.Equal(s.T(), "guide", req.URL.Query().Get("format"))
		return jsonResponse(http.StatusOK, "## math\nPaper"), nil
	}

	text, isError := s.callTool("chat_component", map[string]any{"action": "templates"})
	require.False(s.T(), isError)
	require.Equal(s.T(), "## math\nPaper", text)
}

func (s *MCPServerSuite) TestChatComponentShow() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Equal(s.T(), "/api/components", req.URL.Path)
		require.Equal(s.T(), "test-channel", req.URL.Query().Get("channel_id"))
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), map[string]string{"template": "math", "title": "Fracții", "html": "<p>x</p>", "css": "p{}", "js": "go()"}, payload)
		return jsonResponse(http.StatusCreated, `{"msg_id":"component-1"}`), nil
	}

	text, isError := s.callTool("chat_component", map[string]any{
		"action": "show", "template": "math", "title": "Fracții", "html": "<p>x</p>", "css": "p{}", "js": "go()",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, `Component "math" is shown in the chat`)
}

func (s *MCPServerSuite) TestChatComponentErrors() {
	tests := []struct {
		name  string
		args  map[string]any
		do    func(*http.Request) (*http.Response, error)
		wantT string
	}{
		{name: "unknown action", args: map[string]any{"action": "draw"}, wantT: "action must be one of: templates, show"},
		{name: "show without template", args: map[string]any{"action": "show", "html": "<p>x</p>"}, wantT: "template is required"},
		{
			name:  "templates network error",
			args:  map[string]any{"action": "templates"},
			do:    func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") },
			wantT: "calling API: ",
		},
		{
			name: "templates API error",
			args: map[string]any{"action": "templates"},
			do: func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, "failed to load config"), nil
			},
			wantT: "API error (status 500): failed to load config",
		},
		{
			name:  "show network error",
			args:  map[string]any{"action": "show", "template": "math", "html": "<p>x</p>"},
			do:    func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") },
			wantT: "calling API: ",
		},
		{
			name: "show rejected on another platform",
			args: map[string]any{"action": "show", "template": "math", "html": "<p>x</p>"},
			do: func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusBadRequest, "chat components render in the Loop desktop app only; answer in text instead"), nil
			},
			wantT: "API error (status 400): chat components render in the Loop desktop app only",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.httpClient.doFunc = tc.do
			text, isError := s.callTool("chat_component", tc.args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tc.wantT)
		})
	}
}
