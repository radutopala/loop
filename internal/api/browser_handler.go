package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/browser"
)

// BrowserManager is the interface for managing browser lifecycle.
type BrowserManager interface {
	EnsureBrowser(ctx context.Context, channelID, containerID string) error
	StopBrowser(ctx context.Context, channelID string) error
	IsRunning(ctx context.Context, channelID string) bool
	GetCDPEndpoint(channelID string) string
	GetContainerID(channelID string) (string, bool)
	SetTargetID(channelID, targetID string)
	GetTargetID(channelID string) string
	SetCDPForTarget(channelID, targetID string, cdp any)
	GetCDPForTarget(channelID, targetID string) any
	RemoveCDPForTarget(channelID, targetID string) any
	GetActiveCDP(channelID string) any
	TouchBrowser(channelID string)
	PaneConnected(channelID string)
	PaneDisconnected(channelID string)
	RunIdleMonitor(ctx context.Context, timeout time.Duration)
	NotifyTargetSwitch(channelID, targetID string)
	TargetSwitchCh(channelID string) <-chan string
	NotifyTabAdded(channelID string, tab browser.TabInfo)
	TabAddedCh(channelID string) <-chan browser.TabInfo
	NotifyTabRemoved(channelID, targetID string)
	TabRemovedCh(channelID string) <-chan string
	TrackTab(channelID, targetID string)
	UntrackTab(channelID, targetID string)
	NextTabID(channelID, closedTargetID string) string
	OrderTabs(channelID string, tabs []browser.TabInfo) []browser.TabInfo
}

// SetBrowserManager configures the browser manager.
func (s *Server) SetBrowserManager(mgr BrowserManager) {
	s.browserManager = mgr
	s.browserCDPFactory = func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (browserCDPClient, error) {
		return browser.NewCDPClient(ctx, wsURL, logger, opts...)
	}
	s.browserCDPRetries = cdpMaxRetries
	s.browserCDPDelay = cdpRetryDelay
}

// browserCDPClient abstracts CDP operations for testing.
type browserCDPClient interface {
	Navigate(ctx context.Context, url string) error
	Reload(ctx context.Context) error
	GoBack(ctx context.Context) error
	GoForward(ctx context.Context) error
	GetPageInfo(ctx context.Context) (*browser.PageInfo, error)
	StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte
	StopScreencast()
	ResetScreencast()
	MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error
	MouseMove(ctx context.Context, x, y float64) error
	MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error
	KeyPress(ctx context.Context, key string) error
	TypeText(ctx context.Context, text string) error
	TargetID() string
	SwitchTarget(targetID string) error
	ListTabs(ctx context.Context) ([]browser.TabInfo, error)
	EvaluateJS(ctx context.Context, expression string) (string, error)
	NewTab(ctx context.Context, url string) (string, error)
	CloseTab(ctx context.Context, targetID string) error
	Close()
	GetElementRefs(ctx context.Context) ([]browser.ElementRef, error)
	ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error
	Screenshot(ctx context.Context) ([]byte, error)
	EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error
	EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error
	ResizeWindow(ctx context.Context, width, height int) error
	ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error
	MouseDown(ctx context.Context, x, y float64, button string) error
	MouseUp(ctx context.Context, x, y float64, button string) error
}

// browserWSConn manages a single browser WebSocket connection.
type browserWSConn struct {
	conn    *websocket.Conn
	bMgr    BrowserManager
	cFinder ContainerFinder
	logger  *slog.Logger
	writeMu sync.Mutex

	// Factory functions — set by handleBrowserWS, overridable in tests.
	cdpFactory func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (browserCDPClient, error)

	// CDP connection retry — defaults set in handleBrowserWS, overridable in tests.
	cdpRetries int
	cdpDelay   time.Duration

	mu               sync.Mutex
	cdp              browserCDPClient
	stopCh           chan struct{}
	screencastStopCh chan struct{} // per-screencast stop, separate from WS-level stopCh
	channelID        string        // set by handleStart, used by cleanup for PaneDisconnected
}

