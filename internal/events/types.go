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
	BroadcastAgentTasks(channelID string, data AgentTasksEventData)
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
	BroadcastReviewComment(channelID string, data ReviewCommentEventData)
	BroadcastReviewStatus(channelID string, data ReviewStatusEventData)
	BroadcastReviewDiff(channelID string, data ReviewDiffEventData)
}

// ReviewCommentEventData is the payload for review.comment events. Sent
// once per parsed `<review-comment>` block the agent emits during a
// review run, deduplicated by Comment.ID upstream so each id arrives at
// the FE at most once.
type ReviewCommentEventData struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// ReviewStatusEventData is the payload for review.status events. Sent on
// every session status transition so the FE can swap between idle /
// loading / ready / reviewing / error without re-fetching the session.
type ReviewStatusEventData struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ReviewDiffEventData is the payload for review.diff events. Sent when
// the backend re-renders the diff with widened unified context — e.g.
// when an agent-emitted comment lands on a line outside the current
// hunks and `-U` has to grow to absorb it. The FE swaps raw_diff in
// place so the inline view re-parses without losing scroll/expanded
// state.
type ReviewDiffEventData struct {
	RawDiff string `json:"raw_diff"`
}

// MessageEventData is the payload for message.created events.
// Priority is carried so the FE can render queue position ("1/3") — higher
// priority runs before lower; bot messages always carry 0.
type MessageEventData struct {
	MsgID       string `json:"msg_id"`
	AuthorID    string `json:"author_id"`
	AuthorName  string `json:"author_name"`
	Content     string `json:"content"`
	IsBot       bool   `json:"is_bot"`
	IsProcessed bool   `json:"is_processed"`
	Priority    int    `json:"priority,omitempty"`
	// TriggerMsgID is the msg_id of the user message whose run produced this
	// bot reply. Empty for user messages and bot rows not emitted by a run.
	TriggerMsgID string `json:"trigger_msg_id,omitempty"`
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
	// MsgID identifies which user message triggered this run. The FE uses
	// it to label the correct chat row as "processing" — needed because
	// priority-bumped messages can be processed out of chronological order,
	// so the FE can't infer it from array position. Populated on running,
	// completed, and error transitions for the same message.
	MsgID string `json:"msg_id,omitempty"`
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

// AskedChannelEntry is one entry in the parked-AskUserQuestion snapshot
// returned by GET /api/asks/pending. Lives here so the orchestrator (the
// source) and the api package (the consumer) share the wire type without
// either having to import the other.
type AskedChannelEntry struct {
	ChannelID string                   `json:"channel_id"`
	Data      AskUserQuestionEventData `json:"data"`
}

// ExitPlanModeEventData is the payload for agent.exit_plan events.
type ExitPlanModeEventData struct {
	Plan         string `json:"plan"`
	PlanFilePath string `json:"planFilePath,omitempty"`
}

// TaskItem mirrors the on-disk schema written by Claude's TaskCreate/TaskUpdate
// tools. Reconstructed on the loop server from the JSON streamed by the agent's
// claude binary (input on TaskCreate / TaskUpdate, plus the assigned id parsed
// from the TaskCreate tool_result).
type TaskItem struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	ActiveForm  string   `json:"activeForm,omitempty"`
	Status      string   `json:"status"` // "pending" | "in_progress" | "completed" | "deleted"
	Blocks      []string `json:"blocks,omitempty"`
	BlockedBy   []string `json:"blockedBy,omitempty"`
}

// AgentTasksEventData is the payload for agent.tasks events. Tasks holds the
// cumulative list for the channel after the most recent Task* tool call;
// status="deleted" entries are filtered out by the time they reach the FE.
type AgentTasksEventData struct {
	Tasks []TaskItem `json:"tasks"`
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

// ChannelUpdatedData is the payload for channel.updated events. Sent by the
// backend branch poller when a channel's branch, commit, or diff stats change
// so subscribers can refresh the sidebar without re-fetching /api/channels.
type ChannelUpdatedData struct {
	ChannelID     string `json:"channel_id"`
	Branch        string `json:"branch"`
	Commit        string `json:"commit"`
	DiffAdditions int    `json:"diff_additions"`
	DiffDeletions int    `json:"diff_deletions"`
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
//
// Source identifies the originating process inside the agent container so
// the FE can render the card on the right surface:
//   - "chat" — the chat agent (container entrypoint); show inline in chat.
//   - "terminal:<leafId>" — a terminal pane whose exec carried
//     LOOP_TERMINAL_LEAF=<leafId>; show only in that pane.
//
// Derived backend-side from SO_PEERCRED + /proc walking in the in-container
// dockerproxy; the FE consumes it verbatim and matches by string.
type GateApprovalEventData struct {
	ReqID   string            `json:"req_id"`
	Kind    string            `json:"kind"`
	Target  string            `json:"target"`
	Source  string            `json:"source,omitempty"`
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
