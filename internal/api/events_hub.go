package api

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event type constants.
const (
	EventMessageCreated = "message.created"
	EventAgentStatus    = "agent.status"
)

// Event represents a server-sent event to WebSocket clients.
type Event struct {
	Type      string `json:"type"`
	ChannelID string `json:"channel_id"`
	Data      any    `json:"data"`
	Timestamp int64  `json:"timestamp"`
}

// MessageData is the data payload for message.created events.
type MessageData struct {
	MsgID      string `json:"msg_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	IsBot      bool   `json:"is_bot"`
}

// AgentStatusData is the data payload for agent.status events.
type AgentStatusData struct {
	Status string `json:"status"` // "running", "completed", "error"
	Error  string `json:"error,omitempty"`
}

// EventsHub manages WebSocket event subscribers and broadcasts events.
type EventsHub struct {
	mu          sync.RWMutex
	subscribers map[*eventConn]struct{}
	logger      *slog.Logger
}

type eventConn struct {
	conn     *websocket.Conn
	channels map[string]struct{} // subscribed channel IDs; empty = all
	writeMu  sync.Mutex
}

// NewEventsHub creates a new EventsHub.
func NewEventsHub(logger *slog.Logger) *EventsHub {
	return &EventsHub{
		subscribers: make(map[*eventConn]struct{}),
		logger:      logger,
	}
}

// Register adds a WebSocket connection as a subscriber.
func (h *EventsHub) Register(conn *websocket.Conn, channels []string) *eventConn {
	ec := &eventConn{
		conn:     conn,
		channels: make(map[string]struct{}, len(channels)),
	}
	for _, ch := range channels {
		ec.channels[ch] = struct{}{}
	}

	h.mu.Lock()
	h.subscribers[ec] = struct{}{}
	h.mu.Unlock()
	return ec
}

// Unregister removes a subscriber.
func (h *EventsHub) Unregister(ec *eventConn) {
	h.mu.Lock()
	delete(h.subscribers, ec)
	h.mu.Unlock()
}

// Broadcast sends an event to all subscribers whose channel filter matches.
func (h *EventsHub) Broadcast(evt Event) {
	evt.Timestamp = time.Now().UnixMilli()
	data, err := json.Marshal(evt)
	if err != nil {
		h.logger.Error("events hub: marshal failed", "error", err, "type", evt.Type)
		return
	}

	h.mu.RLock()
	subs := make([]*eventConn, 0, len(h.subscribers))
	for ec := range h.subscribers {
		subs = append(subs, ec)
	}
	h.mu.RUnlock()

	for _, ec := range subs {
		ec.writeMu.Lock()
		if len(ec.channels) > 0 {
			if _, ok := ec.channels[evt.ChannelID]; !ok {
				ec.writeMu.Unlock()
				continue
			}
		}
		err := ec.conn.WriteMessage(websocket.TextMessage, data)
		ec.writeMu.Unlock()
		if err != nil {
			h.logger.Error("events hub: write failed, unregistering client", "error", err)
			h.Unregister(ec)
		}
	}
}

// BroadcastMessageCreated sends a message.created event.
func (h *EventsHub) BroadcastMessageCreated(channelID string, data MessageData) {
	h.Broadcast(Event{
		Type:      EventMessageCreated,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAgentStatus sends an agent.status event.
func (h *EventsHub) BroadcastAgentStatus(channelID string, data AgentStatusData) {
	h.Broadcast(Event{
		Type:      EventAgentStatus,
		ChannelID: channelID,
		Data:      data,
	})
}
