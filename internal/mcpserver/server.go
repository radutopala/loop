package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type memorySearchAPIResponse struct {
	Results []memorySearchResult `json:"results"`
}

type memorySearchResult struct {
	FilePath string  `json:"file_path"`
	Content  string  `json:"content,omitempty"`
	Score    float32 `json:"score"`
}

// HTTPClient abstracts HTTP calls for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Server wraps the MCP server with tools for task scheduling.
type Server struct {
	channelID     string
	apiURL        string
	authorID      string
	dirPath       string
	memoryEnabled bool
	mcpServer     *mcp.Server
	httpClient    HTTPClient
	logger        *slog.Logger
}

type scheduleTaskInput struct {
	Schedule string `json:"schedule" jsonschema:"Cron expression (e.g. 0 9 * * *), Go time.Duration (e.g. 5m, 1h), or RFC3339 timestamp (e.g. 2026-02-09T14:30:00Z) for once type"`
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
	Schedule *string `json:"schedule,omitempty" jsonschema:"New schedule expression (cron, Go time.Duration, or RFC3339 timestamp for once type)"`
	Type     *string `json:"type,omitempty" jsonschema:"New task type: cron, interval, or once"`
	Prompt   *string `json:"prompt,omitempty" jsonschema:"New prompt to execute on schedule"`
}

type createChannelInput struct {
	Name string `json:"name" jsonschema:"The name for the new channel"`
}

type createThreadInput struct {
	Name    string `json:"name" jsonschema:"The name for the new thread"`
	Message string `json:"message,omitempty" jsonschema:"Optional initial message for the thread. If provided, the bot will post it as a self-mention to trigger a runner immediately."`
}

type deleteThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"The ID of the thread to delete"`
}

type searchChannelsInput struct {
	Query string `json:"query,omitempty" jsonschema:"Optional search term to filter channels and threads by name"`
}

type sendMessageInput struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel or thread ID to send the message to"`
	Content   string `json:"content" jsonschema:"The message content to send"`
}

type listTasksInput struct{}

type searchMemoryInput struct {
	Query string `json:"query" jsonschema:"The search query to find relevant memory notes"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"Number of results to return (default 5)"`
}

type indexMemoryInput struct{}

// MemoryOption configures optional memory search for the MCP server.
type MemoryOption func(*Server)

// WithMemoryAPI enables memory search tools via the daemon's HTTP API.
// dirPath is the project directory; if empty, the server falls back to channel_id for lookups.
func WithMemoryAPI(dirPath string) MemoryOption {
	return func(s *Server) {
		s.memoryEnabled = true
		s.dirPath = dirPath
	}
}

// DirPath returns the project directory used for memory lookups.
func (s *Server) DirPath() string { return s.dirPath }

// New creates a new MCP server with scheduler tools.
func New(channelID, apiURL, authorID string, httpClient HTTPClient, logger *slog.Logger, opts ...MemoryOption) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{
		channelID:  channelID,
		apiURL:     apiURL,
		authorID:   authorID,
		httpClient: httpClient,
		logger:     logger,
	}

	s.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    "loop",
		Version: "1.0.0",
	}, &mcp.ServerOptions{Logger: logger})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "schedule_task",
		Description: "Create a scheduled task. Use cron expressions (e.g. '0 9 * * *' for daily at 9am) with type 'cron', Go time.Duration (e.g. '5m', '1h') with type 'interval', or RFC3339 timestamp (e.g. '2026-02-09T14:30:00Z') with type 'once' for one-time execution. When using 'once', first check the user's local time to compute the correct offset. Prefer RFC3339 timestamps for absolute scheduling.",
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

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_channel",
		Description: "Create a new channel. The channel will be registered and the bot will auto-join it.",
	}, s.handleCreateChannel)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_thread",
		Description: "Create a new thread in the current channel. The thread will be registered and the bot will auto-join it. If a message is provided, the bot posts it as a self-mention to trigger a runner immediately with that task.",
	}, s.handleCreateThread)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_thread",
		Description: "Delete a thread by its ID. This removes the thread from the platform and the database.",
	}, s.handleDeleteThread)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "search_channels",
		Description: "Search for channels and threads. Returns channel IDs, names, directory paths, and active status. Use the query parameter to filter by name.",
	}, s.handleSearchChannels)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "send_message",
		Description: "Send a message to a channel or thread. Use search_channels to find the target channel ID first. To trigger the bot in the target channel, include @BotName (e.g. @LoopBot) as plain text in the message — it will be converted to a proper mention automatically.",
	}, s.handleSendMessage)

	for _, opt := range opts {
		opt(s)
	}

	if s.memoryEnabled {
		mcp.AddTool(s.mcpServer, &mcp.Tool{
			Name:        "search_memory",
			Description: "Semantic search across memory files. Returns the most relevant chunks ranked by similarity to the query.",
		}, s.handleSearchMemory)

		mcp.AddTool(s.mcpServer, &mcp.Tool{
			Name:        "index_memory",
			Description: "Force re-index all memory files. Useful after editing memory files to update the search index.",
		}, s.handleIndexMemory)
	}

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
	s.logger.Info("mcp api request", "method", method, "url", url, "body", string(body))

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
		s.logger.Error("mcp api error", "method", method, "url", url, "error", err)
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	s.logger.Info("mcp api response", "method", method, "url", url, "status", resp.StatusCode, "body", string(respBody))
	return respBody, resp.StatusCode, nil
}

