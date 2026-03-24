package mcpbrowser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync"

	cdpproto "github.com/chromedp/cdproto/cdp"

	"github.com/radutopala/loop/internal/browser"
)

// cdpClientFactory creates a CDPClient from a WebSocket endpoint.
type cdpClientFactory func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (*browser.CDPClient, error)

// newCDPDispatcher creates an actionDispatcher that connects directly to Chrome via CDP.
func newCDPDispatcher(cdpEndpoint string, logger *slog.Logger) actionDispatcher {
	d := &cdpDispatcher{
		cdpEndpoint: cdpEndpoint,
		logger:      logger,
		factory:     browser.NewCDPClient,
	}
	return d.dispatch
}

// directCDP is the subset of browser.CDPSession used by the action dispatcher.
// It intentionally excludes lifecycle methods (Close, Screencast, NewContextForTarget)
// which are managed by cdpDispatcher itself.
type directCDP interface {
	Navigate(ctx context.Context, url string) error
	Reload(ctx context.Context) error
	GoBack(ctx context.Context) error
	GoForward(ctx context.Context) error
	GetPageInfo(ctx context.Context) (*browser.PageInfo, error)
	GetElementRefs(ctx context.Context) ([]browser.ElementRef, error)
	ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error
	MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error
	MouseMove(ctx context.Context, x, y float64, buttons int) error
	MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error
	MouseDown(ctx context.Context, x, y float64, button string) error
	MouseUp(ctx context.Context, x, y float64, button string) error
	KeyPress(ctx context.Context, key string) error
	TypeText(ctx context.Context, text string) error
	Screenshot(ctx context.Context) ([]byte, error)
	EvaluateJS(ctx context.Context, expression string) (string, error)
	ListTabs(ctx context.Context) ([]browser.TabInfo, error)
	NewTab(ctx context.Context, url string) (string, error)
	SwitchTarget(targetID string) error
	CloseTab(ctx context.Context, targetID string) error
	ResizeWindow(ctx context.Context, width, height int) error
	EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error
	EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error
	ScrollIntoView(ctx context.Context, backendNodeID cdpproto.BackendNodeID) error
}

type cdpDispatcher struct {
	cdpEndpoint  string
	logger       *slog.Logger
	factory      cdpClientFactory
	mu           sync.Mutex
	cdp          directCDP
	capture      *browser.CaptureState
	newContextFn func(targetID string) (directCDP, error) // for tab switching without new WS dial
}

func (d *cdpDispatcher) ensureCDP() (directCDP, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cdp != nil {
		return d.cdp, nil
	}
	// Use Background context so the CDP connection outlives individual MCP requests.
	client, err := d.factory(context.Background(), d.cdpEndpoint, d.logger, browser.WithNewTarget())
	if err != nil {
		return nil, fmt.Errorf("connecting to Chrome at %s: %w", d.cdpEndpoint, err)
	}
	d.cdp = client
	// Enable console/network capture.
	d.capture = &browser.CaptureState{}
	d.capture.Enable(context.Background(), client)
	// Wire up tab switching via NewContextForTarget on the initial client.
	// CDPSession is a superset of directCDP, so the returned client always satisfies directCDP.
	d.newContextFn = func(targetID string) (directCDP, error) {
		return client.NewContextForTarget(targetID)
	}
	return client, nil
}

// switchTarget creates a new CDP context for the target, reusing the existing
// browser WebSocket connection (no new dial, no permission prompt).
func (d *cdpDispatcher) switchTarget(targetID string) error {
	d.mu.Lock()
	fn := d.newContextFn
	cap := d.capture
	d.mu.Unlock()

	if fn == nil {
		return fmt.Errorf("no CDP connection (call a tool first to connect)")
	}

	cdp, err := fn(targetID)
	if err != nil {
		return fmt.Errorf("attaching to target %s: %w", targetID, err)
	}

	d.mu.Lock()
	d.cdp = cdp
	d.mu.Unlock()

	// Re-enable console/network capture on the new target.
	if cap != nil {
		cap.Rewire(context.Background(), cdp)
	}

	// Activate the target in Chrome's UI (bring tab to foreground).
	return cdp.SwitchTarget(targetID)
}

