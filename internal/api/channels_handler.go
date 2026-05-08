package api

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/container"
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
	Locked           bool   `json:"locked"`
	DiffAdditions    int    `json:"diff_additions,omitempty"`
	DiffDeletions    int    `json:"diff_deletions,omitempty"`
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
		diffAdd, diffDel := gitDiffStats(r.Context(), dirPath)
		resp = append(resp, channelResponse{
			ChannelID:        ch.ChannelID,
			Name:             ch.Name,
			DirPath:          dirPath,
			ParentID:         ch.ParentID,
			SessionID:        ch.SessionID,
			Active:           ch.Active,
			ContainerRunning: running,
			AgentRunning:     runningBot,
			Branch:           gitBranch(r.Context(), dirPath),
			Commit:           gitCommit(r.Context(), dirPath),
			Worktree:         ch.Worktree,
			Locked:           ch.Locked,
			DiffAdditions:    diffAdd,
			DiffDeletions:    diffDel,
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

// gitCommit returns the short commit hash for the given directory, or "".
func gitCommit(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitDiffStats returns the total additions and deletions for uncommitted changes,
// including untracked files (counted as additions).
func gitDiffStats(ctx context.Context, dir string) (add, del int) {
	if dir == "" {
		return 0, 0
	}

	// Tracked changes.
	cmd := exec.CommandContext(ctx, "git", "diff", "--shortstat")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		for _, part := range strings.Split(strings.TrimSpace(string(out)), ",") {
			part = strings.TrimSpace(part)
			if strings.Contains(part, "insertion") {
				_, _ = fmt.Sscanf(part, "%d", &add)
			} else if strings.Contains(part, "deletion") {
				_, _ = fmt.Sscanf(part, "%d", &del)
			}
		}
	}

	// Untracked files: count total lines as additions.
	lsCmd := exec.CommandContext(ctx, "git", "ls-files", "--others", "--exclude-standard")
	lsCmd.Dir = dir
	if lsOut, err := lsCmd.Output(); err == nil {
		files := strings.TrimSpace(string(lsOut))
		if files != "" {
			args := append([]string{"-l"}, strings.Split(files, "\n")...)
			wcCmd := exec.CommandContext(ctx, "wc", args...)
			wcCmd.Dir = dir
			if wcOut, err := wcCmd.Output(); err == nil {
				// wc -l with multiple files prints a "total" line last; with one file just the count.
				lines := strings.Split(strings.TrimSpace(string(wcOut)), "\n")
				lastLine := strings.TrimSpace(lines[len(lines)-1])
				var n int
				_, _ = fmt.Sscanf(lastLine, "%d", &n)
				add += n
			}
		}
	}

	return add, del
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
	if s.dockerBrowserProvider != nil {
		containerID, _ := s.dockerBrowserProvider.StopBrowser(ctx, channelID)
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

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelLocked(channelID, req.Locked)
	}

	w.WriteHeader(http.StatusNoContent)
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
