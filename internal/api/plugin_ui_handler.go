package api

import (
	"context"
	"net/http"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

// handlePluginUICommand runs UI bridge steps for the calling plugin, in its
// channel: like an agent's command, it opens the channel first.
func (s *Server) handlePluginUICommand(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapUIRun)
	if !ok {
		return
	}
	req, steps, ok := parseUICommand(w, r)
	if !ok {
		return
	}
	msg, err := s.uiPluginRefusal(r.Context(), ch, steps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg != "" {
		http.Error(w, msg, http.StatusForbidden)
		return
	}
	s.runUICommand(w, r, req, steps)
}

// uiPluginRefusal returns why the plugin serving ch may not run steps, or
// "" when it may. Its command starts with select_channel, and each one opens
// ch or one of ch's threads the user sees.
func (s *Server) uiPluginRefusal(ctx context.Context, ch *db.Channel, steps []uiStep) (string, error) {
	if steps[0].Op != "select_channel" {
		return "a plugin's command starts with select_channel, to its channel or one of its threads", nil
	}
	for _, step := range steps {
		if step.Op != "select_channel" || step.ChannelID == ch.ChannelID {
			continue
		}
		if step.ChannelID == "" {
			return "a plugin's select_channel needs a channel_id", nil
		}
		thread, err := s.store.GetChannel(ctx, step.ChannelID)
		if err != nil {
			return "", err
		}
		if thread == nil || thread.ParentID != ch.ChannelID || db.IsHiddenKind(thread.Kind) {
			return "channel " + step.ChannelID + " isn't the plugin's channel or one of its threads", nil
		}
	}
	return "", nil
}
