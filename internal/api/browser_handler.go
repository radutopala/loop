package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/browser"
)

// browserWSConn manages a single browser WebSocket connection.
type browserWSConn struct {
	conn            *websocket.Conn
	browserProvider BrowserProvider
	resolveProvider func(string) BrowserProvider // resolves active provider by channel ID
	logger          *slog.Logger
	writeMu         sync.Mutex

	// CDPManager resolvers — set by handleBrowserWS from browserService.
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

// handleBrowserWS handles the /api/ws/browser WebSocket endpoint.
func (s *browserService) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	if s.dockerProvider == nil && s.hostProvider == nil {
		http.Error(w, "browser not configured", http.StatusServiceUnavailable)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.deps.logger.Error("browser ws: upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	bc := &browserWSConn{
		conn:            conn,
		browserProvider: s.dockerProvider,
		resolveProvider: s.activeBrowserProvider,
		logger:          s.deps.logger,
		resolveCDPMgr:   s.getOrCreateCDPManager,
		setMode: func(channelID, mode string) {
			s.modeMu.Lock()
			s.activeMode[channelID] = mode
			s.modeMu.Unlock()
		},
		scheduleRemove: func(containerID string) {
			if containerID != "" && s.containerRegistry != nil {
				s.containerRegistry.ScheduleRemove(containerID, s.keepAlive)
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
func (s *browserService) handleBrowserMode(w http.ResponseWriter, r *http.Request) {
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
	if body.Mode == "host" && s.hostProvider == nil {
		http.Error(w, "host browser not configured", http.StatusServiceUnavailable)
		return
	}

	s.modeMu.Lock()
	oldMode := s.activeMode[body.ChannelID]
	s.modeMu.Unlock()

	s.deps.logger.Info("browser mode: switching",
		"channel_id", body.ChannelID,
		"from", oldMode,
		"to", body.Mode,
	)

	// Clear capture state.
	s.capturesMu.Lock()
	delete(s.captures, body.ChannelID)
	s.capturesMu.Unlock()

	// Set new mode.
	s.modeMu.Lock()
	s.activeMode[body.ChannelID] = body.Mode
	s.modeMu.Unlock()

	s.deps.logger.Info("browser mode: switched",
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

// handleBrowserAction handles POST /api/browser/action.
func (s *browserService) handleBrowserAction(w http.ResponseWriter, r *http.Request) {
	if s.dockerProvider == nil && s.hostProvider == nil {
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
