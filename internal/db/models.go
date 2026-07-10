package db

import (
	"time"

	"github.com/radutopala/loop/internal/types"
)

// Channel represents a chat platform channel where the bot operates.
type Channel struct {
	ID          int64             `json:"id"`
	ChannelID   string            `json:"channel_id"`
	GuildID     string            `json:"guild_id"`
	Name        string            `json:"name"`
	DirPath     string            `json:"dir_path"`
	ParentID    string            `json:"parent_id"`
	Platform    types.Platform    `json:"platform"`
	Active      bool              `json:"active"`
	SessionID   string            `json:"session_id"`
	Permissions types.Permissions `json:"permissions"`
	Worktree    bool              `json:"worktree"`
	// BaseBranch is the branch a worktree thread was created from. Empty for
	// non-worktree channels and for worktrees created before this was tracked.
	BaseBranch string    `json:"base_branch"`
	Locked     bool      `json:"locked"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// StaleRunningMessage describes a (channel_id, msg_id) pair returned by
// ResetStaleRunningMessages so the caller can broadcast per-channel
// messages.processed events for rows it cleaned up at startup.
type StaleRunningMessage struct {
	ChannelID string
	MsgID     string
}

// Paused-channel kinds: the card type a channel is parked on.
const (
	PausedKindAsk  = "ask"
	PausedKindPlan = "plan"
)

// PausedChannel is a persisted ask/plan card park. It mirrors the
// orchestrator's in-memory parked state so a daemon restart can restore it —
// otherwise the card can't rehydrate and the startup pending-message resume
// re-runs the trigger past the unanswered card. Mode preserves the triggering
// run's composer mode (e.g. "plan") so an ask answered mid-plan resumes in
// plan mode; Data is the broadcast event payload JSON used to rehydrate the
// card.
type PausedChannel struct {
	ChannelID string
	Kind      string
	Mode      string
	Data      string
}

// MessageKind discriminates real chat messages from JSONL-backed agent events
// stored in the same table.
type MessageKind string

const (
	MessageKindMessage    MessageKind = "message"
	MessageKindThinking   MessageKind = "thinking"
	MessageKindToolUse    MessageKind = "tool_use"
	MessageKindToolResult MessageKind = "tool_result"
	MessageKindCompacting MessageKind = "compacting"
)

// Message represents either a chat message or an agent event row. Agent event
// rows (kind != "message") carry inline content captured from the docker
// stream at run time; ToolName / IsError add the metadata that doesn't fit in
// Content.
//
// IsTriggered / IsRunning / Priority / Mode drive the DB-pull processor: the
// orchestrator's per-channel processor loop reads these to claim the next
// eligible row (ORDER BY priority DESC, id ASC). IsTriggered marks rows the
// agent should run on (bot mention, DM, prefix, reply). IsRunning is the
// per-row claim flag. Priority lets an interrupt-prompt insert in front of
// queued rows without deleting them. Mode persists "plan" vs "" so the
// processor can rebuild the agent request.
type Message struct {
	ID            int64       `json:"id"`
	ChatID        int64       `json:"chat_id"`
	ChannelID     string      `json:"channel_id"`
	MsgID         string      `json:"msg_id"`
	AuthorID      string      `json:"author_id"`
	AuthorName    string      `json:"author_name"`
	Content       string      `json:"content"`
	IsBot         bool        `json:"is_bot"`
	IsProcessed   bool        `json:"is_processed"`
	IsTriggered   bool        `json:"is_triggered,omitempty"`
	IsRunning     bool        `json:"is_running,omitempty"`
	Priority      int         `json:"priority,omitempty"`
	Mode          string      `json:"mode,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	Kind          MessageKind `json:"kind"`
	ChainPosition int64       `json:"chain_position"`
	ToolUseID     string      `json:"tool_use_id,omitempty"`
	ToolName      string      `json:"tool_name,omitempty"`
	IsError       bool        `json:"is_error,omitempty"`
	// TriggerMsgID is the msg_id of the user message that triggered the agent
	// run that produced this row. Set on bot replies and agent-event rows
	// (thinking, tool_use, tool_result, compacting). Empty for user-authored
	// rows and for legacy pre-migration rows.
	TriggerMsgID string `json:"trigger_msg_id,omitempty"`
}