// browserWSMessage is a control message from the client.
type browserWSMessage struct {
	Type      string `json:"type"` // "start", "stop", "screencast", "input"
	ChannelID string `json:"channel_id,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	// Input fields
	InputType  string  `json:"input_type,omitempty"` // "click", "mousemove", "scroll", "keypress", "typetext"
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	Button     string  `json:"button,omitempty"`
	ClickCount int     `json:"click_count,omitempty"`
	DeltaX     float64 `json:"delta_x,omitempty"`
	DeltaY     float64 `json:"delta_y,omitempty"`
	Key        string  `json:"key,omitempty"`
	Text       string  `json:"text,omitempty"`
}

// browserTabInfo mirrors browser.TabInfo for WS responses.
type browserTabInfo struct {
	TargetID string `json:"target_id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

// browserWSResponse is a status message sent to the client.
type browserWSResponse struct {
	Type           string           `json:"type"` // "started", "stopped", "page_info", "error", "tabs", "tab_switched", "tab_created", "tab_closed", "tabs_updated"
	URL            string           `json:"url,omitempty"`
	Title          string           `json:"title,omitempty"`
	Message        string           `json:"message,omitempty"`
	Tabs           []browserTabInfo `json:"tabs,omitempty"`
	ActiveTargetID string           `json:"active_target_id,omitempty"`
	TargetID       string           `json:"target_id,omitempty"`
}

const (
	bwsMsgStart      = "start"
	bwsMsgStop       = "stop"
	bwsMsgScreencast = "screencast"
	bwsMsgInput      = "input"

	bwsRespStarted     = "started"
	bwsRespStopped     = "stopped"
	bwsRespError       = "error"
	bwsRespTabs        = "tabs"
	bwsRespTabSwitched = "tab_switched"
	bwsRespTabCreated  = "tab_created"
	bwsRespTabClosed   = "tab_closed"

	cdpMaxRetries = 20
	cdpRetryDelay = 500 * time.Millisecond
)

// Legacy HTTP endpoints (ensure, touch, switch-target, tab-added, tab-removed)
// removed — all browser operations now go through POST /api/browser/action
// which handles ensure, touch, switch, tab add/remove internally.

// handleBrowserWS handles the /api/ws/browser WebSocket endpoint.
func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	if s.browserManager == nil {
		http.Error(w, "browser not configured", http.StatusServiceUnavailable)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("browser ws: upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	bc := &browserWSConn{
		conn:    conn,
		bMgr:    s.browserManager,
		cFinder: s.containerFinder,
		logger:  s.logger,
		cdpFactory: func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (browserCDPClient, error) {
			return s.browserCDPFactory(ctx, wsURL, logger, opts...)
		},
		cdpRetries: s.browserCDPRetries,
		cdpDelay:   s.browserCDPDelay,
		stopCh:     make(chan struct{}),
	}
	defer bc.cleanup()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg browserWSMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			bc.sendError("invalid JSON")
			continue
		}

		// The WS handles: start/stop (lifecycle), screencast (frame streaming),
		// and input (mouse/keyboard). All control operations (navigate, tabs,
		// reload, back/forward) go through POST /api/browser/action.
		switch msg.Type {
		case bwsMsgStart:
			bc.handleStart(r.Context(), msg)
		case bwsMsgStop:
			bc.handleStop(r.Context(), msg)
		case bwsMsgScreencast:
			go bc.handleScreencast(msg)
		case bwsMsgInput:
			bc.handleInput(r.Context(), msg)
		default:
			bc.sendError("unknown message type: " + msg.Type)
		}
	}
}

func (bc *browserWSConn) handleStart(ctx context.Context, msg browserWSMessage) {
	if msg.ChannelID == "" {
		bc.sendError("channel_id required")
		return
	}

	bc.logger.Info("browser ws: starting", "channel_id", msg.ChannelID)
	bc.channelID = msg.ChannelID

	// Ensure Chrome sidecar container is running for this channel.
	if err := bc.bMgr.EnsureBrowser(ctx, msg.ChannelID, ""); err != nil {
		bc.sendError("failed to start browser: " + err.Error())
		return
	}

	bc.bMgr.PaneConnected(msg.ChannelID)

	// Reuse cached CDP client if available (survives WS reconnections).
	// Reset screencast state — the old WS cleanup left screencasting=true
	// but the actual screencast is dead (no frame consumer).
	if cached, ok := bc.bMgr.GetActiveCDP(msg.ChannelID).(browserCDPClient); ok && cached != nil {
		bc.logger.Info("browser ws: reusing cached CDP")
		cached.ResetScreencast()
		bc.mu.Lock()
		bc.cdp = cached
		bc.mu.Unlock()
		bc.sendJSON(browserWSResponse{Type: bwsRespStarted})
		go bc.watchMCPTabChanges(msg.ChannelID)
		return
	}

	// Connect CDP client — Chrome needs time to start, so retry with backoff.
	cdpEndpoint := bc.bMgr.GetCDPEndpoint(msg.ChannelID)
	bc.logger.Info("browser ws: connecting CDP", "endpoint", cdpEndpoint)
	var (
		cdpClient browserCDPClient
		err       error
	)
	// Use background context so the CDP client survives WS reconnections.
	cdpCtx := context.Background()
	for attempt := range bc.cdpRetries {
		cdpClient, err = bc.cdpFactory(cdpCtx, cdpEndpoint, bc.logger)
		if err == nil {
			break
		}
		if attempt == bc.cdpRetries-1 {
			bc.sendError("failed to connect CDP: " + err.Error())
			return
		}
		bc.logger.Debug("CDP not ready, retrying", "attempt", attempt+1, "error", err)
		select {
		case <-ctx.Done():
			bc.sendError("context cancelled waiting for CDP")
			return
		case <-time.After(bc.cdpDelay):
		}
	}

	bc.mu.Lock()
	bc.cdp = cdpClient
	bc.mu.Unlock()

	// Cache the CDP client and track all existing tabs.
	if tid := cdpClient.TargetID(); tid != "" {
		bc.bMgr.SetCDPForTarget(msg.ChannelID, tid, cdpClient)
		bc.bMgr.SetTargetID(msg.ChannelID, tid)
		bc.bMgr.TrackTab(msg.ChannelID, tid)
		bc.logger.Info("browser ws: CDP connected", "target_id", tid)
	}
	// Track all existing tabs from Chrome (e.g. from a reused container).
	// CDPs are created on-demand when switching to each tab.
	if tabs, err := cdpClient.ListTabs(ctx); err == nil {
		for _, tab := range tabs {
			bc.bMgr.TrackTab(msg.ChannelID, tab.TargetID)
		}
	}

	bc.sendJSON(browserWSResponse{Type: bwsRespStarted})

	// Watch for MCP-initiated target switches and tab changes.
	go bc.watchMCPTabChanges(msg.ChannelID)
}

