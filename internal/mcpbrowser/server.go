// Package mcpbrowser provides an MCP server for browser automation via CDP.
// It runs as a separate subcommand (`loop mcp-browser`) inside agent containers.
package mcpbrowser

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/radutopala/loop/internal/browser"
)

// HTTPClient abstracts HTTP requests for testing.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// cdpClient abstracts CDP operations for testing.
type cdpClient interface {
	Navigate(ctx context.Context, url string) error
	Reload(ctx context.Context) error
	GoBack(ctx context.Context) error
	GoForward(ctx context.Context) error
	GetPageInfo(ctx context.Context) (*browser.PageInfo, error)
	GetElementRefs(ctx context.Context) ([]browser.ElementRef, error)
	MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error
	MouseMove(ctx context.Context, x, y float64) error
	MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error
	MouseDown(ctx context.Context, x, y float64, button string) error
	MouseUp(ctx context.Context, x, y float64, button string) error
	KeyPress(ctx context.Context, key string) error
	TypeText(ctx context.Context, text string) error
	ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error
	Screenshot(ctx context.Context) ([]byte, error)
	ListTabs(ctx context.Context) ([]browser.TabInfo, error)
	NewTab(ctx context.Context, url string) (string, error)
	SwitchTab(ctx context.Context, targetID string) error
	CloseTab(ctx context.Context, targetID string) error
	EvaluateJS(ctx context.Context, expression string) (string, error)
	EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error
	EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error
	ResizeWindow(ctx context.Context, width, height int) error
	ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error
	Close()
}

// Server provides MCP tools for browser automation via CDP.
type Server struct {
	cdpEndpoint string
	mcpServer   *mcp.Server
	cdp         cdpClient
	cdpFactory  CDPFactory
	logger      *slog.Logger
	refs        []browser.ElementRef // cached element refs
	runCtx      context.Context
	retryDelay  time.Duration // delay between CDP connection retries (default 500ms)
	targetID    string        // if set, attach to this specific page target

	// HTTP callback for lazy Chrome start and idle touch.
	apiURL     string
	channelID  string
	httpClient HTTPClient
	lastTouch  time.Time // debounce touchBrowserViaAPI

	consoleMu   sync.Mutex
	consoleMsgs []browser.ConsoleMessage // captured console messages
	consoleCh   chan browser.ConsoleMessage

	networkMu   sync.Mutex
	networkReqs []browser.NetworkRequest // captured network requests
	networkCh   chan browser.NetworkRequest
}

// SetTargetID configures the server to attach to a specific Chrome page target.
// This allows sharing the same tab with the browser pane's screencast.
func (s *Server) SetTargetID(id string) {
	s.targetID = id
}

// SetAPICallback configures the HTTP callback for lazy Chrome start.
func (s *Server) SetAPICallback(apiURL, channelID string) {
	s.apiURL = apiURL
	s.channelID = channelID
	s.httpClient = &http.Client{Timeout: 30 * time.Second}
}

// New creates a new MCP browser server.
func New(cdpEndpoint string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{
		cdpEndpoint: cdpEndpoint,
		logger:      logger,
		retryDelay:  500 * time.Millisecond,
	}
	s.cdpFactory = func(_ context.Context, wsURL string, logger *slog.Logger) (cdpClient, error) {
		var opts []browser.CDPOption
		if s.targetID != "" {
			opts = append(opts, browser.WithTargetID(s.targetID))
		}
		// Use background context so the CDP client outlives individual tool calls.
		return browser.NewCDPClient(context.Background(), wsURL, logger, opts...)
	}

	s.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    "loop-browser",
		Version: "1.0.0",
	}, &mcp.ServerOptions{Logger: logger})

	s.registerTools()
	return s
}

// CDPFactory creates a CDP client for a given WebSocket URL.
type CDPFactory func(ctx context.Context, wsURL string, logger *slog.Logger) (cdpClient, error)

