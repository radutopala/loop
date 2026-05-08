package api

import (
	"context"

	"github.com/radutopala/loop/internal/db"
)

// MessageSender can send messages to channels or threads.
type MessageSender interface {
	PostMessage(ctx context.Context, channelID, content string) error
}

// ChannelLister can list, look up, and delete channels and their messages from the database.
type ChannelLister interface {
	ListChannels(ctx context.Context) ([]*db.Channel, error)
	GetChannel(ctx context.Context, channelID string) (*db.Channel, error)
	UpsertChannel(ctx context.Context, ch *db.Channel) error
	GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*db.Message, error)
	SearchMessages(ctx context.Context, query string, limit int) ([]*db.Message, error)
	GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*db.Message, error)
	GetTimeline(ctx context.Context, channelID string, cursorPosition, cursorID int64, limit int) ([]*db.Message, error)
	UpdateSessionID(ctx context.Context, channelID string, sessionID string) error
	UpdateChannelLocked(ctx context.Context, channelID string, locked bool) error
	DeleteChannel(ctx context.Context, channelID string) error
	DeleteChannelsByParentID(ctx context.Context, parentID string) error
	ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]db.MemoryFileInfo, error)
	InsertMessage(ctx context.Context, msg *db.Message) error
	DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error)
	ListTaskRunLogs(ctx context.Context, taskID int64, limit int) ([]*db.TaskRunLog, error)
	ListAllScheduledTasks(ctx context.Context) ([]*db.ScheduledTask, error)
}
