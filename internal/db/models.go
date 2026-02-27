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
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Message represents a chat message stored for context.
type Message struct {
	ID          int64     `json:"id"`
	ChatID      int64     `json:"chat_id"`
	ChannelID   string    `json:"channel_id"`
	MsgID       string    `json:"msg_id"`
	AuthorID    string    `json:"author_id"`
	AuthorName  string    `json:"author_name"`
	Content     string    `json:"content"`
	IsBot       bool      `json:"is_bot"`
	IsProcessed bool      `json:"is_processed"`
	CreatedAt   time.Time `json:"created_at"`
}

// ScheduledTask represents a task scheduled for execution.
type ScheduledTask struct {
	ID            int64     `json:"id"`
	ChannelID     string    `json:"channel_id"`
	GuildID       string    `json:"guild_id"`
	Schedule      string    `json:"schedule"`
	Type          TaskType  `json:"type"`
	Prompt        string    `json:"prompt"`
	Enabled       bool      `json:"enabled"`
	NextRunAt     time.Time `json:"next_run_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	TemplateName  string    `json:"template_name"`
	AutoDeleteSec int       `json:"auto_delete_sec"`
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
