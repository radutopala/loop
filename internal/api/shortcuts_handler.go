package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/tailscale/hujson"
)

// Overridable for testing unreachable error paths.
var (
	jsonUnmarshalFn   = json.Unmarshal
	jsonMarshalIndent = json.MarshalIndent
)

// projectConfigFile returns dirPath's .loop/config.json for a write. The
// .loop dir is the agent's to write, so a config.json or .loop that a
// symlink leads outside the project is refused: written through, it would
// change another file, such as the global config, as the project's.
func projectConfigFile(dirPath string) (string, error) {
	path := filepath.Join(dirPath, ".loop", "config.json")
	if err := osutil.CheckIn(dirPath, path); err != nil {
		return "", fmt.Errorf("project config leads outside the project: %w", err)
	}
	return path, nil
}

// readScopedConfig and writeScopedConfig reach a project config file, which
// resolveConfigPath returned, through its project dir: a symlink swapped
// into .loop after the check still can't lead them out of the project. The
// global config is the owner's own.
func (s *Server) readScopedConfig(scope, path string) ([]byte, error) {
	if scope == "project" {
		return s.sys.ReadFileIn(filepath.Dir(filepath.Dir(path)), path)
	}
	return s.sys.ReadFile(path)
}

// projectFS is the hjsonedit.FS of an edit of a project config: every path
// it reaches, its temp file included, stays under the project dir root.
type projectFS struct {
	sys  serverSystem
	root string
}

func (f projectFS) ReadFile(name string) ([]byte, error) { return f.sys.ReadFileIn(f.root, name) }
func (f projectFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return f.sys.WriteFileIn(f.root, name, data, perm)
}
func (f projectFS) Rename(oldpath, newpath string) error {
	return f.sys.RenameIn(f.root, oldpath, newpath)
}
func (f projectFS) Remove(name string) error { return f.sys.RemoveIn(f.root, name) }
func (f projectFS) MkdirAll(path string, perm os.FileMode) error {
	return f.sys.MkdirAllIn(f.root, path, perm)
}

func (s *Server) writeScopedConfig(scope, path string, data []byte) error {
	if scope == "project" {
		return s.sys.WriteFileIn(filepath.Dir(filepath.Dir(path)), path, data, 0644)
	}
	return s.sys.WriteFile(path, data, 0644)
}

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
		configPath, err := projectConfigFile(dirPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return "", false
		}
		if err := s.sys.MkdirAllIn(dirPath, filepath.Dir(configPath), 0755); err != nil {
			http.Error(w, "failed to create .loop directory", http.StatusInternalServerError)
			return "", false
		}
		return configPath, true
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
	Scope       string `json:"scope"`
}

// scopeLabeler reports which config file a shortcut name came from, so list
// output can tell an agent the scope to pass back to add/update/delete. Names
// defined in the project overlay win, mirroring the merge order.
func scopeLabeler(projectNames map[string]bool) func(name string) string {
	return func(name string) string {
		if projectNames[name] {
			return "project"
		}
		return "global"
	}
}

// resolveShortcutContext loads the global config, optionally merges a
// project-level overlay when channel_id is present, and returns the resolution
// dirs and a readFile to use for loading file-backed shortcut bodies. projectCfg
// holds only the project overlay's own entries (nil when there is no overlay) so
// callers can label each shortcut's scope. Writes an HTTP error and returns
// ok=false on failure.
func (s *Server) resolveShortcutContext(w http.ResponseWriter, r *http.Request) (cfg *config.Config, projectCfg *config.Config, loopDirs []string, readFile func(string) ([]byte, error), ok bool) {
	loadConfig := s.configs.load
	if loadConfig == nil {
		loadConfig = config.Load
	}
	c, err := loadConfig()
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return nil, nil, nil, nil, false
	}

	loadProjectConfig := s.configs.projectLoader()

	var dirPath string
	var projectOnly *config.Config
	if channelID := r.URL.Query().Get("channel_id"); channelID != "" {
		dp, dirErr := s.resolveProjectConfigDirPath(r.Context(), channelID)
		if dirErr == nil && dp != "" {
			dirPath = dp
			if merged, mergeErr := loadProjectConfig(dirPath, c); mergeErr == nil {
				c = merged
			}
			// Merging onto an empty base yields the overlay's own entries,
			// which is what distinguishes project shortcuts from global ones.
			if only, onlyErr := loadProjectConfig(dirPath, &config.Config{}); onlyErr == nil {
				projectOnly = only
			}
		}
	}

	loopDir := c.LoopDir
	if loopDir == "" {
		home, _ := s.sys.UserHomeDir()
		loopDir = filepath.Join(home, ".loop")
	}

	dirs := []string{loopDir}
	if dirPath != "" {
		dirs = []string{filepath.Join(dirPath, ".loop"), loopDir}
	}
	rf := s.readFile
	if rf == nil {
		rf = projectFileReader(dirPath)
	}
	return c, projectOnly, dirs, rf, true
}

// projectFileReader reads the files a shortcut or component points at. Those
// under the project's .loop dir, which the agent may write, are read without
// leaving the project, so a symlink there can't serve a host file; the
// global ~/.loop is the user's own and is read as is.
func projectFileReader(dirPath string) func(string) ([]byte, error) {
	if dirPath == "" {
		return os.ReadFile
	}
	projectLoopDir := filepath.Join(dirPath, ".loop") + string(filepath.Separator)
	return func(path string) ([]byte, error) {
		if strings.HasPrefix(path, projectLoopDir) {
			return osutil.ReadFileIn(dirPath, path)
		}
		return os.ReadFile(path)
	}
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
	cfg, projectCfg, loopDirs, readFile, ok := s.resolveShortcutContext(w, r)
	if !ok {
		return
	}
	projectNames := make(map[string]bool)
	if projectCfg != nil {
		for _, sc := range projectCfg.PromptShortcuts {
			projectNames[sc.Name] = true
		}
	}
	scopeOf := scopeLabeler(projectNames)
	result := listShortcutItems(
		cfg.PromptShortcuts, loopDirs, readFile,
		func(sc config.PromptShortcut, dir string, rf func(string) ([]byte, error)) (string, error) {
			return sc.ResolvePrompt(dir, rf)
		},
		func(sc config.PromptShortcut, value string) shortcutResponse {
			return shortcutResponse{Name: sc.Name, Description: sc.Description, Prompt: value, Scope: scopeOf(sc.Name)}
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

// notFoundInScope explains which config file was searched. A shortcut defined
// in the other scope is invisible here, so naming the file that was read turns
// a bare 404 into the next step: retry with the scope `list` reported.
func notFoundInScope(scope, configPath string) string {
	if scope == "" {
		scope = "global"
	}
	return fmt.Sprintf("shortcut not found in %s scope (%s); check the scope reported by list", scope, configPath)
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
	defer s.lockConfig(configPath, "shortcuts")()

	configData, err := s.readScopedConfig(req.Scope, configPath)
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
			http.Error(w, notFoundInScope(req.Scope, configPath), http.StatusNotFound)
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
			http.Error(w, notFoundInScope(req.Scope, configPath), http.StatusNotFound)
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
	out = append(out, '\n')
	if err := s.writeScopedConfig(req.Scope, configPath, out); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	// An agent's shortcut change waits for the owner, like any other agent
	// edit of the project config.
	if req.Scope == "project" && s.projectTrust != nil && !isAgentRequest(r) {
		s.keepProjectTrust(filepath.Dir(filepath.Dir(configPath)), configData, out)
	}
	w.WriteHeader(http.StatusNoContent)
}
