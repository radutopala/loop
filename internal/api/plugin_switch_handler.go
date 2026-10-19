// plugin_switch_handler.go serves the switches plugins with
// ui.composer_switch put in a chat's composer: the app lists and turns
// them, the plugin sets what they show.
package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
)

// PluginSwitchStore keeps the state of the plugins' composer switches.
type PluginSwitchStore interface {
	SetPluginSwitch(ctx context.Context, sw *db.PluginSwitch) error
	DeletePluginSwitch(ctx context.Context, pluginID, channelID, switchID string) error
	ListPluginSwitches(ctx context.Context) ([]*db.PluginSwitch, error)
}

// SetPluginSwitches keeps the composer switches' state in store and serves
// the switch routes.
func (s *Server) SetPluginSwitches(store PluginSwitchStore) {
	s.switches = store
}

const pluginSwitchesDisabled = "composer switches not configured"

// composerSwitch is a switch in a chat's composer as the app shows it: its
// plugin's label, icon, a data URL, and tooltip for when the plugin set
// none, and its state.
type composerSwitch struct {
	PluginID     string `json:"plugin_id"`
	SwitchID     string `json:"switch_id"`
	Label        string `json:"label,omitempty"`
	Icon         string `json:"icon,omitempty"`
	DefaultTitle string `json:"default_title,omitempty"`
	composerSwitchState
}

// composerSwitchState is what a switch shows: whether it's on, its status,
// "" or one of busy, ok and error, and its tooltip.
type composerSwitchState struct {
	On     bool   `json:"on"`
	Status string `json:"status,omitempty"`
	Title  string `json:"title,omitempty"`
}

// pluginSwitchRequest is the body of PUT
// /api/plugin/v1/composer/switches/{id}.
type pluginSwitchRequest struct {
	composerSwitchState
	ThreadID string `json:"thread_id"`
}

// turnSwitchRequest is the body of PUT
// /api/channels/{id}/composer-switches/{plugin}/{switch}.
type turnSwitchRequest struct {
	On bool `json:"on"`
}

// switchRefusal returns what's wrong with a switch's state, or "" when
// it's fine.
func switchRefusal(st composerSwitchState) string {
	switch {
	case !slices.Contains(proxy.SwitchStatuses, st.Status):
		return "status is one of busy, ok and error, or empty"
	case utf8.RuneCountInString(st.Title) > plugin.ComposerSwitchTitleMax:
		return "title is at most 200 characters"
	}
	return ""
}

// pluginSwitchCaller is pluginCaller for the plugin's switch routes, which
// name one of the switches its manifest declares.
func (s *Server) pluginSwitchCaller(w http.ResponseWriter, r *http.Request) (string, *db.Channel, bool) {
	p, ch, ok := s.pluginCaller(w, r, plugin.CapUISwitch)
	if !ok {
		return "", nil, false
	}
	if !requireConfigured(w, s.switches, pluginSwitchesDisabled) {
		return "", nil, false
	}
	_, m, err := s.plugins.UI(r.Context(), p.PluginID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return "", nil, false
	}
	if _, ok := m.ComposerSwitch(r.PathValue("id")); !ok {
		http.Error(w, install.ErrUnknownSwitch.Error(), http.StatusNotFound)
		return "", nil, false
	}
	return p.PluginID, ch, true
}

