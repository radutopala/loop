package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
)

// ChromeHTTPBaseURL converts a CDP WebSocket URL to its HTTP base URL.
// E.g. "ws://127.0.0.1:55008" → "http://127.0.0.1:55008".
func ChromeHTTPBaseURL(wsURL string) string {
	return strings.Replace(wsURL, "ws://", "http://", 1)
}

// ActivateTarget activates a page target in Chrome via its HTTP endpoint.
func ActivateTarget(wsURL, targetID string) error {
	activateURL := ChromeHTTPBaseURL(wsURL) + "/json/activate/" + targetID
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(activateURL) //nolint:gosec,noctx
	if err != nil {
		return fmt.Errorf("activating target %s: %w", targetID, err)
	}
	resp.Body.Close()
	return nil
}

// CreatePageTarget creates a new about:blank page target via Chrome's HTTP endpoint.
func CreatePageTarget(wsURL string) (string, error) {
	newURL := ChromeHTTPBaseURL(wsURL) + "/json/new?about:blank"
	req, err := http.NewRequest(http.MethodPut, newURL, nil) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("creating new target request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating new page target: %w", err)
	}
	defer resp.Body.Close()
	var newTarget struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&newTarget); err != nil {
		return "", fmt.Errorf("decoding new target: %w", err)
	}
	if newTarget.ID == "" {
		return "", fmt.Errorf("empty target ID from /json/new")
	}
	return newTarget.ID, nil
}

// findOrCreatePageTarget queries Chrome's HTTP endpoint to find an existing page target.
// If no page target exists, creates one via /json/new and returns its ID.
func findOrCreatePageTarget(wsURL string, reuseExisting bool) (target.ID, error) {
	// Reuse an existing page target if requested.
	if reuseExisting {
		baseURL := ChromeHTTPBaseURL(wsURL)
		resp, err := http.Get(baseURL + "/json/list") //nolint:gosec,noctx // internal call to local Chrome
		if err == nil {
			defer resp.Body.Close()
			var targets []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&targets); err == nil {
				for _, t := range targets {
					if t.Type == "page" {
						return target.ID(t.ID), nil
					}
				}
			}
		}
	}

	id, err := CreatePageTarget(wsURL)
	return target.ID(id), err
}

// CDPClient wraps a chromedp browser context for CDP operations.
// It handles screencast streaming, input dispatch, navigation, and accessibility.
type CDPClient struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	ctx         context.Context
	wsURL       string // Chrome CDP WebSocket URL for HTTP endpoint access
	logger      *slog.Logger

	// Function deps — set by constructor, overridable in tests via direct struct construction.
	runFn         func(context.Context, ...chromedp.Action) error
	targetsFunc   func(context.Context) ([]*target.Info, error)
	listenFunc    func(context.Context, func(any))
	axTreeFunc    func(context.Context) ([]*accessibility.Node, error)
	boxModelFunc  func(context.Context, cdp.BackendNodeID) (*cdpdom.BoxModel, error)
	createTabFunc func(context.Context, string) (target.ID, error)
	activateFunc  func(context.Context, target.ID) error

	targetID target.ID   // the page target this client is attached to
	exec     cdpExecutor // injectable cdp.Execute

	mu                 sync.Mutex
	screencasting      bool
	listenerRegistered bool        // true after first listenFunc registration
	frameCh            chan []byte // decoded JPEG frames
	stopCh             chan struct{}
}

// TargetID returns the Chrome page target ID this client is attached to.
func (c *CDPClient) TargetID() string {
	return string(c.targetID)
}

// SwitchTarget activates a different page target in Chrome.
// It bypasses the CDP command queue (which can block due to pending
// screencast frame acks) and uses Chrome's HTTP endpoint directly.
func (c *CDPClient) SwitchTarget(targetID string) error {
	c.StopScreencast()

	if err := ActivateTarget(c.wsURL, targetID); err != nil {
		c.logger.Error("SwitchTarget: activate failed", "error", err)
		return err
	}
	c.logger.Info("SwitchTarget: activated", "target_id", targetID)

	c.targetID = target.ID(targetID)

	c.mu.Lock()
	c.frameCh = make(chan []byte, 2)
	c.stopCh = make(chan struct{})
	c.mu.Unlock()

	return nil
}

