package api

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
)

const channelsNotConfiguredMsg = "channel creation not configured (discord_guild_id not set or Slack not configured)"

type ensureChannelRequest struct {
	DirPath  string `json:"dir_path"`
	Platform string `json:"platform,omitempty"`
}

type ensureChannelResponse struct {
	ChannelID string `json:"channel_id"`
}

type createChannelRequest struct {
	Name      string `json:"name"`
	AuthorID  string `json:"author_id"`
	ChannelID string `json:"channel_id"`
	Platform  string `json:"platform,omitempty"`
}

type createChannelResponse struct {
	ChannelID string `json:"channel_id"`
}

type channelResponse struct {
	ChannelID        string `json:"channel_id"`
	Name             string `json:"name"`
	DirPath          string `json:"dir_path"`
	ParentID         string `json:"parent_id"`
	SessionID        string `json:"session_id"`
	Active           bool   `json:"active"`
	ContainerRunning bool   `json:"container_running"`
	AgentRunning     bool   `json:"agent_running"`
	Branch           string `json:"branch,omitempty"`
	Commit           string `json:"commit,omitempty"`
	Worktree         bool   `json:"worktree"`
	BaseBranch       string `json:"base_branch,omitempty"`
	Locked           bool   `json:"locked"`
	DiffAdditions    int    `json:"diff_additions,omitempty"`
	DiffDeletions    int    `json:"diff_deletions,omitempty"`
	ReviewEnabled    bool   `json:"review_enabled"`
}

func (s *Server) handleEnsureChannel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.channels, channelsNotConfiguredMsg) {
		return
	}

	var req ensureChannelRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.DirPath == "" {
		http.Error(w, "dir_path is required", http.StatusBadRequest)
		return
	}

	channelID, err := s.channels.EnsureChannel(r.Context(), req.DirPath, req.Platform)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, ensureChannelResponse{ChannelID: channelID}, s.logger)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.channels, channelsNotConfiguredMsg) {
		return
	}

	var req createChannelRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	channelID, err := s.channels.CreateChannel(r.Context(), req.Name, req.AuthorID, req.ChannelID, req.Platform)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusCreated, createChannelResponse{ChannelID: channelID}, s.logger)
}

