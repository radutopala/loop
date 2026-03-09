package api

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
)

const channelsNotConfiguredMsg = "channel creation not configured (discord_guild_id not set or Slack not configured)"

type ensureChannelRequest struct {
	DirPath string `json:"dir_path"`
}

type ensureChannelResponse struct {
	ChannelID string `json:"channel_id"`
}

type createChannelRequest struct {
	Name     string `json:"name"`
	AuthorID string `json:"author_id"`
}

type createChannelResponse struct {
	ChannelID string `json:"channel_id"`
}

type channelResponse struct {
	ChannelID  string `json:"channel_id"`
	Name       string `json:"name"`
	DirPath    string `json:"dir_path"`
	ParentID   string `json:"parent_id"`
	Active     bool   `json:"active"`
	ContainerRunning bool `json:"container_running"`
	AgentRunning     bool `json:"agent_running"`
	Branch     string `json:"branch,omitempty"`
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

	channelID, err := s.channels.EnsureChannel(r.Context(), req.DirPath)
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

	channelID, err := s.channels.CreateChannel(r.Context(), req.Name, req.AuthorID)
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

	// Get running container channel IDs if a lister is configured.
	var runningIDs map[string]struct{}
	if s.runningChLister != nil {
		runningIDs, err = s.runningChLister.RunningChannelIDs(r.Context())
		if err != nil {
			s.logger.Warn("failed to list running channels", "error", err)
		}
	}

	// Get channels with active chat agent runs.
	var chatRunIDs map[string]struct{}
	if s.activeChatLister != nil {
		chatRunIDs = s.activeChatLister.ActiveChatChannelIDs()
	}

	query := r.URL.Query().Get("query")

	resp := make([]channelResponse, 0, len(channels))
	for _, ch := range channels {
		if s.platform != "" && ch.Platform != s.platform {
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
		resp = append(resp, channelResponse{
			ChannelID:        ch.ChannelID,
			Name:             ch.Name,
			DirPath:          dirPath,
			ParentID:         ch.ParentID,
			Active:           ch.Active,
			ContainerRunning: running,
			AgentRunning:     runningBot,
			Branch:           gitBranch(r.Context(), dirPath),
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

	// Delete child threads first.
	if err := s.store.DeleteChannelsByParentID(r.Context(), channelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.store.DeleteChannel(r.Context(), channelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
