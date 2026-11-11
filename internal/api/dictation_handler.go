package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/dictation"
)

// DictationService turns streamed speech into text with a local model.
type DictationService interface {
	Status() dictation.Status
	DownloadModel(done chan<- struct{}) bool
	Begin(ctx context.Context, language string, emit func(dictation.Event)) (*dictation.Session, func(), error)
}

// SetDictation configures the service behind the /api/dictation/* endpoints.
func (s *Server) SetDictation(d DictationService) {
	s.dictation = d
}

const dictationNotConfigured = "dictation not configured"

// handleDictationStatus handles GET /api/dictation/status.
func (s *Server) handleDictationStatus(w http.ResponseWriter, _ *http.Request) {
	if !requireConfigured(w, s.dictation, dictationNotConfigured) {
		return
	}
	writeJSON(w, s.dictation.Status())
}

// handleDictationModel handles POST /api/dictation/model: it starts the
// model's download in the background, unless it is downloaded or downloading.
func (s *Server) handleDictationModel(w http.ResponseWriter, _ *http.Request) {
	if !requireConfigured(w, s.dictation, dictationNotConfigured) {
		return
	}
	s.dictation.DownloadModel(nil)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(s.dictation.Status()) //nolint:errcheck
}

// dictationControl is a text message from the client.
type dictationControl struct {
	Type string `json:"type"`
}

// handleDictationStream handles GET /api/dictation/stream, a WebSocket. The
// client sends 16 kHz mono PCM16LE audio as binary messages, and {"type":
// "stop"} to end with the final text or {"type":"cancel"} to end without it.
// The server sends ready, then partials, then final or error, and closes.
func (s *Server) handleDictationStream(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.dictation, dictationNotConfigured) {
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var mu sync.Mutex
	send := func(e dictation.Event) {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.WriteJSON(e)
	}
	closeNormally := func() {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sess, release, err := s.dictation.Begin(ctx, r.URL.Query().Get("language"), send)
	if err != nil {
		code := dictation.CodeEngineFailed
		if de, ok := errors.AsType[*dictation.Error](err); ok {
			code = de.Code
		}
		send(dictation.Event{Type: dictation.EventError, Code: code, Message: err.Error()})
		closeNormally()
		return
	}
	defer release()
	send(dictation.Event{Type: dictation.EventReady})

	go readDictation(conn, sess, send)
	sess.Run(ctx)
	closeNormally()
}

// readDictation feeds the client's audio to sess until the client stops,
// cancels or goes away.
func readDictation(conn *websocket.Conn, sess *dictation.Session, send func(dictation.Event)) {
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			sess.Cancel()
			return
		}
		if kind == websocket.BinaryMessage {
			if len(data)%2 != 0 {
				send(dictation.Event{Type: dictation.EventError, Code: dictation.CodeBadAudio, Message: "audio must be 16-bit samples"})
				sess.Cancel()
				return
			}
			samples := make([]int16, len(data)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
			}
			sess.Write(samples)
			continue
		}
		var msg dictationControl
		_ = json.Unmarshal(data, &msg)
		switch msg.Type {
		case "stop":
			sess.Stop()
		case "cancel":
			sess.Cancel()
		}
	}
}
