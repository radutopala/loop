package db

import "context"

// ChannelGetter is the minimal store surface WorktreeRootDirPath needs. Keeping
// it narrow lets callers pass their own store interfaces (or mocks) without
// depending on the full Store.
type ChannelGetter interface {
	GetChannel(ctx context.Context, channelID string) (*Channel, error)
}

// worktreeChainDepth bounds the ancestor walk so a parent-id cycle can't spin
// forever. Real nesting is one or two levels; eight is slack, not a limit.
const worktreeChainDepth = 8

// WorktreeRootDirPath returns the DirPath of the nearest non-worktree ancestor
// for a channel that is (or lives under) a worktree chain, or "" when the
// channel isn't part of one. It handles worktree channels, threads that share a
// worktree's dir without carrying the worktree flag (e.g. a scheduled task's
// thread), and arbitrary nesting (a worktree created from another worktree).
//
// Callers use it to anchor the config merge chain at the root checkout: a
// worktree's own .loop/config.json is untracked and usually holds nothing but
// extra_dirs, so resolving against the worktree alone silently drops the root
// project's mounts, container image, gates, model, and MCP servers.
//
// Any lookup error is treated as "no parent" rather than a hard failure — the
// callers all degrade to the global config, which is the pre-existing behavior.
func WorktreeRootDirPath(ctx context.Context, store ChannelGetter, ch *Channel) string {
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
	for range worktreeChainDepth {
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
