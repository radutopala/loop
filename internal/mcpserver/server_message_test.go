package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// TestSendMessageDefaultsToCurrentChannel covers the optional channel_id
// fallback: when channel_id is omitted, the message targets the agent's own
// channel (s.channelID, "test-channel" in the suite).
func (s *MCPServerSuite) TestSendMessageDefaultsToCurrentChannel() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.Contains(s.T(), string(body), `"content":"hi"`)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("send_message", map[string]any{"content": "hi"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Message sent successfully")
}

func (s *MCPServerSuite) TestSendMessageValidation() {
	// content is required even when channel_id defaults to the current channel.
	text, isError := s.callTool("send_message", map[string]any{"content": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "content is required")
}

// TestSendMessageNoChannelRequired covers the channel_id-required branch: a
// server with no channel of its own and no explicit channel_id has nothing to
// target.
func (s *MCPServerSuite) TestSendMessageNoChannelRequired() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	res, _, err := srv.handleSendMessage(context.Background(), nil, sendMessageInput{Content: "hi"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel_id is required")
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

// --- queue_message ---

func (s *MCPServerSuite) TestQueueMessageSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/messages")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.Contains(s.T(), string(body), `"content":"do the next thing"`)
		require.Contains(s.T(), string(body), `"interrupt":false`)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("queue_message", map[string]any{"content": "do the next thing"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "queued in the current channel")
}

func (s *MCPServerSuite) TestQueueMessageInterrupt() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"interrupt":true`)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("queue_message", map[string]any{"content": "urgent", "interrupt": true})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run next")
}

func (s *MCPServerSuite) TestQueueMessageValidation() {
	text, isError := s.callTool("queue_message", map[string]any{"content": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "content is required")
}

// TestQueueMessageNoChannel covers the channel-scoped guard: an agent with no
// channel of its own cannot self-queue.
func (s *MCPServerSuite) TestQueueMessageNoChannel() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	res, _, err := srv.handleQueueMessage(context.Background(), nil, queueMessageInput{Content: "hi"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel-scoped")
}

func (s *MCPServerSuite) TestQueueMessageErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "queue failed"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("queue_message", map[string]any{"content": "hello"})
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

// --- permission_prompt ---

// TestPermissionPromptAllows verifies the tool accepts Claude's
// {tool_name, input, tool_use_id} permission payload (the shape that a strict
// schema like get_readme rejected) and returns an allow decision that echoes
// the input unchanged.
func (s *MCPServerSuite) TestPermissionPromptAllows() {
	text, isError := s.callTool("permission_prompt", map[string]any{
		"tool_name":   "AskUserQuestion",
		"input":       map[string]any{"questions": []any{map[string]any{"question": "Which?"}}},
		"tool_use_id": "toolu_123",
	})
	require.False(s.T(), isError)

	var decision struct {
		Behavior     string `json:"behavior"`
		UpdatedInput struct {
			Questions []any `json:"questions"`
		} `json:"updatedInput"`
	}
	require.NoError(s.T(), json.Unmarshal([]byte(text), &decision))
	require.Equal(s.T(), "allow", decision.Behavior)
	require.Len(s.T(), decision.UpdatedInput.Questions, 1)
}

// TestPermissionPromptMissingInput defaults updatedInput to an empty object
// when no input field is supplied.
func (s *MCPServerSuite) TestPermissionPromptMissingInput() {
	text, isError := s.callTool("permission_prompt", map[string]any{
		"tool_name": "ExitPlanMode",
	})
	require.False(s.T(), isError)

	var decision map[string]any
	require.NoError(s.T(), json.Unmarshal([]byte(text), &decision))
	require.Equal(s.T(), "allow", decision["behavior"])
	require.Equal(s.T(), map[string]any{}, decision["updatedInput"])
}
