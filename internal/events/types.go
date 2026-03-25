package events

// Broadcaster broadcasts events to connected clients.
type Broadcaster interface {
	BroadcastMessageCreated(channelID string, data MessageEventData)
	BroadcastMessageStreaming(channelID string, data MessageStreamingData)
	BroadcastAgentStatus(channelID string, data AgentStatusEventData)
	BroadcastToolUse(channelID string, data ToolUseEventData)
	BroadcastAgentActivity(channelID string, data AgentActivityEventData)
	BroadcastAskUser(channelID string, data AskUserQuestionEventData)
	BroadcastExitPlan(channelID string, data ExitPlanModeEventData)
	BroadcastMessagesProcessed(channelID string, data MessagesProcessedData)
	BroadcastChannelCreated(parentChannelID, channelID string)
	BroadcastChannelDeleted(channelID string)
	BroadcastAgentInstanceRegistered(channelID string, data AgentInstanceEventData)
	BroadcastAgentInstanceUnregistered(channelID string, data AgentInstanceEventData)
	BroadcastAgentInstanceMetadata(channelID string, data AgentInstanceEventData)
}

// MessageEventData is the payload for message.created events.
type MessageEventData struct {
	MsgID       string `json:"msg_id"`
	AuthorID    string `json:"author_id"`
	AuthorName  string `json:"author_name"`
	Content     string `json:"content"`
	IsBot       bool   `json:"is_bot"`
	IsProcessed bool   `json:"is_processed"`
}

// MessagesProcessedData is the payload for messages.processed events.
type MessagesProcessedData struct {
	MsgIDs []string `json:"msg_ids"`
}

// MessageStreamingData is the payload for message.streaming events (partial bot response).
type MessageStreamingData struct {
	Content string `json:"content"`
}

// AgentStatusEventData is the payload for agent.status events.
type AgentStatusEventData struct {
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	DurationMs     int    `json:"duration_ms,omitempty"`
	NumTurns       int    `json:"num_turns,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	Model          string `json:"model,omitempty"`
	TriggerContent string `json:"trigger_content,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
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

// AskUserQuestionEventData is the payload for agent.ask_user events.
type AskUserQuestionEventData struct {
	Questions []AskUserQuestion `json:"questions"`
}

// ExitPlanModeEventData is the payload for agent.exit_plan events.
type ExitPlanModeEventData struct {
	Plan         string `json:"plan"`
	PlanFilePath string `json:"planFilePath,omitempty"`
}

// AskUserQuestion represents a single question from Claude's AskUserQuestion tool.
type AskUserQuestion struct {
	Question    string          `json:"question"`
	Header      string          `json:"header,omitempty"`
	Options     []AskUserOption `json:"options,omitempty"`
	MultiSelect bool            `json:"multi_select,omitempty"`
}

// AgentInstanceEventData is the payload for agent_instance.* events.
type AgentInstanceEventData struct {
	AgentID     string `json:"agent_id"`
	ChannelID   string `json:"channel_id"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status,omitempty"`
	WorkSummary string `json:"work_summary,omitempty"`
}

// AskUserOption represents a selectable option in a question.
type AskUserOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}