func (bc *browserWSConn) handleStop(ctx context.Context, msg browserWSMessage) {
	bc.logger.Info("browser ws: stopping", "channel_id", msg.ChannelID)
	bc.cleanup()

	if msg.ChannelID != "" {
		_ = bc.bMgr.StopBrowser(ctx, msg.ChannelID)
	}

	bc.sendJSON(browserWSResponse{Type: bwsRespStopped})
}

func (bc *browserWSConn) handleInput(ctx context.Context, msg browserWSMessage) {
	ev := browser.InputEvent{
		Type:       msg.InputType,
		X:          msg.X,
		Y:          msg.Y,
		Button:     msg.Button,
		ClickCount: msg.ClickCount,
		DeltaX:     msg.DeltaX,
		DeltaY:     msg.DeltaY,
		Key:        msg.Key,
		Text:       msg.Text,
	}
	bc.dispatchInput(ev)
}

func (bc *browserWSConn) dispatchInput(ev browser.InputEvent) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		return
	}

	ctx := context.Background()
	var err error

	switch ev.Type {
	case "click":
		count := ev.ClickCount
		if count == 0 {
			count = 1
		}
		btn := ev.Button
		if btn == "" {
			btn = "left"
		}
		err = cdp.MouseClick(ctx, ev.X, ev.Y, btn, count)
	case "mousemove":
		err = cdp.MouseMove(ctx, ev.X, ev.Y)
	case "scroll":
		err = cdp.MouseScroll(ctx, ev.X, ev.Y, ev.DeltaX, ev.DeltaY)
	case "keypress":
		err = cdp.KeyPress(ctx, ev.Key)
	case "typetext":
		err = cdp.TypeText(ctx, ev.Text)
	}

	if err != nil {
		bc.logger.Error("input dispatch failed", "type", ev.Type, "error", err)
	}
}

// frameSender sends frames and signals when to stop.
type frameSender interface {
	SendFrame([]byte) error
	StopCh() <-chan struct{}
}

// handleScreencast starts screencast and pipes JPEG frames over the WebSocket
// as binary messages. This is a simpler alternative to WebRTC that works
// reliably on localhost (where WebRTC ICE often fails).
func (bc *browserWSConn) handleScreencast(msg browserWSMessage) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		bc.sendError("browser not started")
		return
	}

	w, h := msg.Width, msg.Height
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 900
	}

	newStopCh := make(chan struct{})
	bc.mu.Lock()
	bc.screencastStopCh = newStopCh
	bc.mu.Unlock()

	frameCh := cdp.StartScreencast(60, w, h)
	ws := &wsFrameSender{bc: bc, stopCh: newStopCh}
	go bc.pipeFrames(frameCh, ws, cdp.TargetID())
}

// wsFrameSender sends screencast frames as binary WebSocket messages.
// Uses the browserWSConn's writeMu to avoid concurrent writes with sendJSON.
type wsFrameSender struct {
	bc     *browserWSConn
	stopCh <-chan struct{}
}