// ScheduledTask represents a task scheduled for execution.
type ScheduledTask struct {
	ID              int64     `json:"id"`
	ChannelID       string    `json:"channel_id"`
	GuildID         string    `json:"guild_id"`
	Schedule        string    `json:"schedule"`
	Type            TaskType  `json:"type"`
	Prompt          string    `json:"prompt"`
	Enabled         bool      `json:"enabled"`
	NextRunAt       time.Time `json:"next_run_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	TemplateName    string    `json:"template_name"`
	AutoDeleteSec   int       `json:"auto_delete_sec"`
	ThreadID        string    `json:"thread_id"`
	Worktree        bool      `json:"worktree"`
	OriginBranch    string    `json:"origin_branch"`
	UpdateBeforeRun bool      `json:"update_before_run"`
	Running         bool      `json:"running"`
	WorkflowName    string    `json:"workflow_name"`
	WorkflowInputs  string    `json:"workflow_inputs"`
}

// TaskType represents the type of scheduled task.
type TaskType string

const (
	TaskTypeCron     TaskType = "cron"
	TaskTypeInterval TaskType = "interval"
	TaskTypeOnce     TaskType = "once"
	// TaskTypeManual tasks have no schedule and are never auto-run by the
	// poller; they only execute when triggered explicitly ("run now").
	TaskTypeManual TaskType = "manual"
)

// TaskRunLog records the execution history of a scheduled task.
type TaskRunLog struct {
	ID           int64     `json:"id"`
	TaskID       int64     `json:"task_id"`
	Status       RunStatus `json:"status"`
	ResponseText string    `json:"response_text"`
	ErrorText    string    `json:"error_text"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

// RunStatus represents the execution status of a task run.
type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
)

// WorkflowRunStatus represents the status of a workflow run.
type WorkflowRunStatus string

const (
	WorkflowRunStatusRunning   WorkflowRunStatus = "running"
	WorkflowRunStatusCompleted WorkflowRunStatus = "completed"
	WorkflowRunStatusFailed    WorkflowRunStatus = "failed"
	WorkflowRunStatusPaused    WorkflowRunStatus = "paused"
	WorkflowRunStatusCancelled WorkflowRunStatus = "cancelled"
)

// NodeRunStatus represents the status of a workflow node run.
type NodeRunStatus string

const (
	NodeRunStatusPending NodeRunStatus = "pending"
	NodeRunStatusRunning NodeRunStatus = "running"
	NodeRunStatusSuccess NodeRunStatus = "success"
	NodeRunStatusFailed  NodeRunStatus = "failed"
	NodeRunStatusSkipped NodeRunStatus = "skipped"
)

// WorkflowRun records the execution of a workflow.
type WorkflowRun struct {
	ID           string            `json:"id"`
	WorkflowName string            `json:"workflow_name"`
	ChannelID    string            `json:"channel_id"`
	DirPath      string            `json:"dir_path"`
	WorktreePath string            `json:"worktree_path"`
	Status       WorkflowRunStatus `json:"status"`
	Inputs       string            `json:"inputs"`
	PausedNodeID string            `json:"paused_node_id"`
	ErrorText    string            `json:"error_text"`
	WorkflowDef  string            `json:"workflow_def,omitempty"` // JSON snapshot of definition at start
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   *time.Time        `json:"finished_at"`
}

// NodeRun records the execution of a single workflow node.
type NodeRun struct {
	ID              int64         `json:"id"`
	RunID           string        `json:"run_id"`
	NodeID          string        `json:"node_id"`
	Iteration       int           `json:"iteration"`
	Status          NodeRunStatus `json:"status"`
	Output          string        `json:"output"`
	ErrorText       string        `json:"error_text"`
	Attempt         int           `json:"attempt"`
	StartedAt       *time.Time    `json:"started_at"`
	FinishedAt      *time.Time    `json:"finished_at"`
	LastHeartbeatAt *time.Time    `json:"last_heartbeat_at,omitempty"`
}

// MemoryFileInfo holds a distinct file_path + dir_path pair from the memory_files table.
type MemoryFileInfo struct {
	FilePath string `json:"file_path"`
	DirPath  string `json:"dir_path"`
}

// MemoryFile represents an indexed memory file (or chunk) with its embedding.
// ChunkIndex 0 is the header row (stores content_hash for the whole file).
// ChunkIndex 1+ are content chunks with embeddings for large files.
// Small files use a single row with ChunkIndex 0 that has both hash and embedding.
type MemoryFile struct {
	ID          int64     `json:"id"`
	FilePath    string    `json:"file_path"`
	ChunkIndex  int       `json:"chunk_index"`
	Content     string    `json:"content"`
	ContentHash string    `json:"content_hash"`
	Embedding   []byte    `json:"embedding"`
	Dimensions  int       `json:"dimensions"`
	DirPath     string    `json:"dir_path"`
	UpdatedAt   time.Time `json:"updated_at"`
}
