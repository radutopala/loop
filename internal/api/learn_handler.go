package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// learnStateResponse is a channel's learn switch and its hidden learn thread.
// Learn is the override ("on", "off", or empty to inherit DefaultLearn);
// Enabled is the effective value, never true when Available is false (see
// learnUnavailable). Running is a learn pass, not a user's reply.
type learnStateResponse struct {
	Available      bool   `json:"available"`
	Learn          string `json:"learn"`
	DefaultLearn   bool   `json:"default_learn"`
	Enabled        bool   `json:"enabled"`
	LearnChannelID string `json:"learn_channel_id"`
	Running        bool   `json:"running"`
}

type learnStateRequest struct {
	Learn string `json:"learn"`
}

// visibleChannelFor returns the channel at id, failing the request when it's
// missing or is itself a hidden learn or explain thread: those don't learn
// or get explained, and have no switches of their own.
func (s *Server) visibleChannelFor(w http.ResponseWriter, r *http.Request) *db.Channel {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return nil
	}
	ch, err := s.store.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	if ch == nil || db.IsHiddenKind(ch.Kind) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return nil
	}
	return ch
}

// handleGetLearn returns the channel's learn switch, the config default it
// falls back to, and its learn thread.
func (s *Server) handleGetLearn(w http.ResponseWriter, r *http.Request) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	l, err := s.store.GetHiddenThread(r.Context(), ch.ChannelID, db.ChannelKindLearn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	def := false
	if merged := s.configs.merged(ch.DirPath, s.workspace.resolveParentDirPath(r.Context(), ch.ChannelID)); merged != nil {
		def = merged.Learn.Enabled
	}
	available := learnUnavailable(ch) == ""
	resp := learnStateResponse{
		Available:    available,
		Learn:        ch.LearnOverride,
		DefaultLearn: def,
		Enabled:      available && ch.LearnEnabled(def),
	}
	if l != nil {
		resp.LearnChannelID = l.ChannelID
		if s.runCanceller != nil {
			resp.Running = s.runCanceller.IsLearnPassRunning(l.ChannelID)
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
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	if reason := learnUnavailable(ch); reason != "" {
		http.Error(w, reason, http.StatusBadRequest)
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

// learnUnavailable says why ch can't learn, or "" when it can. Only desktop
// app channels do, since their proposals can only be seen and applied in
// the desktop app, and task threads never do: the daemon skips their runs.
func learnUnavailable(ch *db.Channel) string {
	switch {
	case ch.Platform != types.PlatformLocal:
		return "learn runs only in desktop app channels"
	case ch.TaskID != 0:
		return "learn doesn't run in task threads"
	}
	return ""
}
