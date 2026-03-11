package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gorilla/websocket"
)

// Terminal WebSocket control message types (client → server).
const (
	wsMsgCreate = "create"
	wsMsgAttach = "attach"
	wsMsgInput  = "input"
	wsMsgResize = "resize"
	wsMsgStop   = "stop"
	wsMsgClose  = "close"
	wsMsgKill   = "kill"
)

// Terminal WebSocket status message types (server → client).
const (
	wsStatusCreated  = "created"
	wsStatusAttached = "attached"
	wsStatusError    = "error"
	wsStatusClosed   = "closed"
	wsStatusStopped  = "stopped"
)

// Terminal WebSocket error codes sent alongside error messages.
const (
	wsErrCodeInvalidJSON    = "invalid_json"
	wsErrCodeNoSession      = "no_session"
	wsErrCodeMissingField   = "missing_field"
	wsErrCodeInvalidInput   = "invalid_input"
	wsErrCodeSessionFailed  = "session_failed"
	wsErrCodeUnknownMessage = "unknown_message"
)

// TerminalManager abstracts the terminal session operations needed by the handler.
type TerminalManager interface {
	CreateSession(ctx context.Context, containerID string, cmd []string) (sessionID string, output <-chan []byte, history []byte, done <-chan struct{}, err error)
	AttachSession(sessionID string) (output <-chan []byte, history []byte, done <-chan struct{}, err error)
	DetachSession(sessionID string, output <-chan []byte) error
	SendInput(sessionID string, data []byte) error
	Resize(ctx context.Context, sessionID string, rows, cols uint) error
	StopSession(sessionID string) (containerID string, err error)
}

// ContainerFinder resolves a channel ID to a running container ID.
type ContainerFinder interface {
	FindContainerByChannel(ctx context.Context, channelID, dirPath string) (string, error)
}

// ContainerStopper removes a container by ID.
type ContainerStopper interface {
	ContainerRemove(ctx context.Context, containerID string) error
}

