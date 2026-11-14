package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	resolveProvider func(string) BrowserProvider       // resolves active provider by channel ID
	browserEnabled  func(context.Context, string) bool // per-project browser.enabled
	logger          *slog.Logger
	writeMu         sync.Mutex

	// CDPManager resolvers — set by handleBrowserWS from browserService.
	resolveCDPMgr  func(channelID, mode string, provider BrowserProvider) *browser.CDPManager
	setMode        func(channelID, mode string) // sets active browser mode
	scheduleRemove func(containerID string)     // schedules delayed container removal
	// closeCDPMgr drops the channel's cached CDPManager. Stopping the sidecar
	// invalidates the manager: its client's context dies with the container and
	// its endpoint points at a host port that will not come back.
	closeCDPMgr func(channelID string)
	// listDownloads lists the channel's downloads dir for the pane.
	listDownloads func(channelID string) (string, []browserDownloadFile, error)

	// inputQ decouples pointer input from the read loop and collapses bursts;
	// see inputQueue for why dispatching inline made the pane lag.
	inputQ *inputQueue

	mu               sync.Mutex
	cdpMgr           *browser.CDPManager // active CDPManager
	cdp              browser.CDPSession  // active tab's CDP client
	stopCh           chan struct{}
	screencastStopCh chan struct{} // per-screencast stop, separate from WS-level stopCh
	// watchStopCh ends the tab watcher of the current start. A pane can stop
	// and start again on one socket; without it each start left another
	// watcher running, and two of them restarted the screencast at once.
	watchStopCh chan struct{}
	channelID   string // set by handleStart, used by cleanup for PaneDisconnected
}

// browserWSMessage is a control message from the client.
type browserWSMessage struct {
	Type      string `json:"type"` // "start", "stop", "screencast", "input"
	ChannelID string `json:"channel_id,omitempty"`
	Mode      string `json:"mode,omitempty"` // "docker" or "host" — optional, sets active mode on start
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	// Input fields
	InputType  string  `json:"input_type,omitempty"` // "click", "contextmenu", "mousemove", "scroll", "keypress", "typetext", "paste", "copy"
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	Button     string  `json:"button,omitempty"`
	ClickCount int     `json:"click_count,omitempty"`
	DeltaX     float64 `json:"delta_x,omitempty"`
	DeltaY     float64 `json:"delta_y,omitempty"`
	Key        string  `json:"key,omitempty"`
	Text       string  `json:"text,omitempty"`
	Modifiers  int     `json:"modifiers,omitempty"`
	// Index is the option a "select_choose" input picks.
	Index int `json:"index,omitempty"`
	// Dialog answer fields
	TargetID string `json:"target_id,omitempty"`
	Accept   bool   `json:"accept,omitempty"`
}

