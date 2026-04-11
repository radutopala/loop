package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/radutopala/loop/internal/config"
	"github.com/tailscale/hujson"
)

// Overridable for testing unreachable error paths.
var (
	jsonUnmarshalFn   = json.Unmarshal
	jsonMarshalIndent = json.MarshalIndent
)

// resolveConfigPath returns the config file path for the given scope and
// channel. It writes an HTTP error and returns ("", false) on failure.
func (s *Server) resolveConfigPath(w http.ResponseWriter, r *http.Request, scope, channelID string) (string, bool) {
	switch scope {
	case "project":
		if channelID == "" {
			http.Error(w, "channel_id is required for project scope", http.StatusBadRequest)
			return "", false
		}
		dirPath, err := s.resolveProjectConfigDirPath(r.Context(), channelID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return "", false
		}
		loopDir := filepath.Join(dirPath, ".loop")
		if err := s.sys.MkdirAll(loopDir, 0755); err != nil {
			http.Error(w, "failed to create .loop directory", http.StatusInternalServerError)
			return "", false
		}
		return filepath.Join(loopDir, "config.json"), true
	case "global", "":
		home, err := s.sys.UserHomeDir()
		if err != nil {
			http.Error(w, "cannot determine home directory", http.StatusInternalServerError)
			return "", false
		}
		return filepath.Join(home, ".loop", "config.json"), true
	default:
		http.Error(w, "scope must be 'global' or 'project'", http.StatusBadRequest)
		return "", false
	}
}

type shortcutResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

// handleListShortcuts returns the configured prompt shortcuts with resolved
// prompt text. Accepts an optional channel_id query parameter to merge
// project-level shortcuts on top of global ones.
func (s *Server) handleListShortcuts(w http.ResponseWriter, r *http.Request) {
	loadConfig := s.loadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	cfg, err := loadConfig()
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	loadProjectConfig := s.loadProjectConfig
	if loadProjectConfig == nil {
		loadProjectConfig = config.LoadProjectConfig
	}

	// Merge project-level shortcuts when a channel is specified.
	var dirPath string
	if channelID := r.URL.Query().Get("channel_id"); channelID != "" {
		dp, dirErr := s.resolveProjectConfigDirPath(r.Context(), channelID)
		if dirErr == nil && dp != "" {
			dirPath = dp
			if merged, mergeErr := loadProjectConfig(dirPath, cfg); mergeErr == nil {
				cfg = merged
			}
		}
	}

	loopDir := cfg.LoopDir
	if loopDir == "" {
		home, _ := s.sys.UserHomeDir()
		loopDir = filepath.Join(home, ".loop")
	}

	readFile := s.readFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	// Build resolution dirs: project .loop first (if available), then global.
	loopDirs := []string{loopDir}
	if dirPath != "" {
		loopDirs = []string{filepath.Join(dirPath, ".loop"), loopDir}
	}

	result := make([]shortcutResponse, 0, len(cfg.PromptShortcuts))
	for _, sc := range cfg.PromptShortcuts {
		var prompt string
		var resolved bool
		for _, ld := range loopDirs {
			p, err := sc.ResolvePrompt(ld, readFile)
			if err == nil {
				prompt = p
				resolved = true
				break
			}
		}
		if !resolved {
			s.logger.Warn("skipping shortcut with unresolvable prompt", "name", sc.Name)
			continue
		}
		result = append(result, shortcutResponse{
			Name:        sc.Name,
			Description: sc.Description,
			Prompt:      prompt,
		})
	}

	writeHTTPJSON(w, http.StatusOK, result, s.logger)
}

type shortcutModifyRequest struct {
	Action      string `json:"action"`      // "add", "update", "delete"
	Scope       string `json:"scope"`       // "global" or "project"
	ChannelID   string `json:"channel_id"`  // required for project scope
	Name        string `json:"name"`        // shortcut name (required)
	Description string `json:"description"` // optional for add/update
	Prompt      string `json:"prompt"`      // inline prompt (mutually exclusive with prompt_path)
	PromptPath  string `json:"prompt_path"` // file-based prompt (mutually exclusive with prompt)
}

