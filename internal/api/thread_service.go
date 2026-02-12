package api

import (
	"context"
	"fmt"

	"github.com/radutopala/loop/internal/db"
)

// ThreadCreator can create Discord threads.
type ThreadCreator interface {
	CreateThread(ctx context.Context, channelID, name, mentionUserID string) (string, error)
}

// ThreadEnsurer creates a thread in a channel and registers it in the DB.
type ThreadEnsurer interface {
	CreateThread(ctx context.Context, channelID, name, authorID string) (string, error)
}

type threadService struct {
	store   db.Store
	creator ThreadCreator
}

// NewThreadService creates a new ThreadEnsurer.
func NewThreadService(store db.Store, creator ThreadCreator) ThreadEnsurer {
	return &threadService{
		store:   store,
		creator: creator,
	}
}

func (s *threadService) CreateThread(ctx context.Context, channelID, name, authorID string) (string, error) {
	parent, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up parent channel: %w", err)
	}
	if parent == nil {
		return "", fmt.Errorf("parent channel %s not found", channelID)
	}

	threadID, err := s.creator.CreateThread(ctx, channelID, name, authorID)
	if err != nil {
		return "", fmt.Errorf("creating discord thread: %w", err)
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: threadID,
		GuildID:   parent.GuildID,
		Name:      name,
		DirPath:   parent.DirPath,
		ParentID:  channelID,
		SessionID: parent.SessionID,
		Active:    true,
	}); err != nil {
		return "", fmt.Errorf("storing thread mapping: %w", err)
	}

	return threadID, nil
}
