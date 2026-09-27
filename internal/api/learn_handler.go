package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/db"
)

// learnStateResponse is a channel's learn switch and its hidden learn thread.
// Learn is the channel's override ("on", "off", or empty to inherit
// DefaultLearn from the merged config); Enabled is the effective value.
// LearnChannelID is empty until the first learn pass creates the thread, and
// Running says a learn pass is in progress there.
type learnStateResponse struct {
	Learn          string `json:"learn"`
	DefaultLearn   bool   `json:"default_learn"`
	Enabled        bool   `json:"enabled"`
	LearnChannelID string `json:"learn_channel_id"`
	Running        bool   `json:"running"`
}

type learnStateRequest struct {
	Learn string `json:"learn"`
}

// learnChannelFor returns the channel at id, failing the request when it's
// missing or is itself a learn thread (they don't learn from themselves).
func (s *Server) learnChannelFor(w http.ResponseWriter, r *http.Request) *db.Channel {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return nil
	}
	ch, err := s.store.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	if ch == nil || ch.Kind == db.ChannelKindLearn {
		http.Error(w, "channel not found", http.StatusNotFound)
		return nil
	}
	return ch
}

// handleGetLearn returns the channel's learn switch, the config default it
// falls back to, and its learn thread.
func (s *Server) handleGetLearn(w http.ResponseWriter, r *http.Request) {
	ch := s.learnChannelFor(w, r)
	if ch == nil {
		return
	}
	l, err := s.store.GetLearnChannel(r.Context(), ch.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	def := false
	if merged := s.mergedConfig(ch.DirPath, s.workspace.resolveParentDirPath(r.Context(), ch.ChannelID)); merged != nil {
		def = merged.Learn.Enabled
	}
	resp := learnStateResponse{
		Learn:        ch.LearnOverride,
		DefaultLearn: def,
		Enabled:      learnEnabled(ch.LearnOverride, def),
	}
	if l != nil {
		resp.LearnChannelID = l.ChannelID
		if s.activeChatLister != nil {
			_, resp.Running = s.activeChatLister.ActiveChatChannelIDs()[l.ChannelID]
		}
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// handleSetLearn sets the channel's learn switch: "on", "off", or empty to
// inherit the config. It takes effect from the channel's next run.
func (s *Server) handleSetLearn(w http.ResponseWriter, r *http.Request) {
	var req learnStateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Learn != "" && req.Learn != db.LearnOn && req.Learn != db.LearnOff {
		http.Error(w, `invalid learn: must be "on", "off" or empty`, http.StatusBadRequest)
		return
	}
	ch := s.learnChannelFor(w, r)
	if ch == nil {
		return
	}
	if err := s.store.UpdateChannelLearnOverride(r.Context(), ch.ChannelID, req.Learn); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelLearn(ch.ChannelID, req.Learn)
	}
	w.WriteHeader(http.StatusNoContent)
}

// learnEnabled is the effective learn switch: the channel's override when it
// has one, else the config default.
func learnEnabled(override string, def bool) bool {
	switch override {
	case db.LearnOn:
		return true
	case db.LearnOff:
		return false
	}
	return def
}