func (s *Server) handleScheduleTask(_ context.Context, _ *mcp.CallToolRequest, input scheduleTaskInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "schedule_task", "schedule", input.Schedule, "type", input.Type, "prompt", input.Prompt)

	switch input.Type {
	case "once":
		if _, err := time.Parse(time.RFC3339, input.Schedule); err != nil {
			return errorResult(fmt.Sprintf("invalid schedule for type \"once\": must be RFC3339 (e.g. 2026-02-09T14:30:00Z): %v", err)), nil, nil
		}
	case "interval":
		if _, err := time.ParseDuration(input.Schedule); err != nil {
			return errorResult(fmt.Sprintf("invalid schedule for type %q: must be a valid Go time.Duration (e.g. 5m, 1h, 24h): %v", input.Type, err)), nil, nil
		}
	}

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
	s.logger.Info("mcp tool call", "tool", "list_tasks", "channel_id", s.channelID)

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

	var text strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&text, "- ID %d: %s (schedule: %s, type: %s, enabled: %v)\n", t.ID, t.Prompt, t.Schedule, t.Type, t.Enabled)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text.String()},
		},
	}, nil, nil
}

func (s *Server) handleCancelTask(_ context.Context, _ *mcp.CallToolRequest, input cancelTaskInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "cancel_task", "task_id", input.TaskID)

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
	s.logger.Info("mcp tool call", "tool", "edit_task", "task_id", input.TaskID)

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

	// Validate schedule when editing to once/interval type with a schedule
	if input.Schedule != nil && input.Type != nil {
		switch *input.Type {
		case "once":
			if _, err := time.Parse(time.RFC3339, *input.Schedule); err != nil {
				return errorResult(fmt.Sprintf("invalid schedule for type \"once\": must be RFC3339 (e.g. 2026-02-09T14:30:00Z): %v", err)), nil, nil
			}
		case "interval":
			if _, err := time.ParseDuration(*input.Schedule); err != nil {
				return errorResult(fmt.Sprintf("invalid schedule for type %q: must be a valid Go time.Duration (e.g. 5m, 1h, 24h): %v", *input.Type, err)), nil, nil
			}
		}
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
	s.logger.Info("mcp tool call", "tool", "toggle_task", "task_id", input.TaskID, "enabled", input.Enabled)

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

func (s *Server) handleCreateChannel(_ context.Context, _ *mcp.CallToolRequest, input createChannelInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "create_channel", "name", input.Name)

	if input.Name == "" {
		return errorResult("name is required"), nil, nil
	}

	reqBody := map[string]string{
		"name": input.Name,
	}
	if s.authorID != "" {
		reqBody["author_id"] = s.authorID
	}
	data, _ := json.Marshal(reqBody)

	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/channels/create", data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusCreated {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var result struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Channel created successfully (ID: %s).", result.ChannelID)},
		},
	}, nil, nil
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

func (s *Server) handleSearchChannels(_ context.Context, _ *mcp.CallToolRequest, input searchChannelsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "search_channels", "query", input.Query)

	url := fmt.Sprintf("%s/api/channels", s.apiURL)
	if input.Query != "" {
		url += fmt.Sprintf("?query=%s", input.Query)
	}

	respBody, status, err := s.doRequest("GET", url, nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}

	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var channels []struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
		DirPath   string `json:"dir_path"`
		ParentID  string `json:"parent_id"`
		Active    bool   `json:"active"`
	}
	if err := json.Unmarshal(respBody, &channels); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	if len(channels) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No channels found."},
			},
		}, nil, nil
	}

	var text strings.Builder
	for _, ch := range channels {
		chType := "channel"
		if ch.ParentID != "" {
			chType = "thread"
		}
		fmt.Fprintf(&text, "- %s [%s] (ID: %s, dir: %s, active: %v)\n", ch.Name, chType, ch.ChannelID, ch.DirPath, ch.Active)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text.String()},
		},
	}, nil, nil
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

func (s *Server) handleSearchMemory(_ context.Context, _ *mcp.CallToolRequest, input searchMemoryInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "search_memory", "query", input.Query, "top_k", input.TopK)

	if input.Query == "" {
		return errorResult("query is required"), nil, nil
	}

	topK := input.TopK
	if topK <= 0 {
		topK = 5
	}

	body := map[string]any{
		"query": input.Query,
		"top_k": topK,
	}
	if s.dirPath != "" {
		body["dir_path"] = s.dirPath
	} else {
		body["channel_id"] = s.channelID
	}
	data, _ := json.Marshal(body)

	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/memory/search", data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var resp memorySearchAPIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	if len(resp.Results) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No results found."},
			},
		}, nil, nil
	}

	var text strings.Builder
	for i, r := range resp.Results {
		fmt.Fprintf(&text, "## Result %d (score: %.3f)\n", i+1, r.Score)
		fmt.Fprintf(&text, "**File:** %s\n", r.FilePath)
		if r.Content != "" {
			fmt.Fprintf(&text, "\n%s\n\n", r.Content)
		} else {
			text.WriteString("\n")
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text.String()},
		},
	}, nil, nil
}

func (s *Server) handleIndexMemory(_ context.Context, _ *mcp.CallToolRequest, _ indexMemoryInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "index_memory")

	body := map[string]string{}
	if s.dirPath != "" {
		body["dir_path"] = s.dirPath
	} else {
		body["channel_id"] = s.channelID
	}
	data, _ := json.Marshal(body)

	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/memory/index", data)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	var resp struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Indexed %d chunks.", resp.Count)},
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
