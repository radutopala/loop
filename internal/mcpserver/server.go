package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPClient abstracts HTTP calls for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Server wraps the MCP server with tools for task scheduling.
type Server struct {
	channelID  string
	apiURL     string
	mcpServer  *mcp.Server
	httpClient HTTPClient
}

type scheduleTaskInput struct {
	Schedule string `json:"schedule" jsonschema:"Cron expression (e.g. 0 9 * * *) or Go duration (e.g. 5m or 1h)"`
	Type     string `json:"type" jsonschema:"Task type: cron, interval, or once"`
	Prompt   string `json:"prompt" jsonschema:"The prompt to execute on schedule"`
}

type cancelTaskInput struct {
	TaskID int64 `json:"task_id" jsonschema:"The ID of the task to cancel"`
}

type toggleTaskInput struct {
	TaskID  int64 `json:"task_id" jsonschema:"The ID of the task to enable or disable"`
	Enabled bool  `json:"enabled" jsonschema:"Whether to enable (true) or disable (false) the task"`
}

type editTaskInput struct {
	TaskID   int64   `json:"task_id" jsonschema:"The ID of the task to edit"`
	Schedule *string `json:"schedule,omitempty" jsonschema:"New schedule expression (cron or Go duration)"`
	Type     *string `json:"type,omitempty" jsonschema:"New task type: cron, interval, or once"`
	Prompt   *string `json:"prompt,omitempty" jsonschema:"New prompt to execute on schedule"`
}

type listTasksInput struct{}

// New creates a new MCP server with scheduler tools.
func New(channelID, apiURL string, httpClient HTTPClient, logger *slog.Logger) *Server {
	s := &Server{
		channelID:  channelID,
		apiURL:     apiURL,
		httpClient: httpClient,
	}

	s.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    "loop-scheduler",
		Version: "1.0.0",
	}, &mcp.ServerOptions{Logger: logger})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "schedule_task",
		Description: "Create a scheduled task. Use cron expressions (e.g. '0 9 * * *' for daily at 9am) with type 'cron', Go durations (e.g. '5m', '1h') with type 'interval', or type 'once' for one-time execution.",
	}, s.handleScheduleTask)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List all scheduled tasks for this channel.",
	}, s.handleListTasks)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_task",
		Description: "Cancel a scheduled task by its ID.",
	}, s.handleCancelTask)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "toggle_task",
		Description: "Enable or disable a scheduled task by its ID.",
	}, s.handleToggleTask)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "edit_task",
		Description: "Edit a scheduled task's schedule, type, and/or prompt.",
	}, s.handleEditTask)

	return s
}

// Run starts the MCP server on the given transport.
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	return s.mcpServer.Run(ctx, transport)
}

// MCPServer returns the underlying MCP server for testing.
func (s *Server) MCPServer() *mcp.Server {
	return s.mcpServer
}

func (s *Server) doRequest(method, url string, body []byte) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, nil
}

func (s *Server) handleScheduleTask(_ context.Context, _ *mcp.CallToolRequest, input scheduleTaskInput) (*mcp.CallToolResult, any, error) {
	data, _ := json.Marshal(map[string]string{
		"channel_id": s.channelID,
		"schedule":   input.Schedule,
		"type":       input.Type,
		"prompt":     input.Prompt,
	})

	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/tasks", data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusCreated {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task scheduled successfully (ID: %d).", result.ID)},
		},
	}, nil, nil
}

func (s *Server) handleListTasks(_ context.Context, _ *mcp.CallToolRequest, _ listTasksInput) (*mcp.CallToolResult, any, error) {
	respBody, status, err := s.doRequest("GET", fmt.Sprintf("%s/api/tasks?channel_id=%s", s.apiURL, s.channelID), nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var tasks []struct {
		ID        int64  `json:"id"`
		Schedule  string `json:"schedule"`
		Type      string `json:"type"`
		Prompt    string `json:"prompt"`
		Enabled   bool   `json:"enabled"`
		NextRunAt string `json:"next_run_at"`
	}
	if err := json.Unmarshal(respBody, &tasks); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	if len(tasks) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No scheduled tasks."},
			},
		}, nil, nil
	}

	var text string
	for _, t := range tasks {
		text += fmt.Sprintf("- ID %d: %s (schedule: %s, type: %s, enabled: %v)\n", t.ID, t.Prompt, t.Schedule, t.Type, t.Enabled)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}, nil, nil
}

func (s *Server) handleCancelTask(_ context.Context, _ *mcp.CallToolRequest, input cancelTaskInput) (*mcp.CallToolResult, any, error) {
	respBody, status, err := s.doRequest("DELETE", fmt.Sprintf("%s/api/tasks/%d", s.apiURL, input.TaskID), nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusNoContent {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task %d cancelled successfully.", input.TaskID)},
		},
	}, nil, nil
}

func (s *Server) handleEditTask(_ context.Context, _ *mcp.CallToolRequest, input editTaskInput) (*mcp.CallToolResult, any, error) {
	body := map[string]any{}
	if input.Schedule != nil {
		body["schedule"] = *input.Schedule
	}
	if input.Type != nil {
		body["type"] = *input.Type
	}
	if input.Prompt != nil {
		body["prompt"] = *input.Prompt
	}

	if len(body) == 0 {
		return errorResult("at least one of schedule, type, or prompt is required"), nil, nil
	}

	data, _ := json.Marshal(body)

	respBody, status, err := s.doRequest("PATCH", fmt.Sprintf("%s/api/tasks/%d", s.apiURL, input.TaskID), data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task %d updated successfully.", input.TaskID)},
		},
	}, nil, nil
}

func (s *Server) handleToggleTask(_ context.Context, _ *mcp.CallToolRequest, input toggleTaskInput) (*mcp.CallToolResult, any, error) {
	data, _ := json.Marshal(map[string]bool{
		"enabled": input.Enabled,
	})

	respBody, status, err := s.doRequest("PATCH", fmt.Sprintf("%s/api/tasks/%d", s.apiURL, input.TaskID), data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	state := "disabled"
	if input.Enabled {
		state = "enabled"
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task %d %s.", input.TaskID, state)},
		},
	}, nil, nil
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}
}