// Run starts the MCP server over the given transport.
// CDP connection is deferred to first tool use so the server can start
// before Chrome is running (Chrome is started lazily by the API server).
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	s.runCtx = ctx
	return s.mcpServer.Run(ctx, transport)
}

// ensureBrowserViaAPI calls the host API to lazily start the Chrome container.
// Non-fatal: Chrome may already be running (e.g. started by the browser pane).
func (s *Server) ensureBrowserViaAPI() {
	if s.httpClient == nil || s.apiURL == "" || s.channelID == "" {
		return
	}
	url := s.apiURL + "/api/browser/ensure"
	body := []byte(`{"channel_id":"` + s.channelID + `"}`)
	req, err := http.NewRequestWithContext(s.runCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.logger.Warn("ensure browser via API: request build failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Warn("ensure browser via API: request failed", "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.Warn("ensure browser via API: non-200 response", "status", resp.StatusCode)
	}
}

// ensureCDP connects to Chrome via CDP on first use.
// Chrome runs in a separate sidecar container managed by the browser manager.
// Retries for up to 15 seconds to allow for container startup.
func (s *Server) ensureCDP() error {
	if s.cdp != nil {
		return nil
	}
	// Trigger lazy Chrome start via host API before retrying CDP connection.
	s.ensureBrowserViaAPI()

	var lastErr error
	for range 30 {
		cdp, err := s.cdpFactory(s.runCtx, s.cdpEndpoint, s.logger)
		if err == nil {
			s.cdp = cdp
			s.startConsoleCapture()
			return nil
		}
		lastErr = err
		select {
		case <-s.runCtx.Done():
			return fmt.Errorf("connecting to CDP at %s: %w", s.cdpEndpoint, lastErr)
		case <-time.After(s.retryDelay):
		}
	}
	return fmt.Errorf("connecting to CDP at %s: %w", s.cdpEndpoint, lastErr)
}

// startConsoleCapture enables console message capture from the browser.
// Messages are buffered in s.consoleMsgs for the read_console_messages tool.
func (s *Server) startConsoleCapture() {
	s.consoleCh = make(chan browser.ConsoleMessage, 100)
	if err := s.cdp.EnableConsoleCapture(s.runCtx, s.consoleCh); err != nil {
		s.logger.Warn("failed to enable console capture", "error", err)
		return
	}
	go func() {
		for msg := range s.consoleCh {
			s.consoleMu.Lock()
			s.consoleMsgs = append(s.consoleMsgs, msg)
			s.consoleMu.Unlock()
		}
	}()

	s.startNetworkCapture()
}

// startNetworkCapture enables network request capture from the browser.
// Requests are buffered in s.networkReqs for the read_network_requests tool.
func (s *Server) startNetworkCapture() {
	s.networkCh = make(chan browser.NetworkRequest, 100)
	if err := s.cdp.EnableNetworkCapture(s.runCtx, s.networkCh); err != nil {
		s.logger.Warn("failed to enable network capture", "error", err)
		return
	}
	go func() {
		for req := range s.networkCh {
			s.networkMu.Lock()
			s.networkReqs = append(s.networkReqs, req)
			s.networkMu.Unlock()
		}
	}()
}

type computerInput struct {
	Action   string  `json:"action" jsonschema:"The action to perform: click,right_click,double_click,triple_click,type,key,scroll,move,hover,screenshot,wait,left_click_drag"`
	Ref      int     `json:"ref,omitempty" jsonschema:"Element ref number to interact with (from read_page)"`
	X        float64 `json:"x,omitempty" jsonschema:"X coordinate (or end X for left_click_drag)"`
	Y        float64 `json:"y,omitempty" jsonschema:"Y coordinate (or end Y for left_click_drag)"`
	Text     string  `json:"text,omitempty" jsonschema:"Text to type or key name"`
	DeltaX   float64 `json:"delta_x,omitempty" jsonschema:"Horizontal scroll amount"`
	DeltaY   float64 `json:"delta_y,omitempty" jsonschema:"Vertical scroll amount"`
	Button   string  `json:"button,omitempty" jsonschema:"Mouse button: left,right,middle"`
	Duration int     `json:"duration,omitempty" jsonschema:"Wait duration in milliseconds"`
	StartX   float64 `json:"start_x,omitempty" jsonschema:"Start X coordinate for left_click_drag"`
	StartY   float64 `json:"start_y,omitempty" jsonschema:"Start Y coordinate for left_click_drag"`
}

// touchBrowserViaAPI signals the host API that the browser is still in use.
// Debounced: skips if the last touch was less than 1 minute ago.
func (s *Server) touchBrowserViaAPI() {
	if s.httpClient == nil || s.apiURL == "" || s.channelID == "" {
		return
	}
	if time.Since(s.lastTouch) < time.Minute {
		return
	}
	url := s.apiURL + "/api/browser/touch"
	body := []byte(`{"channel_id":"` + s.channelID + `"}`)
	req, err := http.NewRequestWithContext(s.runCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Warn("touch browser via API: request failed", "error", err)
		return
	}
	resp.Body.Close()
	s.lastTouch = time.Now()
}

// requireCDP ensures CDP is connected, returning an error result if not.
func (s *Server) requireCDP() *mcp.CallToolResult {
	if err := s.ensureCDP(); err != nil {
		return errorResult(fmt.Sprintf("browser not ready: %v", err))
	}
	s.touchBrowserViaAPI()
	return nil
}

func (s *Server) registerTools() {
	type navigateInput struct {
		URL string `json:"url" jsonschema:"The URL to navigate to"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "navigate",
		Description: "Navigate the browser to a URL.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input navigateInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.URL == "" {
			return errorResult("url is required"), nil, nil
		}
		if err := s.cdp.Navigate(context.Background(), input.URL); err != nil {
			return errorResult(fmt.Sprintf("navigate failed: %v", err)), nil, nil
		}
		info, _ := s.cdp.GetPageInfo(context.Background())
		if info != nil {
			return textResult(fmt.Sprintf("Navigated to %s — %s", info.URL, info.Title)), nil, nil
		}
		return textResult("Navigated to " + input.URL), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "read_page",
		Description: "Get the accessibility tree of interactive elements on the current page. Returns element refs that can be used with the computer tool.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		refs, err := s.cdp.GetElementRefs(context.Background())
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get element refs: %v", err)), nil, nil
		}
		s.refs = refs

		info, _ := s.cdp.GetPageInfo(context.Background())
		result := ""
		if info != nil {
			result = fmt.Sprintf("Page: %s — %s\n\n", info.URL, info.Title)
		}

		if len(refs) == 0 {
			result += "No interactive elements found."
		} else {
			for _, ref := range refs {
				line := fmt.Sprintf("[%s] %s: %s", ref.RefID, ref.Role, ref.Name)
				if ref.Value != "" {
					line += fmt.Sprintf(" (value: %s)", ref.Value)
				}
				result += line + "\n"
			}
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "computer",
		Description: "Perform computer actions: click, type, key, scroll, move, screenshot, wait, triple_click, double_click. Use ref from read_page for precise element targeting.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input computerInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		return s.handleComputer(input)
	})

	type formInputInput struct {
		Ref   int    `json:"ref" jsonschema:"Element ref number from read_page"`
		Value string `json:"value" jsonschema:"Value to enter in the form field"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "form_input",
		Description: "Fill in a form field by clicking it, clearing existing content, and typing the new value.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input formInputInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.Ref < 1 || input.Ref > len(s.refs) {
			return errorResult(fmt.Sprintf("ref %d out of range (1-%d)", input.Ref, len(s.refs))), nil, nil
		}

		ctx := context.Background()
		if err := s.cdp.ClickRef(ctx, s.refs, input.Ref); err != nil {
			return errorResult(fmt.Sprintf("click failed: %v", err)), nil, nil
		}
		if err := s.cdp.KeyPress(ctx, "Control+a"); err != nil {
			return errorResult(fmt.Sprintf("select all failed: %v", err)), nil, nil
		}
		if err := s.cdp.TypeText(ctx, input.Value); err != nil {
			return errorResult(fmt.Sprintf("type failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Entered %q in ref_%d", input.Value, input.Ref)), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "screenshot",
		Description: "Take a screenshot of the current page.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		data, err := s.cdp.Screenshot(context.Background())
		if err != nil {
			return errorResult(fmt.Sprintf("screenshot failed: %v", err)), nil, nil
		}
		return imageResult(data), nil, nil
	})

	type goBackInput struct{}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "go_back",
		Description: "Navigate back in browser history.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ goBackInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if err := s.cdp.GoBack(context.Background()); err != nil {
			return errorResult(fmt.Sprintf("back failed: %v", err)), nil, nil
		}
		return textResult("Navigated back"), nil, nil
	})

	type goForwardInput struct{}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "go_forward",
		Description: "Navigate forward in browser history.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ goForwardInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if err := s.cdp.GoForward(context.Background()); err != nil {
			return errorResult(fmt.Sprintf("forward failed: %v", err)), nil, nil
		}
		return textResult("Navigated forward"), nil, nil
	})

	type reloadInput struct{}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "reload",
		Description: "Reload the current page.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ reloadInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if err := s.cdp.Reload(context.Background()); err != nil {
			return errorResult(fmt.Sprintf("reload failed: %v", err)), nil, nil
		}
		return textResult("Page reloaded"), nil, nil
	})

	type evaluateInput struct {
		Expression string `json:"expression" jsonschema:"JavaScript expression to evaluate"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "evaluate",
		Description: "Evaluate a JavaScript expression in the page context.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input evaluateInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.Expression == "" {
			return errorResult("expression is required"), nil, nil
		}
		result, err := s.cdp.EvaluateJS(context.Background(), input.Expression)
		if err != nil {
			return errorResult(fmt.Sprintf("evaluate failed: %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tabs",
		Description: "List all open browser tabs.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		tabs, err := s.cdp.ListTabs(context.Background())
		if err != nil {
			return errorResult(fmt.Sprintf("list tabs failed: %v", err)), nil, nil
		}
		if len(tabs) == 0 {
			return textResult("No tabs open"), nil, nil
		}
		result := ""
		for i, tab := range tabs {
			result += fmt.Sprintf("[%d] %s — %s (id: %s)\n", i+1, tab.Title, tab.URL, tab.TargetID)
		}
		return textResult(result), nil, nil
	})

	type newTabInput struct {
		URL string `json:"url" jsonschema:"URL to open in the new tab"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "new_tab",
		Description: "Open a new browser tab with the given URL.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input newTabInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		url := input.URL
		if url == "" {
			url = "about:blank"
		}
		targetID, err := s.cdp.NewTab(context.Background(), url)
		if err != nil {
			return errorResult(fmt.Sprintf("new tab failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Opened new tab (id: %s) at %s", targetID, url)), nil, nil
	})

	type switchTabInput struct {
		TargetID string `json:"target_id" jsonschema:"Target ID of the tab to switch to (from list_tabs)"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "switch_tab",
		Description: "Switch to a browser tab by target ID.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input switchTabInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.TargetID == "" {
			return errorResult("target_id is required"), nil, nil
		}
		if err := s.cdp.SwitchTab(context.Background(), input.TargetID); err != nil {
			return errorResult(fmt.Sprintf("switch tab failed: %v", err)), nil, nil
		}
		return textResult("Switched to tab " + input.TargetID), nil, nil
	})

	type closeTabInput struct {
		TargetID string `json:"target_id" jsonschema:"Target ID of the tab to close (from list_tabs)"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "close_tab",
		Description: "Close a browser tab by target ID.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input closeTabInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.TargetID == "" {
			return errorResult("target_id is required"), nil, nil
		}
		if err := s.cdp.CloseTab(context.Background(), input.TargetID); err != nil {
			return errorResult(fmt.Sprintf("close tab failed: %v", err)), nil, nil
		}
		return textResult("Closed tab " + input.TargetID), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "page_info",
		Description: "Get the current page URL and title.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		info, err := s.cdp.GetPageInfo(context.Background())
		if err != nil {
			return errorResult(fmt.Sprintf("page info failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("URL: %s\nTitle: %s", info.URL, info.Title)), nil, nil
	})

	// get_page_text: extract all text content from the page.
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_page_text",
		Description: "Get all text content from the current page (document.body.innerText).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		text, err := s.cdp.EvaluateJS(context.Background(), "document.body.innerText")
		if err != nil {
			return errorResult(fmt.Sprintf("get page text failed: %v", err)), nil, nil
		}
		return textResult(text), nil, nil
	})

	// find: fuzzy-find elements by natural language query.
	type findInput struct {
		Query string `json:"query" jsonschema:"Natural language query to match against element roles and names"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "find",
		Description: "Find interactive elements matching a natural language query. Returns up to 20 matching refs from the accessibility tree.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input findInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		return s.handleFind(input.Query)
	})

	// read_console_messages: read captured browser console messages.
	type consoleInput struct {
		Pattern    string `json:"pattern,omitempty" jsonschema:"Regex pattern to filter messages"`
		OnlyErrors bool   `json:"onlyErrors,omitempty" jsonschema:"Only return error-level messages"`
		Clear      bool   `json:"clear,omitempty" jsonschema:"Clear the message buffer after reading"`
		Limit      int    `json:"limit,omitempty" jsonschema:"Maximum number of messages to return (default 100)"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "read_console_messages",
		Description: "Read captured browser console messages. Supports filtering by regex pattern and error-only mode.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input consoleInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		return s.handleReadConsoleMessages(input.Pattern, input.OnlyErrors, input.Clear, input.Limit)
	})

	// read_network_requests: read captured network requests.
	type networkInput struct {
		Pattern string `json:"pattern,omitempty" jsonschema:"Regex pattern to filter by URL"`
		Clear   bool   `json:"clear,omitempty" jsonschema:"Clear the request buffer after reading"`
		Limit   int    `json:"limit,omitempty" jsonschema:"Maximum number of requests to return (default 50)"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "read_network_requests",
		Description: "Read captured network requests. Supports filtering by URL regex pattern.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input networkInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		return s.handleReadNetworkRequests(input.Pattern, input.Clear, input.Limit)
	})

	// resize_window: resize the browser viewport.
	type resizeInput struct {
		Width  int `json:"width" jsonschema:"Viewport width in pixels"`
		Height int `json:"height" jsonschema:"Viewport height in pixels"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "resize_window",
		Description: "Resize the browser viewport to the given dimensions.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input resizeInput) (*mcp.CallToolResult, any, error) {
		if r := s.requireCDP(); r != nil {
			return r, nil, nil
		}
		if input.Width <= 0 || input.Height <= 0 {
			return errorResult("width and height must be positive"), nil, nil
		}
		if err := s.cdp.ResizeWindow(context.Background(), input.Width, input.Height); err != nil {
			return errorResult(fmt.Sprintf("resize failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Resized viewport to %dx%d", input.Width, input.Height)), nil, nil
	})
}

