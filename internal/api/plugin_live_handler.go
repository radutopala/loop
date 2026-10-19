// plugin_live_handler.go lets a plugin with live.control drive the live
// session of its chat: send it the control requests its manifest declares,
// keep it from idling out, and hear the system messages it asks for.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/plugin"
)

// pluginLiveControlTimeout bounds waiting for a live session to answer a
// plugin's control request.
const pluginLiveControlTimeout = 30 * time.Second

// pluginHolderPrefix starts the holder a plugin keeps a live session
// running as, "plugin:<id>".
const pluginHolderPrefix = "plugin:"

// pluginLiveControlRequest is the body of POST
// /api/plugin/v1/channel/live/control.
type pluginLiveControlRequest struct {
	Request  json.RawMessage `json:"request"`
	ThreadID string          `json:"thread_id"`
}

// pluginLiveControlResponse is its answer: the session's response.
type pluginLiveControlResponse struct {
	Response json.RawMessage `json:"response"`
}

// pluginLiveCaller is pluginCaller for the live routes, which also need the
// live sessions.
func (s *Server) pluginLiveCaller(w http.ResponseWriter, r *http.Request) (string, *db.Channel, bool) {
	p, ch, ok := s.pluginCaller(w, r, plugin.CapLiveControl)
	if !ok {
		return "", nil, false
	}
	if !requireConfigured(w, s.live, "live sessions not configured") {
		return "", nil, false
	}
	return p.PluginID, ch, true
}

// pluginTarget returns the chat a plugin in ch asks for: ch, or with
// threadID one of ch's listed threads. Otherwise it answers the request and
// returns false.
func (s *Server) pluginTarget(w http.ResponseWriter, r *http.Request, ch *db.Channel, threadID string) (*db.Channel, bool) {
	if threadID == "" || threadID == ch.ChannelID {
		return ch, true
	}
	t, err := s.store.GetChannel(r.Context(), threadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	if t == nil || t.ParentID != ch.ChannelID || db.IsUnlistedKind(t.Kind) {
		http.Error(w, "thread not found in the plugin's channel", http.StatusNotFound)
		return nil, false
	}
	return t, true
}

// handlePluginLiveControl sends a control request the plugin's manifest
// declares to the live session of its chat, and answers with the session's
// response.
func (s *Server) handlePluginLiveControl(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginLiveCaller(w, r)
	if !ok {
		return
	}
	var req pluginLiveControlRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	var head struct {
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(req.Request, &head) != nil || head.Subtype == "" {
		http.Error(w, "request is an object with a subtype", http.StatusBadRequest)
		return
	}
	_, m, err := s.plugins.UI(r.Context(), pluginID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !m.LiveControl(head.Subtype) {
		http.Error(w, "the plugin's manifest doesn't declare the control request "+head.Subtype, http.StatusBadRequest)
		return
	}
	target, ok := s.pluginTarget(w, r, ch, req.ThreadID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pluginLiveControlTimeout)
	defer cancel()
	res, err := s.live.LiveControl(ctx, target.ChannelID, req.Request)
	var refused *container.LiveControlError
	switch {
	case errors.Is(err, container.ErrNoLiveSession):
		http.Error(w, "the chat has no live session", http.StatusConflict)
	case errors.Is(err, orchestrator.ErrLiveUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "the live session didn't answer in time", http.StatusGatewayTimeout)
	case errors.As(err, &refused):
		http.Error(w, refused.Message, http.StatusBadGateway)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		writeHTTPJSON(w, http.StatusOK, pluginLiveControlResponse{Response: res}, s.logger)
	}
}

// handlePutPluginLiveHold keeps the live session of the plugin's chat
// running, starting it if it isn't, until the plugin lets it go; an idle
// session isn't ended while a plugin holds it.
func (s *Server) handlePutPluginLiveHold(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginLiveCaller(w, r)
	if !ok {
		return
	}
	target, ok := s.pluginTarget(w, r, ch, r.URL.Query().Get("thread_id"))
	if !ok {
		return
	}
	if !s.live.LiveSession(r.Context(), target.ChannelID).Enabled {
		http.Error(w, "the chat doesn't run live", http.StatusConflict)
		return
	}
	if !liveHoldDone(w, s.live.HoldLive(target.ChannelID, pluginHolderPrefix+pluginID, true)) {
		return
	}
	err := s.live.KeepLiveSession(r.Context(), target.ChannelID, local.DefaultAuthorID)
	if err != nil && !errors.Is(err, container.ErrLiveSessionRunning) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePluginLiveHold lets the live session of the plugin's chat
// idle out again.
func (s *Server) handleDeletePluginLiveHold(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginLiveCaller(w, r)
	if !ok {
		return
	}
	target, ok := s.pluginTarget(w, r, ch, r.URL.Query().Get("thread_id"))
	if !ok {
		return
	}
	if liveHoldDone(w, s.live.HoldLive(target.ChannelID, pluginHolderPrefix+pluginID, false)) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// liveHoldDone answers the request and returns false when err, from
// HoldLive, failed it.
func liveHoldDone(w http.ResponseWriter, err error) bool {
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return false
	}
	return true
}

// ForwardLiveSystem sends a live session's system message, in the
// background, to the plugins that ask for its subtype. It's not broadcast to
// the app.
func (s *Server) ForwardLiveSystem(channelID, subtype string, event json.RawMessage) {
	s.ForwardToPlugins(Event{Type: plugin.EventLiveSystem, ChannelID: channelID, Timestamp: time.Now().UnixMilli(), Data: plugin.LiveSystem{Subtype: subtype, Message: event}})
}

// releasePluginHolds lets go of the live sessions held by plugins that may
// no longer control them: their chat is gone, or the plugin is removed,
// broken, no longer approved for live.control or no longer enabled by the
// chat's project. A hold whose state can't be read is kept.
func (s *Server) releasePluginHolds(ctx context.Context) {
	if s.plugins == nil || s.live == nil {
		return
	}
	for channelID, holders := range s.live.LiveHolds() {
		for _, holder := range holders {
			pluginID, ok := strings.CutPrefix(holder, pluginHolderPrefix)
			if !ok || s.pluginShowsIn(ctx, pluginID, plugin.CapLiveControl, channelID) {
				continue
			}
			if err := s.live.HoldLive(channelID, holder, false); err != nil {
				s.logger.Warn("releasing a plugin's live session", "plugin", pluginID, "channel_id", channelID, "error", err)
			}
		}
	}
}

// pluginShowsIn reports whether the plugin may still use c in the chat
// channelID, a channel or one of its listed threads, whose parent's
// project decides, or that can't be told.
func (s *Server) pluginShowsIn(ctx context.Context, pluginID string, c plugin.Capability, channelID string) bool {
	root, known := s.pluginChatRoot(ctx, channelID)
	if !known {
		return true
	}
	if root == nil {
		return false
	}
	shown, err := s.plugins.Shows(ctx, pluginID, c, pluginRuntimeChannel(root))
	return err != nil || shown
}

// pluginChatRoot returns the channel whose plugin containers serve the chat
// channelID: the chat itself, or its parent when it's a listed thread; nil
// when there's none. known is false when that can't be told.
func (s *Server) pluginChatRoot(ctx context.Context, channelID string) (root *db.Channel, known bool) {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return nil, false
	}
	if ch == nil || ch.ParentID == "" {
		return ch, true
	}
	if db.IsUnlistedKind(ch.Kind) {
		return nil, true
	}
	parent, err := s.store.GetChannel(ctx, ch.ParentID)
	if err != nil {
		return nil, false
	}
	return parent, true
}
