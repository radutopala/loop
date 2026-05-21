package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/radutopala/loop/internal/events"
)

const (
	askActionAnswer = "answer"
	askActionCancel = "cancel"

	// Stock prompt sent to the agent when the user clicks "Cancel" on an
	// AskUserQuestion card. Kept short and explicit so the agent treats it as
	// a continuation of the prior turn rather than a fresh task.
	askCancelPrompt = "I cancelled the question. Continue with the previous task as best you can without my answer."
)

type askResolveRequest struct {
	Action string `json:"action"`
	Answer string `json:"answer,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// handleAskResolve resolves a channel parked on an AskUserQuestion card.
//
//   - answer: clears the pause and inserts the user-provided answer as a
//     priority-bumped continuation, so any messages the user queued while
//     the ask card was up still run after the agent resumes.
//   - cancel: same flow but with a stock "cancelled" prompt, letting the
//     agent decide how to proceed without the answer.
func (s *Server) handleAskResolve(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.askResolver, "ask resolve not configured") {
		return
	}
	if !requireConfigured(w, s.msgHandler, "ask resolve requires message handler") {
		return
	}

	channelID := r.PathValue("id")

	var req askResolveRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	switch req.Action {
	case askActionAnswer:
		if req.Answer == "" {
			http.Error(w, "answer is required for answer", http.StatusBadRequest)
			return
		}
		s.insertAskContinuation(r.Context(), channelID, req.Answer, req.Mode)
	case askActionCancel:
		s.insertAskContinuation(r.Context(), channelID, askCancelPrompt, req.Mode)
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// insertAskContinuation clears the pause flag and inserts a priority-bumped
// message so the agent picks the answer up before any rows the user queued
// while the ask card was visible. Order mirrors insertPlanContinuation —
// bump priority while still paused, insert, clear, then kick a fresh drain.
func (s *Server) insertAskContinuation(ctx context.Context, channelID, content, mode string) {
	prio := 0
	if s.store != nil {
		if p, err := s.store.MaxQueuedPriority(ctx, channelID); err == nil {
			prio = p + 1
		}
	}
	s.msgHandler.HandleIncomingMessageWithPriority(context.Background(), channelID, "", content, mode, prio)
	s.askResolver.ClearAskedChannel(channelID)
	s.askResolver.ResumeChannel(context.Background(), channelID)
}

// PendingAsksLister snapshots every channel currently parked on an
// AskUserQuestion card along with its question payload. Backed in
// production by *orchestrator.Orchestrator.
type PendingAsksLister interface {
	ListAskedChannels() []events.AskedChannelEntry
}

type pendingAsksResponse struct {
	Asks []events.AskedChannelEntry `json:"asks"`
}

// handleListPendingAsks returns every parked AskUserQuestion. The FE
// calls this on WS reconnect so the ask card re-renders after a renderer
// reload — agent.ask_user only fires on the original tool call, so
// without this snapshot the card would never come back even though the
// backend keeps blocking the channel's drain.
func (s *Server) handleListPendingAsks(w http.ResponseWriter, _ *http.Request) {
	if !requireConfigured(w, s.pendingAsks, "pending asks lister not configured") {
		return
	}
	entries := s.pendingAsks.ListAskedChannels()
	if entries == nil {
		entries = []events.AskedChannelEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pendingAsksResponse{Asks: entries})
}
