package api

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// ChannelCreator can create channels on the chat platform.
type ChannelCreator interface {
	CreateChannel(ctx context.Context, guildID, name string) (string, error)
	InviteUserToChannel(ctx context.Context, channelID, userID string) error
	GetOwnerUserID(ctx context.Context) (string, error)
}

// ChannelEnsurer resolves a directory path to a channel ID,
// creating the channel if it does not yet exist.
type ChannelEnsurer interface {
	EnsureChannel(ctx context.Context, dirPath string) (string, error)
	CreateChannel(ctx context.Context, name, authorID string) (string, error)
}

type channelService struct {
	store    db.Store
	creator  ChannelCreator
	guildID  string
	platform types.Platform
}

// NewChannelService creates a new ChannelEnsurer.
func NewChannelService(store db.Store, creator ChannelCreator, guildID string, platform types.Platform) ChannelEnsurer {
	return &channelService{
		store:    store,
		creator:  creator,
		guildID:  guildID,
		platform: platform,
	}
}

func (s *channelService) CreateChannel(ctx context.Context, name, authorID string) (string, error) {
	channelID, err := s.creator.CreateChannel(ctx, s.guildID, name)
	if err != nil {
		return "", fmt.Errorf("creating channel: %w", err)
	}

	if authorID != "" {
		if err := s.creator.InviteUserToChannel(ctx, channelID, authorID); err != nil {
			return "", fmt.Errorf("inviting user to channel: %w", err)
		}
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		GuildID:   s.guildID,
		Name:      name,
		Platform:  s.platform,
		Active:    true,
	}); err != nil {
		return "", fmt.Errorf("storing channel mapping: %w", err)
	}

	return channelID, nil
}

func (s *channelService) EnsureChannel(ctx context.Context, dirPath string) (string, error) {
	ch, err := s.store.GetChannelByDirPath(ctx, dirPath, s.platform)
	if err != nil {
		return "", fmt.Errorf("looking up channel by dir path: %w", err)
	}
	if ch != nil {
		return ch.ChannelID, nil
	}

	name := filepath.Base(dirPath)
	channelID, err := s.creator.CreateChannel(ctx, s.guildID, name)
	if err != nil {
		return "", fmt.Errorf("creating channel: %w", err)
	}

	if ownerID, ownerErr := s.creator.GetOwnerUserID(ctx); ownerErr == nil && ownerID != "" {
		_ = s.creator.InviteUserToChannel(ctx, channelID, ownerID)
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		GuildID:   s.guildID,
		Name:      name,
		DirPath:   dirPath,
		Platform:  s.platform,
		Active:    true,
	}); err != nil {
		return "", fmt.Errorf("storing channel mapping: %w", err)
	}

	return channelID, nil
}
