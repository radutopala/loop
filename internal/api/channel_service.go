package api

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/radutopala/loop/internal/db"
)

// ChannelCreator can create Discord channels.
type ChannelCreator interface {
	CreateChannel(ctx context.Context, guildID, name string) (string, error)
}

// ChannelEnsurer resolves a directory path to a Discord channel ID,
// creating the channel if it does not yet exist.
type ChannelEnsurer interface {
	EnsureChannel(ctx context.Context, dirPath string) (string, error)
}

type channelService struct {
	store   db.Store
	creator ChannelCreator
	guildID string
}

// NewChannelService creates a new ChannelEnsurer.
func NewChannelService(store db.Store, creator ChannelCreator, guildID string) ChannelEnsurer {
	return &channelService{
		store:   store,
		creator: creator,
		guildID: guildID,
	}
}

func (s *channelService) EnsureChannel(ctx context.Context, dirPath string) (string, error) {
	ch, err := s.store.GetChannelByDirPath(ctx, dirPath)
	if err != nil {
		return "", fmt.Errorf("looking up channel by dir path: %w", err)
	}
	if ch != nil {
		return ch.ChannelID, nil
	}

	name := filepath.Base(dirPath)
	channelID, err := s.creator.CreateChannel(ctx, s.guildID, name)
	if err != nil {
		return "", fmt.Errorf("creating discord channel: %w", err)
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		GuildID:   s.guildID,
		Name:      name,
		DirPath:   dirPath,
		Active:    true,
	}); err != nil {
		return "", fmt.Errorf("storing channel mapping: %w", err)
	}

	return channelID, nil
}
