package mcpserver

import (
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- send_message ---

func (s *MCPServerSuite) TestSendMessageSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/messages")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"ch-1"`)
		require.Contains(s.T(), string(body), `"content":"hello world"`)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("send_message", map[string]any{"channel_id": "ch-1", "content": "hello world"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Message sent successfully")
}

func (s *MCPServerSuite) TestSendMessageValidation() {
	tests := []struct {
		name     string
		args     map[string]any
		wantText string
	}{
		{"empty channel_id", map[string]any{"channel_id": "", "content": "hello"}, "channel_id is required"},
		{"empty content", map[string]any{"channel_id": "ch-1", "content": ""}, "content is required"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			text, isError := s.callTool("send_message", tt.args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestSendMessageErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "send failed"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("send_message", map[string]any{"channel_id": "ch-1", "content": "hello"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- get_readme ---

func (s *MCPServerSuite) TestGetReadmeSuccess() {
	text, isError := s.callTool("get_readme", map[string]any{})
	require.False(s.T(), isError)
	require.NotEmpty(s.T(), text)
}
