package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"path/filepath"
	"strings"

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

type pluginOpenFileRequest struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// handlePluginOpenFile opens a file of the calling plugin's channel in the
// app's editor, at a line when one is given: one open_file step through
// the UI bridge, in the channel. The path is relative to the channel's dir
// and stays inside it.
func (s *Server) handlePluginOpenFile(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapUIOpenFile)
	if !ok {
		return
	}
	var req pluginOpenFileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rel := path.Clean(filepath.ToSlash(req.Path))
	if req.Path == "" || path.IsAbs(rel) || filepath.IsAbs(req.Path) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		http.Error(w, "path must name a file inside the channel's dir, relative to it", http.StatusBadRequest)
		return
	}
	if req.Line < 0 {
		http.Error(w, "line must be a whole number from 1", http.StatusBadRequest)
		return
	}
	steps := []map[string]any{
		{"op": "select_channel", "channel_id": ch.ChannelID},
		{"op": "open_file", "path": rel},
	}
	if req.Line > 0 {
		steps[1]["line"] = req.Line
	}
	raw, _ := json.Marshal(steps)
	// Neither step uses a terminal, which is all runUICommand checks.
	s.runUICommand(w, r, uiCommandRequest{Steps: raw}, nil)
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
