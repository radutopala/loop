package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

// ── Channel tags ──

type editTagsRequest struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

type editTagsResponse struct {
	ChannelID string   `json:"channel_id"`
	Tags      []string `json:"tags"`
}

type listTagsResponse struct {
	Tags []db.TagCount `json:"tags"`
}

// handleEditChannelTags adds tags to and removes tags from a channel or
// thread, and returns its tags after. A tag in both is removed.
func (s *Server) handleEditChannelTags(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}
	channelID := r.PathValue("id")

	var req editTagsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	add, err := types.NormalizeTags(req.Add)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	remove, err := types.NormalizeTags(req.Remove)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tags, err := s.store.UpdateChannelTags(r.Context(), channelID, add, remove)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	case errors.Is(err, types.ErrTooManyTags):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, fmt.Sprintf("updating tags: %v", err), http.StatusInternalServerError)
		return
	}
	if tags == nil {
		tags = []string{}
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID: channelID,
			Tags:      &tags,
		})
	}

	writeHTTPJSON(w, http.StatusOK, editTagsResponse{ChannelID: channelID, Tags: tags}, s.logger)
}

// handleListTags returns every tag in use, sorted, with how many channels
// and threads carry it, for picking an existing tag.
func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}
	tags, err := s.store.ListChannelTags(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, listTagsResponse{Tags: tags}, s.logger)
}
