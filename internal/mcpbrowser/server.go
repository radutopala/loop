// Package mcpbrowser provides an MCP server for browser automation via the host API.
// It runs as a separate subcommand (`loop mcp-browser`) inside agent containers.
package mcpbrowser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radutopala/loop/internal/browser"
)

// HTTPClient abstracts HTTP requests for testing.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Server provides MCP tools for browser automation via the host API.
type Server struct {
	mcpServer  *mcp.Server
	logger     *slog.Logger
	refs       []browser.ElementRef // cached from host
	apiURL     string
	channelID  string
	httpClient HTTPClient
}

// New creates a new MCP browser server that proxies actions through the host API.
func New(apiURL, channelID string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{
		apiURL:    apiURL,
		channelID: channelID,
		logger:    logger,
	}

	s.httpClient = &http.Client{Timeout: 2 * time.Minute}

	s.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    "loop-browser",
		Version: "1.0.0",
	}, &mcp.ServerOptions{Logger: logger})

	s.registerTools()
	return s
}

// Run starts the MCP server over the given transport.
func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	return s.mcpServer.Run(ctx, transport)
}

// actionResponse mirrors the host API's browserActionResponse.
type actionResponse struct {
	Result         string               `json:"result,omitempty"`
	Image          string               `json:"image,omitempty"`
	ScreenshotPath string               `json:"screenshot_path,omitempty"`
	Error          string               `json:"error,omitempty"`
	ElementRefs    []browser.ElementRef `json:"element_refs,omitempty"`
	Tabs           []browser.TabInfo    `json:"tabs,omitempty"`
	PageInfo       *browser.PageInfo    `json:"page_info,omitempty"`
}

