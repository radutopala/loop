package mcpserver

import (
	"context"
	"encoding/json"
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
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "send_message",
		args:      map[string]any{"channel_id": "ch-1", "content": "hello"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "send failed",
	})
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
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "queue_message",
		args:      map[string]any{"content": "hello"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "queue failed",
	})
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
// the input unchanged for non-AskUserQuestion tools.
func (s *MCPServerSuite) TestPermissionPromptAllows() {
	text, isError := s.callTool("permission_prompt", map[string]any{
		"tool_name":   "EnterPlanMode",
		"input":       map[string]any{"reason": "planning"},
		"tool_use_id": "toolu_123",
	})
	require.False(s.T(), isError)

	var decision struct {
		Behavior     string `json:"behavior"`
		UpdatedInput struct {
			Reason string `json:"reason"`
		} `json:"updatedInput"`
	}
	require.NoError(s.T(), json.Unmarshal([]byte(text), &decision))
	require.Equal(s.T(), "allow", decision.Behavior)
	require.Equal(s.T(), "planning", decision.UpdatedInput.Reason)
}

// TestPermissionPromptMissingInput defaults updatedInput to an empty object
// when no input field is supplied.
func (s *MCPServerSuite) TestPermissionPromptMissingInput() {
	text, isError := s.callTool("permission_prompt", map[string]any{
		"tool_name": "EnterPlanMode",
	})
	require.False(s.T(), isError)

	var decision map[string]any
	require.NoError(s.T(), json.Unmarshal([]byte(text), &decision))
	require.Equal(s.T(), "allow", decision["behavior"])
	require.Equal(s.T(), map[string]any{}, decision["updatedInput"])
}

// TestPermissionPromptDeniesInteractiveTools verifies the tool does NOT allow
// AskUserQuestion or ExitPlanMode (which would let Claude natively self-resolve
// them — "user did not answer" / "approved your plan"); it returns a deny
// decision whose message tells the model to wait for the user and not retry,
// so the tool_use closes with a persisted result instead of dangling.
func (s *MCPServerSuite) TestPermissionPromptDeniesInteractiveTools() {
	tests := []struct {
		tool    string
		mustSay string
		mustBan string
	}{
		{"AskUserQuestion", "answers will arrive as the next user message", "Do NOT call AskUserQuestion again"},
		{"ExitPlanMode", "decision will arrive as the next user message", "Do NOT start implementing"},
	}
	for _, tt := range tests {
		s.Run(tt.tool, func() {
			text, isError := s.callTool("permission_prompt", map[string]any{
				"tool_name": tt.tool,
				"input":     map[string]any{},
			})
			require.False(s.T(), isError)

			var decision struct {
				Behavior string `json:"behavior"`
				Message  string `json:"message"`
			}
			require.NoError(s.T(), json.Unmarshal([]byte(text), &decision))
			require.Equal(s.T(), "deny", decision.Behavior)
			require.Contains(s.T(), decision.Message, tt.mustSay)
			require.Contains(s.T(), decision.Message, tt.mustBan)
		})
	}
}
