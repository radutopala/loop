package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createThreadInput struct {
	Name    string `json:"name" jsonschema:"The name for the new thread"`
	Message string `json:"message,omitempty" jsonschema:"Optional initial message for the thread. If provided, the bot will post it as a self-mention to trigger a runner immediately."`
}

type deleteThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"The ID of the thread to delete"`
}

func (s *Server) handleCreateThread(_ context.Context, _ *mcp.CallToolRequest, input createThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "create_thread", "channel_id", s.channelID, "name", input.Name)

	if input.Name == "" {
		return errorResult("name is required"), nil, nil
	}

	reqBody := map[string]string{
		"channel_id": s.channelID,
		"name":       input.Name,
	}
	if s.authorID != "" {
		reqBody["author_id"] = s.authorID
	}
	if input.Message != "" {
		reqBody["message"] = input.Message
	}
	data, _ := json.Marshal(reqBody)

	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/threads", data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusCreated {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var result struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Thread created successfully (ID: %s).", result.ThreadID)},
		},
	}, nil, nil
}

func (s *Server) handleDeleteThread(_ context.Context, _ *mcp.CallToolRequest, input deleteThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "delete_thread", "thread_id", input.ThreadID)

	if input.ThreadID == "" {
		return errorResult("thread_id is required"), nil, nil
	}

	respBody, status, err := s.doRequest("DELETE", fmt.Sprintf("%s/api/threads/%s", s.apiURL, input.ThreadID), nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusNoContent {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Thread %s deleted successfully.", input.ThreadID)},
		},
	}, nil, nil
}
