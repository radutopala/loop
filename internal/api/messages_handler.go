package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

type sendMessageRequest struct {
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	Mode      string `json:"mode,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
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

	// Route through the orchestrator when available. HandleIncomingMessage*
	// is now non-blocking after the InsertMessage step (the orchestrator
	// drains in its own goroutine), so we can call it on the request thread
	// and rely on the insert being persisted before this function returns.
	if s.msgHandler != nil {
		// Interrupt mode: ORDER MATTERS. The new (priority-bumped) row must be
		// inserted BEFORE we cancel the active run — otherwise the cancelled
		// run's drain loop can re-claim an older queued row in the window
		// between cancel and insert, processing it ahead of the interrupt.
		// Queued rows are preserved; markTriggerProcessed only marks the
		// cancelled trigger itself.
		if req.Interrupt && s.runCanceller != nil {
			prio := 0
			if s.store != nil {
				if p, err := s.store.MaxQueuedPriority(r.Context(), req.ChannelID); err == nil {
					prio = p + 1
				}
			}
			s.msgHandler.HandleIncomingMessageWithPriority(context.Background(), req.ChannelID, "", req.Content, req.Mode, prio)
			s.runCanceller.CancelActiveRun(req.ChannelID)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.msgHandler.HandleIncomingMessage(context.Background(), req.ChannelID, "", req.Content, req.Mode)
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

func (s *Server) handleDeleteQueuedMessage(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "message deletion not configured") {
		return
	}

	msgID := r.PathValue("id")
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	if channelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}

	deleted, err := s.store.DeleteQueuedMessage(r.Context(), channelID, msgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "not found or not deletable", http.StatusNotFound)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastMessageDeleted(channelID, events.MessageDeletedData{MsgID: msgID})
	}
	w.WriteHeader(http.StatusNoContent)
}

const defaultMessageLimit = 50

// maxMessageLimit is the upper bound for the limit query parameter.
const maxMessageLimit = 200

type messageResponse struct {
	ID          int64     `json:"id"`
	ChannelID   string    `json:"channel_id"`
	MsgID       string    `json:"msg_id"`
	AuthorID    string    `json:"author_id"`
	AuthorName  string    `json:"author_name"`
	Content     string    `json:"content"`
	IsBot       bool      `json:"is_bot"`
	IsProcessed bool      `json:"is_processed"`
	CreatedAt   time.Time `json:"created_at"`
}

type messagesListResponse struct {
	Messages   []messageResponse `json:"messages"`
	NextCursor *int64            `json:"next_cursor"`
}

func toMessageResponse(m *db.Message) messageResponse {
	return messageResponse{
		ID:          m.ID,
		ChannelID:   m.ChannelID,
		MsgID:       m.MsgID,
		AuthorID:    m.AuthorID,
		AuthorName:  m.AuthorName,
		Content:     m.Content,
		IsBot:       m.IsBot,
		IsProcessed: m.IsProcessed,
		CreatedAt:   m.CreatedAt,
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
	aroundID, ok := parseQueryInt64(w, r, "around")
	if !ok {
		return
	}
	if aroundID > 0 {
		limit, ok := parseQueryInt(w, r, "limit", defaultMessageLimit, maxMessageLimit)
		if !ok {
			return
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

	limit, ok2 := parseQueryInt(w, r, "limit", defaultMessageLimit, maxMessageLimit)
	if !ok2 {
		return
	}

	cursor, ok2 := parseQueryInt64(w, r, "cursor")
	if !ok2 {
		return
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

	limit, ok := parseQueryInt(w, r, "limit", defaultSearchLimit, maxSearchLimit)
	if !ok {
		return
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