// cdpConfig holds constructor-level dependencies, overridable via CDPOption.
type cdpConfig struct {
	allocFunc   func(context.Context, string) (context.Context, context.CancelFunc)
	runFunc     func(context.Context, ...chromedp.Action) error
	exec        cdpExecutor // injectable cdp.Execute for testability
	targetID    target.ID   // attach to existing target instead of creating new
	reuseTarget bool        // if true, reuse existing page target; if false, always create new
}

// CDPOption configures NewCDPClient.
type CDPOption func(*cdpConfig)

// WithAllocator overrides the remote allocator used to connect to Chrome.
func WithAllocator(fn func(context.Context, string) (context.Context, context.CancelFunc)) CDPOption {
	return func(c *cdpConfig) { c.allocFunc = fn }
}

// WithRunFunc overrides the chromedp.Run function used during construction.
func WithRunFunc(fn func(context.Context, ...chromedp.Action) error) CDPOption {
	return func(c *cdpConfig) { c.runFunc = fn }
}

// WithTargetID attaches to a specific page target by ID.
func WithTargetID(id string) CDPOption {
	return func(c *cdpConfig) { c.targetID = target.ID(id) }
}

// WithExec overrides the cdp.Execute function used internally.
func WithExec(fn cdpExecutor) CDPOption {
	return func(c *cdpConfig) { c.exec = fn }
}

// WithNewTarget forces creation of a new page target instead of reusing existing ones.
// Use this when another CDP client may already be attached to the existing target.
func WithNewTarget() CDPOption {
	return func(c *cdpConfig) { c.reuseTarget = false }
}

// NewCDPClient connects to a Chrome instance via its CDP WebSocket URL.
func NewCDPClient(ctx context.Context, wsURL string, logger *slog.Logger, opts ...CDPOption) (*CDPClient, error) {
	cfg := cdpConfig{
		allocFunc: func(parent context.Context, ws string) (context.Context, context.CancelFunc) {
			return chromedp.NewRemoteAllocator(parent, ws)
		},
		runFunc:     chromedp.Run,
		exec:        cdp.Execute,
		reuseTarget: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Chrome 144+ headless rejects Target.createTarget with "no browser is open".
	// Query the HTTP endpoint to find or create a page target and attach to it.
	var contextOpts []chromedp.ContextOption
	var resolvedTargetID target.ID
	if cfg.targetID != "" {
		resolvedTargetID = cfg.targetID
	} else if tid, err := findOrCreatePageTarget(wsURL, cfg.reuseTarget); err == nil {
		resolvedTargetID = tid
	}
	if resolvedTargetID != "" {
		contextOpts = append(contextOpts, chromedp.WithTargetID(resolvedTargetID))
	}

	allocCtx, allocCancel := cfg.allocFunc(ctx, wsURL)
	// Suppress chromedp's error logging for Chrome 136+ unknown enum values.
	contextOpts = append(contextOpts, chromedp.WithErrorf(func(string, ...any) {}))
	cdpCtx, cdpCancel := chromedp.NewContext(allocCtx, contextOpts...)

	// Run a no-op action to establish the connection and verify it works.
	if err := cfg.runFunc(cdpCtx); err != nil {
		cdpCancel()
		allocCancel()
		return nil, fmt.Errorf("connecting to CDP at %s: %w", wsURL, err)
	}

	return &CDPClient{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		ctxCancel:   cdpCancel,
		ctx:         cdpCtx,
		wsURL:       wsURL,
		targetID:    resolvedTargetID,
		exec:        cfg.exec,
		logger:      logger,
		runFn:       cfg.runFunc,
		targetsFunc: chromedp.Targets,
		listenFunc: func(ctx context.Context, fn func(any)) {
			chromedp.ListenTarget(ctx, fn)
		},
		axTreeFunc: makeAxTreeFuncWith(cfg.runFunc, cfg.exec),
		boxModelFunc: func(ctx context.Context, nodeID cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
			var returns cdpdom.GetBoxModelReturns
			err := cfg.runFunc(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return cfg.exec(ctx, string(cdpdom.CommandGetBoxModel),
					cdpdom.GetBoxModel().WithBackendNodeID(nodeID), &returns)
			}))
			return returns.Model, err
		},
		createTabFunc: func(ctx context.Context, url string) (target.ID, error) {
			var returns target.CreateTargetReturns
			err := cfg.runFunc(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return cfg.exec(ctx, string(target.CommandCreateTarget),
					target.CreateTarget(url), &returns)
			}))
			return returns.TargetID, err
		},
		activateFunc: func(ctx context.Context, id target.ID) error {
			return cfg.runFunc(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return cfg.exec(ctx, string(target.CommandActivateTarget),
					target.ActivateTarget(id), nil)
			}))
		},
		frameCh: make(chan []byte, 2),
		stopCh:  make(chan struct{}),
	}, nil
}