// wsControlMessage represents a JSON control message from the client.
type wsControlMessage struct {
	Type        string   `json:"type"`
	ContainerID string   `json:"container_id,omitempty"`
	ChannelID   string   `json:"channel_id,omitempty"`
	Cmd         []string `json:"cmd,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Data        string   `json:"data,omitempty"` // base64-encoded input
	Rows        uint     `json:"rows,omitempty"`
	Cols        uint     `json:"cols,omitempty"`
	Target      string   `json:"target,omitempty"` // "host" or "agent" (default)
}

// wsStatusMessage represents a JSON status message sent to the client.
type wsStatusMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// InteractiveCmdBuilder builds the interactive Claude command for a terminal session.
type InteractiveCmdBuilder interface {
	BuildInteractiveCmd(channelID, dirPath, sessionID string, forkSession bool) string
}

// terminalWSConn manages a single WebSocket terminal connection.
type terminalWSConn struct {
	conn             *websocket.Conn
	manager          TerminalManager
	hostManager      TerminalManager // may be nil
	containerFinder  ContainerFinder
	containerStopper ContainerStopper
	cmdBuilder       InteractiveCmdBuilder
	store            ChannelLister
	loopDir          string // fallback work dir root (e.g. ~/.loop)
	logger           *slog.Logger
	writeMu          sync.Mutex
	stopOnce         sync.Once
	stopCh           chan struct{}

	sessionID     string
	outputCh      <-chan []byte
	sessionTarget string // "host" or "agent"
}

// activeManager returns the correct manager based on the current session target.
func (t *terminalWSConn) activeManager() TerminalManager {
	if t.sessionTarget == "host" {
		return t.hostManager
	}
	return t.manager
}

func newTerminalWSConn(conn *websocket.Conn, manager TerminalManager, hostManager TerminalManager, finder ContainerFinder, stopper ContainerStopper, cmdBuilder InteractiveCmdBuilder, store ChannelLister, loopDir string, logger *slog.Logger) *terminalWSConn {
	return &terminalWSConn{
		conn:             conn,
		manager:          manager,
		hostManager:      hostManager,
		containerFinder:  finder,
		containerStopper: stopper,
		cmdBuilder:       cmdBuilder,
		store:            store,
		loopDir:          loopDir,
		logger:           logger,
		stopCh:           make(chan struct{}),
	}
}

// writeMessage is the shared, mutex-protected write path for all WebSocket frames.
func (t *terminalWSConn) writeMessage(msgType int, data []byte) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := t.conn.WriteMessage(msgType, data); err != nil {
		t.logger.Error("terminal ws: write failed", "error", err, "msg_type", msgType, "len", len(data))
	}
}

func (t *terminalWSConn) writeJSON(msg wsStatusMessage) {
	// wsStatusMessage contains only string fields; Marshal cannot fail.
	data, _ := json.Marshal(msg)
	t.writeMessage(websocket.TextMessage, data)
}

func (t *terminalWSConn) writeBinary(data []byte) {
	t.writeMessage(websocket.BinaryMessage, data)
}

func (t *terminalWSConn) sendError(message, code string) {
	t.writeJSON(wsStatusMessage{Type: wsStatusError, Message: message, ErrorCode: code})
}

// streamOutput forwards terminal output to the WebSocket client.
func (t *terminalWSConn) streamOutput(output <-chan []byte, done <-chan struct{}) {
	for {
		select {
		case data, ok := <-output:
			if !ok {
				t.writeJSON(wsStatusMessage{Type: wsStatusClosed})
				return
			}
			t.writeBinary(data)
		case <-done:
			t.writeJSON(wsStatusMessage{Type: wsStatusClosed})
			return
		case <-t.stopCh:
			return
		}
	}
}

// detachCurrent detaches from the current session if attached.
func (t *terminalWSConn) detachCurrent() {
	if t.sessionID != "" && t.outputCh != nil {
		if mgr := t.activeManager(); mgr != nil {
			if err := mgr.DetachSession(t.sessionID, t.outputCh); err != nil {
				t.logger.Warn("terminal ws: detach failed", "session_id", t.sessionID, "error", err)
			}
		}
		t.sessionID = ""
		t.outputCh = nil
		t.sessionTarget = ""
	}
}

// close stops streaming and detaches from the current session.
func (t *terminalWSConn) close() {
	t.stopOnce.Do(func() { close(t.stopCh) })
	t.detachCurrent()
}

// startSession attaches to a session and begins streaming output.
func (t *terminalWSConn) startSession(sessionID string, output <-chan []byte, history []byte, done <-chan struct{}, statusType string) {
	t.sessionID = sessionID
	t.outputCh = output
	t.writeJSON(wsStatusMessage{Type: statusType, SessionID: sessionID})
	if len(history) > 0 {
		t.writeBinary(history)
	}
	go t.streamOutput(output, done)
}

// maxCmdArgs is the maximum number of arguments allowed in a create command.
const maxCmdArgs = 64

func (t *terminalWSConn) handleCreateHost(ctx context.Context, msg wsControlMessage) {
	if t.hostManager == nil {
		t.sendError("host terminal not configured", wsErrCodeSessionFailed)
		return
	}

	// Resolve channel_id → dir_path. For threads, fall back to the parent's dir_path.
	// When DirPath is empty, fall back to the loop work dir (same logic as channel listing).
	dirPath := os.Getenv("HOME")
	if msg.ChannelID != "" && t.store != nil {
		if ch, err := t.store.GetChannel(ctx, msg.ChannelID); err == nil && ch != nil {
			switch {
			case ch.DirPath != "":
				dirPath = ch.DirPath
			case ch.ParentID != "":
				if parent, err := t.store.GetChannel(ctx, ch.ParentID); err == nil && parent != nil && parent.DirPath != "" {
					dirPath = parent.DirPath
				} else if t.loopDir != "" {
					dirPath = filepath.Join(t.loopDir, msg.ChannelID, "work")
				}
			case t.loopDir != "":
				dirPath = filepath.Join(t.loopDir, msg.ChannelID, "work")
			}
		}
	}

	if len(msg.Cmd) > maxCmdArgs {
		t.sendError("cmd exceeds maximum arguments", wsErrCodeInvalidInput)
		return
	}
	for _, arg := range msg.Cmd {
		if arg == "" {
			t.sendError("cmd contains empty argument", wsErrCodeInvalidInput)
			return
		}
	}

	t.detachCurrent()

	sid, output, history, done, err := t.hostManager.CreateSession(ctx, dirPath, msg.Cmd)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.sessionTarget = "host"
	t.startSession(sid, output, history, done, wsStatusCreated)

	if msg.Rows > 0 && msg.Cols > 0 {
		if err := t.hostManager.Resize(ctx, sid, msg.Rows, msg.Cols); err != nil {
			t.logger.Warn("terminal ws: initial resize failed", "session_id", sid, "error", err)
		}
	}
}

func (t *terminalWSConn) handleCreate(ctx context.Context, msg wsControlMessage) {
	if msg.Target == "host" {
		t.handleCreateHost(ctx, msg)
		return
	}

	// Resolve channel_id to container_id via ContainerFinder.
	var dirPath, claudeSessionID string
	var forkSession bool
	if msg.ContainerID == "" && msg.ChannelID != "" && t.containerFinder != nil {
		// Look up channel's dir_path and session_id for the interactive command.
		if t.store != nil {
			if ch, err := t.store.GetChannel(ctx, msg.ChannelID); err == nil && ch != nil {
				dirPath = ch.DirPath
				claudeSessionID = ch.SessionID
				// For threads still using the parent's session, fork so the thread
				// gets its own session while inheriting the parent's context.
				if ch.ParentID != "" {
					if parent, err := t.store.GetChannel(ctx, ch.ParentID); err == nil && parent != nil && parent.SessionID != "" {
						if claudeSessionID == "" || claudeSessionID == parent.SessionID {
							claudeSessionID = parent.SessionID
							forkSession = true
						}
					}
				}
			}
		}
		containerID, err := t.containerFinder.FindContainerByChannel(ctx, msg.ChannelID, dirPath)
		if err != nil {
			t.sendError("no running container for channel: "+err.Error(), wsErrCodeSessionFailed)
			return
		}
		msg.ContainerID = containerID
	}
	if msg.ContainerID == "" {
		t.sendError("container_id or channel_id required", wsErrCodeMissingField)
		return
	}
	if len(msg.Cmd) > maxCmdArgs {
		t.sendError("cmd exceeds maximum arguments", wsErrCodeInvalidInput)
		return
	}
	for _, arg := range msg.Cmd {
		if arg == "" {
			t.sendError("cmd contains empty argument", wsErrCodeInvalidInput)
			return
		}
	}

	t.detachCurrent()

	sid, output, history, done, err := t.manager.CreateSession(ctx, msg.ContainerID, msg.Cmd)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.startSession(sid, output, history, done, wsStatusCreated)

	// Resize the PTY to match the client's terminal dimensions if provided.
	if msg.Rows > 0 && msg.Cols > 0 {
		if err := t.manager.Resize(ctx, sid, msg.Rows, msg.Cols); err != nil {
			t.logger.Warn("terminal ws: initial resize failed", "session_id", sid, "error", err)
		}
	}

	// When no explicit command was provided, send the interactive Claude
	// command as shell input so the terminal starts Claude automatically.
	if len(msg.Cmd) == 0 && t.cmdBuilder != nil && msg.ChannelID != "" {
		cmd := t.cmdBuilder.BuildInteractiveCmd(msg.ChannelID, dirPath, claudeSessionID, forkSession)
		if err := t.manager.SendInput(sid, []byte(cmd+"\n")); err != nil {
			t.logger.Warn("terminal ws: failed to send interactive cmd", "session_id", sid, "error", err)
		}
	}
}

func (t *terminalWSConn) handleAttach(msg wsControlMessage) {
	if msg.SessionID == "" {
		t.sendError("session_id required", wsErrCodeMissingField)
		return
	}
	t.detachCurrent()

	// Try agent manager first, then host manager.
	var output <-chan []byte
	var history []byte
	var done <-chan struct{}
	var err error
	target := "agent"

	if t.manager != nil {
		output, history, done, err = t.manager.AttachSession(msg.SessionID)
	}
	if (t.manager == nil || err != nil) && t.hostManager != nil {
		output, history, done, err = t.hostManager.AttachSession(msg.SessionID)
		if err == nil {
			target = "host"
		}
	}
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.sessionTarget = target
	t.startSession(msg.SessionID, output, history, done, wsStatusAttached)
}

func (t *terminalWSConn) handleInput(msg wsControlMessage) {
	if t.sessionID == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		t.sendError("invalid base64 data", wsErrCodeInvalidInput)
		return
	}
	if err := t.activeManager().SendInput(t.sessionID, data); err != nil {
		t.logger.Error("terminal ws: send input failed", "session_id", t.sessionID, "error", err)
		t.sendError(err.Error(), wsErrCodeSessionFailed)
	}
}

func (t *terminalWSConn) handleResize(ctx context.Context, msg wsControlMessage) {
	if t.sessionID == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	if msg.Rows == 0 || msg.Cols == 0 {
		t.sendError("rows and cols required", wsErrCodeMissingField)
		return
	}
	if err := t.activeManager().Resize(ctx, t.sessionID, msg.Rows, msg.Cols); err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
	}
}

func (t *terminalWSConn) handleStop(ctx context.Context, msg wsControlMessage) {
	// Allow stopping a detached session by passing session_id explicitly.
	sid := t.sessionID
	if sid == "" {
		sid = msg.SessionID
	}
	if sid == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	isHost := t.sessionTarget == "host"
	containerID, err := t.activeManager().StopSession(sid)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	// Clear session state directly — StopSession already removed the session
	// from the manager, so calling detachCurrent would fail with "session not found".
	t.sessionID = ""
	t.outputCh = nil
	t.sessionTarget = ""

	// Remove the container before notifying the client, so the channel list
	// API reflects the updated running state when the client refreshes.
	// Skip container removal for host sessions (no container involved).
	if !isHost && containerID != "" && t.containerStopper != nil {
		if err := t.containerStopper.ContainerRemove(ctx, containerID); err != nil {
			t.logger.Warn("terminal ws: container remove failed", "container_id", containerID, "error", err)
		}
	}
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

// handleClose stops the exec session but does NOT remove the container.
// Used when closing an individual terminal pane.
func (t *terminalWSConn) handleClose(msg wsControlMessage) {
	sid := t.sessionID
	if sid == "" {
		sid = msg.SessionID
	}
	if sid == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	if _, err := t.activeManager().StopSession(sid); err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.sessionID = ""
	t.outputCh = nil
	t.sessionTarget = ""
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

// handleKill removes the container for a channel without requiring an active session.
// Used when the terminal panel is closed and no panes are open.
func (t *terminalWSConn) handleKill(ctx context.Context, msg wsControlMessage) {
	if msg.ChannelID == "" {
		t.sendError("channel_id required", wsErrCodeMissingField)
		return
	}
	if t.containerFinder == nil || t.containerStopper == nil {
		t.sendError("container management not configured", wsErrCodeSessionFailed)
		return
	}

	// If there's an active agent session, stop it first.
	if t.sessionID != "" && t.sessionTarget != "host" {
		if _, err := t.activeManager().StopSession(t.sessionID); err != nil {
			t.logger.Warn("terminal ws: kill session stop failed", "session_id", t.sessionID, "error", err)
		}
		t.sessionID = ""
		t.outputCh = nil
		t.sessionTarget = ""
	}

	// Resolve channel to container and remove it.
	var dirPath string
	if t.store != nil {
		if ch, err := t.store.GetChannel(ctx, msg.ChannelID); err == nil && ch != nil {
			dirPath = ch.DirPath
		}
	}
	containerID, err := t.containerFinder.FindContainerByChannel(ctx, msg.ChannelID, dirPath)
	if err != nil {
		// No container found — nothing to kill, still report success.
		t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
		return
	}
	if err := t.containerStopper.ContainerRemove(ctx, containerID); err != nil {
		t.logger.Warn("terminal ws: kill container remove failed", "container_id", containerID, "error", err)
	}
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if s.termManager == nil && s.hostTermManager == nil {
		http.Error(w, "terminal not configured", http.StatusNotImplemented)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	tc := newTerminalWSConn(conn, s.termManager, s.hostTermManager, s.containerFinder, s.containerStopper, s.cmdBuilder, s.store, s.loopDir, s.logger)
	defer tc.close()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg wsControlMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			tc.sendError("invalid JSON", wsErrCodeInvalidJSON)
			continue
		}

		switch msg.Type {
		case wsMsgCreate:
			tc.handleCreate(r.Context(), msg)
		case wsMsgAttach:
			tc.handleAttach(msg)
		case wsMsgInput:
			tc.handleInput(msg)
		case wsMsgResize:
			tc.handleResize(r.Context(), msg)
		case wsMsgStop:
			tc.handleStop(r.Context(), msg)
		case wsMsgClose:
			tc.handleClose(msg)
		case wsMsgKill:
			tc.handleKill(r.Context(), msg)
		default:
			tc.sendError("unknown message type: "+msg.Type, wsErrCodeUnknownMessage)
		}
	}
}

// SetTerminalManager configures the terminal manager for WebSocket terminal sessions.
func (s *Server) SetTerminalManager(mgr TerminalManager) {
	s.termManager = mgr
}

// SetContainerFinder configures the container finder for channel_id → container resolution.
func (s *Server) SetContainerFinder(finder ContainerFinder) {
	s.containerFinder = finder
}

// SetContainerStopper configures the container stopper for removing containers on session stop.
func (s *Server) SetContainerStopper(stopper ContainerStopper) {
	s.containerStopper = stopper
}

// SetInteractiveCmdBuilder configures the command builder for interactive terminal sessions.
func (s *Server) SetInteractiveCmdBuilder(builder InteractiveCmdBuilder) {
	s.cmdBuilder = builder
}

// SetHostTerminalManager configures the host terminal manager for non-Docker shell sessions.
func (s *Server) SetHostTerminalManager(mgr TerminalManager) {
	s.hostTermManager = mgr
}
