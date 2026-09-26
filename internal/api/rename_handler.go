package api

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/radutopala/loop/internal/events"
)

// ── Rename Channel ──

type renameChannelRequest struct {
	Name string `json:"name"`
}

type renameChannelResponse struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
}

func (s *Server) handleRenameChannel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	channelID := r.PathValue("id")

	var req renameChannelRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
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

	if err := s.store.UpdateChannelName(r.Context(), channelID, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID: channelID,
			Name:      req.Name,
		})
	}

	writeHTTPJSON(w, http.StatusOK, renameChannelResponse{
		ChannelID: channelID,
		Name:      req.Name,
	}, s.logger)
}

// ── Channel Description ──

// maxDescriptionLen caps a description, in characters: it's a line or two
// shown on hover, not notes.
const maxDescriptionLen = 500

type setDescriptionRequest struct {
	Description string `json:"description"`
}

type setDescriptionResponse struct {
	ChannelID   string `json:"channel_id"`
	Description string `json:"description"`
}

// handleSetChannelDescription sets what a thread (or channel) is for. An empty
// description clears it.
func (s *Server) handleSetChannelDescription(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	channelID := r.PathValue("id")

	var req setDescriptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	description := strings.TrimSpace(req.Description)
	if utf8.RuneCountInString(description) > maxDescriptionLen {
		http.Error(w, fmt.Sprintf("description is longer than %d characters", maxDescriptionLen), http.StatusBadRequest)
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

	if err := s.store.UpdateChannelDescription(r.Context(), channelID, description); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID:   channelID,
			Description: &description,
		})
	}

	writeHTTPJSON(w, http.StatusOK, setDescriptionResponse{
		ChannelID:   channelID,
		Description: description,
	}, s.logger)
}