func (w *wsFrameSender) SendFrame(frame []byte) error {
	w.bc.writeMu.Lock()
	defer w.bc.writeMu.Unlock()
	return w.bc.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (w *wsFrameSender) StopCh() <-chan struct{} {
	return w.stopCh
}

func (bc *browserWSConn) pipeFrames(frameCh <-chan []byte, stream frameSender, targetIDs ...string) {
	tid := ""
	if len(targetIDs) > 0 {
		tid = targetIDs[0]
	}
	frameCount := 0
	for {
		select {
		case frame, ok := <-frameCh:
			if !ok {
				bc.logger.Info("pipeFrames: channel closed", "frames_sent", frameCount, "target_id", tid)
				return
			}
			frameCount++
			if frameCount <= 3 || frameCount%100 == 0 {
				bc.logger.Info("pipeFrames: sending frame", "count", frameCount, "size", len(frame), "target_id", tid)
			}
			if err := stream.SendFrame(frame); err != nil {
				bc.logger.Debug("frame send failed", "error", err)
				return
			}
		case <-stream.StopCh():
			return
		case <-bc.stopCh:
			return
		}
	}
}

// restartScreencastForTarget switches the screencast to a different tab.
// Reuses a cached CDP client for the target if available, or creates a new one.
// The old target's CDP is kept alive in the manager for later reuse.
func (bc *browserWSConn) restartScreencastForTarget(ctx context.Context, _ browserCDPClient, targetID string) {
	bc.logger.Info("browser ws: switching target", "target_id", targetID)

	// Stop the old pipeFrames goroutine so frames from the previous tab
	// don't leak into the WebSocket.
	bc.mu.Lock()
	if bc.screencastStopCh != nil {
		close(bc.screencastStopCh)
	}
	newStopCh := make(chan struct{})
	bc.screencastStopCh = newStopCh
	bc.mu.Unlock()

	// Don't call StopScreencast — it sends a CDP command through chromedp's
	// serial queue which can block the WS read loop for seconds. Just closing
	// screencastStopCh stops the frame piping. The old CDP's screencast will
	// be stopped naturally when we call ResetScreencast + StartScreencast.

	// Create a fresh CDP for the target. Chrome only sends screencast
	// frames to the first CDP session that attaches. Don't Close() old
	// CDPs — Close() destroys the page target in Chrome.
	bc.bMgr.RemoveCDPForTarget(bc.channelID, targetID)
	// Activate the target in Chrome first — Chrome won't screencast
	// background tabs. Then create a fresh CDP attached to it.
	cdpEndpoint := bc.bMgr.GetCDPEndpoint(bc.channelID)
	_ = browser.ActivateTarget(cdpEndpoint, targetID)
	newCDP, err := bc.cdpFactory(context.Background(), cdpEndpoint, bc.logger,
		browser.WithTargetID(targetID))
	if err != nil {
		bc.logger.Error("browser ws: switch target failed", "error", err)
		bc.sendError("switch target failed: " + err.Error())
		return
	}
	bc.bMgr.SetCDPForTarget(bc.channelID, targetID, newCDP)

	bc.mu.Lock()
	bc.cdp = newCDP
	bc.mu.Unlock()

	bc.bMgr.SetTargetID(bc.channelID, targetID)

	newCDP.ResetScreencast()
	frameCh := newCDP.StartScreencast(60, 1920, 1080)
	ws := &wsFrameSender{bc: bc, stopCh: newStopCh}
	go bc.pipeFrames(frameCh, ws, targetID)
	_, _ = newCDP.EvaluateJS(ctx, "window.scrollBy(0,1);window.scrollBy(0,-1)")

	bc.sendJSON(browserWSResponse{Type: bwsRespTabSwitched, TargetID: targetID})

	tabs, err := newCDP.ListTabs(ctx)
	if err == nil {
		bc.sendTabsResponse(tabs, targetID)
	}
}

// sendTabsResponse sends a tabs response with the current tab list and active target.
func (bc *browserWSConn) sendTabsResponse(tabs []browser.TabInfo, activeTargetID string) {
	tabs = bc.bMgr.OrderTabs(bc.channelID, tabs)
	tabInfos := make([]browserTabInfo, len(tabs))
	for i, t := range tabs {
		tabInfos[i] = browserTabInfo{
			TargetID: t.TargetID,
			URL:      t.URL,
			Title:    t.Title,
		}
	}
	bc.sendJSON(browserWSResponse{
		Type:           bwsRespTabs,
		Tabs:           tabInfos,
		ActiveTargetID: activeTargetID,
	})
}

// watchMCPTabChanges watches for MCP-initiated target switches, tab additions,
// and tab removals, forwarding them to the frontend via WebSocket.
func (bc *browserWSConn) watchMCPTabChanges(channelID string) {
	switchCh := bc.bMgr.TargetSwitchCh(channelID)
	tabAddedCh := bc.bMgr.TabAddedCh(channelID)
	tabRemovedCh := bc.bMgr.TabRemovedCh(channelID)

	for {
		select {
		case targetID, ok := <-switchCh:
			if !ok {
				return
			}
			bc.mu.Lock()
			cdp := bc.cdp
			bc.mu.Unlock()
			if cdp == nil {
				return
			}
			bc.restartScreencastForTarget(context.Background(), cdp, targetID)
		case tab, ok := <-tabAddedCh:
			if !ok {
				return
			}
			bc.sendJSON(browserWSResponse{
				Type:     bwsRespTabCreated,
				TargetID: tab.TargetID,
				URL:      tab.URL,
				Title:    tab.Title,
			})
		case targetID, ok := <-tabRemovedCh:
			if !ok {
				return
			}
			bc.sendJSON(browserWSResponse{
				Type:     bwsRespTabClosed,
				TargetID: targetID,
			})
		case <-bc.stopCh:
			return
		}
	}
}

func (bc *browserWSConn) cleanup() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.channelID != "" {
		bc.bMgr.PaneDisconnected(bc.channelID)
	}

	if bc.cdp != nil {
		bc.logger.Info("browser ws: CDP disconnected", "channel_id", bc.channelID)
		// Don't call StopScreencast — it blocks on the chromedp queue and
		// corrupts the CDP state for reuse. Just stop piping frames by
		// closing screencastStopCh (pipeFrames exits). The CDP stays alive
		// in the browser manager cache for WS reconnect.
		if bc.screencastStopCh != nil {
			close(bc.screencastStopCh)
			bc.screencastStopCh = nil
		}
		bc.cdp = nil
	}
}