// Close shuts down the CDP connection and closes the page target.
func (c *CDPClient) Close() {
	c.mu.Lock()
	wasScreencasting := c.screencasting
	c.screencasting = false
	c.mu.Unlock()

	if wasScreencasting {
		close(c.stopCh)
		_ = c.runFn(c.ctx, cdppage.StopScreencast())
	}

	c.ctxCancel()
	c.allocCancel()
}

// Navigate navigates to the given URL.
func (c *CDPClient) Navigate(ctx context.Context, url string) error {
	return c.runFn(c.ctx, chromedp.Navigate(url))
}

// Reload reloads the current page.
func (c *CDPClient) Reload(ctx context.Context) error {
	return c.runFn(c.ctx, chromedp.Reload())
}

// GoBack navigates back in history via window.history.back().
// This is a no-op if there is no history to go back to.
func (c *CDPClient) GoBack(ctx context.Context) error {
	return c.runFn(c.ctx, chromedp.Evaluate(`void(window.history.back())`, nil))
}

// GoForward navigates forward in history via window.history.forward().
// This is a no-op if there is no history to go forward to.
func (c *CDPClient) GoForward(ctx context.Context) error {
	return c.runFn(c.ctx, chromedp.Evaluate(`void(window.history.forward())`, nil))
}

// PageInfo holds the current page URL and title.
type PageInfo struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// GetPageInfo returns the current page URL and title.
func (c *CDPClient) GetPageInfo(ctx context.Context) (*PageInfo, error) {
	var url, title string
	if err := c.runFn(c.ctx,
		chromedp.Location(&url),
		chromedp.Title(&title),
	); err != nil {
		return nil, fmt.Errorf("getting page info: %w", err)
	}
	return &PageInfo{URL: url, Title: title}, nil
}