func (s *Server) handleComputer(input computerInput) (*mcp.CallToolResult, any, error) {
	ctx := context.Background()

	// Resolve coordinates from ref if provided.
	x, y := input.X, input.Y
	if input.Ref > 0 {
		if input.Ref > len(s.refs) {
			return errorResult(fmt.Sprintf("ref %d out of range (1-%d), call read_page first", input.Ref, len(s.refs))), nil, nil
		}
		ref := s.refs[input.Ref-1]
		x = ref.X + ref.Width/2
		y = ref.Y + ref.Height/2
	}

	btn := input.Button
	if btn == "" {
		btn = "left"
	}

	switch input.Action {
	case "click":
		if err := s.cdp.MouseClick(ctx, x, y, btn, 1); err != nil {
			return errorResult(fmt.Sprintf("click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "double_click":
		if err := s.cdp.MouseClick(ctx, x, y, btn, 2); err != nil {
			return errorResult(fmt.Sprintf("double click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Double-clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "triple_click":
		if err := s.cdp.MouseClick(ctx, x, y, btn, 3); err != nil {
			return errorResult(fmt.Sprintf("triple click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Triple-clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "type":
		if input.Text == "" {
			return errorResult("text is required for type action"), nil, nil
		}
		if err := s.cdp.TypeText(ctx, input.Text); err != nil {
			return errorResult(fmt.Sprintf("type failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Typed %q", input.Text)), nil, nil

	case "key":
		if input.Text == "" {
			return errorResult("text is required for key action (key name)"), nil, nil
		}
		if err := s.cdp.KeyPress(ctx, input.Text); err != nil {
			return errorResult(fmt.Sprintf("key failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Pressed key %q", input.Text)), nil, nil

	case "scroll":
		dy := input.DeltaY
		if dy == 0 {
			dy = -3 // Default scroll down.
		}
		if err := s.cdp.MouseScroll(ctx, x, y, input.DeltaX, dy); err != nil {
			return errorResult(fmt.Sprintf("scroll failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Scrolled at (%.0f, %.0f)", x, y)), nil, nil

	case "right_click":
		if err := s.cdp.MouseClick(ctx, x, y, "right", 1); err != nil {
			return errorResult(fmt.Sprintf("right click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Right-clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "move", "hover":
		if err := s.cdp.MouseMove(ctx, x, y); err != nil {
			return errorResult(fmt.Sprintf("move failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Moved to (%.0f, %.0f)", x, y)), nil, nil

	case "screenshot":
		data, err := s.cdp.Screenshot(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("screenshot failed: %v", err)), nil, nil
		}
		return imageResult(data), nil, nil

	case "left_click_drag":
		if err := s.cdp.MouseDown(ctx, input.StartX, input.StartY, "left"); err != nil {
			return errorResult(fmt.Sprintf("mouse down failed: %v", err)), nil, nil
		}
		if err := s.cdp.MouseMove(ctx, x, y); err != nil {
			return errorResult(fmt.Sprintf("drag move failed: %v", err)), nil, nil
		}
		if err := s.cdp.MouseUp(ctx, x, y, "left"); err != nil {
			return errorResult(fmt.Sprintf("mouse up failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Dragged from (%.0f, %.0f) to (%.0f, %.0f)", input.StartX, input.StartY, x, y)), nil, nil

	case "scroll_to":
		if input.Ref < 1 || input.Ref > len(s.refs) {
			return errorResult(fmt.Sprintf("ref %d out of range (1-%d)", input.Ref, len(s.refs))), nil, nil
		}
		ref := s.refs[input.Ref-1]
		if err := s.cdp.ScrollIntoView(ctx, ref.BackendDOMNodeID); err != nil {
			return errorResult(fmt.Sprintf("scroll_to failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Scrolled ref %d (%s: %s) into view", input.Ref, ref.Role, ref.Name)), nil, nil

	case "wait":
		return textResult("Waited"), nil, nil

	default:
		return errorResult(fmt.Sprintf("unknown action: %s", input.Action)), nil, nil
	}
}

// handleFind searches cached element refs for matches against a natural language query.
func (s *Server) handleFind(query string) (*mcp.CallToolResult, any, error) {
	// Refresh element refs from the page.
	refs, err := s.cdp.GetElementRefs(context.Background())
	if err != nil {
		return errorResult(fmt.Sprintf("failed to get element refs: %v", err)), nil, nil
	}
	s.refs = refs

	queryLower := strings.ToLower(query)
	var matches []browser.ElementRef
	for _, ref := range refs {
		roleLower := strings.ToLower(ref.Role)
		nameLower := strings.ToLower(ref.Name)
		descLower := strings.ToLower(ref.Description)
		if strings.Contains(roleLower, queryLower) ||
			strings.Contains(nameLower, queryLower) ||
			strings.Contains(descLower, queryLower) {
			matches = append(matches, ref)
			if len(matches) >= 20 {
				break
			}
		}
	}

	if len(matches) == 0 {
		return textResult(fmt.Sprintf("No elements found matching %q", query)), nil, nil
	}

	result := fmt.Sprintf("Found %d element(s) matching %q:\n", len(matches), query)
	for _, ref := range matches {
		line := fmt.Sprintf("[%s] %s: %s", ref.RefID, ref.Role, ref.Name)
		if ref.Value != "" {
			line += fmt.Sprintf(" (value: %s)", ref.Value)
		}
		result += line + "\n"
	}
	return textResult(result), nil, nil
}

// handleReadConsoleMessages returns captured console messages with optional filtering.
func (s *Server) handleReadConsoleMessages(pattern string, onlyErrors bool, clear bool, limit int) (*mcp.CallToolResult, any, error) {
	if limit <= 0 {
		limit = 100
	}

	var re *regexp.Regexp
	if pattern != "" {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return errorResult(fmt.Sprintf("invalid regex pattern: %v", err)), nil, nil
		}
	}

	s.consoleMu.Lock()
	msgs := make([]browser.ConsoleMessage, len(s.consoleMsgs))
	copy(msgs, s.consoleMsgs)
	if clear {
		s.consoleMsgs = nil
	}
	s.consoleMu.Unlock()

	var filtered []browser.ConsoleMessage
	for _, msg := range msgs {
		if onlyErrors && msg.Level != "error" {
			continue
		}
		if re != nil && !re.MatchString(msg.Text) {
			continue
		}
		filtered = append(filtered, msg)
	}

	// Apply limit from the end (most recent messages).
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	if len(filtered) == 0 {
		return textResult("No console messages"), nil, nil
	}

	result := fmt.Sprintf("%d console message(s):\n", len(filtered))
	for _, msg := range filtered {
		result += fmt.Sprintf("[%s] %s: %s\n", msg.Time.Format("15:04:05"), msg.Level, msg.Text)
	}
	return textResult(result), nil, nil
}

// handleReadNetworkRequests returns captured network requests with optional filtering.
func (s *Server) handleReadNetworkRequests(pattern string, clear bool, limit int) (*mcp.CallToolResult, any, error) {
	if limit <= 0 {
		limit = 50
	}

	var re *regexp.Regexp
	if pattern != "" {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return errorResult(fmt.Sprintf("invalid regex pattern: %v", err)), nil, nil
		}
	}

	s.networkMu.Lock()
	reqs := make([]browser.NetworkRequest, len(s.networkReqs))
	copy(reqs, s.networkReqs)
	if clear {
		s.networkReqs = nil
	}
	s.networkMu.Unlock()

	var filtered []browser.NetworkRequest
	for _, req := range reqs {
		if re != nil && !re.MatchString(req.URL) {
			continue
		}
		filtered = append(filtered, req)
	}

	// Apply limit from the end (most recent requests).
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	if len(filtered) == 0 {
		return textResult("No network requests"), nil, nil
	}

	result := fmt.Sprintf("%d network request(s):\n", len(filtered))
	for _, req := range filtered {
		result += fmt.Sprintf("[%s] %s %s — %d %s\n", req.Time.Format("15:04:05"), req.Method, req.URL, req.Status, req.StatusText)
	}
	return textResult(result), nil, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}
}

func imageResult(data []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{
				Data:     data,
				MIMEType: "image/png",
			},
		},
	}
}
