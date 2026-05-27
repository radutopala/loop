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
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/browser"
)

// BrowserProvider is the interface for managing browser lifecycle.
type BrowserProvider interface {
	EnsureBrowser(ctx context.Context, channelID, containerID string) error
	StopBrowser(ctx context.Context, channelID string) (string, error)
	IsRunning(ctx context.Context, channelID string) bool
	GetCDPEndpoint(channelID string) string
	GetContainerID(channelID string) (string, bool)
	IsHostMode() bool
}

// SetBrowserProvider configures the Docker browser provider.
func (s *Server) SetBrowserProvider(mgr BrowserProvider) {
	s.dockerBrowserProvider = mgr
}

// SetHostBrowserProvider configures the host Chrome browser provider.
func (s *Server) SetHostBrowserProvider(mgr BrowserProvider) {
	s.hostBrowserProvider = mgr
}

// activeBrowserProvider returns the BrowserProvider for the given channel based on active mode.
func (s *Server) activeBrowserProvider(channelID string) BrowserProvider {
	s.browserModeMu.Lock()
	mode := s.activeBrowserMode[channelID]
	s.browserModeMu.Unlock()

	if mode == "host" && s.hostBrowserProvider != nil {
		return s.hostBrowserProvider
	}
	return s.dockerBrowserProvider
}

// browserWSConn manages a single browser WebSocket connection.
type browserWSConn struct {
	conn            *websocket.Conn
	browserProvider BrowserProvider
	resolveProvider func(string) BrowserProvider // resolves active provider by channel ID
	logger          *slog.Logger
	writeMu         sync.Mutex

	// CDPManager resolvers — set by handleBrowserWS from Server.
	resolveCDPMgr  func(channelID, mode string, provider BrowserProvider) *browser.CDPManager
	setMode        func(channelID, mode string) // sets active browser mode
	scheduleRemove func(containerID string)     // schedules delayed container removal

	mu               sync.Mutex
	cdpMgr           *browser.CDPManager // active CDPManager
	cdp              browser.CDPSession  // active tab's CDP client
	stopCh           chan struct{}
	screencastStopCh chan struct{} // per-screencast stop, separate from WS-level stopCh
	channelID        string        // set by handleStart, used by cleanup for PaneDisconnected
}