// StartScreencast begins streaming JPEG frames from Chrome.
// Frames are sent to the returned channel. Call StopScreencast to stop.
// Each call returns a NEW channel — callers must not hold old references.
func (c *CDPClient) StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Always create a fresh frameCh so old pipeFrames goroutines can't
	// steal frames from the new one (two readers on one channel = race).
	c.frameCh = make(chan []byte, 2)
	c.logger.Info("StartScreencast", "already_screencasting", c.screencasting, "target_id", string(c.targetID))

	// Register the frame listener ONCE per CDP client lifetime.
	// chromedp.ListenTarget adds listeners — never removes them.
	// Multiple registrations cause duplicate acks that clog the queue.
	if !c.listenerRegistered {
		c.listenerRegistered = true
		c.listenFunc(c.ctx, func(ev any) {
			e, ok := ev.(*cdppage.EventScreencastFrame)
			if !ok {
				return
			}
			data, err := base64.StdEncoding.DecodeString(e.Data)
			if err != nil {
				c.logger.Error("failed to decode screencast frame", "error", err)
				return
			}

			go func() {
				if err := c.runFn(c.ctx, cdppage.ScreencastFrameAck(e.SessionID)); err != nil {
					c.logger.Debug("screencast ack failed", "error", err)
				}
			}()

			c.mu.Lock()
			ch := c.frameCh
			c.mu.Unlock()
			select {
			case ch <- data:
			default:
			}
		})
	}

	if !c.screencasting {
		c.screencasting = true
		c.stopCh = make(chan struct{})

		go func() {
			err := c.runFn(c.ctx,
				cdppage.StartScreencast().
					WithFormat(cdppage.ScreencastFormatJpeg).
					WithQuality(int64(quality)).
					WithMaxWidth(int64(maxWidth)).
					WithMaxHeight(int64(maxHeight)).
					WithEveryNthFrame(1),
			)
			if err != nil {
				c.logger.Error("failed to start screencast", "error", err)
			}
		}()
	}

	return c.frameCh
}

// ResetScreencast marks the screencast as stopped without sending a CDP command.
// Use this when the WS connection was lost and the screencast state is stale.
func (c *CDPClient) ResetScreencast() {
	c.mu.Lock()
	c.screencasting = false
	c.mu.Unlock()
}

// StopScreencast stops the screencast stream.
func (c *CDPClient) StopScreencast() {
	c.mu.Lock()
	wasScreencasting := c.screencasting
	c.screencasting = false
	c.mu.Unlock()

	if wasScreencasting {
		close(c.stopCh)
		c.logger.Info("StopScreencast: sending CDP stop command")
		_ = c.runFn(c.ctx, cdppage.StopScreencast())
		c.logger.Info("StopScreencast: done")
	}
}

// MouseClick dispatches a mouse click at the given coordinates.
func (c *CDPClient) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	btn := input.Left
	switch button {
	case "right":
		btn = input.Right
	case "middle":
		btn = input.Middle
	}

	return c.runFn(c.ctx,
		input.DispatchMouseEvent(input.MousePressed, x, y).
			WithButton(btn).
			WithClickCount(int64(clickCount)),
		input.DispatchMouseEvent(input.MouseReleased, x, y).
			WithButton(btn).
			WithClickCount(int64(clickCount)),
	)
}

// MouseMove dispatches a mouse move event.
func (c *CDPClient) MouseMove(ctx context.Context, x, y float64) error {
	return c.runFn(c.ctx,
		input.DispatchMouseEvent(input.MouseMoved, x, y),
	)
}

// MouseScroll dispatches a mouse wheel event.
func (c *CDPClient) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return c.runFn(c.ctx,
		input.DispatchMouseEvent(input.MouseWheel, x, y).
			WithDeltaX(deltaX).
			WithDeltaY(deltaY),
	)
}

// keyCodeMap maps key names to their virtual key codes for special keys.
var keyCodeMap = map[string]int64{
	"Backspace": 8, "Tab": 9, "Enter": 13, "Escape": 27,
	"ArrowLeft": 37, "ArrowUp": 38, "ArrowRight": 39, "ArrowDown": 40,
	"Delete": 46, "Home": 36, "End": 35, "PageUp": 33, "PageDown": 34,
}

// KeyPress dispatches key down and key up events.
func (c *CDPClient) KeyPress(ctx context.Context, key string) error {
	down := input.DispatchKeyEvent(input.KeyDown).WithKey(key)
	up := input.DispatchKeyEvent(input.KeyUp).WithKey(key)
	if code, ok := keyCodeMap[key]; ok {
		down = down.WithWindowsVirtualKeyCode(code).WithNativeVirtualKeyCode(code)
		up = up.WithWindowsVirtualKeyCode(code).WithNativeVirtualKeyCode(code)
	}
	return c.runFn(c.ctx, down, up)
}