func (bc *browserWSConn) sendJSON(resp browserWSResponse) {
	bc.writeMu.Lock()
	defer bc.writeMu.Unlock()
	if err := bc.conn.WriteJSON(resp); err != nil {
		// Suppress broken pipe — client disconnected, nothing to do.
		if !strings.Contains(err.Error(), "broken pipe") && !strings.Contains(err.Error(), "use of closed") {
			bc.logger.Error("browser ws: write failed", "error", err)
		}
	}
}

func (bc *browserWSConn) sendError(msg string) {
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: msg})
}

// browserCaptureState tracks per-channel console and network capture state.
type browserCaptureState struct {
	consoleMu   sync.Mutex
	consoleMsgs []browser.ConsoleMessage
	consoleCh   chan browser.ConsoleMessage

	networkMu   sync.Mutex
	networkReqs []browser.NetworkRequest
	networkCh   chan browser.NetworkRequest

	started bool
}

// browserActionRequest is the request body for POST /api/browser/action.
type browserActionRequest struct {
	ChannelID string         `json:"channel_id"`
	Action    string         `json:"action"`
	Params    map[string]any `json:"params"`
}

// browserActionResponse is the response for POST /api/browser/action.
type browserActionResponse struct {
	Result         string               `json:"result,omitempty"`
	Image          string               `json:"image,omitempty"` // base64 PNG
	ScreenshotPath string               `json:"screenshot_path,omitempty"`
	Error          string               `json:"error,omitempty"`
	ElementRefs    []browser.ElementRef `json:"element_refs,omitempty"`
	Tabs           []browser.TabInfo    `json:"tabs,omitempty"`
	PageInfo       *browser.PageInfo    `json:"page_info,omitempty"`
}

