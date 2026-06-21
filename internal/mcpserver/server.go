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
		Name:        "create_worktree_thread",
		Description: "Create a new thread backed by a fresh git worktree, forked from an existing BASE branch (the `branch` arg, e.g. 'main'). Like the +wt button, a new 'worktree/<name>' branch is created off that base and checked out — pass the base to start from, NOT a new branch name (a non-existent ref fails). The thread's working directory is the worktree path. If a message is provided, the bot posts it as a self-mention to trigger a runner immediately with that task.",
	}, s.handleCreateWorktreeThread)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_thread",
		Description: "Delete a thread by its ID. This removes the thread from the platform and the database.",
	}, s.handleDeleteThread)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "rename_thread",
		Description: "Rename a thread or channel's display name. Only updates the name — sessions and directory are preserved.",
	}, s.handleRenameThread)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "rename_worktree_thread",
		Description: "Rename a worktree thread: renames the worktree directory (.worktrees/<old> → .worktrees/<new>), renames the git branch (worktree/<old> → worktree/<new>), relocates the Claude session store, and updates the channel name and dir_path. Preserves all Claude sessions.",
	}, s.handleRenameWorktreeThread)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "search_channels",
		Description: "Search for channels and threads. Returns channel IDs, names, directory paths, and active status. Use the query parameter to filter by name.",
	}, s.handleSearchChannels)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "send_message",
		Description: "Send a message to a channel or thread. channel_id is optional — omit it to target the current channel/thread this agent is running in; use search_channels to find another channel's ID. To trigger the bot in the target channel, include @BotName (e.g. @LoopBot) as plain text in the message — it will be converted to a proper mention automatically.",
	}, s.handleSendMessage)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "queue_message",
		Description: "Queue a follow-up prompt for yourself in the current channel/thread/worktree. The prompt is enqueued as a new turn behind any currently-running or already-queued work and appears in the chat's queued-messages list. Set interrupt=true to cancel the active run and jump the queue so it runs next. Use this to chain your own follow-up tasks without discovering your channel ID.",
	}, s.handleQueueMessage)

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
		Name:        "bash_shortcut",
		Description: "Manage bash shortcuts — quick-access commands triggered via $ in the terminal shortcuts bar. Actions: list (show all shortcuts), add (create new), update (modify existing), delete (remove by name). Scope: 'global' (default, ~/.loop/config.json) or 'project' (project .loop/config.json).",
	}, s.handleBashShortcut)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "playground",
		Description: "Manage playgrounds — live interactive sandboxes for HTML/CSS/JS that render in the user's Playground panel. Actions: create (new playground with html + title + description), update (modify html/title/description), delete (remove entirely). After creating, use playground_file to add script.js, style.css, and other files. JS runs as ES module — use import for npm packages via esm.sh CDN.",
	}, s.handlePlayground)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "playground_file",
		Description: "Manage files within a playground. Actions: create/update (write a file), read (get content), delete (remove a file), list (show all files). Use for script.js, style.css, importmap.json, lib/utils.js, assets, etc. Files are served at relative URLs — use import './lib/utils.js' between JS modules.",
	}, s.handlePlaygroundFile)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_scan",
		Description: "Trigger a quality scan of the current channel's working directory. Returns immediately; the scan runs asynchronously and fires a 'quality.scanned' event with the full result (signal value, metric breakdown, rule pass/fail). Use quality_snapshot to read the most recent persisted result.",
	}, s.handleQualityScan)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_snapshot",
		Description: "Read the persisted quality snapshot for the current channel: the signal value (0-10000 band), per-metric scores, and the scanned-at timestamp. Returns 'no snapshot yet' when the channel has never been scanned — call quality_scan first.",
	}, s.handleQualitySnapshot)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_cycles",
		Description: "List import cycles in the current channel's codebase — strongly connected components of size > 1 in the import graph. Each cycle is the set of files that mutually depend on each other. Requires a prior quality_scan.",
	}, s.handleQualityCycles)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_metrics",
		Description: "Read the per-metric breakdown from the latest quality snapshot — modularity, cycles, depth, equality, redundancy. Each entry has score (0..1, higher is better) and raw value. Returns 'no snapshot yet' when no scan has run.",
	}, s.handleQualityMetrics)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_diagnostics",
		Description: "List the files contributing most to the quality signal's deficit — sorted descending by per-file deficit, with the worst-offending metric named per file. Optional 'limit' caps the list size (default = all). Requires a prior quality_scan.",
	}, s.handleQualityDiagnostics)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_rules",
		Description: "Run the quality rules engine against the cached graph for the current channel and return the pass/fail outcome of each built-in rule with file/line citations on failures. Built-in rules: no_import_cycles, signal_floor, parse_fail. Requires a prior quality_scan.",
	}, s.handleQualityRules)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_whatif",
		Description: "Simulate one or more refactor mutations against the cached graph and return the predicted quality_signal delta. Each mutation has op = 'delete' (drop a file) or 'split' (slice a file into N parts). Use to A/B candidate refactors before touching the codebase.",
	}, s.handleQualityWhatif)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_evolution",
		Description: "Mine git history for the current channel's working directory and return coupling pairs (files that change together), churn hotspots (most-changed files), and bus-factor risks (single-author files). Default scope: last 12 months, capped at 1000 commits. Requires the workdir to be a git repo.",
	}, s.handleQualityEvolution)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_bugfactor",
		Description: "Surface bus-factor risks only — files whose change history is concentrated on a single author above the configured threshold. Useful for ownership reviews and offboarding planning. Requires the workdir to be a git repo.",
	}, s.handleQualityBugFactor)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_c4",
		Description: "Emit a C4 component-level diagram (Mermaid syntax) for the cached graph — clusters by top-level package, draws cross-package import edges. Returns a fenced Mermaid block ready to render in chat or paste into a markdown doc. Requires a prior quality_scan.",
	}, s.handleQualityC4)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_complexity",
		Description: "List the per-function complexity hotspots from the cached graph — cyclomatic, cognitive, max nesting, parameter count, and LOC per function. Optional 'limit' (default 50, max 100) and 'offset' (default 0) page through the worst-first list. Requires a prior quality_scan.",
	}, s.handleQualityComplexity)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "quality_clones",
		Description: "List clone clusters detected by SimHash + Hamming distance — groups of functions that share near-duplicate AST shape. Each cluster lists its members, total LOC, and worst pairwise distance. Optional 'limit' (default 25, max 50) and 'offset' page through the largest-first list. Requires a prior quality_scan.",
	}, s.handleQualityClones)

	if s.workflowsEnabled {
		s.registerWorkflowTools()
	}

	// Register agent tools after mcpServer is created.
	if s.agentID != "" {
		s.registerAgentTools()
	}

	return s
}

// Run starts the MCP server on the given transport. The push-receiver
// goroutine (when channel tools are enabled) is bound to a derived ctx
// that is cancelled on return so tests and graceful shutdowns don't leak
// DNS-hung websocket dial loops.
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	// Use the channel transport when agent tools are enabled,
	// so channel notifications share the stdout mutex with MCP responses.
	if s.channelTransport != nil {
		s.channelTransport.inner = transport
		pushCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		startPushReceiver(pushCtx, s.apiURL, s.channelID, s.agentID, s.channelTransport, s.logger)
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