// TypeText types text character by character.
func (c *CDPClient) TypeText(ctx context.Context, text string) error {
	for _, ch := range text {
		s := string(ch)
		if err := c.runFn(c.ctx,
			input.DispatchKeyEvent(input.KeyDown).WithText(s).WithKey(s),
			input.DispatchKeyEvent(input.KeyUp).WithKey(s),
		); err != nil {
			return fmt.Errorf("typing character %q: %w", s, err)
		}
	}
	return nil
}

// ElementRef represents an interactive element with a ref ID for precise interaction.
type ElementRef struct {
	RefID            string            `json:"ref_id"`
	Role             string            `json:"role"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Value            string            `json:"value,omitempty"`
	X                float64           `json:"x"`
	Y                float64           `json:"y"`
	Width            float64           `json:"width"`
	Height           float64           `json:"height"`
	BackendDOMNodeID cdp.BackendNodeID `json:"backend_dom_node_id,omitempty"` // internal, used for scroll_to
}

// makeAxTreeFunc creates an accessibility tree function with lenient JSON parsing.
// Chrome 136+ adds PropertyName values that cdproto's strict enum unmarshaler
// rejects. This function parses nodes individually, ignoring unmarshal errors.
// Uses the provided runFn (instead of chromedp.Run directly) for testability.
// cdpExecutor abstracts cdp.Execute for testability.
type cdpExecutor func(ctx context.Context, method string, params, res any) error

func makeAxTreeFunc(runFn func(context.Context, ...chromedp.Action) error) func(context.Context) ([]*accessibility.Node, error) {
	return makeAxTreeFuncWith(runFn, cdp.Execute)
}

func makeAxTreeFuncWith(runFn func(context.Context, ...chromedp.Action) error, exec cdpExecutor) func(context.Context) ([]*accessibility.Node, error) {
	return func(ctx context.Context) ([]*accessibility.Node, error) {
		var nodes []*accessibility.Node
		err := runFn(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var raw struct {
				Nodes []json.RawMessage `json:"nodes"`
			}
			if e := exec(ctx, "Accessibility.getFullAXTree", nil, &raw); e != nil {
				return e
			}
			for _, nodeJSON := range raw.Nodes {
				// Try strict unmarshal first.
				var node accessibility.Node
				if err := json.Unmarshal(nodeJSON, &node); err == nil {
					nodes = append(nodes, &node)
					continue
				}
				// Fallback: extract only the fields we need for GetElementRefs.
				var m struct {
					NodeID           string            `json:"nodeId"`
					Ignored          bool              `json:"ignored"`
					BackendDOMNodeID cdp.BackendNodeID `json:"backendDOMNodeId"`
					Role             *struct {
						Value string `json:"value"`
					} `json:"role"`
					Name *struct {
						Value string `json:"value"`
					} `json:"name"`
					Description *struct {
						Value string `json:"value"`
					} `json:"description"`
					Value *struct {
						Value string `json:"value"`
					} `json:"value"`
				}
				if err := json.Unmarshal(nodeJSON, &m); err != nil {
					continue
				}
				n := &accessibility.Node{
					Ignored:          m.Ignored,
					BackendDOMNodeID: m.BackendDOMNodeID,
				}
				if m.Role != nil {
					n.Role = &accessibility.Value{Value: jsontext.Value(`"` + m.Role.Value + `"`)}
				}
				if m.Name != nil {
					n.Name = &accessibility.Value{Value: jsontext.Value(`"` + m.Name.Value + `"`)}
				}
				if m.Description != nil {
					n.Description = &accessibility.Value{Value: jsontext.Value(`"` + m.Description.Value + `"`)}
				}
				if m.Value != nil {
					n.Value = &accessibility.Value{Value: jsontext.Value(`"` + m.Value.Value + `"`)}
				}
				nodes = append(nodes, n)
			}
			return nil
		}))
		return nodes, err
	}
}

// interactiveRoles lists the accessibility roles that represent interactive elements.
var interactiveRoles = map[string]bool{
	"button":           true,
	"link":             true,
	"textbox":          true,
	"checkbox":         true,
	"radio":            true,
	"combobox":         true,
	"menuitem":         true,
	"tab":              true,
	"switch":           true,
	"slider":           true,
	"spinbutton":       true,
	"searchbox":        true,
	"option":           true,
	"menuitemcheckbox": true,
	"menuitemradio":    true,
}

// GetElementRefs returns interactive elements from the accessibility tree with bounding boxes.
func (c *CDPClient) GetElementRefs(ctx context.Context) ([]ElementRef, error) {
	// Get the accessibility tree.
	nodes, err := c.axTreeFunc(c.ctx)
	if err != nil {
		return nil, fmt.Errorf("getting accessibility tree: %w", err)
	}

	var refs []ElementRef
	refNum := 1

	for _, node := range nodes {
		if node.Ignored || node.Role == nil {
			continue
		}

		role := strings.Trim(fmt.Sprintf("%v", node.Role.Value), `"`)
		if !interactiveRoles[role] {
			continue
		}

		// Skip elements without a backend DOM node.
		if node.BackendDOMNodeID == 0 {
			continue
		}

		name := ""
		if node.Name != nil {
			name = strings.Trim(fmt.Sprintf("%v", node.Name.Value), `"`)
		}
		desc := ""
		if node.Description != nil {
			desc = strings.Trim(fmt.Sprintf("%v", node.Description.Value), `"`)
		}
		val := ""
		if node.Value != nil {
			val = strings.Trim(fmt.Sprintf("%v", node.Value.Value), `"`)
		}

		// Get bounding box via DOM.getBoxModel. Skip if not available
		// (element might be off-screen, hidden, or detached).
		boxModel, err := c.boxModelFunc(c.ctx, node.BackendDOMNodeID)
		if err != nil || boxModel == nil || len(boxModel.Content) < 8 {
			continue
		}

		// Content quad: [x1,y1, x2,y2, x3,y3, x4,y4]
		quad := boxModel.Content
		x := quad[0]
		y := quad[1]
		w := quad[2] - quad[0]
		h := quad[5] - quad[1]

		if w <= 0 || h <= 0 {
			continue
		}

		refs = append(refs, ElementRef{
			RefID:            fmt.Sprintf("ref_%d", refNum),
			Role:             role,
			Name:             name,
			Description:      desc,
			Value:            val,
			X:                x,
			Y:                y,
			Width:            w,
			Height:           h,
			BackendDOMNodeID: node.BackendDOMNodeID,
		})
		refNum++
	}

	return refs, nil
}