// writeJSON writes a JSON response to w.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// paramStr extracts a string param from params map.
func paramStr(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// paramFloat extracts a float64 param from params map.
func paramFloat(params map[string]any, key string) float64 {
	if v, ok := params[key].(float64); ok {
		return v
	}
	return 0
}

// paramInt extracts an int param from params map (JSON numbers are float64).
func paramInt(params map[string]any, key string) int {
	if v, ok := params[key].(float64); ok {
		return int(v)
	}
	return 0
}

// paramBool extracts a bool param from params map.
func paramBool(params map[string]any, key string) bool {
	if v, ok := params[key].(bool); ok {
		return v
	}
	return false
}

// getBrowserCDP returns the CDPClient for a channel, creating one if needed.
func (s *Server) getBrowserCDP(ctx context.Context, channelID string) (browserCDPClient, error) {
	raw := s.browserManager.GetActiveCDP(channelID)
	if cached, ok := raw.(browserCDPClient); ok && cached != nil {
		s.logger.Info("getBrowserCDP: reusing cached CDP", "channel_id", channelID)
		s.ensureBrowserCapture(ctx, channelID, cached)
		return cached, nil
	}
	s.logger.Info("getBrowserCDP: no cached CDP, creating new", "channel_id", channelID, "raw_type", fmt.Sprintf("%T", raw))

	if err := s.browserManager.EnsureBrowser(ctx, channelID, ""); err != nil {
		return nil, fmt.Errorf("ensuring browser: %w", err)
	}
	s.logger.Info("getBrowserCDP: browser ensured", "channel_id", channelID)

	cdpEndpoint := s.browserManager.GetCDPEndpoint(channelID)
	if cdpEndpoint == "" {
		return nil, fmt.Errorf("no CDP endpoint for channel %s", channelID)
	}

	var cdpClient browserCDPClient
	var lastErr error
	for attempt := range s.browserCDPRetries {
		cdpClient, lastErr = s.browserCDPFactory(context.Background(), cdpEndpoint, s.logger)
		if lastErr == nil {
			break
		}
		if attempt == s.browserCDPRetries-1 {
			return nil, fmt.Errorf("connecting CDP after %d attempts: %w", s.browserCDPRetries, lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.browserCDPDelay):
		}
	}

	if tid := cdpClient.TargetID(); tid != "" {
		s.browserManager.SetCDPForTarget(channelID, tid, cdpClient)
		s.browserManager.SetTargetID(channelID, tid)
		s.browserManager.TrackTab(channelID, tid)
	}

	// Start capture if needed.
	s.ensureBrowserCapture(ctx, channelID, cdpClient)

	return cdpClient, nil
}

// ensureBrowserCapture initializes console/network capture for a channel if not already started.
func (s *Server) ensureBrowserCapture(ctx context.Context, channelID string, cdpCl browserCDPClient) {
	s.browserCapturesMu.Lock()
	defer s.browserCapturesMu.Unlock()

	if s.browserCaptures == nil {
		s.browserCaptures = make(map[string]*browserCaptureState)
	}

	cs, exists := s.browserCaptures[channelID]
	if exists && cs.started {
		return
	}

	cs = &browserCaptureState{started: true}
	s.browserCaptures[channelID] = cs

	cs.consoleCh = make(chan browser.ConsoleMessage, 100)
	if err := cdpCl.EnableConsoleCapture(ctx, cs.consoleCh); err != nil {
		s.logger.Warn("failed to enable console capture", "channel_id", channelID, "error", err)
	} else {
		go func() {
			for msg := range cs.consoleCh {
				cs.consoleMu.Lock()
				cs.consoleMsgs = append(cs.consoleMsgs, msg)
				cs.consoleMu.Unlock()
			}
		}()
	}

	cs.networkCh = make(chan browser.NetworkRequest, 100)
	if err := cdpCl.EnableNetworkCapture(ctx, cs.networkCh); err != nil {
		s.logger.Warn("failed to enable network capture", "channel_id", channelID, "error", err)
	} else {
		go func() {
			for req := range cs.networkCh {
				cs.networkMu.Lock()
				cs.networkReqs = append(cs.networkReqs, req)
				cs.networkMu.Unlock()
			}
		}()
	}
}

// handleBrowserAction handles POST /api/browser/action.
func (s *Server) handleBrowserAction(w http.ResponseWriter, r *http.Request) {
	if s.browserManager == nil {
		http.Error(w, "browser not configured", http.StatusServiceUnavailable)
		return
	}

	var req browserActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}

	cdpCl, err := s.getBrowserCDP(r.Context(), req.ChannelID)
	if err != nil {
		writeJSON(w, browserActionResponse{Error: err.Error()})
		return
	}

	s.browserManager.TouchBrowser(req.ChannelID)

	resp := s.dispatchBrowserAction(r.Context(), req, cdpCl)
	writeJSON(w, resp)
}