// handleModifyShortcut adds, updates, or deletes a prompt shortcut in the
// global or project config file.
func (s *Server) handleModifyShortcut(w http.ResponseWriter, r *http.Request) {
	var req shortcutModifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Action != "add" && req.Action != "update" && req.Action != "delete" {
		http.Error(w, "action must be add, update, or delete", http.StatusBadRequest)
		return
	}
	if (req.Action == "add" || req.Action == "update") && req.Prompt == "" && req.PromptPath == "" {
		http.Error(w, "prompt or prompt_path is required for add/update", http.StatusBadRequest)
		return
	}
	if req.Prompt != "" && req.PromptPath != "" {
		http.Error(w, "prompt and prompt_path are mutually exclusive", http.StatusBadRequest)
		return
	}

	// Resolve the config file path based on scope.
	configPath, ok := s.resolveConfigPath(w, r, req.Scope, req.ChannelID)
	if !ok {
		return
	}

	// Read existing config.
	configData, err := s.sys.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			http.Error(w, "failed to read config file", http.StatusInternalServerError)
			return
		}
		configData = []byte("{}")
	}

	// Standardize HJSON to JSON, parse into generic map.
	standardized, err := hujson.Standardize(configData)
	if err != nil {
		http.Error(w, "config file contains invalid HJSON", http.StatusInternalServerError)
		return
	}
	var configMap map[string]any
	if err := jsonUnmarshalFn(standardized, &configMap); err != nil {
		http.Error(w, "config file contains invalid JSON", http.StatusInternalServerError)
		return
	}

	// Extract existing prompt_shortcuts array.
	var shortcuts []map[string]any
	if raw, ok := configMap["prompt_shortcuts"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					shortcuts = append(shortcuts, m)
				}
			}
		}
	}

	switch req.Action {
	case "add":
		// Check for duplicate name.
		for _, sc := range shortcuts {
			if sc["name"] == req.Name {
				http.Error(w, "shortcut with this name already exists; use update to modify it", http.StatusConflict)
				return
			}
		}
		entry := map[string]any{"name": req.Name}
		if req.Description != "" {
			entry["description"] = req.Description
		}
		if req.Prompt != "" {
			entry["prompt"] = req.Prompt
		}
		if req.PromptPath != "" {
			entry["prompt_path"] = req.PromptPath
		}
		shortcuts = append(shortcuts, entry)

	case "update":
		found := false
		for _, sc := range shortcuts {
			if sc["name"] == req.Name {
				found = true
				if req.Description != "" {
					sc["description"] = req.Description
				}
				// Replace prompt fields: clear the other when one is set.
				if req.Prompt != "" {
					sc["prompt"] = req.Prompt
					delete(sc, "prompt_path")
				}
				if req.PromptPath != "" {
					sc["prompt_path"] = req.PromptPath
					delete(sc, "prompt")
				}
				break
			}
		}
		if !found {
			http.Error(w, "shortcut not found", http.StatusNotFound)
			return
		}

	case "delete":
		found := false
		filtered := shortcuts[:0]
		for _, sc := range shortcuts {
			if sc["name"] == req.Name {
				found = true
				continue
			}
			filtered = append(filtered, sc)
		}
		if !found {
			http.Error(w, "shortcut not found", http.StatusNotFound)
			return
		}
		shortcuts = filtered
	}

	// Write back.
	configMap["prompt_shortcuts"] = shortcuts
	out, err := jsonMarshalIndent(configMap, "", "  ")
	if err != nil {
		http.Error(w, "failed to serialize config", http.StatusInternalServerError)
		return
	}
	if err := s.sys.WriteFile(configPath, append(out, '\n'), 0644); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