// ClickRef clicks the center of an element by its ref index (1-based).
func (c *CDPClient) ClickRef(ctx context.Context, refs []ElementRef, refIndex int) error {
	if refIndex < 1 || refIndex > len(refs) {
		return fmt.Errorf("ref index %d out of range (1-%d)", refIndex, len(refs))
	}
	ref := refs[refIndex-1]
	centerX := ref.X + ref.Width/2
	centerY := ref.Y + ref.Height/2
	return c.MouseClick(ctx, centerX, centerY, "left", 1)
}

// Screenshot captures a full-page screenshot as PNG.
func (c *CDPClient) Screenshot(ctx context.Context) ([]byte, error) {
	var buf []byte
	if err := c.runFn(c.ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return nil, fmt.Errorf("capturing screenshot: %w", err)
	}
	return buf, nil
}

// TabInfo holds information about a browser tab.
type TabInfo struct {
	TargetID string `json:"target_id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Active   bool   `json:"active,omitempty"`
}

// ListTabs returns all open browser tabs.
// Uses Chrome's HTTP /json/list endpoint to avoid blocking the chromedp
// command queue (which can be clogged by pending screencast frame acks).
func (c *CDPClient) ListTabs(_ context.Context) ([]TabInfo, error) {
	if c.wsURL == "" {
		// Fallback to chromedp for tests without wsURL.
		targets, err := c.targetsFunc(c.ctx)
		if err != nil {
			return nil, fmt.Errorf("listing targets: %w", err)
		}
		var tabs []TabInfo
		for _, t := range targets {
			if t.Type == "page" {
				tabs = append(tabs, TabInfo{
					TargetID: string(t.TargetID),
					URL:      t.URL,
					Title:    t.Title,
				})
			}
		}
		return tabs, nil
	}

	baseURL := ChromeHTTPBaseURL(c.wsURL)
	client := &http.Client{Timeout: 5 * time.Second}
	listURL := baseURL + "/json/list"
	resp, err := client.Get(listURL) //nolint:gosec,noctx
	if err != nil {
		// Fallback to chromedp targets if HTTP fails (e.g. network issues).
		targets, err2 := c.targetsFunc(c.ctx)
		if err2 != nil {
			return nil, fmt.Errorf("listing targets via HTTP (%s: %w) and CDP (%w)", listURL, err, err2)
		}
		var tabs []TabInfo
		for _, t := range targets {
			if t.Type == "page" {
				tabs = append(tabs, TabInfo{
					TargetID: string(t.TargetID),
					URL:      t.URL,
					Title:    t.Title,
				})
			}
		}
		return tabs, nil
	}
	defer resp.Body.Close()

	var targets []struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		// Response might be HTML (Chrome error page). Fallback to chromedp.
		cdpTargets, err2 := c.targetsFunc(c.ctx)
		if err2 != nil {
			return nil, fmt.Errorf("decoding targets from %s: %w", listURL, err)
		}
		var tabs []TabInfo
		for _, t := range cdpTargets {
			if t.Type == "page" {
				tabs = append(tabs, TabInfo{
					TargetID: string(t.TargetID),
					URL:      t.URL,
					Title:    t.Title,
				})
			}
		}
		return tabs, nil
	}

	var tabs []TabInfo
	for _, t := range targets {
		if t.Type == "page" {
			tabs = append(tabs, TabInfo{
				TargetID: t.ID,
				URL:      t.URL,
				Title:    t.Title,
			})
		}
	}
	return tabs, nil
}

// NewTab opens a new tab with the given URL.
func (c *CDPClient) NewTab(ctx context.Context, url string) (string, error) {
	tCtx, err := c.createTabFunc(c.ctx, url)
	if err != nil {
		return "", fmt.Errorf("creating new tab: %w", err)
	}
	return string(tCtx), nil
}

// SwitchTab switches to a tab by its target ID.
func (c *CDPClient) SwitchTab(ctx context.Context, targetID string) error {
	return c.activateFunc(c.ctx, target.ID(targetID))
}

// CloseTab closes a tab by its target ID.
// Uses Chrome's HTTP /json/close endpoint to avoid chromedp context issues.
func (c *CDPClient) CloseTab(_ context.Context, targetID string) error {
	if c.wsURL != "" {
		baseURL := ChromeHTTPBaseURL(c.wsURL)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(baseURL + "/json/close/" + targetID) //nolint:gosec,noctx
		if err != nil {
			return fmt.Errorf("closing tab %s: %w", targetID, err)
		}
		resp.Body.Close()
		return nil
	}
	return c.runFn(c.ctx, target.CloseTarget(target.ID(targetID)))
}

// EvaluateJS evaluates a JavaScript expression and returns the result as a string.
func (c *CDPClient) EvaluateJS(ctx context.Context, expression string) (string, error) {
	var result string
	if err := c.runFn(c.ctx, chromedp.Evaluate(expression, &result)); err != nil {
		return "", fmt.Errorf("evaluating JS: %w", err)
	}
	return result, nil
}

// ConsoleMessage represents a captured browser console message.
type ConsoleMessage struct {
	Level string    `json:"level"` // "log", "info", "warning", "error", etc.
	Text  string    `json:"text"`
	Time  time.Time `json:"time"`
}

// EnableConsoleCapture enables the Runtime domain and listens for console API calls.
// Captured messages are sent to the provided channel. The caller owns the channel
// and should close it when done.
func (c *CDPClient) EnableConsoleCapture(ctx context.Context, ch chan<- ConsoleMessage) error {
	// Enable Runtime domain so we receive consoleAPICalled events.
	if err := c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return c.exec(ctx, cdpruntime.CommandEnable, nil, nil)
	})); err != nil {
		return fmt.Errorf("enabling runtime domain: %w", err)
	}

	c.listenFunc(c.ctx, func(ev any) {
		e, ok := ev.(*cdpruntime.EventConsoleAPICalled)
		if !ok {
			return
		}
		var parts []string
		for _, arg := range e.Args {
			if arg.Description != "" {
				parts = append(parts, arg.Description)
			} else {
				v := strings.Trim(string(arg.Value), `"`)
				if v != "" {
					parts = append(parts, v)
				}
			}
		}
		msg := ConsoleMessage{
			Level: string(e.Type),
			Text:  strings.Join(parts, " "),
			Time:  time.Now(),
		}
		// Non-blocking send; drop if channel is full.
		select {
		case ch <- msg:
		default:
		}
	})

	return nil
}

