package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/radutopala/loop/internal/events"
)

const (
	planActionApprove = "approve"
	planActionReject  = "reject"
	planActionDeny    = "deny"

	// Stock prompt sent to the agent when the user clicks "Approve" on an
	// ExitPlanMode card. Kept short and explicit so the agent treats it as a
	// continuation of the prior turn rather than a fresh task.
	planApprovePrompt = "I approve the plan. Please proceed with the implementation."
)

type planResolveRequest struct {
	Action string `json:"action"`
	Prompt string `json:"prompt,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// handlePlanResolve resolves a channel parked on an ExitPlanMode card.
//
//   - approve: clears the pause and inserts a stock approval message at the
//     front of the queue (priority bump), so any messages the user queued
//     while the plan card was up still run after the agent resumes.
//   - deny: same flow but with the user-provided prompt and mode, letting
//     them request changes before approval.
//   - reject: just clears the pause and kicks the drain so any queued
//     messages run as if the plan never happened.
func (s *Server) handlePlanResolve(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.planResolver, "plan resolve not configured") {
		return
	}
	if !requireConfigured(w, s.msgHandler, "plan resolve requires message handler") {
		return
	}

	channelID := r.PathValue("id")

	var req planResolveRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	switch req.Action {
	case planActionApprove:
		s.insertPlanContinuation(r.Context(), channelID, planApprovePrompt, "")
	case planActionDeny:
		if req.Prompt == "" {
			http.Error(w, "prompt is required for deny", http.StatusBadRequest)
			return
		}
		s.insertPlanContinuation(r.Context(), channelID, req.Prompt, req.Mode)
	case planActionReject:
		s.planResolver.ClearPlannedChannel(channelID)
		s.planResolver.ResumeChannel(context.Background(), channelID)
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// insertPlanContinuation clears the pause flag and inserts a priority-bumped
// message so the agent picks it up before any rows the user queued while the
// plan card was visible.
//
// ORDER MATTERS — mirroring the interrupt path in handleSendMessage:
//  1. Compute the new priority while pause is still set (no drain races).
//  2. Insert the bumped row (the handler spawns its own drain goroutine,
//     which may bail early because the pause flag is still set).
//  3. Clear the pause flag.
//  4. Kick a fresh drain. Without this, the drain spawned in step 2 may
//     have already bailed and the row would sit unprocessed.
func (s *Server) insertPlanContinuation(ctx context.Context, channelID, content, mode string) {
	prio := 0
	if s.store != nil {
		if p, err := s.store.MaxQueuedPriority(ctx, channelID); err == nil {
			prio = p + 1
		}
	}
	s.msgHandler.HandleIncomingMessageWithPriority(context.Background(), channelID, "", content, mode, prio)
	s.planResolver.ClearPlannedChannel(channelID)
	s.planResolver.ResumeChannel(context.Background(), channelID)
}

// PendingPlansLister snapshots every channel currently parked on an
// ExitPlanMode card along with its plan payload. Backed in production by
// *orchestrator.Orchestrator.
type PendingPlansLister interface {
	ListPlannedChannels() []events.PlannedChannelEntry
}

type pendingPlansResponse struct {
	Plans []events.PlannedChannelEntry `json:"plans"`
}

// handleListPendingPlans returns every parked ExitPlanMode card. The FE calls
// this on WS reconnect so the plan card re-renders after a renderer reload —
// agent.exit_plan only fires on the original tool call, so without this
// snapshot the card would never come back even though the backend keeps
// blocking the channel's drain.
func (s *Server) handleListPendingPlans(w http.ResponseWriter, _ *http.Request) {
	if !requireConfigured(w, s.pendingPlans, "pending plans lister not configured") {
		return
	}
	entries := s.pendingPlans.ListPlannedChannels()
	if entries == nil {
		entries = []events.PlannedChannelEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pendingPlansResponse{Plans: entries})
}
