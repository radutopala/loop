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
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// MessageKind discriminates real chat messages from JSONL-backed agent events
// stored in the same table.
type MessageKind string

const (
	MessageKindMessage    MessageKind = "message"
	MessageKindThinking   MessageKind = "thinking"
	MessageKindToolUse    MessageKind = "tool_use"
	MessageKindToolResult MessageKind = "tool_result"
)

// Message represents either a chat message or an agent event row. Agent event
// rows (kind != "message") carry inline content captured from the docker
// stream at run time; ToolName / IsError add the metadata that doesn't fit in
// Content.
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
	CreatedAt     time.Time   `json:"created_at"`
	Kind          MessageKind `json:"kind"`
	ChainPosition int64       `json:"chain_position"`
	ToolUseID     string      `json:"tool_use_id,omitempty"`
	ToolName      string      `json:"tool_name,omitempty"`
	IsError       bool        `json:"is_error,omitempty"`
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