// NetworkRequest represents a captured network request with response metadata.
type NetworkRequest struct {
	URL        string    `json:"url"`
	Method     string    `json:"method"`
	Status     int64     `json:"status"`
	StatusText string    `json:"status_text"`
	Type       string    `json:"type"`
	Time       time.Time `json:"time"`
}

// EnableNetworkCapture enables the Network domain and listens for request/response events.
// Request metadata is sent to the provided channel. The caller owns the channel.
func (c *CDPClient) EnableNetworkCapture(ctx context.Context, ch chan<- NetworkRequest) error {
	if err := c.runFn(c.ctx, network.Enable()); err != nil {
		return fmt.Errorf("enabling network domain: %w", err)
	}

	// Track pending requests so we can pair responses with request metadata.
	pending := &sync.Map{}

	c.listenFunc(c.ctx, func(ev any) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			pending.Store(string(e.RequestID), NetworkRequest{
				URL:    e.Request.URL,
				Method: e.Request.Method,
				Type:   string(e.Type),
				Time:   time.Now(),
			})
		case *network.EventResponseReceived:
			val, ok := pending.LoadAndDelete(string(e.RequestID))
			if !ok {
				return
			}
			req := val.(NetworkRequest)
			req.Status = e.Response.Status
			req.StatusText = e.Response.StatusText
			req.Type = string(e.Type)
			select {
			case ch <- req:
			default:
			}
		}
	})

	return nil
}