func (s *Server) callAction(ctx context.Context, action string, params map[string]any) (*actionResponse, error) {
	body := map[string]any{
		"channel_id": s.channelID,
		"action":     action,
		"params":     params,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL+"/api/browser/action", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling host API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("host API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result actionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}

	return &result, nil
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

func (s *Server) registerTools() {
	type navigateInput struct {
		URL string `json:"url" jsonschema:"The URL to navigate to"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "navigate",
		Description: "Navigate the browser to a URL.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input navigateInput) (*mcp.CallToolResult, any, error) {
		if input.URL == "" {
			return errorResult("url is required"), nil, nil
		}
		resp, err := s.callAction(ctx, "navigate", map[string]any{"url": input.URL})
		if err != nil {
			return errorResult(fmt.Sprintf("navigate failed: %v", err)), nil, nil
		}
		if resp.PageInfo != nil {
			return textResult(fmt.Sprintf("Navigated to %s — %s", resp.PageInfo.URL, resp.PageInfo.Title)), nil, nil
		}
		return textResult("Navigated to " + input.URL), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "read_page",
		Description: "Get the accessibility tree of interactive elements on the current page. Returns element refs that can be used with the computer tool.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		resp, err := s.callAction(ctx, "get_element_refs", nil)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get element refs: %v", err)), nil, nil
		}
		s.refs = resp.ElementRefs

		infoResp, _ := s.callAction(ctx, "get_page_info", nil)
		result := ""
		if infoResp != nil && infoResp.PageInfo != nil {
			result = fmt.Sprintf("Page: %s — %s\n\n", infoResp.PageInfo.URL, infoResp.PageInfo.Title)
		}

		if len(s.refs) == 0 {
			result += "No interactive elements found."
		} else {
			for _, ref := range s.refs {
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input computerInput) (*mcp.CallToolResult, any, error) {
		return s.handleComputer(ctx, input)
	})

	type formInputInput struct {
		Ref   int    `json:"ref" jsonschema:"Element ref number from read_page"`
		Value string `json:"value" jsonschema:"Value to enter in the form field"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "form_input",
		Description: "Fill in a form field by clicking it, clearing existing content, and typing the new value.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input formInputInput) (*mcp.CallToolResult, any, error) {
		if input.Ref < 1 || input.Ref > len(s.refs) {
			return errorResult(fmt.Sprintf("ref %d out of range (1-%d)", input.Ref, len(s.refs))), nil, nil
		}
		if _, err := s.callAction(ctx, "click_ref", map[string]any{"refs": s.refs, "ref_index": input.Ref}); err != nil {
			return errorResult(fmt.Sprintf("click failed: %v", err)), nil, nil
		}
		if _, err := s.callAction(ctx, "key_press", map[string]any{"key": "Control+a"}); err != nil {
			return errorResult(fmt.Sprintf("select all failed: %v", err)), nil, nil
		}
		if _, err := s.callAction(ctx, "type_text", map[string]any{"text": input.Value}); err != nil {
			return errorResult(fmt.Sprintf("type failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Entered %q in ref_%d", input.Value, input.Ref)), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "screenshot",
		Description: "Take a screenshot of the current page.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		resp, err := s.callAction(ctx, "screenshot", nil)
		if err != nil {
			return errorResult(fmt.Sprintf("screenshot failed: %v", err)), nil, nil
		}
		// If host returned a file path, read the file directly (avoids base64 over HTTP).
		if resp.ScreenshotPath != "" {
			data, readErr := os.ReadFile(resp.ScreenshotPath)
			if readErr != nil {
				return errorResult(fmt.Sprintf("reading screenshot file: %v", readErr)), nil, nil
			}
			os.Remove(resp.ScreenshotPath) //nolint:errcheck
			return imageResult(data), nil, nil
		}
		// Fallback to base64 decode.
		data, err := base64.StdEncoding.DecodeString(resp.Image)
		if err != nil {
			return errorResult(fmt.Sprintf("screenshot decode failed: %v", err)), nil, nil
		}
		return imageResult(data), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "go_back",
		Description: "Navigate back in browser history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if _, err := s.callAction(ctx, "go_back", nil); err != nil {
			return errorResult(fmt.Sprintf("back failed: %v", err)), nil, nil
		}
		return textResult("Navigated back"), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "go_forward",
		Description: "Navigate forward in browser history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if _, err := s.callAction(ctx, "go_forward", nil); err != nil {
			return errorResult(fmt.Sprintf("forward failed: %v", err)), nil, nil
		}
		return textResult("Navigated forward"), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "reload",
		Description: "Reload the current page.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if _, err := s.callAction(ctx, "reload", nil); err != nil {
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input evaluateInput) (*mcp.CallToolResult, any, error) {
		if input.Expression == "" {
			return errorResult("expression is required"), nil, nil
		}
		resp, err := s.callAction(ctx, "evaluate_js", map[string]any{"expression": input.Expression})
		if err != nil {
			return errorResult(fmt.Sprintf("evaluate failed: %v", err)), nil, nil
		}
		return textResult(resp.Result), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tabs",
		Description: "List all open browser tabs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		resp, err := s.callAction(ctx, "list_tabs", nil)
		if err != nil {
			return errorResult(fmt.Sprintf("list tabs failed: %v", err)), nil, nil
		}
		if len(resp.Tabs) == 0 {
			return textResult("No tabs open"), nil, nil
		}
		result := ""
		for i, tab := range resp.Tabs {
			marker := "  "
			if tab.Active {
				marker = "* "
			}
			result += fmt.Sprintf("%s[%d] %s — %s (id: %s)\n", marker, i+1, tab.Title, tab.URL, tab.TargetID)
		}
		return textResult(result), nil, nil
	})

	type newTabInput struct {
		URL string `json:"url" jsonschema:"URL to open in the new tab"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "new_tab",
		Description: "Open a new browser tab with the given URL.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input newTabInput) (*mcp.CallToolResult, any, error) {
		url := input.URL
		if url == "" {
			url = "about:blank"
		}
		resp, err := s.callAction(ctx, "new_tab", map[string]any{"url": url})
		if err != nil {
			return errorResult(fmt.Sprintf("new tab failed: %v", err)), nil, nil
		}
		return textResult(resp.Result), nil, nil
	})

	type switchTabInput struct {
		TargetID string `json:"target_id" jsonschema:"Target ID of the tab to switch to (from list_tabs)"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "switch_tab",
		Description: "Switch to a browser tab by target ID.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input switchTabInput) (*mcp.CallToolResult, any, error) {
		if input.TargetID == "" {
			return errorResult("target_id is required"), nil, nil
		}
		if _, err := s.callAction(ctx, "switch_tab", map[string]any{"target_id": input.TargetID}); err != nil {
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input closeTabInput) (*mcp.CallToolResult, any, error) {
		if input.TargetID == "" {
			return errorResult("target_id is required"), nil, nil
		}
		if _, err := s.callAction(ctx, "close_tab", map[string]any{"target_id": input.TargetID}); err != nil {
			return errorResult(fmt.Sprintf("close tab failed: %v", err)), nil, nil
		}
		return textResult("Closed tab " + input.TargetID), nil, nil
	})

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "page_info",
		Description: "Get the current page URL and title.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		resp, err := s.callAction(ctx, "get_page_info", nil)
		if err != nil {
			return errorResult(fmt.Sprintf("page info failed: %v", err)), nil, nil
		}
		if resp.PageInfo == nil {
			return errorResult("page info failed: no page info returned"), nil, nil
		}
		return textResult(fmt.Sprintf("URL: %s\nTitle: %s", resp.PageInfo.URL, resp.PageInfo.Title)), nil, nil
	})

	// get_page_text: extract all text content from the page.
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_page_text",
		Description: "Get all text content from the current page (document.body.innerText).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		resp, err := s.callAction(ctx, "evaluate_js", map[string]any{"expression": "document.body.innerText"})
		if err != nil {
			return errorResult(fmt.Sprintf("get page text failed: %v", err)), nil, nil
		}
		return textResult(resp.Result), nil, nil
	})

	// find: fuzzy-find elements by natural language query.
	type findInput struct {
		Query string `json:"query" jsonschema:"Natural language query to match against element roles and names"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "find",
		Description: "Find interactive elements matching a natural language query. Returns up to 20 matching refs from the accessibility tree.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input findInput) (*mcp.CallToolResult, any, error) {
		if input.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		return s.handleFind(ctx, input.Query)
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input consoleInput) (*mcp.CallToolResult, any, error) {
		params := map[string]any{
			"pattern":     input.Pattern,
			"only_errors": input.OnlyErrors,
			"clear":       input.Clear,
			"limit":       input.Limit,
		}
		resp, err := s.callAction(ctx, "read_console", params)
		if err != nil {
			return errorResult(fmt.Sprintf("read console failed: %v", err)), nil, nil
		}
		return textResult(resp.Result), nil, nil
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input networkInput) (*mcp.CallToolResult, any, error) {
		params := map[string]any{
			"pattern": input.Pattern,
			"clear":   input.Clear,
			"limit":   input.Limit,
		}
		resp, err := s.callAction(ctx, "read_network", params)
		if err != nil {
			return errorResult(fmt.Sprintf("read network failed: %v", err)), nil, nil
		}
		return textResult(resp.Result), nil, nil
	})

	// resize_window: resize the browser viewport.
	type resizeInput struct {
		Width  int `json:"width" jsonschema:"Viewport width in pixels"`
		Height int `json:"height" jsonschema:"Viewport height in pixels"`
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "resize_window",
		Description: "Resize the browser viewport to the given dimensions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input resizeInput) (*mcp.CallToolResult, any, error) {
		if input.Width <= 0 || input.Height <= 0 {
			return errorResult("width and height must be positive"), nil, nil
		}
		if _, err := s.callAction(ctx, "resize_window", map[string]any{"width": input.Width, "height": input.Height}); err != nil {
			return errorResult(fmt.Sprintf("resize failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Resized viewport to %dx%d", input.Width, input.Height)), nil, nil
	})
}

func (s *Server) handleComputer(ctx context.Context, input computerInput) (*mcp.CallToolResult, any, error) {
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
		if _, err := s.callAction(ctx, "mouse_click", map[string]any{"x": x, "y": y, "button": btn, "click_count": 1}); err != nil {
			return errorResult(fmt.Sprintf("click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "double_click":
		if _, err := s.callAction(ctx, "mouse_click", map[string]any{"x": x, "y": y, "button": btn, "click_count": 2}); err != nil {
			return errorResult(fmt.Sprintf("double click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Double-clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "triple_click":
		if _, err := s.callAction(ctx, "mouse_click", map[string]any{"x": x, "y": y, "button": btn, "click_count": 3}); err != nil {
			return errorResult(fmt.Sprintf("triple click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Triple-clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "type":
		if input.Text == "" {
			return errorResult("text is required for type action"), nil, nil
		}
		if _, err := s.callAction(ctx, "type_text", map[string]any{"text": input.Text}); err != nil {
			return errorResult(fmt.Sprintf("type failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Typed %q", input.Text)), nil, nil

	case "key":
		if input.Text == "" {
			return errorResult("text is required for key action (key name)"), nil, nil
		}
		if _, err := s.callAction(ctx, "key_press", map[string]any{"key": input.Text}); err != nil {
			return errorResult(fmt.Sprintf("key failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Pressed key %q", input.Text)), nil, nil

	case "scroll":
		dy := input.DeltaY
		if dy == 0 {
			dy = -3 // Default scroll down.
		}
		if _, err := s.callAction(ctx, "mouse_scroll", map[string]any{"x": x, "y": y, "delta_x": input.DeltaX, "delta_y": dy}); err != nil {
			return errorResult(fmt.Sprintf("scroll failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Scrolled at (%.0f, %.0f)", x, y)), nil, nil

	case "right_click":
		if _, err := s.callAction(ctx, "mouse_click", map[string]any{"x": x, "y": y, "button": "right", "click_count": 1}); err != nil {
			return errorResult(fmt.Sprintf("right click failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Right-clicked at (%.0f, %.0f)", x, y)), nil, nil

	case "move", "hover":
		if _, err := s.callAction(ctx, "mouse_move", map[string]any{"x": x, "y": y}); err != nil {
			return errorResult(fmt.Sprintf("move failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Moved to (%.0f, %.0f)", x, y)), nil, nil

	case "screenshot":
		resp, err := s.callAction(ctx, "screenshot", nil)
		if err != nil {
			return errorResult(fmt.Sprintf("screenshot failed: %v", err)), nil, nil
		}
		// If host returned a file path, read the file directly (avoids base64 over HTTP).
		if resp.ScreenshotPath != "" {
			data, readErr := os.ReadFile(resp.ScreenshotPath)
			if readErr != nil {
				return errorResult(fmt.Sprintf("reading screenshot file: %v", readErr)), nil, nil
			}
			os.Remove(resp.ScreenshotPath) //nolint:errcheck
			return imageResult(data), nil, nil
		}
		// Fallback to base64 decode.
		data, err := base64.StdEncoding.DecodeString(resp.Image)
		if err != nil {
			return errorResult(fmt.Sprintf("screenshot decode failed: %v", err)), nil, nil
		}
		return imageResult(data), nil, nil

	case "left_click_drag":
		if _, err := s.callAction(ctx, "mouse_down", map[string]any{"x": input.StartX, "y": input.StartY, "button": "left"}); err != nil {
			return errorResult(fmt.Sprintf("mouse down failed: %v", err)), nil, nil
		}
		if _, err := s.callAction(ctx, "mouse_move", map[string]any{"x": x, "y": y}); err != nil {
			return errorResult(fmt.Sprintf("drag move failed: %v", err)), nil, nil
		}
		if _, err := s.callAction(ctx, "mouse_up", map[string]any{"x": x, "y": y, "button": "left"}); err != nil {
			return errorResult(fmt.Sprintf("mouse up failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Dragged from (%.0f, %.0f) to (%.0f, %.0f)", input.StartX, input.StartY, x, y)), nil, nil

	case "scroll_to":
		if input.Ref < 1 || input.Ref > len(s.refs) {
			return errorResult(fmt.Sprintf("ref %d out of range (1-%d)", input.Ref, len(s.refs))), nil, nil
		}
		ref := s.refs[input.Ref-1]
		if _, err := s.callAction(ctx, "scroll_into_view", map[string]any{"backend_node_id": ref.BackendDOMNodeID}); err != nil {
			return errorResult(fmt.Sprintf("scroll_to failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Scrolled ref %d (%s: %s) into view", input.Ref, ref.Role, ref.Name)), nil, nil

	case "wait":
		return textResult("Waited"), nil, nil

	default:
		return errorResult(fmt.Sprintf("unknown action: %s", input.Action)), nil, nil
	}
}

// handleFind searches element refs from the host for matches against a natural language query.
func (s *Server) handleFind(ctx context.Context, query string) (*mcp.CallToolResult, any, error) {
	resp, err := s.callAction(ctx, "get_element_refs", nil)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to get element refs: %v", err)), nil, nil
	}
	s.refs = resp.ElementRefs

	queryLower := strings.ToLower(query)
	var matches []browser.ElementRef
	for _, ref := range s.refs {
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
