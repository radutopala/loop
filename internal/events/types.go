package events

// Broadcaster broadcasts events to connected clients.
type Broadcaster interface {
	BroadcastMessageCreated(channelID string, data MessageEventData)
	BroadcastMessageStreaming(channelID string, data MessageStreamingData)
	BroadcastAgentStatus(channelID string, data AgentStatusEventData)
	BroadcastToolUse(channelID string, data ToolUseEventData)
	BroadcastAgentActivity(channelID string, data AgentActivityEventData)
	BroadcastChannelCreated(parentChannelID, channelID string)
	BroadcastChannelDeleted(channelID string)
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
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
	NumTurns   int    `json:"num_turns,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	Model      string `json:"model,omitempty"`
}

// ToolUseEventData is the payload for tool.use events.
type ToolUseEventData struct {
	ToolName string `json:"tool_name"`
	Input    string `json:"input"`
}

// AgentActivityEventData is the payload for agent.activity events.
// Activity can be "model", "subagent_started", "subagent_progress", "compacting".
type AgentActivityEventData struct {
	Activity    string `json:"activity"`
	Model       string `json:"model,omitempty"`
	Description string `json:"description,omitempty"`
}
