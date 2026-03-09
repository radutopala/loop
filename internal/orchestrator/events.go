package orchestrator

// EventBroadcaster broadcasts events to connected clients.
type EventBroadcaster interface {
	BroadcastMessageCreated(channelID string, data MessageEventData)
	BroadcastMessageStreaming(channelID string, data MessageStreamingData)
	BroadcastAgentStatus(channelID string, data AgentStatusEventData)
}

// MessageEventData is the payload for message.created events.
type MessageEventData struct {
	MsgID      string `json:"msg_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	IsBot      bool   `json:"is_bot"`
}

// MessageStreamingData is the payload for message.streaming events (partial bot response).
type MessageStreamingData struct {
	Content string `json:"content"`
}

// AgentStatusEventData is the payload for agent.status events.
type AgentStatusEventData struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
