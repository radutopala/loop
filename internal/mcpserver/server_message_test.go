package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
	s.runToolErrorCases(toolErrorSpec{
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

func (s *MCPServerSuite) TestQueueMessageDelayed() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"delay_seconds":30`)
		// A delay overrides interrupt — it is forced false on the wire.
		require.Contains(s.T(), string(body), `"interrupt":false`)
		return jsonResponse(http.StatusOK, `{"msg_id":"ask-1"}`), nil
	}

	text, isError := s.callTool("queue_message", map[string]any{"content": "later", "delay_seconds": 30, "interrupt": true})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "30s delay")
	require.Contains(s.T(), text, "msg_id: ask-1")
}

func (s *MCPServerSuite) TestQueueMessageDelayedErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "queue_message",
		args:         map[string]any{"content": "later", "delay_seconds": 30},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "queue failed",
		decodeStatus: http.StatusOK,
	})
}

func (s *MCPServerSuite) TestQueueMessageNegativeDelay() {
	text, isError := s.callTool("queue_message", map[string]any{"content": "later", "delay_seconds": -5})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "delay_seconds cannot be negative")
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
	s.runToolErrorCases(toolErrorSpec{
		tool:      "queue_message",
		args:      map[string]any{"content": "hello"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "queue failed",
	})
}

// --- list_queued_messages / delete_queued_message ---

func (s *MCPServerSuite) TestListQueuedMessages() {
	long := strings.Repeat("x", 130)
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "empty",
			body: `{"messages":[]}`,
			want: []string{"No queued messages"},
		},
		{
			name: "running, delayed and queued",
			body: `{"messages":[` +
				`{"msg_id":"m1","content":"check the PR","is_running":true},` +
				`{"msg_id":"m2","content":"tag\nthe release","not_before":1790000000},` +
				`{"msg_id":"m3","content":"` + long + `"}]}`,
			want: []string{
				"3 queued message(s)",
				"- m1 [running] check the PR",
				"- m2 [delayed until 2026-09-21T14:13:20Z] tag the release",
				"- m3 [queued] " + strings.Repeat("x", 120) + "…",
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "GET", req.Method)
				require.Equal(s.T(), "/api/channels/test-channel/queued", req.URL.Path)
				return jsonResponse(http.StatusOK, tt.body), nil
			}
			text, isError := s.callTool("list_queued_messages", map[string]any{})
			require.False(s.T(), isError)
			for _, w := range tt.want {
				require.Contains(s.T(), text, w)
			}
		})
	}
}

func (s *MCPServerSuite) TestListQueuedMessagesErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "list_queued_messages",
		args:         map[string]any{},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "list failed",
		decodeStatus: http.StatusOK,
	})
}

func (s *MCPServerSuite) TestDeleteQueuedMessage() {
	const queue = `{"messages":[{"msg_id":"m1","content":"now","is_running":true},{"msg_id":"m2","content":"later","not_before":1790000000}]}`
	tests := []struct {
		name       string
		msgID      string
		deleteCode int
		wantDelete bool
		wantError  bool
		wantText   string
	}{
		{name: "removes a waiting message", msgID: "m2", deleteCode: http.StatusNoContent, wantDelete: true, wantText: "Removed queued message m2."},
		{name: "refuses the running message", msgID: "m1", wantError: true, wantText: "m1 is already running"},
		{name: "unknown id", msgID: "gone", wantError: true, wantText: "no queued message gone"},
		{name: "delete fails", msgID: "m2", deleteCode: http.StatusNotFound, wantDelete: true, wantError: true, wantText: "API error (status 404)"},
		{name: "msg_id required", msgID: "", wantError: true, wantText: "msg_id is required"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			deleted := false
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				if req.Method == "GET" {
					return jsonResponse(http.StatusOK, queue), nil
				}
				require.Equal(s.T(), "DELETE", req.Method)
				require.Equal(s.T(), "/api/messages/"+tt.msgID, req.URL.Path)
				require.Equal(s.T(), "test-channel", req.URL.Query().Get("channel_id"))
				deleted = true
				return jsonResponse(tt.deleteCode, "not found or not deletable"), nil
			}
			text, isError := s.callTool("delete_queued_message", map[string]any{"msg_id": tt.msgID})
			require.Equal(s.T(), tt.wantError, isError)
			require.Contains(s.T(), text, tt.wantText)
			require.Equal(s.T(), tt.wantDelete, deleted)
		})
	}
}

func (s *MCPServerSuite) TestDeleteQueuedMessageListErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "delete_queued_message",
		args:         map[string]any{"msg_id": "m2"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "list failed",
		decodeStatus: http.StatusOK,
	})
}

// TestQueuedMessagesNoChannel covers the channel-scoped guard: an agent with
// no channel of its own has no queue to list or remove from.
func (s *MCPServerSuite) TestQueuedMessagesNoChannel() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	res, _, err := srv.handleListQueuedMessages(context.Background(), nil, listQueuedMessagesInput{})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel-scoped")

	res, _, err = srv.handleDeleteQueuedMessage(context.Background(), nil, deleteQueuedMessageInput{MsgID: "m1"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel-scoped")
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
