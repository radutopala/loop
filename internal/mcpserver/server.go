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
	channelID        string
	apiURL           string
	authorID         string
	agentID          string
	dirPath          string
	memoryEnabled    bool
	workflowsEnabled bool
	mcpServer        *mcp.Server
	httpClient       HTTPClient
	logger           *slog.Logger
	channelTransport *channelTransport // non-nil when agent tools enabled
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

// WithWorkflowAPI enables workflow-related MCP tools.
func WithWorkflowAPI() MemoryOption {
	return func(s *Server) {
		s.workflowsEnabled = true
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

	// Apply options first so agentID is set before creating the MCP server.
	for _, opt := range opts {
		opt(s)
	}

	serverOpts := &mcp.ServerOptions{Logger: logger}
	if s.agentID != "" {
		serverOpts.Instructions = fmt.Sprintf("You are agent %q connected to Loop's inter-agent communication channel.\n", s.agentID) +
			"Your agent ID is marked with * in list_agents output.\n" +
			"Messages from other agents arrive as <channel source=\"loop\" from_agent=\"...\">.\n\n" +
			"IMPORTANT: When you receive a channel message, RESPOND IMMEDIATELY. " +
			"Pause what you are doing, act on the message, then resume your work. " +
			"Treat incoming messages like a coworker tapping you on the shoulder.\n\n" +
			"Use `list_agents` to discover other running agents in this channel.\n" +
			"Use `send_agent_message` to send a message to another agent by ID.\n" +
			"Use `update_agent_status` to set your name and work summary."
		serverOpts.Capabilities = &mcp.ServerCapabilities{
			Experimental: map[string]any{"claude/channel": map[string]any{}},
		}
	}
	s.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    "loop",
		Version: "1.0.0",
	}, serverOpts)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "schedule_task",
		Description: "Create a scheduled task. Use cron expressions (e.g. '0 9 * * *' for daily at 9am) with type 'cron', Go time.Duration (e.g. '5m', '1h') with type 'interval', or RFC3339 timestamp (e.g. '2026-02-09T14:30:00Z') with type 'once' for one-time execution. When using 'once', first check the user's local time to compute the correct offset. Prefer RFC3339 timestamps for absolute scheduling. Optionally set template_name to associate the task with a named template for identification and deduplication.",
	}, s.handleScheduleTask)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List all scheduled tasks for this channel.",
	}, s.handleListTasks)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "show_task",
		Description: "Show full details of a scheduled task by its ID, including the complete prompt text.",
	}, s.handleShowTask)

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

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "prompt_shortcut",
		Description: "Manage prompt shortcuts — quick-access prompts triggered via # in the chat input. Actions: list (show all shortcuts), add (create new), update (modify existing), delete (remove by name). Scope: 'global' (default, ~/.loop/config.json) or 'project' (project .loop/config.json).",
	}, s.handlePromptShortcut)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "playground",
		Description: "Manage playgrounds — live interactive sandboxes for HTML/CSS/JS that render in the user's Playground panel. Actions: create (new playground with html + title + description), update (modify html/title/description), delete (remove entirely). After creating, use playground_file to add script.js, style.css, and other files. JS runs as ES module — use import for npm packages via esm.sh CDN.",
	}, s.handlePlayground)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "playground_file",
		Description: "Manage files within a playground. Actions: create/update (write a file), read (get content), delete (remove a file), list (show all files). Use for script.js, style.css, importmap.json, lib/utils.js, assets, etc. Files are served at relative URLs — use import './lib/utils.js' between JS modules.",
	}, s.handlePlaygroundFile)

	if s.workflowsEnabled {
		s.registerWorkflowTools()
	}

	// Register agent tools after mcpServer is created.
	if s.agentID != "" {
		s.registerAgentTools()
	}

	return s
}

// Run starts the MCP server on the given transport.
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	// Use the channel transport when agent tools are enabled,
	// so channel notifications share the stdout mutex with MCP responses.
	if s.channelTransport != nil {
		startPushReceiver(s.apiURL, s.channelID, s.agentID, s.channelTransport, s.logger)
		transport = s.channelTransport
	}
	return s.mcpServer.Run(ctx, transport)
}

// MCPServer returns the underlying MCP server for testing.
func (s *Server) MCPServer() *mcp.Server {
	return s.mcpServer
}

// RegisterAgent registers this agent in the backend registry.
// Called on MCP server startup so the agent is discoverable by others.
func (s *Server) RegisterAgent() {
	if s.agentID == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"channel_id": s.channelID,
		"agent_id":   s.agentID,
		"name":       s.agentID,
		"status":     "idle",
	})
	url := s.apiURL + "/api/agents"
	_, status, err := s.doRequest("POST", url, body)
	switch {
	case err != nil:
		s.logger.Warn("mcp: agent register failed", "error", err)
	case status >= 400:
		s.logger.Warn("mcp: agent register unexpected status", "status", status)
	default:
		s.logger.Info("mcp: agent registered", "agent_id", s.agentID)
	}
}

// UnregisterAgent removes this agent from the backend registry.
// Called on MCP server shutdown so other agents see it as gone.
func (s *Server) UnregisterAgent() {
	if s.agentID == "" {
		return
	}
	url := s.apiURL + "/api/agents/" + s.agentID + "?channel_id=" + s.channelID
	_, status, err := s.doRequest("DELETE", url, nil)
	switch {
	case err != nil:
		s.logger.Warn("mcp: agent unregister failed", "error", err)
	case status >= 400:
		s.logger.Warn("mcp: agent unregister unexpected status", "status", status)
	default:
		s.logger.Info("mcp: agent unregistered on shutdown", "agent_id", s.agentID)
	}
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