// dispatchBrowserAction dispatches a browser action to the CDP client.
func (s *Server) dispatchBrowserAction(ctx context.Context, req browserActionRequest, cdpCl browserCDPClient) browserActionResponse {
	bg := context.Background()
	params := req.Params

	switch req.Action {
	case "navigate":
		url := paramStr(params, "url")
		if err := cdpCl.Navigate(bg, url); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("navigate failed: %v", err)}
		}
		info, err := cdpCl.GetPageInfo(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("get page info failed: %v", err)}
		}
		// Notify the pane so it updates the tab bar URL/title.
		activeTarget := s.browserManager.GetTargetID(req.ChannelID)
		if activeTarget != "" {
			s.browserManager.NotifyTargetSwitch(req.ChannelID, activeTarget)
		}
		return browserActionResponse{Result: fmt.Sprintf("Navigated to %s", info.URL), PageInfo: info}

	case "reload":
		if err := cdpCl.Reload(bg); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("reload failed: %v", err)}
		}
		return browserActionResponse{Result: "Page reloaded"}

	case "go_back":
		if err := cdpCl.GoBack(bg); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("go back failed: %v", err)}
		}
		return browserActionResponse{Result: "Navigated back"}

	case "go_forward":
		if err := cdpCl.GoForward(bg); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("go forward failed: %v", err)}
		}
		return browserActionResponse{Result: "Navigated forward"}

	case "get_page_info":
		info, err := cdpCl.GetPageInfo(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("get page info failed: %v", err)}
		}
		return browserActionResponse{PageInfo: info}

	case "get_element_refs":
		refs, err := cdpCl.GetElementRefs(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("get element refs failed: %v", err)}
		}
		return browserActionResponse{ElementRefs: refs}

	case "mouse_click":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		button := paramStr(params, "button")
		if button == "" {
			button = "left"
		}
		clickCount := paramInt(params, "click_count")
		if clickCount == 0 {
			clickCount = 1
		}
		if err := cdpCl.MouseClick(bg, x, y, button, clickCount); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse click failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Clicked at (%.0f, %.0f)", x, y)}

	case "mouse_move":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		if err := cdpCl.MouseMove(bg, x, y); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse move failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Moved mouse to (%.0f, %.0f)", x, y)}

	case "mouse_scroll":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		deltaX := paramFloat(params, "delta_x")
		deltaY := paramFloat(params, "delta_y")
		if err := cdpCl.MouseScroll(bg, x, y, deltaX, deltaY); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse scroll failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Scrolled at (%.0f, %.0f) by (%.0f, %.0f)", x, y, deltaX, deltaY)}

	case "mouse_down":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		button := paramStr(params, "button")
		if button == "" {
			button = "left"
		}
		if err := cdpCl.MouseDown(bg, x, y, button); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse down failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Mouse down at (%.0f, %.0f)", x, y)}

	case "mouse_up":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		button := paramStr(params, "button")
		if button == "" {
			button = "left"
		}
		if err := cdpCl.MouseUp(bg, x, y, button); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse up failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Mouse up at (%.0f, %.0f)", x, y)}

	case "key_press":
		key := paramStr(params, "key")
		if err := cdpCl.KeyPress(bg, key); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("key press failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Pressed key %q", key)}

	case "type_text":
		text := paramStr(params, "text")
		if err := cdpCl.TypeText(bg, text); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("type text failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Typed %q", text)}

	case "click_ref":
		var refs []browser.ElementRef
		if refsRaw, ok := params["refs"].([]any); ok {
			for _, r := range refsRaw {
				if m, ok := r.(map[string]any); ok {
					data, _ := json.Marshal(m)
					var ref browser.ElementRef
					if err := json.Unmarshal(data, &ref); err == nil {
						refs = append(refs, ref)
					}
				}
			}
		}
		refIndex := paramInt(params, "ref_index")
		if err := cdpCl.ClickRef(bg, refs, refIndex); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("click ref failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Clicked ref %d", refIndex)}

	case "screenshot":
		data, err := cdpCl.Screenshot(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("screenshot failed: %v", err)}
		}
		if s.screenshotDir != "" {
			fname := fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano())
			fpath := filepath.Join(s.screenshotDir, fname)
			if err := os.WriteFile(fpath, data, 0o644); err != nil {
				return browserActionResponse{Error: fmt.Sprintf("writing screenshot file: %v", err)}
			}
			return browserActionResponse{ScreenshotPath: fpath}
		}
		return browserActionResponse{Image: base64.StdEncoding.EncodeToString(data)}

	case "evaluate_js":
		expression := paramStr(params, "expression")
		result, err := cdpCl.EvaluateJS(bg, expression)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("evaluate JS failed: %v", err)}
		}
		return browserActionResponse{Result: result}

	case "list_tabs":
		tabs, err := cdpCl.ListTabs(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("list tabs failed: %v", err)}
		}
		tabs = s.browserManager.OrderTabs(req.ChannelID, tabs)
		activeTarget := s.browserManager.GetTargetID(req.ChannelID)
		for i := range tabs {
			if tabs[i].TargetID == activeTarget {
				tabs[i].Active = true
			}
		}
		return browserActionResponse{Tabs: tabs}

	case "new_tab":
		url := paramStr(params, "url")
		if url == "" {
			url = "about:blank"
		}
		targetID, err := cdpCl.NewTab(bg, url)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("new tab failed: %v", err)}
		}
		s.browserManager.TrackTab(req.ChannelID, targetID)
		s.browserManager.NotifyTabAdded(req.ChannelID, browser.TabInfo{TargetID: targetID, URL: url})
		s.browserManager.NotifyTargetSwitch(req.ChannelID, targetID)
		return browserActionResponse{Result: fmt.Sprintf("Created new tab with target ID %s", targetID)}

	case "switch_tab":
		targetID := paramStr(params, "target_id")
		if targetID == "" {
			return browserActionResponse{Error: "target_id required"}
		}
		if err := cdpCl.SwitchTarget(targetID); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("switch tab failed: %v", err)}
		}
		s.browserManager.NotifyTargetSwitch(req.ChannelID, targetID)
		return browserActionResponse{Result: fmt.Sprintf("Switched to tab %s", targetID)}

	case "close_tab":
		targetID := paramStr(params, "target_id")
		if targetID == "" {
			return browserActionResponse{Error: "target_id required"}
		}
		s.logger.Info("dispatchBrowserAction: close_tab", "target_id", targetID, "channel_id", req.ChannelID)
		// Find the next tab BEFORE untracking (need position info).
		nextTab := s.browserManager.NextTabID(req.ChannelID, targetID)
		if err := cdpCl.CloseTab(bg, targetID); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("close tab failed: %v", err)}
		}
		s.browserManager.UntrackTab(req.ChannelID, targetID)
		s.browserManager.NotifyTabRemoved(req.ChannelID, targetID)
		if nextTab != "" {
			// Switch to the adjacent tab so the pane updates screencast.
			s.browserManager.NotifyTargetSwitch(req.ChannelID, nextTab)
		} else {
			// Last tab closed — the CDP's context is dead (target destroyed).
			// Use Chrome's HTTP endpoint to create a new about:blank tab.
			cdpEndpoint := s.browserManager.GetCDPEndpoint(req.ChannelID)
			if newID, err := browser.CreatePageTarget(cdpEndpoint); err == nil {
				s.browserManager.TrackTab(req.ChannelID, newID)
				s.browserManager.NotifyTabAdded(req.ChannelID, browser.TabInfo{TargetID: newID, URL: "about:blank"})
				s.browserManager.NotifyTargetSwitch(req.ChannelID, newID)
			}
		}
		return browserActionResponse{Result: fmt.Sprintf("Closed tab %s", targetID)}

	case "resize_window":
		width := paramInt(params, "width")
		height := paramInt(params, "height")
		if err := cdpCl.ResizeWindow(bg, width, height); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("resize window failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Resized viewport to %dx%d", width, height)}

	case "scroll_into_view":
		backendNodeID := cdp.BackendNodeID(paramInt(params, "backend_node_id"))
		if err := cdpCl.ScrollIntoView(bg, backendNodeID); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("scroll into view failed: %v", err)}
		}
		return browserActionResponse{Result: "Scrolled element into view"}

	case "read_console":
		return s.readConsoleMessages(req.ChannelID, params)

	case "read_network":
		return s.readNetworkRequests(req.ChannelID, params)

	default:
		return browserActionResponse{Error: fmt.Sprintf("unknown action: %s", req.Action)}
	}
}

