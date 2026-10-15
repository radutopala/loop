// live_handler.go describes and ends a channel's live session, the one
// long-lived claude process that takes the channel's chat turns, and
// creates, lists and deletes side chats: unlisted threads a chat pane of
// their own talks to, on a fork of their parent's session or a fresh one.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

// LiveSessions runs the channels' live sessions. orchestrator.Orchestrator
// implements it.
type LiveSessions interface {
	EndLiveSession(channelID, reason string) error
	LiveSession(ctx context.Context, channelID string) orchestrator.LiveStatus
	SweepLiveSessions(ctx context.Context)
	WarmLiveSession(ctx context.Context, channelID, authorID string) (bool, error)
}

// SetLiveSessions configures the live sessions behind
// /api/channels/{id}/live.
func (s *Server) SetLiveSessions(l LiveSessions) {
	s.live = l
}

// Side chat modes: a fork of the parent's session, or a fresh session.
const (
	sideChatFork  = "fork"
	sideChatFresh = "fresh"
)

// registerLiveRoutes registers the live session and side chat routes.
func (s *Server) registerLiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/channels/{id}/live", s.handleGetLive)
	mux.HandleFunc("POST /api/channels/{id}/live", s.handleWarmLive)
	mux.HandleFunc("DELETE /api/channels/{id}/live", s.handleStopLive)
	mux.HandleFunc("POST /api/channels/{id}/side-chats", s.handleCreateSideChat)
	mux.HandleFunc("GET /api/channels/{id}/side-chats", s.handleListSideChats)
	mux.HandleFunc("DELETE /api/side-chats/{id}", s.handleDeleteSideChat)
}

func (s *Server) handleGetLive(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.live, "live sessions not configured") {
		return
	}
	writeHTTPJSON(w, http.StatusOK, s.live.LiveSession(r.Context(), r.PathValue("id")), s.logger)
}

func (s *Server) handleStopLive(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.live, "live sessions not configured") {
		return
	}
	switch err := s.live.EndLiveSession(r.PathValue("id"), "stopped"); {
	case errors.Is(err, container.ErrNoLiveSession):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// warmLiveResponse is the body of POST /api/channels/{id}/live's answer.
type warmLiveResponse struct {
	Started bool `json:"started"`
}

// handleWarmLive starts the channel's live session ahead of the turn the
// user is typing.
func (s *Server) handleWarmLive(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.live, "live sessions not configured") {
		return
	}
	started, err := s.live.WarmLiveSession(r.Context(), r.PathValue("id"), local.DefaultAuthorID)
	switch {
	case errors.Is(err, container.ErrLiveSessionRunning):
	case errors.Is(err, orchestrator.ErrLiveUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, warmLiveResponse{Started: started}, s.logger)
}

// sweepLive ends the live sessions of channels just deleted.
func (s *Server) sweepLive(ctx context.Context) {
	if s.live != nil {
		s.live.SweepLiveSessions(ctx)
	}
}

// createSideChatRequest is the body of POST /api/channels/{id}/side-chats.
type createSideChatRequest struct {
	Mode string `json:"mode"`
}

// createSideChatResponse is what POST /api/channels/{id}/side-chats
// returns.
type createSideChatResponse struct {
	ChannelID string `json:"channel_id"`
}

// handleCreateSideChat creates a side chat of the channel or thread: a
// local thread, whatever the parent's platform, so its answers stay in the
// app; in fork mode on the parent's session, which its first turn forks.
func (s *Server) handleCreateSideChat(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "side chats not configured") {
		return
	}
	var req createSideChatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Mode != sideChatFork && req.Mode != sideChatFresh {
		http.Error(w, "mode must be fork or fresh", http.StatusBadRequest)
		return
	}
	parent, err := s.store.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if db.IsUnlistedKind(parent.Kind) {
		http.Error(w, "this channel can't have side chats", http.StatusConflict)
		return
	}
	side := &db.Channel{
		ChannelID: sideAgentPrefix + randutil.HexID(6),
		GuildID:   parent.GuildID,
		Name:      parent.Name + " (" + req.Mode + ")",
		DirPath:   parent.DirPath,
		ParentID:  parent.ChannelID,
		Platform:  types.PlatformLocal,
	}
	if req.Mode == sideChatFork {
		if parent.SessionID == "" {
			http.Error(w, "the channel has no session to fork yet", http.StatusConflict)
			return
		}
		side.SessionID = parent.SessionID
		side.ForkPending = true
	}
	switch err := s.store.InsertSideChat(r.Context(), side); {
	case errors.Is(err, db.ErrParentGone):
		http.Error(w, "channel not found", http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		writeHTTPJSON(w, http.StatusCreated, createSideChatResponse{ChannelID: side.ChannelID}, s.logger)
	}
}

func (s *Server) handleListSideChats(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "side chats not configured") {
		return
	}
	sides, err := s.store.ListSideChats(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sides == nil {
		sides = []*db.Channel{}
	}
	writeHTTPJSON(w, http.StatusOK, sides, s.logger)
}

// handleDeleteSideChat ends the side chat's live session and deletes it as
// a thread. Its session stays, to resume from the sessions panel.
func (s *Server) handleDeleteSideChat(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "side chats not configured") ||
		!requireConfigured(w, s.threads, "side chats not configured") {
		return
	}
	id := r.PathValue("id")
	side, err := s.store.GetChannel(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if side == nil || side.Kind != db.ChannelKindSide {
		http.Error(w, "side chat not found", http.StatusNotFound)
		return
	}
	if s.live != nil {
		_ = s.live.EndLiveSession(id, "closed")
	}
	if err := s.deleteThread(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
