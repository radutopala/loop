package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
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

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if s.terminal == nil {
		http.Error(w, "terminal not configured", http.StatusNotImplemented)
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
					writeJSON(wsStatusMessage{Type: "closed"})
					return
				}
				writeBinary(data)
			case <-done:
				writeJSON(wsStatusMessage{Type: "closed"})
				return
			case <-stopCh:
				return
			}
		}
	}

	// detachCurrent detaches from the current session if attached.
	detachCurrent := func() {
		if sessionID != "" && outputCh != nil {
			s.terminal.DetachSession(sessionID, outputCh)
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
			writeJSON(wsStatusMessage{Type: "error", Message: "invalid JSON"})
			continue
		}

		switch msg.Type {
		case "create":
			if msg.ContainerID == "" {
				writeJSON(wsStatusMessage{Type: "error", Message: "container_id required"})
				continue
			}
			detachCurrent()

			sid, output, history, done, err := s.terminal.CreateSession(r.Context(), msg.ContainerID, msg.Cmd)
			if err != nil {
				writeJSON(wsStatusMessage{Type: "error", Message: err.Error()})
				continue
			}
			sessionID = sid
			outputCh = output

			writeJSON(wsStatusMessage{Type: "created", SessionID: sid})
			if len(history) > 0 {
				writeBinary(history)
			}
			go streamOutput(output, done)

		case "attach":
			if msg.SessionID == "" {
				writeJSON(wsStatusMessage{Type: "error", Message: "session_id required"})
				continue
			}
			detachCurrent()

			output, history, done, err := s.terminal.AttachSession(msg.SessionID)
			if err != nil {
				writeJSON(wsStatusMessage{Type: "error", Message: err.Error()})
				continue
			}
			sessionID = msg.SessionID
			outputCh = output

			writeJSON(wsStatusMessage{Type: "attached", SessionID: msg.SessionID})
			if len(history) > 0 {
				writeBinary(history)
			}
			go streamOutput(output, done)

		case "input":
			if sessionID == "" {
				writeJSON(wsStatusMessage{Type: "error", Message: "no active session"})
				continue
			}
			data, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				writeJSON(wsStatusMessage{Type: "error", Message: "invalid base64 data"})
				continue
			}
			if err := s.terminal.SendInput(sessionID, data); err != nil {
				writeJSON(wsStatusMessage{Type: "error", Message: err.Error()})
			}

		case "resize":
			if sessionID == "" {
				writeJSON(wsStatusMessage{Type: "error", Message: "no active session"})
				continue
			}
			if msg.Rows == 0 || msg.Cols == 0 {
				writeJSON(wsStatusMessage{Type: "error", Message: "rows and cols required"})
				continue
			}
			if err := s.terminal.Resize(r.Context(), sessionID, msg.Rows, msg.Cols); err != nil {
				writeJSON(wsStatusMessage{Type: "error", Message: err.Error()})
			}

		case "stop":
			if sessionID == "" {
				writeJSON(wsStatusMessage{Type: "error", Message: "no active session"})
				continue
			}
			if err := s.terminal.StopSession(sessionID); err != nil {
				writeJSON(wsStatusMessage{Type: "error", Message: err.Error()})
				continue
			}
			detachCurrent()
			writeJSON(wsStatusMessage{Type: "stopped"})

		default:
			writeJSON(wsStatusMessage{Type: "error", Message: "unknown message type: " + msg.Type})
		}
	}
}

// SetTerminalManager configures the terminal manager for WebSocket terminal sessions.
func (s *Server) SetTerminalManager(mgr TerminalManager) {
	s.terminal = mgr
}
