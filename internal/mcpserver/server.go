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
	channelID     string
	apiURL        string
	authorID      string
	dirPath       string
	memoryEnabled bool
	mcpServer     *mcp.Server
	httpClient    HTTPClient
	logger        *slog.Logger
}

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

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_readme",
		Description: "Get the Loop README documentation. Returns the full project README with setup instructions, configuration, commands, and architecture details.",
	}, s.handleGetReadme)

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

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}
}

// doAPICall performs an HTTP request, checks the status code, and unmarshals the JSON response into T.
// On any failure it returns an errorResult suitable for returning directly from a tool handler.
func doAPICall[T any](s *Server, method, url string, expectedStatus int, body []byte) (*T, *mcp.CallToolResult, error) {
	respBody, status, err := s.doRequest(method, url, body)
	if err != nil {
		return nil, errorResult(fmt.Sprintf("calling API: %v", err)), nil
	}
	if status != expectedStatus {
		return nil, errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil
	}
	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, errorResult(fmt.Sprintf("decoding response: %v", err)), nil
	}
	return &result, nil, nil
}

// doAPICallNoBody performs an HTTP request and checks the status code, without decoding a response body.
// Suitable for DELETE/POST endpoints that return no content (e.g. 204).
func doAPICallNoBody(s *Server, method, url string, expectedStatus int, body []byte) (*mcp.CallToolResult, error) {
	respBody, status, err := s.doRequest(method, url, body)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil
	}
	if status != expectedStatus {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil
	}
	return nil, nil
}
