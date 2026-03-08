package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// TerminalManager abstracts the terminal session operations needed by the handler.
type TerminalManager interface {
	CreateSession(ctx context.Context, containerID string, cmd []string) (sessionID string, output <-chan []byte, history []byte, done <-chan struct{}, err error)
	AttachSession(sessionID string) (output <-chan []byte, history []byte, done <-chan struct{}, err error)
	DetachSession(sessionID string, output <-chan []byte)
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

	var (
		writeMu   sync.Mutex
		sessionID string
		outputCh  <-chan []byte
		stopOnce  sync.Once
		stopCh    = make(chan struct{})
	)

	writeJSON := func(msg wsStatusMessage) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(msg)
	}

	writeBinary := func(data []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}

	// streamOutput forwards terminal output to the WebSocket client.
	streamOutput := func(output <-chan []byte, done <-chan struct{}) {
		for {
			select {
			case data, ok := <-output:
				if !ok {
					writeJSON(wsStatusMessage{Type: wsStatusClosed})
					return
				}
				writeBinary(data)
			case <-done:
				writeJSON(wsStatusMessage{Type: wsStatusClosed})
				return
			case <-stopCh:
				return
			}
		}
	}

	// detachCurrent detaches from the current session if attached.
	detachCurrent := func() {
		if sessionID != "" && outputCh != nil {
			s.termManager.DetachSession(sessionID, outputCh)
			sessionID = ""
			outputCh = nil
		}
	}

	defer func() {
		stopOnce.Do(func() { close(stopCh) })
		detachCurrent()
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg wsControlMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			writeJSON(wsStatusMessage{Type: wsStatusError, Message: "invalid JSON"})
			continue
		}

		switch msg.Type {
		case wsMsgCreate:
			if msg.ContainerID == "" {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "container_id required"})
				continue
			}
			detachCurrent()

			sid, output, history, done, err := s.termManager.CreateSession(r.Context(), msg.ContainerID, msg.Cmd)
			if err != nil {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: err.Error()})
				continue
			}
			sessionID = sid
			outputCh = output

			writeJSON(wsStatusMessage{Type: wsStatusCreated, SessionID: sid})
			if len(history) > 0 {
				writeBinary(history)
			}
			go streamOutput(output, done)

		case wsMsgAttach:
			if msg.SessionID == "" {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "session_id required"})
				continue
			}
			detachCurrent()

			output, history, done, err := s.termManager.AttachSession(msg.SessionID)
			if err != nil {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: err.Error()})
				continue
			}
			sessionID = msg.SessionID
			outputCh = output

			writeJSON(wsStatusMessage{Type: wsStatusAttached, SessionID: msg.SessionID})
			if len(history) > 0 {
				writeBinary(history)
			}
			go streamOutput(output, done)

		case wsMsgInput:
			if sessionID == "" {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "no active session"})
				continue
			}
			data, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "invalid base64 data"})
				continue
			}
			if err := s.termManager.SendInput(sessionID, data); err != nil {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: err.Error()})
			}

		case wsMsgResize:
			if sessionID == "" {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "no active session"})
				continue
			}
			if msg.Rows == 0 || msg.Cols == 0 {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "rows and cols required"})
				continue
			}
			if err := s.termManager.Resize(r.Context(), sessionID, msg.Rows, msg.Cols); err != nil {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: err.Error()})
			}

		case wsMsgStop:
			if sessionID == "" {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: "no active session"})
				continue
			}
			if err := s.termManager.StopSession(sessionID); err != nil {
				writeJSON(wsStatusMessage{Type: wsStatusError, Message: err.Error()})
				continue
			}
			detachCurrent()
			writeJSON(wsStatusMessage{Type: wsStatusStopped})

		default:
			writeJSON(wsStatusMessage{Type: wsStatusError, Message: "unknown message type: " + msg.Type})
		}
	}
}

// SetTerminalManager configures the terminal manager for WebSocket terminal sessions.
func (s *Server) SetTerminalManager(mgr TerminalManager) {
	s.termManager = mgr
}
