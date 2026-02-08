package db

import "time"

// Channel represents a Discord channel where the bot operates.
type Channel struct {
	ID        int64     `json:"id"`
	ChannelID string    `json:"channel_id"`
	GuildID   string    `json:"guild_id"`
	Name      string    `json:"name"`
	DirPath   string    `json:"dir_path"`
	Active    bool      `json:"active"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message represents a Discord message stored for context.
type Message struct {
	ID           int64     `json:"id"`
	ChatID       int64     `json:"chat_id"`
	ChannelID    string    `json:"channel_id"`
	DiscordMsgID string    `json:"discord_msg_id"`
	AuthorID     string    `json:"author_id"`
	AuthorName   string    `json:"author_name"`
	Content      string    `json:"content"`
	IsBot        bool      `json:"is_bot"`
	IsProcessed  bool      `json:"is_processed"`
	CreatedAt    time.Time `json:"created_at"`
}

// ScheduledTask represents a task scheduled for execution.
type ScheduledTask struct {
	ID        int64     `json:"id"`
	ChannelID string    `json:"channel_id"`
	GuildID   string    `json:"guild_id"`
	Schedule  string    `json:"schedule"`
	Type      TaskType  `json:"type"`
	Prompt    string    `json:"prompt"`
	Enabled   bool      `json:"enabled"`
	NextRunAt time.Time `json:"next_run_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	RunStatusPending RunStatus = "pending"
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
)
