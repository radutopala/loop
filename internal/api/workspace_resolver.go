package api

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/radutopala/loop/internal/db"
)

// workspaceResolver resolves request-supplied dir_path / channel_id pairs to
// concrete workspace directories via the channel store. It is the shared
// path-resolution layer used by the memory, files, git, agent-config, and
// quality/review/playground domains — extracted from Server so those
// consumers depend on exactly this, not the whole server.
type workspaceResolver struct {
	store   ChannelLister
	loopDir string // set alongside Server.loopDir; "" until SetLoopDir runs
}

// resolveDirPath returns the dir_path from the request, resolving via
// channel_id lookup if needed.
func (w *workspaceResolver) resolveDirPath(ctx context.Context, dirPath, channelID string) (string, error) {
	if dirPath != "" {
		return dirPath, nil
	}
	if channelID == "" {
		return "", fmt.Errorf("dir_path or channel_id is required")
	}
	if w.store == nil {
		return "", fmt.Errorf("channel lookup not configured")
	}
	ch, err := w.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up channel: %w", err)
	}
	if ch == nil {
		return "", fmt.Errorf("channel %s not found", channelID)
	}
	if ch.DirPath == "" {
		// Fall back to the default work dir for channels without a project dir.
		if w.loopDir != "" {
			return filepath.Join(w.loopDir, channelID, "work"), nil
		}
		return "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, nil
}

// resolveParentDirPath returns the root project's dir_path for a channel that
// is (or lives under) a worktree chain, or "" for channels outside such a chain
// (and for any lookup error — callers treat that as "no parent" rather than a
// hard failure). Used by the quality engine config-merge layer so worktree
// scans see the parent project's `.loop/config.json` overrides.
func (w *workspaceResolver) resolveParentDirPath(ctx context.Context, channelID string) string {
	if channelID == "" || w.store == nil {
		return ""
	}
	ch, err := w.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return ""
	}
	return worktreeRootDirPath(ctx, w.store, ch)
}

// worktreeRootDirPath returns the DirPath of the nearest non-worktree ancestor
// for an already-fetched channel that is (or lives under) a worktree chain, or
// "" when it isn't part of one. It handles worktree channels, threads that
// share a worktree's dir without carrying the worktree flag (e.g. a task
// thread created under a worktree thread), and nested worktrees. The walk is
// bounded to guard against parent-id cycles. Shared by the config, shortcut,
// workflow, quality, and playground domains so worktree-nested threads resolve
// the root project's .loop/config.json rather than the worktree checkout's
// (which usually has no .loop overrides of its own).
func worktreeRootDirPath(ctx context.Context, store ChannelLister, ch *db.Channel) string {
	cur := ch
	if !cur.Worktree {
		// A thread row under a worktree channel: hop to the worktree itself.
		if cur.ParentID == "" {
			return ""
		}
		p, err := store.GetChannel(ctx, cur.ParentID)
		if err != nil || p == nil || !p.Worktree {
			return ""
		}
		cur = p
	}
	for range 8 {
		if cur.ParentID == "" {
			return ""
		}
		p, err := store.GetChannel(ctx, cur.ParentID)
		if err != nil || p == nil {
			return ""
		}
		if !p.Worktree {
			return p.DirPath
		}
		cur = p
	}
	return ""
}
