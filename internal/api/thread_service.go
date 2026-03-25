package api

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/randutil"
)

// ThreadCreator can create and delete threads on the chat platform.
type ThreadCreator interface {
	CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error)
	DeleteThread(ctx context.Context, threadID string) error
}

// ThreadEnsurer manages threads on the chat platform and the DB.
type ThreadEnsurer interface {
	CreateThread(ctx context.Context, channelID, name, authorID, message string) (string, error)
	DeleteThread(ctx context.Context, threadID string) error
}

type threadService struct {
	store            db.Store
	creator          ThreadCreator
	logger           *slog.Logger
	generateThreadID func() string
	removeMCPConfig  func(string, string) error
	keepMCPConfigs   bool
	removeWorktree   func(ctx context.Context, mainRepoDir, worktreePath string) error
}

// NewThreadService creates a new ThreadEnsurer.
func NewThreadService(store db.Store, creator ThreadCreator, logger *slog.Logger, keepMCPConfigs bool) ThreadEnsurer {
	return &threadService{
		store:            store,
		creator:          creator,
		logger:           logger,
		generateThreadID: func() string { return randutil.HexID(6) },
		removeMCPConfig:  bot.RemoveMCPConfig,
		keepMCPConfigs:   keepMCPConfigs,
		removeWorktree:   removeWorktreeExec,
	}
}

func removeWorktreeExec(ctx context.Context, mainRepoDir, worktreePath string) error {
	// Read the branch checked out in the worktree before removing it.
	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = worktreePath
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))

	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = mainRepoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}

	// Delete the worktree branch if we captured one.
	if branch != "" && branch != "HEAD" {
		delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branch)
		delCmd.Dir = mainRepoDir
		_ = delCmd.Run() // best-effort
	}
	return nil
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

	if !s.keepMCPConfigs {
		if err := s.removeMCPConfig(ch.DirPath, threadID); err != nil {
			s.logger.Warn("removing MCP config for thread", "error", err, "thread_id", threadID)
		}
	}

	if ch.Worktree && ch.DirPath != "" && s.removeWorktree != nil {
		parent, err := s.store.GetChannel(ctx, ch.ParentID)
		if err != nil {
			s.logger.Warn("looking up parent for worktree removal", "error", err, "thread_id", threadID)
		} else if parent != nil && parent.DirPath != "" {
			if err := s.removeWorktree(ctx, parent.DirPath, ch.DirPath); err != nil {
				s.logger.Warn("removing git worktree", "error", err, "thread_id", threadID, "path", ch.DirPath)
			}
		}
	}

	if s.creator != nil {
		if err := s.creator.DeleteThread(ctx, threadID); err != nil {
			return fmt.Errorf("deleting thread: %w", err)
		}
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

	// If channelID is a thread, resolve to its parent channel.
	if parent.ParentID != "" {
		channelID = parent.ParentID
		parent, err = s.store.GetChannel(ctx, channelID)
		if err != nil {
			return "", fmt.Errorf("looking up resolved parent channel: %w", err)
		}
		if parent == nil {
			return "", fmt.Errorf("resolved parent channel %s not found", channelID)
		}
	}

	var threadID string
	if s.creator != nil {
		threadID, err = s.creator.CreateThread(ctx, channelID, name, authorID, message)
		if err != nil {
			return "", fmt.Errorf("creating thread: %w", err)
		}
	}
	if threadID == "" {
		// No-op creator (e.g. local platform) — generate ID locally.
		threadID = s.generateThreadID()
	}

	if err := s.store.UpsertChannel(ctx, &db.Channel{
		ChannelID:   threadID,
		GuildID:     parent.GuildID,
		Name:        name,
		DirPath:     parent.DirPath,
		ParentID:    channelID,
		Platform:    parent.Platform,
		SessionID:   parent.SessionID,
		Permissions: parent.Permissions,
		Active:      true,
	}); err != nil {
		return "", fmt.Errorf("storing thread mapping: %w", err)
	}

	return threadID, nil
}
