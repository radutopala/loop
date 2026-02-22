package api

import (
	"net/http"
)

type sendMessageRequest struct {
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.messages, "message sending not configured") {
		return
	}

	var req sendMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	if err := s.messages.PostMessage(r.Context(), req.ChannelID, req.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
