package api

import (
	"context"
	"fmt"

	"github.com/radutopala/loop/internal/db"
)

// ThreadCreator can create and delete Discord threads.
type ThreadCreator interface {
	CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error)
	DeleteThread(ctx context.Context, threadID string) error
}

// ThreadEnsurer manages threads in Discord and the DB.
type ThreadEnsurer interface {
	CreateThread(ctx context.Context, channelID, name, authorID, message string) (string, error)
	DeleteThread(ctx context.Context, threadID string) error
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

func (s *threadService) DeleteThread(ctx context.Context, threadID string) error {
	ch, err := s.store.GetChannel(ctx, threadID)
	if err != nil {
		return fmt.Errorf("looking up thread: %w", err)
	}
	if ch == nil {
		return fmt.Errorf("thread %s not found", threadID)
	}
	if ch.ParentID == "" {
		return fmt.Errorf("channel %s is not a thread", threadID)
	}

	if err := s.creator.DeleteThread(ctx, threadID); err != nil {
		return fmt.Errorf("deleting discord thread: %w", err)
	}

	if err := s.store.DeleteChannel(ctx, threadID); err != nil {
		return fmt.Errorf("deleting thread from db: %w", err)
	}

	return nil
}

func (s *threadService) CreateThread(ctx context.Context, channelID, name, authorID, message string) (string, error) {
	parent, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up parent channel: %w", err)
	}
	if parent == nil {
		return "", fmt.Errorf("parent channel %s not found", channelID)
	}

	threadID, err := s.creator.CreateThread(ctx, channelID, name, authorID, message)
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