// browserTabInfo mirrors browser.TabInfo for WS responses.
type browserTabInfo struct {
	TargetID   string `json:"target_id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	FaviconURL string `json:"favicon_url,omitempty"`
}

// browserWSResponse is a status message sent to the client.
type browserWSResponse struct {
	Type           string              `json:"type"` // "started", "stopped", "page_info", "error", "tabs", "tab_switched", "tab_created", "tab_closed", "clipboard", "context_menu", "dialog", "dialog_closed", "select_menu", "download", "tab_updated"
	URL            string              `json:"url,omitempty"`
	Title          string              `json:"title,omitempty"`
	Message        string              `json:"message,omitempty"`
	Tabs           []browserTabInfo    `json:"tabs,omitempty"`
	ActiveTargetID string              `json:"active_target_id,omitempty"`
	TargetID       string              `json:"target_id,omitempty"`
	Text           string              `json:"text,omitempty"`
	Menu           *browserContextMenu `json:"menu,omitempty"`
	Dialog         *browser.Dialog     `json:"dialog,omitempty"`
	Select         *browserSelectMenu  `json:"select,omitempty"`
	Download       *browser.Download   `json:"download,omitempty"`
	// DownloadsDir and Files answer list_downloads.
	DownloadsDir string                `json:"downloads_dir,omitempty"`
	Files        []browserDownloadFile `json:"files,omitempty"`
}

// browserSelectMenu lists the options of the <select> a click landed on.
// Headless Chrome opens its dropdown in a popup the screencast never shows, so
// the pane draws the options and sends back the one picked.
type browserSelectMenu struct {
	Selected int                   `json:"selected"`
	Options  []browserSelectOption `json:"options"`
}

type browserSelectOption struct {
	Label    string `json:"label"`
	Disabled bool   `json:"disabled,omitempty"`
	Group    string `json:"group,omitempty"`
}

// browserContextMenu describes what a right-click landed on, so the pane can
// draw the menu headless Chrome never shows.
type browserContextMenu struct {
	Link      string `json:"link,omitempty"`
	Image     string `json:"image,omitempty"`
	Selection string `json:"selection,omitempty"`
	Editable  bool   `json:"editable,omitempty"`
}

const (
	bwsMsgStart      = "start"
	bwsMsgStop       = "stop"
	bwsMsgScreencast = "screencast"
	bwsMsgInput      = "input"
	bwsMsgDialog     = "dialog_answer"
	bwsMsgDownloads  = "list_downloads"

	bwsInputPaste       = "paste"
	bwsInputCopy        = "copy"
	bwsInputContextMenu = "contextmenu"
	bwsInputSelect      = "select_choose"

	bwsRespClipboard   = "clipboard"
	bwsRespContextMenu = "context_menu"
	bwsRespSelectMenu  = "select_menu"
	bwsRespStarted     = "started"
	bwsRespStopped     = "stopped"
	bwsRespError       = "error"
	bwsRespTabs        = "tabs"
	bwsRespTabSwitched = "tab_switched"
	bwsRespTabCreated  = "tab_created"
	bwsRespTabClosed   = "tab_closed"
	bwsRespDialog      = "dialog"
	bwsRespDialogDone  = "dialog_closed"
	bwsRespDownload    = "download"
	bwsRespDownloads   = "downloads"
	bwsRespTabUpdated  = "tab_updated"

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
		browserEnabled:  s.browserEnabledFor,
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
		closeCDPMgr: func(channelID string) {
			s.closeCDPManager(channelID, s.modeFor(channelID))
		},
		listDownloads: s.downloadFiles,
		stopCh:        make(chan struct{}),
		inputQ:        newInputQueue(),
	}
	defer bc.disconnect()
	go bc.runInputWorker()

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
		case bwsMsgDialog:
			go bc.answerDialog(msg)
		case bwsMsgDownloads:
			bc.sendDownloads(msg.ChannelID)
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
	if bc.browserEnabled != nil && !bc.browserEnabled(ctx, msg.ChannelID) {
		bc.sendError(errBrowserDisabled.Error())
		return
	}
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
			if !activeClient.Alive() {
				// The sidecar this manager was built for is gone, and the
				// manager holds its old endpoint, so reconnecting in place
				// would dial a dead port. Start over on a fresh one.
				bc.logger.Info("browser ws: dropping dead CDP", "channel_id", msg.ChannelID)
				if bc.closeCDPMgr != nil {
					bc.closeCDPMgr(msg.ChannelID)
				}
				cdpMgr = bc.resolveCDPMgr(msg.ChannelID, mode, bc.browserProvider)
			} else {
				// A tab that closed while the pane was away leaves this client
				// attached to nothing, and the screencast below would stream
				// from a target that no longer exists.
				if live, err := cdpMgr.EnsureLiveTarget(ctx); err != nil {
					bc.sendError("failed to reattach CDP: " + err.Error())
					return
				} else if live != nil {
					activeClient = live
				}
				// The agent may have opened or switched a tab while the pane
				// was away. That records the new tab as active before anything
				// attaches to it, so streaming the attached client would show
				// the old tab under a strip that marks the new one.
				if want := cdpMgr.ActiveTargetID(); want != "" && activeClient.TargetID() != want {
					if moved, err := cdpMgr.GetOrCreate(ctx, want); err == nil {
						activeClient = moved
					} else {
						bc.logger.Warn("browser ws: attaching to the active tab failed", "target_id", want, "error", err)
					}
				}
				bc.logger.Info("browser ws: reusing cached CDP")
				activeClient.ResetScreencast()
				// Chrome does not composite a backgrounded tab, so a screencast
				// restarted on one streams nothing and the pane sits on its last
				// frame — indistinguishable from a hang, and silent. Anything
				// that opened a tab while the pane was away leaves this client
				// holding a background one, so reuse has to activate for the
				// same reason the fresh-connect path below does.
				//
				// Order matters: ResetScreencast above clears the stale flag so
				// SwitchTarget skips its CDP stop, which is unbounded and would
				// otherwise block on the very tab being recovered.
				if tid := activeClient.TargetID(); tid != "" {
					_ = activeClient.SwitchTarget(tid)
				}
				cdpMgr.PaneConnected()
				bc.mu.Lock()
				bc.cdpMgr = cdpMgr
				bc.cdp = activeClient
				bc.mu.Unlock()
				bc.startTabWatch()
				bc.sendJSON(browserWSResponse{Type: bwsRespStarted})
				bc.sendPendingDialog(cdpMgr)
				return
			}
		}
	}

	// Mark pane connected BEFORE Connect so the idle monitor doesn't kill
	// the container while we're still connecting (Connect retries take ~10s).
	cdpMgr.PaneConnected()

	// Connect CDP — CDPManager handles retries internally.
	//
	// Log the manager's own endpoint, not the provider's: they can differ, and
	// the one that gets dialed is the manager's. Log the failure too — the retry
	// attempts underneath are Debug and sendError only reaches the panel, so a
	// pane that never connects otherwise leaves nothing behind to read.
	bc.logger.Info("browser ws: connecting CDP", "channel_id", msg.ChannelID, "endpoint", cdpMgr.WSEndpoint())
	if err := cdpMgr.Connect(ctx); err != nil {
		cdpMgr.PaneDisconnected()
		bc.logger.Error("browser ws: CDP connect failed", "channel_id", msg.ChannelID, "endpoint", cdpMgr.WSEndpoint(), "error", err)
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

	// Watch before saying started: the pane fetches the tab list on started,
	// and a tab opened between that fetch and a later subscription was lost.
	bc.startTabWatch()
	bc.sendJSON(browserWSResponse{Type: bwsRespStarted})
	bc.sendPendingDialog(cdpMgr)
}

// sendPendingDialog shows the pane a dialog the active tab was already blocked
// on when it started, which no event will announce again.
func (bc *browserWSConn) sendPendingDialog(cdpMgr *browser.CDPManager) {
	if d, ok := cdpMgr.PendingDialog(cdpMgr.ActiveTargetID()); ok {
		bc.sendJSON(browserWSResponse{Type: bwsRespDialog, TargetID: d.TargetID, Dialog: &d})
	}
}

// answerDialog accepts or dismisses the dialog the user answered in the pane.
func (bc *browserWSConn) answerDialog(msg browserWSMessage) {
	bc.mu.Lock()
	cdpMgr := bc.cdpMgr
	bc.mu.Unlock()
	if cdpMgr == nil {
		return
	}
	if err := cdpMgr.HandleDialog(context.Background(), msg.TargetID, msg.Accept, msg.Text); err != nil {
		bc.sendError("answering the dialog failed: " + err.Error())
	}
}

// sendDownloads answers the pane's list_downloads with what channelID's
// browser has saved. Reading a dir needs no browser, so it works before start.
func (bc *browserWSConn) sendDownloads(channelID string) {
	if channelID == "" {
		bc.sendError("channel_id required")
		return
	}
	dir, files, err := bc.listDownloads(channelID)
	if err != nil {
		bc.sendError(err.Error())
		return
	}
	bc.sendJSON(browserWSResponse{Type: bwsRespDownloads, DownloadsDir: dir, Files: files})
}

func (bc *browserWSConn) handleStop(ctx context.Context, msg browserWSMessage) {
	bc.logger.Info("browser ws: stopping", "channel_id", msg.ChannelID)
	bc.cleanup()

	if msg.ChannelID != "" {
		// Drop the cached manager before the container goes away. cleanup only
		// clears this connection's own references, so without this the next
		// start — or any MCP browser tool — would reuse a client bound to the
		// container being destroyed and fail with "context canceled".
		if bc.closeCDPMgr != nil {
			bc.closeCDPMgr(msg.ChannelID)
		}
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
		Modifiers:  msg.Modifiers,
		Index:      msg.Index,
	}
	// Queued rather than dispatched here: a wheel or move event costs about a
	// compositor frame, and blocking the read loop on that is what made a
	// scroll burst arrive as a backlog.
	bc.inputQ.push(ev)
}

func (bc *browserWSConn) dispatchInput(ev browser.InputEvent) {
	bc.mu.Lock()
	cdp := bc.cdp
	bc.mu.Unlock()

	if cdp == nil {
		return
	}

	// Background, not a deadline: every CDP command runs on the session's own
	// context and cancelling that closes the tab, so the client bounds these
	// itself — see defaultCommandTimeout. Without that bound one event
	// dispatched to a backgrounded target would park this worker for good and
	// silently swallow every event queued behind it.
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
		if btn == "left" && count == 1 {
			err = bc.clickOrOpenSelect(ctx, cdp, ev.X, ev.Y)
		} else {
			err = cdp.MouseClick(ctx, ev.X, ev.Y, btn, count)
		}
	case bwsInputSelect:
		err = bc.chooseSelectOption(ctx, cdp, ev.Index)
	case "mousemove":
		err = cdp.MouseMove(ctx, ev.X, ev.Y, 0)
	case "scroll":
		err = cdp.MouseScroll(ctx, ev.X, ev.Y, ev.DeltaX, ev.DeltaY)
	case "keypress":
		err = cdp.KeyPress(ctx, ev.Key, ev.Modifiers)
	case "typetext":
		err = cdp.TypeText(ctx, ev.Text)
	case bwsInputPaste:
		// The sidecar has its own clipboard that the host cannot reach, so the
		// client sends the text and it is inserted directly.
		err = cdp.InsertText(ctx, ev.Text)
	case bwsInputCopy:
		err = bc.sendSelection(ctx, cdp)
	case bwsInputContextMenu:
		err = bc.openContextMenu(ctx, cdp, ev.X, ev.Y)
	}

	if err != nil {
		bc.logger.Error("input dispatch failed", "type", ev.Type, "error", err)
	}
}

// sendSelection reads the page selection and hands it back to the client, which
// writes it to the host clipboard. Copying inside the sidecar would otherwise
// land in a clipboard nobody can read.
func (bc *browserWSConn) sendSelection(ctx context.Context, cdp browser.CDPSession) error {
	text, err := cdp.ReadSelection(ctx)
	if err != nil {
		return err
	}
	bc.sendJSON(browserWSResponse{Type: bwsRespClipboard, Text: text})
	return nil
}

// armContextMenuJS clears the last right-click and, once per document, starts
// recording every contextmenu event. The listener is on window in the capture
// phase, so it sees the event before the page can stop it.
const armContextMenuJS = `(() => {
  if (!window.__loopContextMenuArmed) {
    window.__loopContextMenuArmed = true;
    addEventListener("contextmenu", (e) => { window.__loopContextMenu = e; }, true);
  }
  window.__loopContextMenu = null;
  return "";
})()`

// contextMenuJS describes the element the right-click landed on, or returns ""
// when no contextmenu event arrived or the page prevented it to draw its own.
const contextMenuJS = `(() => {
  const e = window.__loopContextMenu;
  window.__loopContextMenu = null;
  if (!e || e.defaultPrevented) return "";
  const t = e.target instanceof Element ? e.target : null;
  const link = t && t.closest("a[href]");
  const img = t && t.closest("img");
  const editable = !!t && (t.isContentEditable || t.matches("textarea, input:not([type]), input[type=text], input[type=search], input[type=email], input[type=url], input[type=tel], input[type=password], input[type=number]"));
  return JSON.stringify({
    link: link ? link.href : "",
    image: img ? img.currentSrc || img.src : "",
    selection: String(getSelection()),
    editable,
  });
})()`

// openContextMenu right-clicks at (x, y) and tells the client what was under
// the pointer. Headless Chrome draws no menu of its own, so the pane draws one;
// it doesn't when the page drew its own.
func (bc *browserWSConn) openContextMenu(ctx context.Context, cdp browser.CDPSession, x, y float64) error {
	if _, err := cdp.EvaluateJS(ctx, armContextMenuJS); err != nil {
		// The page still gets its right-click; only the pane's menu is lost.
		bc.logger.Warn("browser ws: arming the context menu failed", "error", err)
		return cdp.MouseClick(ctx, x, y, "right", 1)
	}
	if err := cdp.MouseClick(ctx, x, y, "right", 1); err != nil {
		return err
	}
	raw, err := cdp.EvaluateJS(ctx, contextMenuJS)
	if err != nil || raw == "" {
		return err
	}
	var menu browserContextMenu
	if err := json.Unmarshal([]byte(raw), &menu); err != nil {
		return fmt.Errorf("reading the context menu: %w", err)
	}
	bc.sendJSON(browserWSResponse{Type: bwsRespContextMenu, Menu: &menu})
	return nil
}

// selectAtJS finds the single-choice <select> at (x, y), through open shadow
// roots, focuses it and lists its options; it returns "" for anything else.
// The select is kept on window so the pick that follows finds it again.
const selectAtJS = `((x, y) => {
  let el = document.elementFromPoint(x, y);
  while (el && el.shadowRoot) {
    const inner = el.shadowRoot.elementFromPoint(x, y);
    if (!inner || inner === el) break;
    el = inner;
  }
  const s = el && el.closest("select");
  if (!s || s.multiple || s.size > 1 || s.disabled) return "";
  window.__loopSelect = s;
  s.focus();
  return JSON.stringify({
    selected: s.selectedIndex,
    options: Array.from(s.options, (o) => {
      const g = o.parentElement instanceof HTMLOptGroupElement ? o.parentElement : null;
      return { label: o.label || o.text, disabled: o.disabled || !!(g && g.disabled), group: g ? g.label : "" };
    }),
  });
})(%g, %g)`

// chooseSelectJS picks option %d of the select selectAtJS found, firing input
// and change as a real pick does when the choice differs. It returns "gone"
// when the select left the page meanwhile.
const chooseSelectJS = `((i) => {
  const s = window.__loopSelect;
  if (!s || !s.isConnected || i < 0 || i >= s.options.length) return "gone";
  if (s.selectedIndex !== i) {
    s.selectedIndex = i;
    s.dispatchEvent(new Event("input", { bubbles: true }));
    s.dispatchEvent(new Event("change", { bubbles: true }));
  }
  return "";
})(%d)`

// clickOrOpenSelect left-clicks at (x, y), unless that lands on a <select>:
// then the pane gets its options instead, since Chrome's own dropdown would
// open where nobody can see it.
func (bc *browserWSConn) clickOrOpenSelect(ctx context.Context, cdp browser.CDPSession, x, y float64) error {
	raw, err := cdp.EvaluateJS(ctx, fmt.Sprintf(selectAtJS, x, y))
	if err != nil || raw == "" {
		if err != nil {
			bc.logger.Warn("browser ws: looking for a select failed", "error", err)
		}
		return cdp.MouseClick(ctx, x, y, "left", 1)
	}
	var menu browserSelectMenu
	if err := json.Unmarshal([]byte(raw), &menu); err != nil {
		return fmt.Errorf("reading the select: %w", err)
	}
	bc.sendJSON(browserWSResponse{Type: bwsRespSelectMenu, Select: &menu})
	return nil
}

// chooseSelectOption applies the option picked in the pane's select menu.
func (bc *browserWSConn) chooseSelectOption(ctx context.Context, cdp browser.CDPSession, index int) error {
	res, err := cdp.EvaluateJS(ctx, fmt.Sprintf(chooseSelectJS, index))
	if err != nil {
		return err
	}
	if res != "" {
		bc.sendError("The dropdown is no longer on the page")
	}
	return nil
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
	if bc.screencastStopCh != nil {
		close(bc.screencastStopCh)
	}
	bc.screencastStopCh = newStopCh
	bc.mu.Unlock()

	frameCh := cdp.StartScreencast(60, w, h, newStopCh)
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

	bc.mu.Lock()
	cdpMgr, current := bc.cdpMgr, bc.cdp
	bc.mu.Unlock()

	if cdpMgr == nil {
		bc.logger.Error("browser ws: no CDPManager for target switch")
		return
	}

	// Attach before tearing the running screencast down. A tab can die
	// between the strip being drawn and the click on it arriving — a renderer
	// killed for running the sidecar out of memory is enough — and stopping
	// the stream first would blank a pane that was working in order to show a
	// tab that no longer exists.
	client, err := cdpMgr.GetOrCreate(ctx, targetID)
	if err != nil {
		bc.logger.Error("browser ws: switch target failed", "target_id", targetID, "error", err)
		if !errors.Is(err, browser.ErrTargetGone) {
			bc.sendError("switch target failed: " + err.Error())
			return
		}
		// Say what happened and redraw the strip without the tab that went
		// away, so it stops being something the user can click again.
		bc.sendError("that tab is no longer open")
		if current != nil {
			if tabs, listErr := current.ListTabs(ctx); listErr == nil {
				bc.sendTabsResponse(tabs, current.TargetID())
			}
		}
		return
	}

	// Stop the old pipeFrames goroutine.
	bc.mu.Lock()
	if bc.screencastStopCh != nil {
		close(bc.screencastStopCh)
	}
	newStopCh := make(chan struct{})
	bc.screencastStopCh = newStopCh
	bc.mu.Unlock()

	activeCDP := client

	_ = activeCDP.SwitchTarget(targetID)
	cdpMgr.SwitchActive(targetID)

	bc.mu.Lock()
	bc.cdp = activeCDP
	bc.mu.Unlock()

	activeCDP.ResetScreencast()
	frameCh := activeCDP.StartScreencast(60, 1920, 1080, newStopCh)
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
	cdpMgr, client := bc.cdpMgr, bc.cdp
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
	// Icons come from Chrome itself rather than being guessed from the URL,
	// so an intranet page with its own icon gets it and a page with none
	// keeps the plain dot instead of a broken image.
	var favicons map[string]string
	if client != nil {
		favicons = client.Favicons()
	}
	tabInfos := make([]browserTabInfo, len(tabs))
	for i, t := range tabs {
		tabInfos[i] = browserTabInfo{
			TargetID:   t.TargetID,
			URL:        t.URL,
			Title:      t.Title,
			FaviconURL: favicons[t.TargetID],
		}
	}
	bc.sendJSON(browserWSResponse{
		Type:           bwsRespTabs,
		Tabs:           tabInfos,
		ActiveTargetID: activeTargetID,
	})
}

// startTabWatch subscribes to the manager's tab changes and forwards them to
// the pane until the next start, a stop, or the socket closing. Subscribing
// happens here rather than on the goroutine, so no change made after this
// returns can slip past it.
func (bc *browserWSConn) startTabWatch() {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.watchStopCh != nil {
		close(bc.watchStopCh)
		bc.watchStopCh = nil
	}
	if bc.cdpMgr == nil {
		return
	}
	events, unsubscribe := bc.cdpMgr.Subscribe()
	stop := make(chan struct{})
	bc.watchStopCh = stop
	go func() {
		defer unsubscribe()
		bc.watchTabEvents(events, stop)
	}()
}

// watchTabEvents forwards tab changes — the agent's and the ones Chrome makes
// on its own — to the pane until stop or the socket closes.
func (bc *browserWSConn) watchTabEvents(events <-chan browser.TabEvent, stop <-chan struct{}) {
	for {
		select {
		case ev := <-events:
			switch ev.Kind {
			case browser.TabSwitched:
				bc.mu.Lock()
				cdp := bc.cdp
				bc.mu.Unlock()
				if cdp == nil {
					return
				}
				if cdp.TargetID() == ev.Tab.TargetID {
					bc.logger.Debug("watchTabEvents: skipping switch to same target", "target_id", ev.Tab.TargetID)
					continue
				}
				// Detached on purpose: the switch must outlive whatever
				// prompted it, and the CDP calls it makes carry their own
				// deadlines.
				bc.restartScreencastForTarget(context.Background(), cdp, ev.Tab.TargetID)
			case browser.TabAdded:
				bc.sendJSON(browserWSResponse{
					Type:     bwsRespTabCreated,
					TargetID: ev.Tab.TargetID,
					URL:      ev.Tab.URL,
					Title:    ev.Tab.Title,
				})
			case browser.TabRemoved:
				bc.sendJSON(browserWSResponse{
					Type:     bwsRespTabClosed,
					TargetID: ev.Tab.TargetID,
				})
			case browser.DialogOpened:
				d := ev.Dialog
				bc.sendJSON(browserWSResponse{Type: bwsRespDialog, TargetID: d.TargetID, Dialog: &d})
			case browser.DialogClosed:
				bc.sendJSON(browserWSResponse{Type: bwsRespDialogDone, TargetID: ev.Tab.TargetID})
			case browser.DownloadChanged:
				d := ev.Download
				bc.sendJSON(browserWSResponse{Type: bwsRespDownload, Download: &d})
			case browser.TabUpdated:
				bc.sendJSON(browserWSResponse{
					Type:     bwsRespTabUpdated,
					TargetID: ev.Tab.TargetID,
					URL:      ev.Tab.URL,
					Title:    ev.Tab.Title,
				})
			}
		case <-stop:
			return
		case <-bc.stopCh:
			return
		}
	}
}

// disconnect ends the connection: it stops the input worker and any frame
// pipe still waiting on it, then releases the session. A stop message only
// releases the session, since the pane may start again on the same socket.
func (bc *browserWSConn) disconnect() {
	close(bc.stopCh)
	bc.cleanup()
}

func (bc *browserWSConn) cleanup() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.cdpMgr != nil {
		bc.cdpMgr.PaneDisconnected()
	}
	if bc.watchStopCh != nil {
		close(bc.watchStopCh)
		bc.watchStopCh = nil
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

// handleBrowserProfileReset handles POST /api/browser/profile/reset — wipes the
// channel's persistent Chrome profile, signing the agent out of everything.
//
// The sidecar must be gone before the volume can be dropped, so this tears the
// session down synchronously rather than going through ScheduleRemove: Docker
// refuses to remove a volume that is still attached to a container. The next
// EnsureBrowser builds a fresh sidecar on a fresh volume.
func (s *browserService) handleBrowserProfileReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}
	if s.dockerProvider == nil {
		http.Error(w, "docker browser not configured", http.StatusServiceUnavailable)
		return
	}
	if s.modeFor(body.ChannelID) == "host" {
		http.Error(w, "host mode uses your own Chrome profile; switch to docker mode to reset", http.StatusConflict)
		return
	}

	s.closeCDPManager(body.ChannelID, "docker")

	containerID, _ := s.dockerProvider.StopBrowser(r.Context(), body.ChannelID)
	if containerID != "" && s.containerRegistry != nil {
		if err := s.containerRegistry.RemoveContainer(r.Context(), containerID); err != nil {
			s.deps.logger.Warn("browser profile reset: container remove failed",
				"channel_id", body.ChannelID, "container_id", containerID, "error", err)
		}
	}

	if err := s.dockerProvider.RemoveProfile(r.Context(), body.ChannelID); err != nil {
		s.deps.logger.Error("browser profile reset failed", "channel_id", body.ChannelID, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.capturesMu.Lock()
	delete(s.captures, body.ChannelID)
	s.capturesMu.Unlock()

	s.deps.logger.Info("browser profile reset", "channel_id", body.ChannelID)
	w.WriteHeader(http.StatusNoContent)
}

// closeCDPManager drops and closes the CDPManager for a channel/mode pair, if
// one is live. Mirrors the teardown half of cleanIdleBrowserSessions.
func (s *browserService) closeCDPManager(channelID, mode string) {
	key := channelID + "|" + mode
	s.cdpManagersMu.Lock()
	mgr := s.cdpManagers[key]
	delete(s.cdpManagers, key)
	s.cdpManagersMu.Unlock()
	if mgr != nil {
		mgr.Close()
	}
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

	// Listing downloads reads a dir: it needs no browser, so it doesn't
	// start one.
	if req.Action == "list_downloads" {
		writeJSON(w, s.listDownloads(req.ChannelID))
		return
	}

	if resp, done := s.dialogAction(r.Context(), req); done {
		writeJSON(w, resp)
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

// worksUnderDialog reports whether action still works while the active tab is
// blocked on a JavaScript dialog: tab actions go through the browser, not the
// page.
func worksUnderDialog(action string) bool {
	switch action {
	case "list_tabs", "new_tab", "switch_tab", "close_tab":
		return true
	}
	return false
}

// dialogAction answers the active tab's JavaScript dialog for handle_dialog,
// and refuses page actions while one is open. A blocked page takes no input
// and runs no script, so without this an agent's click waited out the whole
// command deadline and came back with a timeout that said nothing of why.
// done is false when the action should run as usual.
func (s *browserService) dialogAction(ctx context.Context, req browserActionRequest) (resp browserActionResponse, done bool) {
	var d browser.Dialog
	open := false
	mgr := s.getActiveCDPManager(req.ChannelID)
	if mgr != nil {
		d, open = mgr.PendingDialog(mgr.ActiveTargetID())
	}
	if req.Action == "handle_dialog" {
		if !open {
			return browserActionResponse{Error: browser.ErrNoDialog.Error()}, true
		}
		accept := paramBool(req.Params, "accept")
		if err := mgr.HandleDialog(ctx, d.TargetID, accept, paramStr(req.Params, "prompt_text")); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("handling the dialog failed: %v", err)}, true
		}
		verb := "Dismissed"
		if accept {
			verb = "Accepted"
		}
		return browserActionResponse{Result: fmt.Sprintf("%s the %s %q", verb, d.Type, d.Message)}, true
	}
	if !open || worksUnderDialog(req.Action) {
		return browserActionResponse{}, false
	}
	return browserActionResponse{Error: fmt.Sprintf(
		"a JavaScript %s is open on this tab and blocks it: %q. Answer it with handle_dialog (accept, and prompt_text for a prompt) first", d.Type, d.Message)}, true
}
