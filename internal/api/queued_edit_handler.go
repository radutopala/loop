package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/events"
)

// editHoldTTL is how long one hold keeps a queued message from starting. The
// app renews it while the edit is open, so this only bounds how long an
// abandoned edit (app closed, laptop asleep) can stall the queue.
const editHoldTTL = 5 * time.Minute

// errAlreadyStarted is the 409 body when an edit arrives after a run claimed
// the message; the app keeps the text so the user can send it anew.
const errAlreadyStarted = "message already started or no longer queued"

type editHoldResponse struct {
	HoldUntil int64 `json:"hold_until"`
}

type updateQueuedRequest struct {
	Content string `json:"content"`
}

// handleHoldQueuedMessage starts (or renews) an edit of a queued message. The
// hold stops the drain from claiming the row — and, to keep order, anything
// queued behind it — until the edit is saved, cancelled, or the hold lapses.
// A row a run already claimed can't be held: that's the 409 the app shows as
// "already started".
func (s *Server) handleHoldQueuedMessage(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "queued message editing not configured") {
		return
	}

	until := time.Now().Add(editHoldTTL).Unix()
	held, err := s.store.HoldQueuedMessage(r.Context(), r.PathValue("id"), r.PathValue("msg_id"), until)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !held {
		http.Error(w, errAlreadyStarted, http.StatusConflict)
		return
	}
	writeHTTPJSON(w, http.StatusOK, editHoldResponse{HoldUntil: until}, s.logger)
}

// handleReleaseQueuedHold cancels an edit, leaving the message as it was, and
// restarts the drain the hold was blocking. Releasing a row that already
// started is a no-op, not an error: the edit is over either way.
func (s *Server) handleReleaseQueuedHold(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "queued message editing not configured") {
		return
	}

	channelID := r.PathValue("id")
	released, err := s.store.ReleaseQueuedHold(r.Context(), channelID, r.PathValue("msg_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if released {
		s.resumeQueue(r, channelID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateQueuedMessage saves an edit: it replaces the queued message's
// content, lifts the hold, tells open views about the new text, and restarts
// the drain. The write only matches a row no run has claimed, so an edit can
// never change a prompt the agent already received.
func (s *Server) handleUpdateQueuedMessage(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "queued message editing not configured") {
		return
	}

	var req updateQueuedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	channelID := r.PathValue("id")
	msgID := r.PathValue("msg_id")
	updated, err := s.store.UpdateQueuedMessage(r.Context(), channelID, msgID, req.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !updated {
		http.Error(w, errAlreadyStarted, http.StatusConflict)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastMessageUpdated(channelID, events.MessageUpdatedData{MsgID: msgID, Content: req.Content})
	}
	s.resumeQueue(r, channelID)
	w.WriteHeader(http.StatusNoContent)
}

// resumeQueue kicks the channel's drain when a resumer is configured.
func (s *Server) resumeQueue(r *http.Request, channelID string) {
	if s.queueResumer != nil {
		s.queueResumer.ResumeChannel(r.Context(), channelID)
	}
}
