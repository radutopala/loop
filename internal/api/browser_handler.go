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

// BrowserManager is the interface for managing browser lifecycle.
type BrowserManager interface {
	EnsureBrowser(ctx context.Context, channelID, containerID string) error
	StopBrowser(ctx context.Context, channelID string) error
	IsRunning(ctx context.Context, channelID string) bool
	GetCDPEndpoint(channelID string) string
	GetContainerID(channelID string) (string, bool)
	SetTargetID(channelID, targetID string)
	GetTargetID(channelID string) string
	SetCDP(channelID string, cdp any)
	GetCDP(channelID string) any
	TouchBrowser(channelID string)
	PaneConnected(channelID string)
	PaneDisconnected(channelID string)
	RunIdleMonitor(ctx context.Context, timeout time.Duration)
}

// SetBrowserManager configures the browser manager.
func (s *Server) SetBrowserManager(mgr BrowserManager) {
	s.browserManager = mgr
	s.browserCDPFactory = func(ctx context.Context, wsURL string, logger *slog.Logger) (browserCDPClient, error) {
		return browser.NewCDPClient(ctx, wsURL, logger)
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
	MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error
	MouseMove(ctx context.Context, x, y float64) error
	MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error
	KeyPress(ctx context.Context, key string) error
	TypeText(ctx context.Context, text string) error
	TargetID() string
	Close()
}

// browserWSConn manages a single browser WebSocket connection.
type browserWSConn struct {
	conn    *websocket.Conn
	bMgr    BrowserManager
	cFinder ContainerFinder
	logger  *slog.Logger
	writeMu sync.Mutex

	// Factory functions — set by handleBrowserWS, overridable in tests.
	cdpFactory func(ctx context.Context, wsURL string, logger *slog.Logger) (browserCDPClient, error)

	// CDP connection retry — defaults set in handleBrowserWS, overridable in tests.
	cdpRetries int
	cdpDelay   time.Duration

	mu        sync.Mutex
	cdp       browserCDPClient
	stopCh    chan struct{}
	channelID string // set by handleStart, used by cleanup for PaneDisconnected
}

// browserWSMessage is a control message from the client.
type browserWSMessage struct {
	Type      string `json:"type"` // "start", "stop", "navigate", "screencast", "input", "page_info"
	ChannelID string `json:"channel_id,omitempty"`
	URL       string `json:"url,omitempty"`
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

// browserWSResponse is a status message sent to the client.
type browserWSResponse struct {
	Type    string `json:"type"` // "started", "stopped", "page_info", "error"
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

const (
	bwsMsgStart      = "start"
	bwsMsgStop       = "stop"
	bwsMsgNavigate   = "navigate"
	bwsMsgScreencast = "screencast"
	bwsMsgInput      = "input"
	bwsMsgPageInfo   = "page_info"
	bwsMsgReload     = "reload"
	bwsMsgBack       = "back"
	bwsMsgForward    = "forward"

	bwsRespStarted  = "started"
	bwsRespStopped  = "stopped"
	bwsRespPageInfo = "page_info"
	bwsRespError    = "error"

	cdpMaxRetries = 20
	cdpRetryDelay = 500 * time.Millisecond
)

// handleTouchBrowser handles POST /api/browser/touch — called by the
// mcp-browser MCP server to signal ongoing browser usage (prevents idle shutdown).
func (s *Server) handleTouchBrowser(w http.ResponseWriter, r *http.Request) {
	if s.browserManager == nil {
		http.Error(w, "browser not configured", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}

	s.browserManager.TouchBrowser(body.ChannelID)
	w.WriteHeader(http.StatusOK)
}

// handleEnsureBrowser handles POST /api/browser/ensure — called by the
// mcp-browser MCP server inside agent containers to lazily start Chrome.
func (s *Server) handleEnsureBrowser(w http.ResponseWriter, r *http.Request) {
	if s.browserManager == nil {
		http.Error(w, "browser not configured", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}

	if err := s.browserManager.EnsureBrowser(r.Context(), body.ChannelID, ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

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
		conn:       conn,
		bMgr:       s.browserManager,
		cFinder:    s.containerFinder,
		logger:     s.logger,
		cdpFactory: s.browserCDPFactory,
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

		switch msg.Type {
		case bwsMsgStart:
			bc.handleStart(r.Context(), msg)
		case bwsMsgStop:
			bc.handleStop(r.Context(), msg)
		case bwsMsgNavigate:
			bc.handleNavigate(r.Context(), msg)
		case bwsMsgScreencast:
			bc.handleScreencast(msg)
		case bwsMsgInput:
			bc.handleInput(r.Context(), msg)
		case bwsMsgPageInfo:
			bc.handlePageInfo(r.Context())
		case bwsMsgReload:
			bc.handleReload(r.Context())
		case bwsMsgBack:
			bc.handleBack(r.Context())
		case bwsMsgForward:
			bc.handleForward(r.Context())
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
	if cached, ok := bc.bMgr.GetCDP(msg.ChannelID).(browserCDPClient); ok && cached != nil {
		bc.logger.Info("browser ws: reusing cached CDP")
		bc.mu.Lock()
		bc.cdp = cached
		bc.mu.Unlock()
		bc.sendJSON(browserWSResponse{Type: bwsRespStarted})
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

	// Cache the CDP client and target ID in the browser manager.
	bc.bMgr.SetCDP(msg.ChannelID, cdpClient)
	if tid := cdpClient.TargetID(); tid != "" {
		bc.bMgr.SetTargetID(msg.ChannelID, tid)
		bc.logger.Info("browser ws: CDP connected", "target_id", tid)
	}

	bc.sendJSON(browserWSResponse{Type: bwsRespStarted})
}

func (bc *browserWSConn) handleStop(ctx context.Context, msg browserWSMessage) {
	bc.logger.Info("browser ws: stopping", "channel_id", msg.ChannelID)
	bc.cleanup()

	if msg.ChannelID != "" {
		_ = bc.bMgr.StopBrowser(ctx, msg.ChannelID)
	}

	bc.sendJSON(browserWSResponse{Type: bwsRespStopped})
}

func (bc *browserWSConn) handleNavigate(ctx context.Context, msg browserWSMessage) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		bc.sendError("browser not started")
		return
	}

	if err := cdp.Navigate(ctx, msg.URL); err != nil {
		bc.sendError("navigate failed: " + err.Error())
		return
	}

	// Send back page info after navigation.
	bc.handlePageInfo(ctx)
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

func (bc *browserWSConn) handlePageInfo(ctx context.Context) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		bc.sendError("browser not started")
		return
	}

	info, err := cdp.GetPageInfo(ctx)
	if err != nil {
		bc.sendError("page info failed: " + err.Error())
		return
	}

	bc.sendJSON(browserWSResponse{
		Type:  bwsRespPageInfo,
		URL:   info.URL,
		Title: info.Title,
	})
}

func (bc *browserWSConn) handleReload(ctx context.Context) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		bc.sendError("browser not started")
		return
	}

	if err := cdp.Reload(ctx); err != nil {
		bc.sendError("reload failed: " + err.Error())
	}
}

func (bc *browserWSConn) handleBack(ctx context.Context) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		bc.sendError("browser not started")
		return
	}

	// GoBack may return "no history" on timeout even when navigation succeeded.
	// Don't report it as an error to the client.
	_ = cdp.GoBack(ctx)
}

func (bc *browserWSConn) handleForward(ctx context.Context) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		bc.sendError("browser not started")
		return
	}

	_ = cdp.GoForward(ctx)
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

	frameCh := cdp.StartScreencast(60, w, h)
	ws := &wsFrameSender{bc: bc, stopCh: bc.stopCh}
	go bc.pipeFrames(frameCh, ws)
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

func (bc *browserWSConn) pipeFrames(frameCh <-chan []byte, stream frameSender) {
	for {
		select {
		case frame, ok := <-frameCh:
			if !ok {
				return
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

func (bc *browserWSConn) cleanup() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.channelID != "" {
		bc.bMgr.PaneDisconnected(bc.channelID)
	}

	if bc.cdp != nil {
		bc.logger.Info("browser ws: CDP disconnected", "channel_id", bc.channelID)
		// Stop the screencast but don't close the CDP client — it's cached
		// in the browser manager and will be reused on WS reconnect.
		bc.cdp.StopScreencast()
		bc.cdp = nil
	}
}

func (bc *browserWSConn) sendJSON(resp browserWSResponse) {
	bc.writeMu.Lock()
	defer bc.writeMu.Unlock()
	if err := bc.conn.WriteJSON(resp); err != nil {
		bc.logger.Error("browser ws: write failed", "error", err)
	}
}

func (bc *browserWSConn) sendError(msg string) {
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: msg})
}
