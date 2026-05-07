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
	Message string `json:"message" jsonschema:"required,The task or topic for the thread. A new agent will be triggered in the thread with this message as its prompt."`
}

type createWorktreeThreadInput struct {
	Branch  string `json:"branch" jsonschema:"required,The git branch to check out in the new worktree (existing or new branch name)"`
	Name    string `json:"name,omitempty" jsonschema:"Optional name for the worktree directory. If omitted, a random 'wt-XXXX' name is generated."`
	Message string `json:"message,omitempty" jsonschema:"Optional task or topic for the new worktree thread. If provided, an agent is triggered immediately with this message as its prompt."`
}

type deleteThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"The ID of the thread to delete"`
}

func (s *Server) handleCreateThread(_ context.Context, _ *mcp.CallToolRequest, input createThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "create_thread", "channel_id", s.channelID, "name", input.Name)

	if input.Name == "" {
		return errorResult("name is required"), nil, nil
	}
	if input.Message == "" {
		return errorResult("message is required"), nil, nil
	}

	reqBody := map[string]string{
		"channel_id": s.channelID,
		"name":       input.Name,
		"message":    input.Message,
	}
	if s.authorID != "" {
		reqBody["author_id"] = s.authorID
	}
	data, _ := json.Marshal(reqBody)

	type threadResult struct {
		ThreadID string `json:"thread_id"`
	}
	result, errResult, err := doAPICall[threadResult](s, "POST", s.apiURL+"/api/threads", http.StatusCreated, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Thread created successfully (ID: %s). A new agent has been triggered in the thread. Do NOT perform the task yourself — just tell the user the thread was created.", result.ThreadID)},
		},
	}, nil, nil
}

func (s *Server) handleCreateWorktreeThread(_ context.Context, _ *mcp.CallToolRequest, input createWorktreeThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "create_worktree_thread", "channel_id", s.channelID, "branch", input.Branch, "name", input.Name)

	if input.Branch == "" {
		return errorResult("branch is required"), nil, nil
	}

	reqBody := map[string]string{
		"channel_id": s.channelID,
		"branch":     input.Branch,
	}
	if input.Name != "" {
		reqBody["name"] = input.Name
	}
	if input.Message != "" {
		reqBody["message"] = input.Message
	}
	if s.authorID != "" {
		reqBody["author_id"] = s.authorID
	}
	data, _ := json.Marshal(reqBody)

	type worktreeResult struct {
		ThreadID     string `json:"thread_id"`
		WorktreePath string `json:"worktree_path"`
	}
	result, errResult, err := doAPICall[worktreeResult](s, "POST", s.apiURL+"/api/worktrees", http.StatusCreated, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	suffix := "The thread has no agent triggered — use send_message to post a task into it."
	if input.Message != "" {
		suffix = "An agent has been triggered in the thread with the supplied message. Do NOT perform the task yourself — just tell the user the thread was created."
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Worktree thread created (ID: %s, path: %s). Branch %q is checked out at the worktree path. %s", result.ThreadID, result.WorktreePath, input.Branch, suffix)},
		},
	}, nil, nil
}

func (s *Server) handleDeleteThread(_ context.Context, _ *mcp.CallToolRequest, input deleteThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "delete_thread", "thread_id", input.ThreadID)

	if input.ThreadID == "" {
		return errorResult("thread_id is required"), nil, nil
	}

	if errResult, err := doAPICallNoBody(s, "DELETE", fmt.Sprintf("%s/api/threads/%s", s.apiURL, input.ThreadID), http.StatusNoContent, nil); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Thread %s deleted successfully.", input.ThreadID)},
		},
	}, nil, nil
}
