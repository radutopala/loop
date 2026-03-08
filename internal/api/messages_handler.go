package api

import (
	"net/http"
	"strconv"
	"time"
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

const defaultMessageLimit = 50

type messageResponse struct {
	ID         int64     `json:"id"`
	ChannelID  string    `json:"channel_id"`
	MsgID      string    `json:"msg_id"`
	AuthorID   string    `json:"author_id"`
	AuthorName string    `json:"author_name"`
	Content    string    `json:"content"`
	IsBot      bool      `json:"is_bot"`
	CreatedAt  time.Time `json:"created_at"`
}

type messagesListResponse struct {
	Messages   []messageResponse `json:"messages"`
	NextCursor *int64            `json:"next_cursor"`
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "message listing not configured") {
		return
	}

	channelID := r.PathValue("id")

	limit := defaultMessageLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		parsed, err := strconv.Atoi(l)
		if err != nil || parsed < 1 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = parsed
	}

	var cursor int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		parsed, err := strconv.ParseInt(c, 10, 64)
		if err != nil || parsed < 1 {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		cursor = parsed
	}

	msgs, err := s.store.GetMessagesCursor(r.Context(), channelID, cursor, limit+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := messagesListResponse{
		Messages: make([]messageResponse, 0, len(msgs)),
	}

	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}

	for _, m := range msgs {
		resp.Messages = append(resp.Messages, messageResponse{
			ID:         m.ID,
			ChannelID:  m.ChannelID,
			MsgID:      m.MsgID,
			AuthorID:   m.AuthorID,
			AuthorName: m.AuthorName,
			Content:    m.Content,
			IsBot:      m.IsBot,
			CreatedAt:  m.CreatedAt,
		})
	}

	if hasMore {
		last := msgs[len(msgs)-1].ID
		resp.NextCursor = &last
	}

	writeJSON(w, http.StatusOK, resp, s.logger)
}