// ResizeWindow overrides the device metrics to emulate a given viewport size.
func (c *CDPClient) ResizeWindow(ctx context.Context, width, height int) error {
	return c.runFn(c.ctx, emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1.0, false))
}

// ScrollIntoView scrolls a DOM element identified by backendNodeID into view.
func (c *CDPClient) ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error {
	return c.runFn(c.ctx, cdpdom.ScrollIntoViewIfNeeded().WithBackendNodeID(backendNodeID))
}

// MouseDown dispatches a mouse pressed event at the given coordinates.
func (c *CDPClient) MouseDown(ctx context.Context, x, y float64, button string) error {
	btn := input.Left
	switch button {
	case "right":
		btn = input.Right
	case "middle":
		btn = input.Middle
	}
	return c.runFn(c.ctx, input.DispatchMouseEvent(input.MousePressed, x, y).
		WithButton(btn).WithClickCount(1))
}

// MouseUp dispatches a mouse released event at the given coordinates.
func (c *CDPClient) MouseUp(ctx context.Context, x, y float64, button string) error {
	btn := input.Left
	switch button {
	case "right":
		btn = input.Right
	case "middle":
		btn = input.Middle
	}
	return c.runFn(c.ctx, input.DispatchMouseEvent(input.MouseReleased, x, y).
		WithButton(btn).WithClickCount(1))
}
