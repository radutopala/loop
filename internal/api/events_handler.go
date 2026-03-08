package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

const eventMsgSubscribe = "subscribe"

// eventSubscribeMessage is the JSON message a client sends to subscribe to channels.
type eventSubscribeMessage struct {
	Type     string   `json:"type"`
	Channels []string `json:"channels,omitempty"`
}

func (s *Server) handleEventsWS(w http.ResponseWriter, r *http.Request) {
	if s.eventsHub == nil {
		http.Error(w, "events not configured", http.StatusNotImplemented)
		return
	}

	// Allow subscribing via query param for simple clients.
	var channels []string
	if q := r.URL.Query().Get("channels"); q != "" {
		channels = strings.Split(q, ",")
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("events websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	ec := s.eventsHub.Register(conn, channels)
	defer s.eventsHub.Unregister(ec)

	// Read loop: handle subscribe messages or detect disconnect.
	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg eventSubscribeMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		if msg.Type == eventMsgSubscribe {
			ec.writeMu.Lock()
			ec.channels = make(map[string]struct{}, len(msg.Channels))
			for _, ch := range msg.Channels {
				ec.channels[ch] = struct{}{}
			}
			ec.writeMu.Unlock()
		}
	}
}
