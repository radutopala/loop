package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/db"
)

type sendMessageRequest struct {
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
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

	// Route through the orchestrator when available.
	if s.msgHandler != nil {
		// Use a detached context — r.Context() is cancelled when the HTTP response is sent.
		go s.msgHandler.HandleIncomingMessage(context.Background(), req.ChannelID, "", req.Content)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !requireConfigured(w, s.messages, "message sending not configured") {
		return
	}

	if err := s.messages.PostMessage(r.Context(), req.ChannelID, req.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

const defaultMessageLimit = 50

// maxMessageLimit is the upper bound for the limit query parameter.
const maxMessageLimit = 200

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

func toMessageResponse(m *db.Message) messageResponse {
	return messageResponse{
		ID:         m.ID,
		ChannelID:  m.ChannelID,
		MsgID:      m.MsgID,
		AuthorID:   m.AuthorID,
		AuthorName: m.AuthorName,
		Content:    m.Content,
		IsBot:      m.IsBot,
		CreatedAt:  m.CreatedAt,
	}
}

func toSearchMessageResponse(m *db.Message) searchMessageResponse {
	return searchMessageResponse{
		ID:         m.ID,
		ChannelID:  m.ChannelID,
		AuthorName: m.AuthorName,
		Content:    m.Content,
		IsBot:      m.IsBot,
		CreatedAt:  m.CreatedAt,
	}
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "message listing not configured") {
		return
	}

	channelID := r.PathValue("id")

	// "around" mode: return messages centered around a specific message ID.
	if a := r.URL.Query().Get("around"); a != "" {
		aroundID, err := strconv.ParseInt(a, 10, 64)
		if err != nil || aroundID < 1 {
			http.Error(w, "invalid around", http.StatusBadRequest)
			return
		}
		limit := defaultMessageLimit
		if l := r.URL.Query().Get("limit"); l != "" {
			parsed, err := strconv.Atoi(l)
			if err != nil || parsed < 1 {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			if parsed > maxMessageLimit {
				parsed = maxMessageLimit
			}
			limit = parsed
		}
		msgs, err := s.store.GetMessagesAround(r.Context(), channelID, aroundID, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp := messagesListResponse{Messages: make([]messageResponse, 0, len(msgs))}
		for _, m := range msgs {
			resp.Messages = append(resp.Messages, toMessageResponse(m))
		}
		writeHTTPJSON(w, http.StatusOK, resp, s.logger)
		return
	}

	limit := defaultMessageLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		parsed, err := strconv.Atoi(l)
		if err != nil || parsed < 1 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if parsed > maxMessageLimit {
			parsed = maxMessageLimit
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
		resp.Messages = append(resp.Messages, toMessageResponse(m))
	}

	if hasMore {
		last := msgs[len(msgs)-1].ID
		resp.NextCursor = &last
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

const defaultSearchLimit = 20
const maxSearchLimit = 50

type searchMessageResponse struct {
	ID         int64     `json:"id"`
	ChannelID  string    `json:"channel_id"`
	AuthorName string    `json:"author_name"`
	Content    string    `json:"content"`
	IsBot      bool      `json:"is_bot"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Server) handleSearchMessages(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "message search not configured") {
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Error(w, "q is required", http.StatusBadRequest)
		return
	}

	limit := defaultSearchLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		parsed, err := strconv.Atoi(l)
		if err != nil || parsed < 1 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if parsed > maxSearchLimit {
			parsed = maxSearchLimit
		}
		limit = parsed
	}

	msgs, err := s.store.SearchMessages(r.Context(), q, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	results := make([]searchMessageResponse, 0, len(msgs))
	for _, m := range msgs {
		results = append(results, toSearchMessageResponse(m))
	}

	writeHTTPJSON(w, http.StatusOK, results, s.logger)
}
