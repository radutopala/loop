package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/config"
)

type bashShortcutResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

// handleListBashShortcuts returns the configured bash shortcuts with resolved
// command text. Accepts an optional channel_id query parameter to merge
// project-level shortcuts on top of global ones.
func (s *Server) handleListBashShortcuts(w http.ResponseWriter, r *http.Request) { //nolint:dupl
	cfg, loopDirs, readFile, ok := s.resolveShortcutContext(w, r)
	if !ok {
		return
	}
	result := listShortcutItems(
		cfg.BashShortcuts, loopDirs, readFile,
		func(sc config.BashShortcut, dir string, rf func(string) ([]byte, error)) (string, error) {
			return sc.ResolveCommand(dir, rf)
		},
		func(sc config.BashShortcut, value string) bashShortcutResponse {
			return bashShortcutResponse{Name: sc.Name, Description: sc.Description, Command: value}
		},
		func(sc config.BashShortcut) {
			s.logger.Warn("skipping bash shortcut with unresolvable command", "name", sc.Name)
		},
	)
	writeHTTPJSON(w, http.StatusOK, result, s.logger)
}

type bashShortcutModifyRequest struct {
	Action      string `json:"action"`
	Scope       string `json:"scope"`
	ChannelID   string `json:"channel_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
	CommandPath string `json:"command_path"`
}

// handleModifyBashShortcut adds, updates, or deletes a bash shortcut in the
// global or project config file.
func (s *Server) handleModifyBashShortcut(w http.ResponseWriter, r *http.Request) {
	var req bashShortcutModifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.modifyShortcutEntry(w, r,
		shortcutEntryRequest{
			Action:      req.Action,
			Scope:       req.Scope,
			ChannelID:   req.ChannelID,
			Name:        req.Name,
			Description: req.Description,
			Inline:      req.Command,
			Path:        req.CommandPath,
		},
		shortcutEntryFields{
			ArrayKey:    "bash_shortcuts",
			InlineField: "command",
			PathField:   "command_path",
		},
	)
}
