package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/explain"
)

// Explainer explains a chat turn: it returns the turn's explanation, or
// queues a new one when there's none or force is set (see
// orchestrator.Explain).
type Explainer interface {
	Explain(ctx context.Context, ch *db.Channel, messageID string, force bool) (*db.Explanation, error)
}

// SetExplainer configures what POST /api/channels/{id}/explanations
// explains turns with.
func (s *Server) SetExplainer(e Explainer) {
	s.explainer = e
}

// explainStateResponse is a channel's explain switch. Explain is the
// override ("on", "off", or empty to inherit DefaultExplain); Enabled is the
// effective value, never true when Available is false.
type explainStateResponse struct {
	Available      bool   `json:"available"`
	Explain        string `json:"explain"`
	DefaultExplain bool   `json:"default_explain"`
	Enabled        bool   `json:"enabled"`
}

type explainStateRequest struct {
	Explain string `json:"explain"`
}

type explainRequest struct {
	MessageID string `json:"message_id"`
	Force     bool   `json:"force"`
}

// handleGetExplain returns the channel's explain switch and the config
// default it falls back to.
func (s *Server) handleGetExplain(w http.ResponseWriter, r *http.Request) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	def := false
	if merged := s.configs.merged(ch.DirPath, s.workspace.resolveParentDirPath(r.Context(), ch.ChannelID)); merged != nil {
		def = merged.Explain.Enabled
	}
	available := explain.Unavailable(ch) == ""
	writeHTTPJSON(w, http.StatusOK, explainStateResponse{
		Available:      available,
		Explain:        ch.ExplainOverride,
		DefaultExplain: def,
		Enabled:        available && ch.ExplainEnabled(def),
	}, s.logger)
}

// handleSetExplain sets the channel's explain switch: "on", "off", or empty
// to inherit the config. It takes effect from the channel's next run.
func (s *Server) handleSetExplain(w http.ResponseWriter, r *http.Request) {
	var req explainStateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Explain != "" && req.Explain != db.LearnOn && req.Explain != db.LearnOff {
		http.Error(w, `invalid explain: must be "on", "off" or empty`, http.StatusBadRequest)
		return
	}
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	if reason := explain.Unavailable(ch); reason != "" {
		http.Error(w, reason, http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateChannelExplainOverride(r.Context(), ch.ChannelID, req.Explain); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelExplain(ch.ChannelID, req.Explain)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListExplanations returns the channel's explanations, newest first.
func (s *Server) handleListExplanations(w http.ResponseWriter, r *http.Request) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	list, err := s.store.ListExplanations(r.Context(), ch.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, append([]*db.Explanation{}, list...), s.logger)
}

// handleExplain explains the turn that ended with message_id: it returns
// the turn's explanation, or queues a new one when there's none or force
// (re-explain) is set.
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.explainer, "explain not configured") {
		return
	}
	var req explainRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		http.Error(w, "message_id is required", http.StatusBadRequest)
		return
	}
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	e, err := s.explainer.Explain(r.Context(), ch, req.MessageID, req.Force)
	switch {
	case errors.Is(err, explain.ErrUnavailable), errors.Is(err, explain.ErrNotATurn):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, explain.ErrNoSession):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, db.ErrParentGone):
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, e, s.logger)
}
