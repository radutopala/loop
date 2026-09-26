package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createThreadInput struct {
	Name    string `json:"name" jsonschema:"The name for the new thread"`
	Message string `json:"message" jsonschema:"required,The task or topic for the thread. A new agent will be triggered in the thread with this message as its prompt."`
}

type createWorktreeThreadInput struct {
	Branch  string `json:"branch" jsonschema:"required,The existing base branch to fork the worktree from (e.g. 'main'). A fresh 'worktree/<name>' branch is created off it and checked out — this is NOT the name of the new branch. Must be an existing ref."`
	Name    string `json:"name,omitempty" jsonschema:"Optional name for the worktree directory. If omitted, a random 'wt-XXXX' name is generated."`
	Message string `json:"message,omitempty" jsonschema:"Optional task or topic for the new worktree thread. If provided, an agent is triggered immediately with this message as its prompt."`
}

type deleteThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"The ID of the thread to delete"`
}

type forkThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"required,The ID of the thread to fork. A sibling thread is created that continues this thread's conversation on a forked session."`
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
	// The worktree is checked out on a fresh branch "worktree/<dir>" forked from
	// the base branch (input.Branch), not on input.Branch itself.
	newBranch := "worktree/" + filepath.Base(result.WorktreePath)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Worktree thread created (ID: %s, path: %s). A fresh branch %q (forked from base %q) is checked out at the worktree path. %s", result.ThreadID, result.WorktreePath, newBranch, input.Branch, suffix)},
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

func (s *Server) handleForkThread(_ context.Context, _ *mcp.CallToolRequest, input forkThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "fork_thread", "thread_id", input.ThreadID)

	if input.ThreadID == "" {
		return errorResult("thread_id is required"), nil, nil
	}

	type forkResult struct {
		ThreadID     string `json:"thread_id"`
		WorktreePath string `json:"worktree_path"`
	}
	result, errResult, err := doAPICall[forkResult](s, "POST", fmt.Sprintf("%s/api/threads/%s/fork", s.apiURL, input.ThreadID), http.StatusCreated, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	msg := fmt.Sprintf("Thread %s forked (new thread ID: %s). It continues the source conversation on a forked session — the source thread is untouched. No agent is triggered; use send_message with the new thread's ID to post a task into the fork.", input.ThreadID, result.ThreadID)
	if result.WorktreePath != "" {
		msg = fmt.Sprintf("Worktree thread %s forked (new thread ID: %s, path: %s). A fresh worktree is branched from the source worktree's committed state, and the fork continues the source conversation on a forked session — the source thread is untouched. No agent is triggered; use send_message with the new thread's ID to post a task into the fork.", input.ThreadID, result.ThreadID, result.WorktreePath)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}, nil, nil
}

type renameThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"required,The ID of the thread to rename"`
	Name     string `json:"name" jsonschema:"required,New display name for the thread"`
}

func (s *Server) handleRenameThread(_ context.Context, _ *mcp.CallToolRequest, input renameThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "rename_thread", "thread_id", input.ThreadID)

	if input.ThreadID == "" {
		return errorResult("thread_id is required"), nil, nil
	}
	if input.Name == "" {
		return errorResult("name is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]string{"name": input.Name})
	if errResult, err := doAPICallNoBody(s, "POST", fmt.Sprintf("%s/api/channels/%s/rename", s.apiURL, input.ThreadID), http.StatusOK, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Thread %s renamed to %q successfully.", input.ThreadID, input.Name)},
		},
	}, nil, nil
}

type setThreadDescriptionInput struct {
	ThreadID    string `json:"thread_id,omitempty" jsonschema:"The ID of the thread to describe; defaults to the current thread"`
	Description string `json:"description" jsonschema:"What the thread is for, in a line or two; empty clears it"`
}

func (s *Server) handleSetThreadDescription(_ context.Context, _ *mcp.CallToolRequest, input setThreadDescriptionInput) (*mcp.CallToolResult, any, error) {
	threadID := input.ThreadID
	if threadID == "" {
		threadID = s.channelID
	}
	s.logger.Info("mcp tool call", "tool", "set_thread_description", "thread_id", threadID)

	if threadID == "" {
		return errorResult("thread_id is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]string{"description": input.Description})
	if errResult, err := doAPICallNoBody(s, "POST", fmt.Sprintf("%s/api/channels/%s/description", s.apiURL, threadID), http.StatusOK, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	text := fmt.Sprintf("Description of thread %s set.", threadID)
	if strings.TrimSpace(input.Description) == "" {
		text = fmt.Sprintf("Description of thread %s cleared.", threadID)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

type setTicketURLInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"The ID of the channel or thread to link; defaults to the current one"`
	TicketURL string `json:"ticket_url" jsonschema:"The ticket's absolute http(s) URL (Jira, GitHub, Linear, …); empty clears it"`
}

func (s *Server) handleSetTicketURL(_ context.Context, _ *mcp.CallToolRequest, input setTicketURLInput) (*mcp.CallToolResult, any, error) {
	channelID := input.ChannelID
	if channelID == "" {
		channelID = s.channelID
	}
	s.logger.Info("mcp tool call", "tool", "set_ticket_url", "channel_id", channelID)

	if channelID == "" {
		return errorResult("channel_id is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]string{"ticket_url": input.TicketURL})
	if errResult, err := doAPICallNoBody(s, "POST", fmt.Sprintf("%s/api/channels/%s/ticket", s.apiURL, channelID), http.StatusOK, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	text := fmt.Sprintf("Ticket of %s set to %s.", channelID, strings.TrimSpace(input.TicketURL))
	if strings.TrimSpace(input.TicketURL) == "" {
		text = fmt.Sprintf("Ticket of %s cleared.", channelID)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}