// browserWSMessage is a control message from the client.
type browserWSMessage struct {
	Type      string `json:"type"` // "start", "stop", "screencast", "input"
	ChannelID string `json:"channel_id,omitempty"`
	Mode      string `json:"mode,omitempty"` // "docker" or "host" — optional, sets active mode on start
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

// getOrCreateCDPManager returns or creates a CDPManager for the given channel+mode.
func (s *Server) getOrCreateCDPManager(channelID, mode string, provider BrowserProvider) *browser.CDPManager {
	s.cdpManagersMu.Lock()
	defer s.cdpManagersMu.Unlock()
	key := channelID + "|" + mode
	if mgr, ok := s.cdpManagers[key]; ok {
		return mgr
	}
	if s.cdpManagers == nil {
		s.cdpManagers = make(map[string]*browser.CDPManager)
	}
	isHost := provider.IsHostMode()
	cfg := browser.CDPManagerConfig{
		DiscoverExisting: !isHost,
		MaxRetries:       cdpMaxRetries,
		RetryDelay:       cdpRetryDelay,
	}
	if isHost {
		cfg.MaxRetries = 1
	}
	wsEndpoint := provider.GetCDPEndpoint(channelID)
	mgr := browser.NewCDPManager(wsEndpoint, cfg, s.logger)
	s.cdpManagers[key] = mgr
	return mgr
}

// getActiveCDPManager returns the CDPManager for the given channel's active mode.
func (s *Server) getActiveCDPManager(channelID string) *browser.CDPManager {
	mode := s.activeMode(channelID)
	s.cdpManagersMu.Lock()
	defer s.cdpManagersMu.Unlock()
	return s.cdpManagers[channelID+"|"+mode]
}

// activeMode returns the active browser mode for a channel ("docker" or "host").
func (s *Server) activeMode(channelID string) string {
	s.browserModeMu.Lock()
	mode := s.activeBrowserMode[channelID]
	s.browserModeMu.Unlock()
	if mode == "" {
		return "docker"
	}
	return mode
}

// handleBrowserWS handles the /api/ws/browser WebSocket endpoint.
func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	if s.dockerBrowserProvider == nil && s.hostBrowserProvider == nil {
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
		conn:            conn,
		browserProvider: s.dockerBrowserProvider,
		resolveProvider: s.activeBrowserProvider,
		logger:          s.logger,
		resolveCDPMgr:   s.getOrCreateCDPManager,
		setMode: func(channelID, mode string) {
			s.browserModeMu.Lock()
			if s.activeBrowserMode == nil {
				s.activeBrowserMode = make(map[string]string)
			}
			s.activeBrowserMode[channelID] = mode
			s.browserModeMu.Unlock()
		},
		scheduleRemove: func(containerID string) {
			if containerID != "" && s.containerRegistry != nil {
				s.containerRegistry.ScheduleRemove(containerID, s.browserKeepAlive)
			}
		},
		stopCh: make(chan struct{}),
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

		switch msg.Type {
		case bwsMsgStart:
			bc.handleStart(r.Context(), msg)
		case bwsMsgStop:
			bc.handleStop(r.Context(), msg)
		case bwsMsgScreencast:
			go bc.handleScreencast(msg)
		case bwsMsgInput:
			bc.handleInput(msg)
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

	bc.logger.Info("browser ws: starting", "channel_id", msg.ChannelID, "mode", msg.Mode)
	bc.channelID = msg.ChannelID

	// If the client specifies a mode, set it before resolving the provider.
	// This handles reconnects after daemon restart where the frontend
	// remembers the mode but the server lost its in-memory state.
	if msg.Mode == "host" || msg.Mode == "docker" {
		if bc.setMode != nil {
			bc.setMode(msg.ChannelID, msg.Mode)
		}
	}

	// Resolve the active browser provider for this channel.
	if bc.resolveProvider != nil {
		bc.browserProvider = bc.resolveProvider(msg.ChannelID)
	}
	isHost := bc.browserProvider.IsHostMode()
	bc.logger.Info("browser ws: resolved provider", "channel_id", msg.ChannelID, "host_mode", isHost)

	// Ensure browser is running for this channel.
	if err := bc.browserProvider.EnsureBrowser(ctx, msg.ChannelID, ""); err != nil {
		bc.sendError("failed to start browser: " + err.Error())
		return
	}

	// Determine active mode.
	mode := "docker"
	if isHost {
		mode = "host"
	}

	// Get or create CDPManager for this channel+mode.
	cdpMgr := bc.resolveCDPMgr(msg.ChannelID, mode, bc.browserProvider)

	// Reuse cached CDP client if available (survives WS reconnections).
	if cdpMgr.IsConnected() {
		if activeClient := cdpMgr.ActiveClient(); activeClient != nil {
			bc.logger.Info("browser ws: reusing cached CDP")
			activeClient.ResetScreencast()
			cdpMgr.PaneConnected()
			bc.mu.Lock()
			bc.cdpMgr = cdpMgr
			bc.cdp = activeClient
			bc.mu.Unlock()
			bc.sendJSON(browserWSResponse{Type: bwsRespStarted})
			go bc.watchMCPTabChanges()
			return
		}
	}

	// Mark pane connected BEFORE Connect so the idle monitor doesn't kill
	// the container while we're still connecting (Connect retries take ~10s).
	cdpMgr.PaneConnected()

	// Connect CDP — CDPManager handles retries internally.
	bc.logger.Info("browser ws: connecting CDP", "endpoint", bc.browserProvider.GetCDPEndpoint(msg.ChannelID))
	if err := cdpMgr.Connect(ctx); err != nil {
		cdpMgr.PaneDisconnected()
		bc.sendError("failed to connect CDP: " + err.Error())
		return
	}

	cdpClient := cdpMgr.ActiveClient()

	bc.mu.Lock()
	bc.cdpMgr = cdpMgr
	bc.cdp = cdpClient
	bc.mu.Unlock()

	if cdpClient != nil && cdpClient.TargetID() != "" {
		tid := cdpClient.TargetID()
		bc.logger.Info("browser ws: CDP connected", "target_id", tid)
		// Activate the tab so Chrome brings it to foreground (screencast needs this).
		_ = cdpClient.SwitchTarget(tid)
	}

	bc.sendJSON(browserWSResponse{Type: bwsRespStarted})

	// Watch for MCP-initiated target switches and tab changes.
	go bc.watchMCPTabChanges()
}

func (bc *browserWSConn) handleStop(ctx context.Context, msg browserWSMessage) {
	bc.logger.Info("browser ws: stopping", "channel_id", msg.ChannelID)
	bc.cleanup()

	if msg.ChannelID != "" {
		containerID, _ := bc.browserProvider.StopBrowser(ctx, msg.ChannelID)
		if bc.scheduleRemove != nil {
			bc.scheduleRemove(containerID)
		}
	}

	bc.sendJSON(browserWSResponse{Type: bwsRespStopped})
}

func (bc *browserWSConn) handleInput(msg browserWSMessage) {
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
		err = cdp.MouseMove(ctx, ev.X, ev.Y, 0)
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

// handleScreencast starts screencast and pipes JPEG frames over the WebSocket.
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
func (bc *browserWSConn) restartScreencastForTarget(ctx context.Context, _ browser.CDPSession, targetID string) {
	bc.logger.Info("browser ws: switching target", "target_id", targetID)

	// Stop the old pipeFrames goroutine.
	bc.mu.Lock()
	if bc.screencastStopCh != nil {
		close(bc.screencastStopCh)
	}
	newStopCh := make(chan struct{})
	bc.screencastStopCh = newStopCh
	cdpMgr := bc.cdpMgr
	bc.mu.Unlock()

	if cdpMgr == nil {
		bc.logger.Error("browser ws: no CDPManager for target switch")
		return
	}

	// Get or create a CDP client for the target.
	client, err := cdpMgr.GetOrCreate(targetID)
	if err != nil {
		bc.logger.Error("browser ws: switch target failed", "error", err)
		bc.sendError("switch target failed: " + err.Error())
		return
	}

	activeCDP := client

	_ = activeCDP.SwitchTarget(targetID)
	cdpMgr.SwitchActive(targetID)

	bc.mu.Lock()
	bc.cdp = activeCDP
	bc.mu.Unlock()

	activeCDP.ResetScreencast()
	frameCh := activeCDP.StartScreencast(60, 1920, 1080)
	ws := &wsFrameSender{bc: bc, stopCh: newStopCh}
	go bc.pipeFrames(frameCh, ws, targetID)
	_, _ = activeCDP.EvaluateJS(ctx, "window.scrollBy(0,1);window.scrollBy(0,-1)")

	bc.sendJSON(browserWSResponse{Type: bwsRespTabSwitched, TargetID: targetID})

	tabs, err := activeCDP.ListTabs(ctx)
	if err == nil {
		bc.sendTabsResponse(tabs, targetID)
	}
}

// sendTabsResponse sends a tabs response with the current tab list and active target.
func (bc *browserWSConn) sendTabsResponse(tabs []browser.TabInfo, activeTargetID string) {
	bc.mu.Lock()
	cdpMgr := bc.cdpMgr
	bc.mu.Unlock()

	// Filter to agent-tracked tabs only (hides Chrome's startup tabs
	// and other untracked targets like extensions).
	if cdpMgr != nil {
		var filtered []browser.TabInfo
		for _, t := range tabs {
			if cdpMgr.IsTrackedTab(t.TargetID) {
				filtered = append(filtered, t)
			}
		}
		tabs = filtered
	}
	if cdpMgr != nil {
		tabs = cdpMgr.OrderTabs(tabs)
	}
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
func (bc *browserWSConn) watchMCPTabChanges() {
	bc.mu.Lock()
	cdpMgr := bc.cdpMgr
	bc.mu.Unlock()

	var switchCh <-chan string
	var tabAddedCh <-chan browser.TabInfo
	var tabRemovedCh <-chan string

	if cdpMgr != nil {
		switchCh = cdpMgr.TargetSwitchCh()
		tabAddedCh = cdpMgr.TabAddedCh()
		tabRemovedCh = cdpMgr.TabRemovedCh()
	}

	for {
		select {
		case targetID := <-switchCh:
			bc.mu.Lock()
			cdp := bc.cdp
			bc.mu.Unlock()
			if cdp == nil {
				return
			}
			// Skip if already on this target.
			if cdp.TargetID() == targetID {
				bc.logger.Debug("watchMCPTabChanges: skipping switch to same target", "target_id", targetID)
				continue
			}
			bc.restartScreencastForTarget(context.Background(), cdp, targetID)
		case tab := <-tabAddedCh:
			bc.sendJSON(browserWSResponse{
				Type:     bwsRespTabCreated,
				TargetID: tab.TargetID,
				URL:      tab.URL,
				Title:    tab.Title,
			})
		case targetID := <-tabRemovedCh:
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

	if bc.cdpMgr != nil {
		bc.cdpMgr.PaneDisconnected()
	}

	if bc.cdp != nil {
		bc.logger.Info("browser ws: CDP disconnected", "channel_id", bc.channelID)
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
		bc.logger.Debug("browser ws: write failed", "error", err)
	}
}

func (bc *browserWSConn) sendError(msg string) {
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: msg})
}

// handleBrowserMode handles POST /api/browser/mode — switches between docker and host Chrome.
func (s *Server) handleBrowserMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelID string `json:"channel_id"`
		Mode      string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}
	if body.Mode != "docker" && body.Mode != "host" {
		http.Error(w, `mode must be "docker" or "host"`, http.StatusBadRequest)
		return
	}
	if body.Mode == "host" && s.hostBrowserProvider == nil {
		http.Error(w, "host browser not configured", http.StatusServiceUnavailable)
		return
	}

	s.browserModeMu.Lock()
	oldMode := s.activeBrowserMode[body.ChannelID]
	s.browserModeMu.Unlock()

	s.logger.Info("browser mode: switching",
		"channel_id", body.ChannelID,
		"from", oldMode,
		"to", body.Mode,
	)

	// Clear capture state.
	s.browserCapturesMu.Lock()
	delete(s.browserCaptures, body.ChannelID)
	s.browserCapturesMu.Unlock()

	// Set new mode.
	s.browserModeMu.Lock()
	if s.activeBrowserMode == nil {
		s.activeBrowserMode = make(map[string]string)
	}
	s.activeBrowserMode[body.ChannelID] = body.Mode
	s.browserModeMu.Unlock()

	s.logger.Info("browser mode: switched",
		"channel_id", body.ChannelID,
		"mode", body.Mode,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct { //nolint:errcheck
		Mode string `json:"mode"`
	}{Mode: body.Mode})
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
func (s *Server) getBrowserCDP(ctx context.Context, channelID string) (browser.CDPSession, error) {
	// Check if there's an existing CDPManager with an active client.
	cdpMgr := s.getActiveCDPManager(channelID)
	if cdpMgr != nil {
		if activeClient := cdpMgr.ActiveClient(); activeClient != nil {
			s.logger.Info("getBrowserCDP: reusing cached CDP", "channel_id", channelID)
			s.ensureBrowserCapture(ctx, channelID, activeClient)
			return activeClient, nil
		}
	}

	// No cached client — ensure browser and create CDPManager.
	provider := s.activeBrowserProvider(channelID)
	isHost := provider.IsHostMode()
	s.logger.Info("getBrowserCDP: no cached CDP, creating new", "channel_id", channelID, "host_mode", isHost)

	if err := provider.EnsureBrowser(ctx, channelID, ""); err != nil {
		return nil, fmt.Errorf("ensuring browser: %w", err)
	}
	s.logger.Info("getBrowserCDP: browser ensured", "channel_id", channelID)

	cdpEndpoint := provider.GetCDPEndpoint(channelID)
	if cdpEndpoint == "" {
		return nil, fmt.Errorf("no CDP endpoint for channel %s", channelID)
	}

	mode := s.activeMode(channelID)
	cdpMgr = s.getOrCreateCDPManager(channelID, mode, provider)

	if err := cdpMgr.Connect(ctx); err != nil {
		return nil, err
	}

	// Connect always sets activeClient on success, so this is safe.
	cdpClient := cdpMgr.ActiveClient()

	// Start capture if needed.
	s.ensureBrowserCapture(ctx, channelID, cdpClient)

	return cdpClient, nil
}

// ensureBrowserCapture initializes console/network capture for a channel if not already started.
// If capture is already active, rewires to the current client (e.g. after a tab switch).
func (s *Server) ensureBrowserCapture(ctx context.Context, channelID string, cdpCl browser.CDPSession) {
	s.browserCapturesMu.Lock()
	defer s.browserCapturesMu.Unlock()

	if s.browserCaptures == nil {
		s.browserCaptures = make(map[string]*browser.CaptureState)
	}

	cs, exists := s.browserCaptures[channelID]
	if exists && cs.Started {
		// Rewire capture if the active client has changed (e.g. tab switch).
		// No-op if client is the same.
		cs.Rewire(ctx, cdpCl)
		return
	}

	cs = &browser.CaptureState{}
	s.browserCaptures[channelID] = cs
	cs.Enable(ctx, cdpCl)
}

// handleBrowserAction handles POST /api/browser/action.
func (s *Server) handleBrowserAction(w http.ResponseWriter, r *http.Request) {
	if s.dockerBrowserProvider == nil && s.hostBrowserProvider == nil {
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

	// Touch the CDPManager to update lastUsedAt.
	if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
		cdpMgr.Touch()
	}

	resp := s.dispatchBrowserAction(req, cdpCl)
	writeJSON(w, resp)
}

// dispatchBrowserAction dispatches a browser action to the CDP client.
// Uses a detached context.Background() so the browser action survives
// the HTTP request's lifecycle (long CDP calls shouldn't abort if the
// caller navigates away).
func (s *Server) dispatchBrowserAction(req browserActionRequest, cdpCl browser.CDPSession) browserActionResponse {
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
		if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
			activeTarget := cdpMgr.ActiveTargetID()
			if activeTarget != "" {
				cdpMgr.NotifyTargetSwitch(activeTarget)
			}
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
		if err := cdpCl.MouseMove(bg, x, y, 0); err != nil {
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
		cdpMgr := s.getActiveCDPManager(req.ChannelID)
		tabs, err := cdpCl.ListTabs(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("list tabs failed: %v", err)}
		}
		// Filter to agent-tracked tabs only.
		if cdpMgr != nil {
			var filtered []browser.TabInfo
			for _, t := range tabs {
				if cdpMgr.IsTrackedTab(t.TargetID) {
					filtered = append(filtered, t)
				}
			}
			tabs = filtered
		}
		if cdpMgr != nil {
			tabs = cdpMgr.OrderTabs(tabs)
		}
		activeTarget := ""
		if cdpMgr != nil {
			activeTarget = cdpMgr.ActiveTargetID()
		}
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
		if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
			cdpMgr.TrackTab(targetID)
			cdpMgr.NotifyTabAdded(browser.TabInfo{TargetID: targetID, URL: url})
			cdpMgr.NotifyTargetSwitch(targetID)
		}
		return browserActionResponse{Result: fmt.Sprintf("Created new tab with target ID %s", targetID)}

	case "switch_tab":
		targetID := paramStr(params, "target_id")
		if targetID == "" {
			return browserActionResponse{Error: "target_id required"}
		}
		if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
			cdpMgr.SwitchActive(targetID)
			cdpMgr.NotifyTargetSwitch(targetID)
		}
		return browserActionResponse{Result: fmt.Sprintf("Switched to tab %s", targetID)}

	case "close_tab":
		targetID := paramStr(params, "target_id")
		if targetID == "" {
			return browserActionResponse{Error: "target_id required"}
		}
		s.logger.Info("dispatchBrowserAction: close_tab", "target_id", targetID, "channel_id", req.ChannelID)
		cdpMgr := s.getActiveCDPManager(req.ChannelID)
		nextTab := ""
		if cdpMgr != nil {
			nextTab = cdpMgr.NextTabID(targetID)
		}
		// When closing the last tab, create a replacement about:blank BEFORE
		// closing — the active CDP client's context dies with the closed tab,
		// so NewTab would fail after CloseTab.
		isLastTab := cdpMgr != nil && nextTab == ""
		if isLastTab {
			if newID, err := cdpCl.NewTab(bg, "about:blank"); err == nil {
				cdpMgr.TrackTab(newID)
				nextTab = newID
				s.logger.Info("dispatchBrowserAction: created replacement tab", "new_target_id", newID)
			}
		}
		if err := cdpCl.CloseTab(bg, targetID); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("close tab failed: %v", err)}
		}
		if cdpMgr != nil {
			cdpMgr.UntrackTab(targetID)
			cdpMgr.NotifyTabRemoved(targetID)
			if isLastTab && nextTab != "" {
				cdpMgr.NotifyTabAdded(browser.TabInfo{TargetID: nextTab, URL: "about:blank"})
			}
			if nextTab != "" {
				cdpMgr.NotifyTargetSwitch(nextTab)
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

	result, err := cs.ReadConsole(paramStr(params, "pattern"), paramBool(params, "only_errors"), paramInt(params, "limit"), paramBool(params, "clear"))
	if err != nil {
		return browserActionResponse{Error: err.Error()}
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

	result, err := cs.ReadNetwork(paramStr(params, "pattern"), paramInt(params, "limit"), paramBool(params, "clear"))
	if err != nil {
		return browserActionResponse{Error: err.Error()}
	}
	return browserActionResponse{Result: result}
}

// RunBrowserIdleMonitor periodically checks for idle browser sessions and stops them.
func (s *Server) RunBrowserIdleMonitor(ctx context.Context, timeout time.Duration) {
	s.runBrowserIdleMonitorWithInterval(ctx, timeout, time.Minute)
}

func (s *Server) runBrowserIdleMonitorWithInterval(ctx context.Context, timeout, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanIdleBrowserSessions(ctx, timeout)
		}
	}
}

// cleanIdleBrowserSessions collects idle CDPManagers and cleans them up.
func (s *Server) cleanIdleBrowserSessions(ctx context.Context, timeout time.Duration) {
	s.cdpManagersMu.Lock()
	now := time.Now()
	var idle []string
	for key, mgr := range s.cdpManagers {
		if mgr.PaneCount() == 0 && now.Sub(mgr.LastUsedAt()) > timeout {
			idle = append(idle, key)
		}
	}
	s.cdpManagersMu.Unlock()

	for _, key := range idle {
		parts := strings.SplitN(key, "|", 2)
		channelID, mode := parts[0], parts[1]

		s.cdpManagersMu.Lock()
		mgr := s.cdpManagers[key]
		delete(s.cdpManagers, key)
		s.cdpManagersMu.Unlock()

		if mgr != nil {
			mgr.Close()
		}
		if mode == "docker" && s.dockerBrowserProvider != nil {
			containerID, _ := s.dockerBrowserProvider.StopBrowser(ctx, channelID)
			if containerID != "" && s.containerRegistry != nil {
				s.containerRegistry.ScheduleRemove(containerID, s.browserKeepAlive)
			}
		}
	}
}
