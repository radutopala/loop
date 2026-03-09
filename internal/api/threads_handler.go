package api

import (
	"context"
	"net/http"
)

type createThreadRequest struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	AuthorID  string `json:"author_id"`
	Message   string `json:"message"`
}

type createThreadResponse struct {
	ThreadID string `json:"thread_id"`
}

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}

	var req createThreadRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, req.Name, req.AuthorID, req.Message)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// For local platform: trigger the agent in the new thread by routing the
	// initial message through the orchestrator (analogous to Discord/Slack
	// sending a bot-mention greeting message in the thread).
	if s.msgHandler != nil && req.Message != "" {
		authorID := req.AuthorID
		if authorID == "" {
			authorID = "local-user"
		}
		go s.msgHandler.HandleIncomingMessage(
			context.Background(), threadID,
			authorID, "@LoopBot "+req.Message,
		)
	}

	writeHTTPJSON(w, http.StatusCreated, createThreadResponse{ThreadID: threadID}, s.logger)
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.threads, "thread deletion not configured") {
		return
	}

	threadID := r.PathValue("id")

	if err := s.threads.DeleteThread(r.Context(), threadID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
