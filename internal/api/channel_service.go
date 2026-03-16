package api

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

var invalidChannelChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// sanitizeChannelName normalises a directory base name into a valid
// Slack/Discord channel name (lowercase, alphanumeric, hyphens, underscores).
func sanitizeChannelName(name string) string {
	name = strings.ToLower(name)
	name = invalidChannelChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "project"
	}
	return name
}

// ChannelCreator can create channels on the chat platform.
type ChannelCreator interface {
	CreateChannel(ctx context.Context, name string) (string, error)
	InviteUserToChannel(ctx context.Context, channelID, userID string) error
	GetOwnerUserID(ctx context.Context) (string, error)
	SetChannelTopic(ctx context.Context, channelID, topic string) error
}

// EnsureResult describes the outcome of ensuring a channel for one platform.
type EnsureResult struct {
	Platform  types.Platform `json:"platform"`
	ChannelID string         `json:"channel_id"`
	Created   bool           `json:"created"`
}

// ChannelEnsurer resolves a directory path to a channel ID,
// creating the channel if it does not yet exist.
type ChannelEnsurer interface {
	EnsureChannel(ctx context.Context, dirPath, platform string) (string, error)
	CreateChannel(ctx context.Context, name, authorID, sourceChannelID, platform string) (string, error)
	EnsureChannelAllPlatforms(ctx context.Context, dirPath string) ([]EnsureResult, error)
}

type channelService struct {
	store      db.Store
	creators   map[types.Platform]ChannelCreator
	randSuffix func() string
}

// NewChannelService creates a new ChannelEnsurer.
func NewChannelService(store db.Store, creators map[types.Platform]ChannelCreator) ChannelEnsurer {
	return &channelService{
		store:      store,
		creators:   creators,
		randSuffix: func() string { return randutil.HexID(2) },
	}
}

func (s *channelService) CreateChannel(ctx context.Context, name, authorID, sourceChannelID, platform string) (string, error) {
	p := types.Platform(platform)
	var guildID string
	if sourceChannelID != "" {
		if ch, err := s.store.GetChannel(ctx, sourceChannelID); err == nil && ch != nil {
			p = ch.Platform
			guildID = ch.GuildID
		}
	}

	creator := s.creators[p]

	var channelID string
	if creator != nil {
		var err error
		channelID, err = creator.CreateChannel(ctx, name)
		if err != nil {
			return "", fmt.Errorf("creating channel: %w", err)
		}

		if authorID != "" && channelID != "" {
			if err := creator.InviteUserToChannel(ctx, channelID, authorID); err != nil {
				return "", fmt.Errorf("inviting user to channel: %w", err)
			}
		}
	}
	if channelID == "" {
		channelID = s.randSuffix() + s.randSuffix() + s.randSuffix()
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		GuildID:   guildID,
		Name:      name,
		Platform:  p,
		Active:    true,
	}); err != nil {
		return "", fmt.Errorf("storing channel mapping: %w", err)
	}

	return channelID, nil
}

func (s *channelService) EnsureChannel(ctx context.Context, dirPath, platform string) (string, error) {
	p := types.Platform(platform)
	ch, err := s.store.GetChannelByDirPath(ctx, dirPath, p)
	if err != nil {
		return "", fmt.Errorf("looking up channel by dir path: %w", err)
	}
	if ch != nil {
		return ch.ChannelID, nil
	}

	creator := s.creators[p]

	name := sanitizeChannelName(filepath.Base(dirPath)) + "-" + s.randSuffix()
	var channelID string
	if creator != nil {
		var err error
		channelID, err = creator.CreateChannel(ctx, name)
		if err != nil {
			return "", fmt.Errorf("creating channel: %w", err)
		}

		if channelID != "" {
			_ = creator.SetChannelTopic(ctx, channelID, dirPath)

			if ownerID, ownerErr := creator.GetOwnerUserID(ctx); ownerErr == nil && ownerID != "" {
				_ = creator.InviteUserToChannel(ctx, channelID, ownerID)
			}
		}
	}
	if channelID == "" {
		channelID = s.randSuffix() + s.randSuffix() + s.randSuffix()
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		Name:      name,
		DirPath:   dirPath,
		Platform:  p,
		Active:    true,
	}); err != nil {
		return "", fmt.Errorf("storing channel mapping: %w", err)
	}

	return channelID, nil
}

func (s *channelService) EnsureChannelAllPlatforms(ctx context.Context, dirPath string) ([]EnsureResult, error) {
	existing, err := s.store.GetChannelsByDirPath(ctx, dirPath)
	if err != nil {
		return nil, fmt.Errorf("looking up channels by dir path: %w", err)
	}
	havePlatform := make(map[types.Platform]string, len(existing))
	for _, ch := range existing {
		havePlatform[ch.Platform] = ch.ChannelID
	}

	var results []EnsureResult
	for platform := range s.creators {
		if id, ok := havePlatform[platform]; ok {
			results = append(results, EnsureResult{Platform: platform, ChannelID: id, Created: false})
			continue
		}
		channelID, err := s.EnsureChannel(ctx, dirPath, string(platform))
		if err != nil {
			return nil, fmt.Errorf("ensuring channel for platform %s: %w", platform, err)
		}
		results = append(results, EnsureResult{Platform: platform, ChannelID: channelID, Created: true})
	}
	return results, nil
}