// readConsoleMessages reads captured console messages with optional filtering.
func (s *Server) readConsoleMessages(channelID string, params map[string]any) browserActionResponse {
	s.browserCapturesMu.Lock()
	cs := s.browserCaptures[channelID]
	s.browserCapturesMu.Unlock()

	if cs == nil {
		return browserActionResponse{Result: "No console messages"}
	}

	pattern := paramStr(params, "pattern")
	onlyErrors := paramBool(params, "only_errors")
	clear := paramBool(params, "clear")
	limit := paramInt(params, "limit")
	if limit <= 0 {
		limit = 100
	}

	var re *regexp.Regexp
	if pattern != "" {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("invalid regex pattern: %v", err)}
		}
	}

	cs.consoleMu.Lock()
	msgs := make([]browser.ConsoleMessage, len(cs.consoleMsgs))
	copy(msgs, cs.consoleMsgs)
	if clear {
		cs.consoleMsgs = nil
	}
	cs.consoleMu.Unlock()

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

	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	if len(filtered) == 0 {
		return browserActionResponse{Result: "No console messages"}
	}

	result := fmt.Sprintf("%d console message(s):\n", len(filtered))
	for _, msg := range filtered {
		result += fmt.Sprintf("[%s] %s: %s\n", msg.Time.Format("15:04:05"), msg.Level, msg.Text)
	}
	return browserActionResponse{Result: result}
}

// readNetworkRequests reads captured network requests with optional filtering.
func (s *Server) readNetworkRequests(channelID string, params map[string]any) browserActionResponse {
	s.browserCapturesMu.Lock()
	cs := s.browserCaptures[channelID]
	s.browserCapturesMu.Unlock()

	if cs == nil {
		return browserActionResponse{Result: "No network requests"}
	}

	pattern := paramStr(params, "pattern")
	clear := paramBool(params, "clear")
	limit := paramInt(params, "limit")
	if limit <= 0 {
		limit = 50
	}

	var re *regexp.Regexp
	if pattern != "" {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("invalid regex pattern: %v", err)}
		}
	}

	cs.networkMu.Lock()
	reqs := make([]browser.NetworkRequest, len(cs.networkReqs))
	copy(reqs, cs.networkReqs)
	if clear {
		cs.networkReqs = nil
	}
	cs.networkMu.Unlock()

	var filtered []browser.NetworkRequest
	for _, req := range reqs {
		if re != nil && !re.MatchString(req.URL) {
			continue
		}
		filtered = append(filtered, req)
	}

	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	if len(filtered) == 0 {
		return browserActionResponse{Result: "No network requests"}
	}

	result := fmt.Sprintf("%d network request(s):\n", len(filtered))
	for _, req := range filtered {
		result += fmt.Sprintf("[%s] %s %s — %d %s\n", req.Time.Format("15:04:05"), req.Method, req.URL, req.Status, req.StatusText)
	}
	return browserActionResponse{Result: result}
}
