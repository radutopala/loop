package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/promptedit"
)

// PromptEditor rewrites a composer draft as a spoken instruction asks, with
// a run in the channel's hidden prompt-edit thread (see
// orchestrator.PromptEdit).
type PromptEditor interface {
	PromptEdit(ctx context.Context, ch *db.Channel, req promptedit.Request) (*promptedit.Result, error)
}

// SetPromptEditor configures what POST /api/channels/{id}/prompt-edit edits
// drafts with.
func (s *Server) SetPromptEditor(e PromptEditor) {
	s.promptEditor = e
}

// promptEditRequest is a draft, its selection in UTF-16 units, and the
// instruction to apply to it.
type promptEditRequest struct {
	Draft       string `json:"draft"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Instruction string `json:"instruction"`
}

// handlePromptEdit rewrites the composer's draft as the instruction asks, on
// the model and effort the channel's composer has selected.
func (s *Server) handlePromptEdit(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.promptEditor, "prompt edit not configured") {
		return
	}
	var req promptEditRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	res, err := s.promptEditor.PromptEdit(r.Context(), ch, promptedit.Request{
		Draft:       req.Draft,
		Start:       req.Start,
		End:         req.End,
		Instruction: req.Instruction,
	})
	switch {
	case errors.Is(err, promptedit.ErrNoInstruction):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, res, s.logger)
}

// promptEditState is the channel's hidden prompt-edit thread, whose chat
// holds its edits by voice; "" before the first.
type promptEditState struct {
	EditChannelID string `json:"edit_channel_id"`
}

// handlePromptEditState returns the channel's prompt-edit thread.
func (s *Server) handlePromptEditState(w http.ResponseWriter, r *http.Request) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	h, err := s.store.GetHiddenThread(r.Context(), ch.ChannelID, db.ChannelKindPromptEdit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var st promptEditState
	if h != nil {
		st.EditChannelID = h.ChannelID
	}
	writeHTTPJSON(w, http.StatusOK, st, s.logger)
}
