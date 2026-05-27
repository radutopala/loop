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

// resolveShortcutContext loads the global config, optionally merges a
// project-level overlay when channel_id is present, and returns the resolution
// dirs and a readFile to use for loading file-backed shortcut bodies. Writes
// an HTTP error and returns ok=false on failure.
func (s *Server) resolveShortcutContext(w http.ResponseWriter, r *http.Request) (cfg *config.Config, loopDirs []string, readFile func(string) ([]byte, error), ok bool) {
	loadConfig := s.loadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	c, err := loadConfig()
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return nil, nil, nil, false
	}

	loadProjectConfig := s.loadProjectConfig
	if loadProjectConfig == nil {
		loadProjectConfig = config.LoadProjectConfig
	}

	var dirPath string
	if channelID := r.URL.Query().Get("channel_id"); channelID != "" {
		dp, dirErr := s.resolveProjectConfigDirPath(r.Context(), channelID)
		if dirErr == nil && dp != "" {
			dirPath = dp
			if merged, mergeErr := loadProjectConfig(dirPath, c); mergeErr == nil {
				c = merged
			}
		}
	}

	loopDir := c.LoopDir
	if loopDir == "" {
		home, _ := s.sys.UserHomeDir()
		loopDir = filepath.Join(home, ".loop")
	}

	rf := s.readFile
	if rf == nil {
		rf = os.ReadFile
	}

	dirs := []string{loopDir}
	if dirPath != "" {
		dirs = []string{filepath.Join(dirPath, ".loop"), loopDir}
	}
	return c, dirs, rf, true
}

// listShortcutItems is the generic resolution + collection loop used by both
// list handlers. It walks `items`, tries each loopDir until `resolve`
// succeeds, and either calls `mapResult` or `onUnresolved`.
func listShortcutItems[T any, R any](
	items []T,
	loopDirs []string,
	readFile func(string) ([]byte, error),
	resolve func(item T, dir string, rf func(string) ([]byte, error)) (string, error),
	mapResult func(item T, value string) R,
	onUnresolved func(item T),
) []R {
	result := make([]R, 0, len(items))
	for _, it := range items {
		var value string
		var resolved bool
		for _, ld := range loopDirs {
			v, err := resolve(it, ld, readFile)
			if err == nil {
				value = v
				resolved = true
				break
			}
		}
		if !resolved {
			onUnresolved(it)
			continue
		}
		result = append(result, mapResult(it, value))
	}
	return result
}

// handleListShortcuts returns the configured prompt shortcuts with resolved
// prompt text. Accepts an optional channel_id query parameter to merge
// project-level shortcuts on top of global ones.
func (s *Server) handleListShortcuts(w http.ResponseWriter, r *http.Request) { //nolint:dupl
	cfg, loopDirs, readFile, ok := s.resolveShortcutContext(w, r)
	if !ok {
		return
	}
	result := listShortcutItems(
		cfg.PromptShortcuts, loopDirs, readFile,
		func(sc config.PromptShortcut, dir string, rf func(string) ([]byte, error)) (string, error) {
			return sc.ResolvePrompt(dir, rf)
		},
		func(sc config.PromptShortcut, value string) shortcutResponse {
			return shortcutResponse{Name: sc.Name, Description: sc.Description, Prompt: value}
		},
		func(sc config.PromptShortcut) {
			s.logger.Warn("skipping shortcut with unresolvable prompt", "name", sc.Name)
		},
	)
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

// shortcutEntryRequest is the field-agnostic view of a shortcut-modify request
// passed to the shared writer. Inline / Path are the value pair that differs by
// shortcut kind (prompt|prompt_path vs command|command_path).
type shortcutEntryRequest struct {
	Action      string
	Scope       string
	ChannelID   string
	Name        string
	Description string
	Inline      string
	Path        string
}

// shortcutEntryFields names the config-map keys for a shortcut kind.
type shortcutEntryFields struct {
	ArrayKey    string // "prompt_shortcuts" or "bash_shortcuts"
	InlineField string // "prompt" or "command"
	PathField   string // "prompt_path" or "command_path"
}

// handleModifyShortcut adds, updates, or deletes a prompt shortcut in the
// global or project config file.
func (s *Server) handleModifyShortcut(w http.ResponseWriter, r *http.Request) {
	var req shortcutModifyRequest
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
			Inline:      req.Prompt,
			Path:        req.PromptPath,
		},
		shortcutEntryFields{
			ArrayKey:    "prompt_shortcuts",
			InlineField: "prompt",
			PathField:   "prompt_path",
		},
	)
}

// modifyShortcutEntry is the shared add/update/delete writer for prompt and
// bash shortcuts. The two kinds share the entire flow except for the array key
// and the inline/path field names.
func (s *Server) modifyShortcutEntry(w http.ResponseWriter, r *http.Request, req shortcutEntryRequest, f shortcutEntryFields) {
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Action != "add" && req.Action != "update" && req.Action != "delete" {
		http.Error(w, "action must be add, update, or delete", http.StatusBadRequest)
		return
	}
	if (req.Action == "add" || req.Action == "update") && req.Inline == "" && req.Path == "" {
		http.Error(w, f.InlineField+" or "+f.PathField+" is required for add/update", http.StatusBadRequest)
		return
	}
	if req.Inline != "" && req.Path != "" {
		http.Error(w, f.InlineField+" and "+f.PathField+" are mutually exclusive", http.StatusBadRequest)
		return
	}

	configPath, ok := s.resolveConfigPath(w, r, req.Scope, req.ChannelID)
	if !ok {
		return
	}

	configData, err := s.sys.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			http.Error(w, "failed to read config file", http.StatusInternalServerError)
			return
		}
		configData = []byte("{}")
	}

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

	var shortcuts []map[string]any
	if raw, ok := configMap[f.ArrayKey]; ok {
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
		if req.Inline != "" {
			entry[f.InlineField] = req.Inline
		}
		if req.Path != "" {
			entry[f.PathField] = req.Path
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
				if req.Inline != "" {
					sc[f.InlineField] = req.Inline
					delete(sc, f.PathField)
				}
				if req.Path != "" {
					sc[f.PathField] = req.Path
					delete(sc, f.InlineField)
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

	configMap[f.ArrayKey] = shortcuts
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
