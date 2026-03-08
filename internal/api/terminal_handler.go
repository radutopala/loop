package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
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
	StopSession(sessionID string) error
}

// wsControlMessage represents a JSON control message from the client.
type wsControlMessage struct {
	Type        string   `json:"type"`
	ContainerID string   `json:"container_id,omitempty"`
	Cmd         []string `json:"cmd,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Data        string   `json:"data,omitempty"` // base64-encoded input
	Rows        uint     `json:"rows,omitempty"`
	Cols        uint     `json:"cols,omitempty"`
}

// wsStatusMessage represents a JSON status message sent to the client.
type wsStatusMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// terminalWSConn manages a single WebSocket terminal connection.
type terminalWSConn struct {
	conn     *websocket.Conn
	manager  TerminalManager
	logger   *slog.Logger
	writeMu  sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}

	sessionID string
	outputCh  <-chan []byte
}

func newTerminalWSConn(conn *websocket.Conn, manager TerminalManager, logger *slog.Logger) *terminalWSConn {
	return &terminalWSConn{
		conn:    conn,
		manager: manager,
		logger:  logger,
		stopCh:  make(chan struct{}),
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
		if err := t.manager.DetachSession(t.sessionID, t.outputCh); err != nil {
			t.logger.Warn("terminal ws: detach failed", "session_id", t.sessionID, "error", err)
		}
		t.sessionID = ""
		t.outputCh = nil
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

func (t *terminalWSConn) handleCreate(ctx context.Context, msg wsControlMessage) {
	if msg.ContainerID == "" {
		t.sendError("container_id required", wsErrCodeMissingField)
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
}

func (t *terminalWSConn) handleAttach(msg wsControlMessage) {
	if msg.SessionID == "" {
		t.sendError("session_id required", wsErrCodeMissingField)
		return
	}
	t.detachCurrent()

	output, history, done, err := t.manager.AttachSession(msg.SessionID)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
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
	if err := t.manager.SendInput(t.sessionID, data); err != nil {
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
	if err := t.manager.Resize(ctx, t.sessionID, msg.Rows, msg.Cols); err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
	}
}

func (t *terminalWSConn) handleStop() {
	if t.sessionID == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	if err := t.manager.StopSession(t.sessionID); err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.detachCurrent()
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.termManager, "terminal not configured") {
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	tc := newTerminalWSConn(conn, s.termManager, s.logger)
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
			tc.handleStop()
		default:
			tc.sendError("unknown message type: "+msg.Type, wsErrCodeUnknownMessage)
		}
	}
}

// SetTerminalManager configures the terminal manager for WebSocket terminal sessions.
func (s *Server) SetTerminalManager(mgr TerminalManager) {
	s.termManager = mgr
}
