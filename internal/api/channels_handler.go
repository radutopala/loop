package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

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
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	DirPath   string `json:"dir_path"`
	ParentID  string `json:"parent_id"`
	Active    bool   `json:"active"`
}

func (s *Server) handleEnsureChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		http.Error(w, "channel creation not configured (discord_guild_id not set or Slack not configured)", http.StatusNotImplemented)
		return
	}

	var req ensureChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ensureChannelResponse{ChannelID: channelID})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		http.Error(w, "channel creation not configured (discord_guild_id not set or Slack not configured)", http.StatusNotImplemented)
		return
	}

	var req createChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createChannelResponse{ChannelID: channelID})
}

func (s *Server) handleSearchChannels(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "channel listing not configured", http.StatusNotImplemented)
		return
	}

	channels, err := s.store.ListChannels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	query := r.URL.Query().Get("query")

	resp := make([]channelResponse, 0, len(channels))
	for _, ch := range channels {
		if query != "" && !containsFold(ch.Name, query) {
			continue
		}
		resp = append(resp, channelResponse{
			ChannelID: ch.ChannelID,
			Name:      ch.Name,
			DirPath:   ch.DirPath,
			ParentID:  ch.ParentID,
			Active:    ch.Active,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