// handlePutPluginSwitch sets what one of the plugin's switches shows in its
// chat.
func (s *Server) handlePutPluginSwitch(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginSwitchCaller(w, r)
	if !ok {
		return
	}
	var req pluginSwitchRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := switchRefusal(req.composerSwitchState); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	target, ok := s.pluginTarget(w, r, ch, req.ThreadID)
	if !ok {
		return
	}
	sw := composerSwitch{PluginID: pluginID, SwitchID: r.PathValue("id"), composerSwitchState: req.composerSwitchState}
	if err := s.setSwitch(r.Context(), target.ChannelID, sw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePluginSwitch resets one of the plugin's switches in its chat:
// off, without a status or tooltip.
func (s *Server) handleDeletePluginSwitch(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginSwitchCaller(w, r)
	if !ok {
		return
	}
	target, ok := s.pluginTarget(w, r, ch, r.URL.Query().Get("thread_id"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.switches.DeletePluginSwitch(r.Context(), pluginID, target.ChannelID, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.broadcastSwitch(target.ChannelID, composerSwitch{PluginID: pluginID, SwitchID: id})
	w.WriteHeader(http.StatusNoContent)
}

// setSwitch stores a switch's state in the chat channelID and tells the
// chat's windows.
func (s *Server) setSwitch(ctx context.Context, channelID string, sw composerSwitch) error {
	err := s.switches.SetPluginSwitch(ctx, &db.PluginSwitch{
		PluginID: sw.PluginID, ChannelID: channelID, SwitchID: sw.SwitchID,
		On: sw.On, Status: sw.Status, Title: sw.Title,
	})
	if err != nil {
		return err
	}
	s.broadcastSwitch(channelID, sw)
	return nil
}

// broadcastSwitch tells a chat's windows a switch's state changed.
func (s *Server) broadcastSwitch(channelID string, sw composerSwitch) {
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{Type: "plugin.composer_switch", ChannelID: channelID, Data: sw})
	}
}

// switchChat returns the chat {id} of an app switch route and the channel
// whose plugin containers serve it; otherwise it answers the request and
// returns false. Side chats and hidden threads have no switches.
func (s *Server) switchChat(w http.ResponseWriter, r *http.Request) (chat, root *db.Channel, ok bool) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) || !requireConfigured(w, s.switches, pluginSwitchesDisabled) {
		return nil, nil, false
	}
	chat, err := s.store.GetChannel(r.Context(), r.PathValue("id"))
	switch {
	case err != nil:
	case chat == nil, db.IsUnlistedKind(chat.Kind):
	case chat.ParentID == "":
		root = chat
	default:
		root, err = s.store.GetChannel(r.Context(), chat.ParentID)
	}
	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, nil, false
	case root == nil:
		http.Error(w, "channel not found", http.StatusNotFound)
		return nil, nil, false
	}
	return chat, root, true
}

