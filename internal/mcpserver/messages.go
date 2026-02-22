package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sendMessageInput struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel or thread ID to send the message to"`
	Content   string `json:"content" jsonschema:"The message content to send"`
}

func (s *Server) handleSendMessage(_ context.Context, _ *mcp.CallToolRequest, input sendMessageInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "send_message", "channel_id", input.ChannelID, "content", input.Content)

	if input.ChannelID == "" {
		return errorResult("channel_id is required"), nil, nil
	}
	if input.Content == "" {
		return errorResult("content is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]string{
		"channel_id": input.ChannelID,
		"content":    input.Content,
	})

	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/messages", data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusNoContent {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Message sent successfully."},
		},
	}, nil, nil
}
