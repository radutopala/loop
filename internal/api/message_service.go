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
	GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*db.Message, error)
	SearchMessages(ctx context.Context, query string, limit int) ([]*db.Message, error)
	GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*db.Message, error)
	DeleteChannel(ctx context.Context, channelID string) error
	DeleteChannelsByParentID(ctx context.Context, parentID string) error
	ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]db.MemoryFileInfo, error)
}
