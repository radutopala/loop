package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sendMessageInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"The channel or thread ID to send the message to. Optional — defaults to the current channel/thread this agent is running in when omitted."`
	Content   string `json:"content" jsonschema:"The message content to send"`
}

func (s *Server) handleSendMessage(_ context.Context, _ *mcp.CallToolRequest, input sendMessageInput) (*mcp.CallToolResult, any, error) {
	// channel_id is optional: an empty value targets the agent's own
	// channel/thread, matching every other channel-scoped tool in this
	// package (create_thread, tasks, quality_*, ...). This lets an agent
	// enqueue a follow-up into its own queue without first discovering its
	// channel id via search_channels.
	channelID := input.ChannelID
	if channelID == "" {
		channelID = s.channelID
	}

	s.logger.Info("mcp tool call", "tool", "send_message", "channel_id", channelID, "content", input.Content)

	if channelID == "" {
		return errorResult("channel_id is required"), nil, nil
	}
	if input.Content == "" {
		return errorResult("content is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]string{
		"channel_id": channelID,
		"content":    input.Content,
	})

	if errResult, err := doAPICallNoBody(s, "POST", s.apiURL+"/api/messages", http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Message sent successfully."},
		},
	}, nil, nil
}

type queueMessageInput struct {
	Content   string `json:"content" jsonschema:"The prompt to enqueue as a follow-up turn in the current channel/thread/worktree"`
	Interrupt bool   `json:"interrupt,omitempty" jsonschema:"When true, jump the queue and cancel the active run so this prompt runs next. When false (default), the prompt waits behind any already-queued items."`
}

// handleQueueMessage enqueues a follow-up prompt into the agent's OWN channel
// (s.channelID). It is a focused self-queue affordance: unlike send_message it
// never targets another channel, and it exposes the interrupt flag so an agent
// can either append a follow-up turn (default) or bump it to run next. The row
// lands in the same per-channel pending queue user messages flow through and
// shows up in the chat UI's queued-messages list.
func (s *Server) handleQueueMessage(_ context.Context, _ *mcp.CallToolRequest, input queueMessageInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "queue_message", "channel_id", s.channelID, "interrupt", input.Interrupt, "content", input.Content)

	if s.channelID == "" {
		return errorResult("queue_message is only available to channel-scoped agents"), nil, nil
	}
	if input.Content == "" {
		return errorResult("content is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]any{
		"channel_id": s.channelID,
		"content":    input.Content,
		"interrupt":  input.Interrupt,
	})

	if errResult, err := doAPICallNoBody(s, "POST", s.apiURL+"/api/messages", http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	msg := "Prompt queued in the current channel."
	if input.Interrupt {
		msg = "Prompt queued to run next (active run interrupted)."
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}, nil, nil
}