// handleListComposerSwitches lists the switches the plugins enabled in the
// chat put in its composer, in plugin order, with their state there.
func (s *Server) handleListComposerSwitches(w http.ResponseWriter, r *http.Request) {
	chat, root, ok := s.switchChat(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	states, err := s.plugins.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saved, err := s.switches.ListPluginSwitches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := []composerSwitch{}
	for _, st := range states {
		shown, err := s.plugins.Shows(ctx, st.PluginID, plugin.CapUISwitch, pluginRuntimeChannel(root))
		if err != nil || !shown {
			continue
		}
		dir, m, err := s.plugins.UI(ctx, st.PluginID)
		if err != nil || m.UI == nil {
			continue
		}
		for _, cs := range m.UI.ComposerSwitches {
			sw := composerSwitch{PluginID: st.PluginID, SwitchID: cs.ID, Label: cs.Label, Icon: switchIcon(dir, cs.Icon), DefaultTitle: cs.Title}
			if i := slices.IndexFunc(saved, func(v *db.PluginSwitch) bool {
				return v.PluginID == st.PluginID && v.ChannelID == chat.ChannelID && v.SwitchID == cs.ID
			}); i >= 0 {
				sw.composerSwitchState = composerSwitchState{On: saved[i].On, Status: saved[i].Status, Title: saved[i].Title}
			}
			out = append(out, sw)
		}
	}
	writeHTTPJSON(w, http.StatusOK, out, s.logger)
}

// switchIcon returns the icon at rel in a plugin's dir as a data URL; ""
// when it can't be read.
func switchIcon(dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(b)
}

// handleTurnComposerSwitch asks a plugin to turn its switch in the chat on
// or off, from its container in the chat's channel, and answers with the
// switch's state, which the plugin decides.
func (s *Server) handleTurnComposerSwitch(w http.ResponseWriter, r *http.Request) {
	chat, root, ok := s.switchChat(w, r)
	if !ok {
		return
	}
	var req turnSwitchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	pluginID, id := r.PathValue("plugin"), r.PathValue("switch")
	sreq := proxy.SwitchRequest{On: req.On}
	if chat != root {
		sreq.ThreadID = chat.ChannelID
	}
	res, err := s.plugins.Switch(r.Context(), pluginID, id, pluginRuntimeChannel(root), sreq)
	switch {
	case errors.Is(err, install.ErrNotInstalled), errors.Is(err, install.ErrUnknownSwitch):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, install.ErrNotApproved), errors.Is(err, install.ErrNotEnabled):
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	case errors.Is(err, install.ErrNotRunnable):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	st := composerSwitchState{On: res.On, Status: res.Status, Title: res.Title}
	if msg := switchRefusal(st); msg != "" {
		http.Error(w, "the plugin answered an invalid switch state: "+msg, http.StatusBadGateway)
		return
	}
	sw := composerSwitch{PluginID: pluginID, SwitchID: id, composerSwitchState: st}
	if err := s.setSwitch(r.Context(), chat.ChannelID, sw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, st, s.logger)
}

// prunePluginSwitches forgets the state of the switches no longer shown:
// those of a deleted chat, and those of a plugin that may no longer show
// them there or no longer declares them. The app learns of it from
// plugins.changed. A switch whose state can't be read is kept.
func (s *Server) prunePluginSwitches(ctx context.Context) {
	if s.plugins == nil || s.switches == nil {
		return
	}
	saved, err := s.switches.ListPluginSwitches(ctx)
	if err != nil {
		s.logger.Warn("composer switches lookup failed", "error", err)
		return
	}
	for _, sw := range saved {
		if s.pluginShowsIn(ctx, sw.PluginID, plugin.CapUISwitch, sw.ChannelID) && s.switchDeclared(ctx, sw) {
			continue
		}
		if err := s.switches.DeletePluginSwitch(ctx, sw.PluginID, sw.ChannelID, sw.SwitchID); err != nil {
			s.logger.Warn("clearing a composer switch", "plugin", sw.PluginID, "channel_id", sw.ChannelID, "switch", sw.SwitchID, "error", err)
		}
	}
}

// WakePluginSwitches starts the container of each plugin in each channel
// where one of its switches is on, at daemon start: the containers an
// earlier daemon ran are gone, and a plugin keeps up what its switch stands
// for, such as a hold on a live session, only while its container runs.
func (s *Server) WakePluginSwitches(ctx context.Context) {
	if s.plugins == nil || s.switches == nil {
		return
	}
	saved, err := s.switches.ListPluginSwitches(ctx)
	if err != nil {
		s.logger.Warn("composer switches lookup failed", "error", err)
		return
	}
	woken := map[[2]string]bool{}
	for _, sw := range saved {
		if !sw.On {
			continue
		}
		root, _ := s.pluginChatRoot(ctx, sw.ChannelID)
		if root == nil || woken[[2]string{sw.PluginID, root.ChannelID}] {
			continue
		}
		woken[[2]string{sw.PluginID, root.ChannelID}] = true
		if err := s.plugins.Wake(ctx, sw.PluginID, pluginRuntimeChannel(root)); err != nil {
			s.logger.Warn("starting a plugin with a switch on", "plugin", sw.PluginID, "channel_id", root.ChannelID, "error", err)
		}
	}
}

// switchDeclared reports whether sw's plugin still declares it, or that
// can't be told.
func (s *Server) switchDeclared(ctx context.Context, sw *db.PluginSwitch) bool {
	_, m, err := s.plugins.UI(ctx, sw.PluginID)
	if err != nil {
		return true
	}
	_, ok := m.ComposerSwitch(sw.SwitchID)
	return ok
}

// prunePluginState clears what plugins show or hold that they no longer
// may: badges, composer switches and live session holds.
func (s *Server) prunePluginState(ctx context.Context) {
	s.prunePluginBadges(ctx)
	s.prunePluginSwitches(ctx)
	s.releasePluginHolds(ctx)
}
