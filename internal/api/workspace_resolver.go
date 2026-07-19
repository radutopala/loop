package api

import (
	"context"
	"fmt"
	"path/filepath"
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

// resolveParentDirPath returns the parent project's dir_path for a
// worktree channel, or "" for non-worktree channels (and for any
// lookup error — callers treat that as "no parent" rather than a hard
// failure). Used by the quality engine config-merge layer so worktree
// scans see the parent project's `.loop/config.json` overrides.
func (w *workspaceResolver) resolveParentDirPath(ctx context.Context, channelID string) string {
	if channelID == "" || w.store == nil {
		return ""
	}
	ch, err := w.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil || !ch.Worktree || ch.ParentID == "" {
		return ""
	}
	parent, err := w.store.GetChannel(ctx, ch.ParentID)
	if err != nil || parent == nil {
		return ""
	}
	return parent.DirPath
}
