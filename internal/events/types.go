package events

// Broadcaster broadcasts events to connected clients.
type Broadcaster interface {
	BroadcastMessageCreated(channelID string, data MessageEventData)
	BroadcastMessageStreaming(channelID string, data MessageStreamingData)
	BroadcastAgentStatus(channelID string, data AgentStatusEventData)
	BroadcastToolUse(channelID string, data ToolUseEventData)
	BroadcastAgentThinking(channelID string, data AgentThinkingEventData)
	BroadcastToolResult(channelID string, data ToolResultEventData)
	BroadcastAgentActivity(channelID string, data AgentActivityEventData)
	BroadcastAskUser(channelID string, data AskUserQuestionEventData)
	BroadcastExitPlan(channelID string, data ExitPlanModeEventData)
	BroadcastTodoWrite(channelID string, data TodoWriteEventData)
	BroadcastMessagesProcessed(channelID string, data MessagesProcessedData)
	BroadcastMessageDeleted(channelID string, data MessageDeletedData)
	BroadcastChannelCreated(parentChannelID, channelID string)
	BroadcastChannelDeleted(channelID string)
	BroadcastAgentInstanceRegistered(channelID string, data AgentInstanceEventData)
	BroadcastAgentInstanceUnregistered(channelID string, data AgentInstanceEventData)
	BroadcastAgentInstanceMetadata(channelID string, data AgentInstanceEventData)
	BroadcastImageBuildStatus(data ImageBuildStatusData)
	BroadcastImageUpdateAvailable(data ImageUpdateAvailableData)
	BroadcastGateApprovalRequested(channelID string, data GateApprovalEventData)
	BroadcastGateApprovalResolved(channelID string, data GateApprovalResolvedData)
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

// MessageDeletedData is the payload for message.deleted events.
type MessageDeletedData struct {
	MsgID string `json:"msg_id"`
}

// MessageStreamingData is the payload for message.streaming events (partial bot response).
type MessageStreamingData struct {
	Content string `json:"content"`
}

// AgentStatusEventData is the payload for agent.status events.
type AgentStatusEventData struct {
	Status         string `json:"status"`
	RunID          string `json:"run_id,omitempty"`
	Error          string `json:"error,omitempty"`
	DurationMs     int    `json:"duration_ms,omitempty"`
	NumTurns       int    `json:"num_turns,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	Model          string `json:"model,omitempty"`
	TriggerContent string `json:"trigger_content,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
	// Trigger identifies what kicked off the run — "scheduled" for runs
	// driven by the task scheduler, empty for user-message runs. The
	// renderer uses this to suppress the macOS dock bounce on scheduled
	// completions (they happen often and aren't user-actionable).
	Trigger string `json:"trigger,omitempty"`
}

// ToolUseEventData is the payload for tool.use events.
// ToolUseID is the per-block id from the assistant message; pairs with the
// matching ToolResultEventData carrying the same id when the tool finishes.
type ToolUseEventData struct {
	ToolUseID string `json:"tool_use_id,omitempty"`
	ToolName  string `json:"tool_name"`
	Input     string `json:"input"`
}

// AgentThinkingEventData is the payload for agent.thinking events.
type AgentThinkingEventData struct {
	Text string `json:"text"`
}

// ToolResultEventData is the payload for tool.result events. Output is already
// truncated by the runner; full content remains in the JSONL and may be
// hydrated later by /timeline.
type ToolResultEventData struct {
	ToolUseID string `json:"tool_use_id,omitempty"`
	Output    string `json:"output"`
	IsError   bool   `json:"is_error,omitempty"`
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

// TodoItem represents a single todo item from Claude's TodoWrite tool.
type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`     // "completed", "in_progress", "pending"
	ActiveForm string `json:"activeForm"` // present-continuous form shown during execution
}

// TodoWriteEventData is the payload for agent.todos events.
type TodoWriteEventData struct {
	Todos []TodoItem `json:"todos"`
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

// ImageBuildStatusData is the payload for image.build_status events.
type ImageBuildStatusData struct {
	State string `json:"state"`           // "idle", "building", "completed", "failed"
	Phase string `json:"phase,omitempty"` // "removing", "building", ""
	Error string `json:"error,omitempty"`
}

// ImageUpdateAvailableData is the payload for image.update_available events.
type ImageUpdateAvailableData struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Component      string `json:"component"` // "claude_code"
}

// TaskEventData is the payload for task.created and task.updated events.
type TaskEventData struct {
	TaskID    int64  `json:"task_id"`
	ChannelID string `json:"channel_id"`
}

// TaskRunEventData is the payload for task.run_completed events.
type TaskRunEventData struct {
	TaskID    int64  `json:"task_id"`
	RunID     int64  `json:"run_id"`
	Status    string `json:"status"`
	ChannelID string `json:"channel_id"`
}

// WorkflowRunEventData is the payload for workflow.run.* events.
type WorkflowRunEventData struct {
	RunID        string `json:"run_id"`
	WorkflowName string `json:"workflow_name"`
	ChannelID    string `json:"channel_id"`
	Status       string `json:"status"`
	PausedNodeID string `json:"paused_node_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

// GateApprovalEventData is the payload for gate.approval_requested events.
// ReqID is the gate-server-assigned correlation id; the frontend echoes it
// back on its resolve POST so the Manager can route the decision. Details
// are optional structured key/value pairs (e.g. image, binds, privileged
// for a docker create) the UI can render alongside Target.
type GateApprovalEventData struct {
	ReqID   string            `json:"req_id"`
	Kind    string            `json:"kind"`
	Target  string            `json:"target"`
	Message string            `json:"message,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// GateApprovalResolvedData is the payload for gate.approval_resolved events.
// Broadcast after a decision is recorded so the UI can dismiss the card.
type GateApprovalResolvedData struct {
	ReqID    string `json:"req_id"`
	Decision string `json:"decision,omitempty"`
	Actor    string `json:"actor,omitempty"`
}

// WorkflowNodeEventData is the payload for workflow.node.* events.
type WorkflowNodeEventData struct {
	RunID  string `json:"run_id"`
	NodeID string `json:"node_id"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
}
