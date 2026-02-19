package api

import (
	"context"

	"github.com/radutopala/loop/internal/db"
)

// MessageSender can send messages to channels or threads.
type MessageSender interface {
	PostMessage(ctx context.Context, channelID, content string) error
}

// ChannelLister can list and look up channels from the database.
type ChannelLister interface {
	ListChannels(ctx context.Context) ([]*db.Channel, error)
	GetChannel(ctx context.Context, channelID string) (*db.Channel, error)
}