func (d *cdpDispatcher) dispatch(ctx context.Context, action string, params map[string]any) (*actionResponse, error) {
	cdp, err := d.ensureCDP()
	if err != nil {
		return nil, err
	}

	switch action {
	case "navigate":
		url, _ := params["url"].(string)
		if err := cdp.Navigate(ctx, url); err != nil {
			return nil, fmt.Errorf("navigate failed: %w", err)
		}
		info, err := cdp.GetPageInfo(ctx)
		if err != nil {
			return &actionResponse{Result: "Navigated"}, nil
		}
		return &actionResponse{Result: fmt.Sprintf("Navigated to %s", info.URL), PageInfo: info}, nil

	case "reload":
		if err := cdp.Reload(ctx); err != nil {
			return nil, fmt.Errorf("reload failed: %w", err)
		}
		return &actionResponse{Result: "Page reloaded"}, nil

	case "go_back":
		if err := cdp.GoBack(ctx); err != nil {
			return nil, fmt.Errorf("go back failed: %w", err)
		}
		return &actionResponse{Result: "Navigated back"}, nil

	case "go_forward":
		if err := cdp.GoForward(ctx); err != nil {
			return nil, fmt.Errorf("go forward failed: %w", err)
		}
		return &actionResponse{Result: "Navigated forward"}, nil

	case "get_page_info":
		info, err := cdp.GetPageInfo(ctx)
		if err != nil {
			return nil, fmt.Errorf("get page info failed: %w", err)
		}
		return &actionResponse{PageInfo: info}, nil

	case "get_element_refs":
		refs, err := cdp.GetElementRefs(ctx)
		if err != nil {
			return nil, fmt.Errorf("get element refs failed: %w", err)
		}
		return &actionResponse{ElementRefs: refs}, nil

	case "mouse_click":
		x, _ := params["x"].(float64)
		y, _ := params["y"].(float64)
		btn, _ := params["button"].(string)
		if btn == "" {
			btn = "left"
		}
		count := 1
		if c, ok := params["click_count"].(float64); ok && c > 0 {
			count = int(c)
		}
		if err := cdp.MouseClick(ctx, x, y, btn, count); err != nil {
			return nil, fmt.Errorf("mouse click failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Clicked at (%.0f, %.0f)", x, y)}, nil

	case "mouse_move":
		x, _ := params["x"].(float64)
		y, _ := params["y"].(float64)
		buttons := 0
		if b, ok := params["buttons"].(float64); ok {
			buttons = int(b)
		}
		if err := cdp.MouseMove(ctx, x, y, buttons); err != nil {
			return nil, fmt.Errorf("mouse move failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Moved to (%.0f, %.0f)", x, y)}, nil

	case "mouse_scroll":
		x, _ := params["x"].(float64)
		y, _ := params["y"].(float64)
		dx, _ := params["delta_x"].(float64)
		dy, _ := params["delta_y"].(float64)
		if err := cdp.MouseScroll(ctx, x, y, dx, dy); err != nil {
			return nil, fmt.Errorf("scroll failed: %w", err)
		}
		return &actionResponse{Result: "Scrolled"}, nil

	case "key_press":
		key, _ := params["key"].(string)
		if err := cdp.KeyPress(ctx, key); err != nil {
			return nil, fmt.Errorf("key press failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Pressed %s", key)}, nil

	case "type_text":
		text, _ := params["text"].(string)
		if err := cdp.TypeText(ctx, text); err != nil {
			return nil, fmt.Errorf("type text failed: %w", err)
		}
		return &actionResponse{Result: "Typed text"}, nil

	case "screenshot":
		data, err := cdp.Screenshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("screenshot failed: %w", err)
		}
		return &actionResponse{Image: base64.StdEncoding.EncodeToString(data)}, nil

	case "evaluate_js":
		expr, _ := params["expression"].(string)
		result, err := cdp.EvaluateJS(ctx, expr)
		if err != nil {
			return nil, fmt.Errorf("evaluate failed: %w", err)
		}
		return &actionResponse{Result: result}, nil

	case "list_tabs":
		tabs, err := cdp.ListTabs(ctx)
		if err != nil {
			return nil, fmt.Errorf("list tabs failed: %w", err)
		}
		return &actionResponse{Tabs: tabs}, nil

	case "new_tab":
		url, _ := params["url"].(string)
		if url == "" {
			url = "about:blank"
		}
		tid, err := cdp.NewTab(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("new tab failed: %w", err)
		}
		// Switch dispatcher context to the new tab so subsequent actions target it.
		if err := d.switchTarget(tid); err != nil {
			return nil, fmt.Errorf("new tab created but switch failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Opened new tab %s", tid)}, nil

	case "switch_tab":
		tid, _ := params["target_id"].(string)
		if err := d.switchTarget(tid); err != nil {
			return nil, fmt.Errorf("switch tab failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Switched to tab %s", tid)}, nil

	case "close_tab":
		tid, _ := params["target_id"].(string)
		if err := cdp.CloseTab(ctx, tid); err != nil {
			return nil, fmt.Errorf("close tab failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Closed tab %s", tid)}, nil

	case "resize_window":
		w, _ := params["width"].(float64)
		h, _ := params["height"].(float64)
		if err := cdp.ResizeWindow(ctx, int(w), int(h)); err != nil {
			return nil, fmt.Errorf("resize failed: %w", err)
		}
		return &actionResponse{Result: fmt.Sprintf("Resized to %dx%d", int(w), int(h))}, nil

	case "click_ref":
		refs, _ := params["refs"].([]browser.ElementRef)
		refIndex, _ := params["ref_index"].(int)
		if err := cdp.ClickRef(ctx, refs, refIndex); err != nil {
			return nil, fmt.Errorf("click ref failed: %w", err)
		}
		return &actionResponse{Result: "Clicked ref"}, nil

	case "mouse_down":
		x, _ := params["x"].(float64)
		y, _ := params["y"].(float64)
		btn, _ := params["button"].(string)
		if err := cdp.MouseDown(ctx, x, y, btn); err != nil {
			return nil, fmt.Errorf("mouse down failed: %w", err)
		}
		return &actionResponse{Result: "Mouse down"}, nil

	case "mouse_up":
		x, _ := params["x"].(float64)
		y, _ := params["y"].(float64)
		btn, _ := params["button"].(string)
		if err := cdp.MouseUp(ctx, x, y, btn); err != nil {
			return nil, fmt.Errorf("mouse up failed: %w", err)
		}
		return &actionResponse{Result: "Mouse up"}, nil

	case "scroll_into_view":
		nodeID, _ := params["backend_node_id"].(float64)
		if err := cdp.ScrollIntoView(ctx, cdpproto.BackendNodeID(nodeID)); err != nil {
			return nil, fmt.Errorf("scroll into view failed: %w", err)
		}
		return &actionResponse{Result: "Scrolled into view"}, nil

	case "read_console":
		if d.capture == nil {
			return &actionResponse{Result: "No console messages"}, nil
		}
		pattern, _ := params["pattern"].(string)
		onlyErrors, _ := params["only_errors"].(bool)
		clear, _ := params["clear"].(bool)
		limit := 100
		if l, ok := params["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		result, err := d.capture.ReadConsole(pattern, onlyErrors, limit, clear)
		if err != nil {
			return nil, fmt.Errorf("read console failed: %w", err)
		}
		return &actionResponse{Result: result}, nil

	case "read_network":
		if d.capture == nil {
			return &actionResponse{Result: "No network requests"}, nil
		}
		pattern, _ := params["pattern"].(string)
		clear, _ := params["clear"].(bool)
		limit := 50
		if l, ok := params["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		result, err := d.capture.ReadNetwork(pattern, limit, clear)
		if err != nil {
			return nil, fmt.Errorf("read network failed: %w", err)
		}
		return &actionResponse{Result: result}, nil

	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}