func (s *Server) handleSearchChannels(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channels, err := s.store.ListChannels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get running container channel IDs if a registry is configured.
	var runningIDs map[string]struct{}
	if s.containerRegistry != nil {
		runningIDs = s.containerRegistry.RunningChannelIDs(r.Context())
	}

	// Get channels with active chat agent runs.
	var chatRunIDs map[string]struct{}
	if s.activeChatLister != nil {
		chatRunIDs = s.activeChatLister.ActiveChatChannelIDs()
	}

	query := r.URL.Query().Get("query")
	platformFilter := r.URL.Query().Get("platform")

	// Build a channel-id → channel index once so per-row parent lookups
	// for review-enabled resolution stay in-memory instead of issuing
	// O(N) GetChannel queries against the store.
	byID := make(map[string]*db.Channel, len(channels))
	for _, ch := range channels {
		byID[ch.ChannelID] = ch
	}

	// Git state comes from the branch poller's per-dir snapshot (computed
	// once per tick, deduped across channels sharing a worktree dir) instead
	// of spawning git subprocesses per channel per request. Dirs the poller
	// hasn't covered yet (fresh channel between ticks, or no poller in tests)
	// are computed inline, once per unique dir within this request.
	gitStates := make(map[string]gitState)
	gitStateFor := func(dir string) gitState {
		if st, ok := gitStates[dir]; ok {
			return st
		}
		st, ok := gitState{}, false
		if s.branchPoller != nil {
			st, ok = s.branchPoller.Snapshot(dir)
		}
		if !ok {
			st = collectGitState(r.Context(), dir)
		}
		gitStates[dir] = st
		return st
	}

	resp := make([]channelResponse, 0, len(channels))
	for _, ch := range channels {
		if platformFilter != "" && string(ch.Platform) != platformFilter {
			continue
		}
		if query != "" && !containsFold(ch.Name, query) {
			continue
		}
		_, running := runningIDs[ch.ChannelID]
		_, runningBot := chatRunIDs[ch.ChannelID]
		dirPath := ch.DirPath
		if dirPath == "" && s.loopDir != "" {
			dirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
		}
		git := gitStateFor(dirPath)
		parentDirPath := ""
		if ch.Worktree && ch.ParentID != "" {
			if parent := byID[ch.ParentID]; parent != nil {
				parentDirPath = parent.DirPath
			}
		}
		reviewEnabled := s.configs.reviewEnabled(dirPath, parentDirPath)
		resp = append(resp, channelResponse{
			ChannelID:        ch.ChannelID,
			Name:             ch.Name,
			DirPath:          dirPath,
			ParentID:         ch.ParentID,
			SessionID:        ch.SessionID,
			Active:           ch.Active,
			ContainerRunning: running,
			AgentRunning:     runningBot,
			Branch:           git.Branch,
			Commit:           git.Commit,
			Worktree:         ch.Worktree,
			BaseBranch:       ch.BaseBranch,
			Locked:           ch.Locked,
			DiffAdditions:    git.DiffAdditions,
			DiffDeletions:    git.DiffDeletions,
			ReviewEnabled:    reviewEnabled,
		})
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// gitBranch returns the current git branch for the given directory, or "".
func gitBranch(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel deletion not configured") {
		return
	}

	channelID := r.PathValue("id")

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if ch.Locked {
		http.Error(w, ErrChannelLocked.Error(), http.StatusConflict)
		return
	}

	// Delete child threads first.
	if err := s.store.DeleteChannelsByParentID(r.Context(), channelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.store.DeleteChannel(r.Context(), channelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Clean up containers associated with this channel.
	s.cleanupChannelContainers(r.Context(), channelID)

	w.WriteHeader(http.StatusNoContent)
}

// cleanupChannelContainers removes all containers (agent, shell, chrome)
// associated with a channel. Called on channel deletion to prevent orphaned containers.
func (s *Server) cleanupChannelContainers(ctx context.Context, channelID string) {
	if s.containerRegistry != nil {
		for _, info := range s.containerRegistry.ListByChannel(channelID) {
			if info.Type == container.ContainerTypeChrome {
				continue // handled separately via BrowserProvider
			}
			if err := s.containerRegistry.RemoveContainer(ctx, info.ContainerID); err != nil {
				s.logger.Warn("channel cleanup: container remove failed",
					"channel_id", channelID,
					"container_id", info.ContainerID,
					"error", err,
				)
			}
		}
	}
	if s.browser.dockerProvider != nil {
		containerID, _ := s.browser.dockerProvider.StopBrowser(ctx, channelID)
		if containerID != "" && s.containerRegistry != nil {
			if err := s.containerRegistry.RemoveContainer(ctx, containerID); err != nil {
				s.logger.Warn("channel cleanup: chrome container remove failed",
					"channel_id", channelID,
					"container_id", containerID,
					"error", err,
				)
			}
		}
	}
}

type setLockedRequest struct {
	Locked bool `json:"locked"`
}

// handleSetChannelLocked toggles the locked flag on a channel or thread.
// Locking guards against accidental UI deletes; an unlock is required before
// the corresponding DELETE endpoint will succeed.
func (s *Server) handleSetChannelLocked(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel locking not configured") {
		return
	}

	channelID := r.PathValue("id")

	var req setLockedRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	if err := s.store.UpdateChannelLocked(r.Context(), channelID, req.Locked); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If this is an imported worktree thread, mirror the lock state into git
	// so `git worktree remove` and external tooling see the same truth.
	if ch.Worktree && ch.DirPath != "" && s.worktreeCreator != nil {
		parentDir := s.resolveWorktreeParentDir(r.Context(), ch)
		if parentDir != "" {
			var gitErr error
			if req.Locked {
				gitErr = s.worktreeCreator.Lock(r.Context(), parentDir, ch.DirPath, "locked from Loop UI")
			} else {
				gitErr = s.worktreeCreator.Unlock(r.Context(), parentDir, ch.DirPath)
			}
			if gitErr != nil {
				s.logger.Warn("git worktree lock/unlock", "channel_id", channelID, "locked", req.Locked, "error", gitErr)
			}
		}
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelLocked(channelID, req.Locked)
	}

	w.WriteHeader(http.StatusNoContent)
}

// resolveWorktreeParentDir returns the parent project dir_path for an imported
// worktree thread. Falls back to "" if the parent can't be resolved.
func (s *Server) resolveWorktreeParentDir(ctx context.Context, ch *db.Channel) string {
	if ch.ParentID == "" {
		return ""
	}
	parent, err := s.store.GetChannel(ctx, ch.ParentID)
	if err != nil || parent == nil {
		return ""
	}
	return parent.DirPath
}

type ensureAllChannelsRequest struct {
	DirPath string `json:"dir_path"`
}

func (s *Server) handleEnsureAllChannels(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.channels, channelsNotConfiguredMsg) {
		return
	}

	var req ensureAllChannelsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.DirPath == "" {
		http.Error(w, "dir_path is required", http.StatusBadRequest)
		return
	}

	results, err := s.channels.EnsureChannelAllPlatforms(r.Context(), req.DirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, results, s.logger)
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
