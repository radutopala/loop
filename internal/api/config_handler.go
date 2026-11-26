package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/config/hjsonedit"
	"github.com/tailscale/hujson"
)

type configResponse struct {
	Path    string         `json:"path"`
	Content map[string]any `json:"content"`
	Raw     string         `json:"raw"`
	// Hash names the file's text as read, empty for a missing file; a save
	// sends it back as base_hash.
	Hash string `json:"hash"`
}

type configSaveRequest struct {
	Content string `json:"content"`
	// BaseHash, when set, is the hash of the file the content was made
	// from. The save is refused when the file has changed since, so it
	// doesn't overwrite an edit made in between.
	BaseHash *string `json:"base_hash,omitempty"`
}

// configSaveResponse carries the hash of the file as saved, to send as the
// next save's base_hash.
type configSaveResponse struct {
	Hash string `json:"hash"`
}

// configHash names a config file's text; a missing file has the empty hash.
func configHash(data []byte, exists bool) string {
	if !exists {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// configChanged reports, with the config lock held, whether the file at
// path no longer has the text req was made from. It writes the response
// when it has changed or can't be read.
func configChanged(w http.ResponseWriter, read func(string) ([]byte, error), path string, req configSaveRequest) bool {
	if req.BaseHash == nil {
		return false
	}
	data, err := read(path)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "failed to read config file", http.StatusInternalServerError)
		return true
	}
	if configHash(data, err == nil) != *req.BaseHash {
		http.Error(w, "the config file changed since it was loaded; reload it and save again", http.StatusConflict)
		return true
	}
	return false
}

// handleConfigSchema returns the JSON schema describing all configuration fields.
func (s *Server) handleConfigSchema(w http.ResponseWriter, _ *http.Request) {
	writeHTTPJSON(w, http.StatusOK, config.GlobalConfigSchema(), s.logger)
}

// handleGetConfig reads the global ~/.loop/config.json and returns it as
// structured content plus raw text.
func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	home, err := s.sys.UserHomeDir()
	if err != nil {
		http.Error(w, "cannot determine home directory", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(home, ".loop", "config.json")
	resp, err := readConfigFile(s.sys.ReadFile, path)
	if err != nil {
		http.Error(w, "failed to read config file", http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// handleSaveConfig writes JSON content to the global ~/.loop/config.json,
// reformatted with its comments kept.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req configSaveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	content, err := hjsonedit.Format([]byte(req.Content))
	if err != nil {
		http.Error(w, "content is not valid HJSON", http.StatusBadRequest)
		return
	}

	home, err := s.sys.UserHomeDir()
	if err != nil {
		http.Error(w, "cannot determine home directory", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(home, ".loop", "config.json")
	// A whole-file write, but it must not land inside another edit's
	// read-modify-write of the file, which would then overwrite it.
	defer s.lockConfig(path, "settings")()
	if configChanged(w, s.sys.ReadFile, path, req) {
		return
	}
	if err := writeConfigAtomic(s.sys, path, content); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, configSaveResponse{Hash: configHash(content, true)}, s.logger)
}

// handleGetProjectConfig reads the project-level .loop/config.json for the
// given channel.
func (s *Server) handleGetProjectConfig(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	dirPath, err := s.resolveProjectConfigDirPath(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Say why when a symlink leads it out, rather than show an empty config
	// the owner would take for the real one.
	path, err := projectConfigFile(dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Read through the project dir: a link swapped in after the check
	// still can't lead out.
	resp, err := readConfigFile(func(p string) ([]byte, error) { return s.sys.ReadFileIn(dirPath, p) }, path)
	if err != nil {
		http.Error(w, "failed to read config file", http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// handleSaveProjectConfig writes JSON content to the project-level
// .loop/config.json for the given channel, reformatted like the global one.
func (s *Server) handleSaveProjectConfig(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	dirPath, err := s.resolveProjectConfigDirPath(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req configSaveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	content, err := hjsonedit.Format([]byte(req.Content))
	if err != nil {
		http.Error(w, "content is not valid HJSON", http.StatusBadRequest)
		return
	}

	path, err := projectConfigFile(dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.sys.MkdirAllIn(dirPath, filepath.Dir(path), 0755); err != nil {
		http.Error(w, "failed to create .loop directory", http.StatusInternalServerError)
		return
	}
	// See handleSaveConfig.
	defer s.lockConfig(path, "settings")()
	if configChanged(w, func(p string) ([]byte, error) { return s.sys.ReadFileIn(dirPath, p) }, path, req) {
		return
	}
	before, keep := s.readProjectConfigBefore(path)
	if err := writeConfigAtomic(projectFS{s.sys, dirPath}, path, content); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	if keep {
		s.keepProjectTrust(dirPath, before, content)
	}
	writeHTTPJSON(w, http.StatusOK, configSaveResponse{Hash: configHash(content, true)}, s.logger)
}

// resolveProjectConfigDirPath returns the directory to use for project config.
// Worktree channels — and threads nested under them, such as task threads
// created under a worktree thread — delegate config to the root project so all
// worktrees share the root's .loop/config.json.
func (s *Server) resolveProjectConfigDirPath(ctx context.Context, channelID string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("dir_path or channel_id is required")
	}
	if s.store == nil {
		return "", fmt.Errorf("channel lookup not configured")
	}
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up channel: %w", err)
	}
	if ch == nil {
		return "", fmt.Errorf("channel %s not found", channelID)
	}
	// Worktree channels (and threads living under them) delegate config to the
	// nearest non-worktree ancestor — the root project checkout.
	if root := worktreeRootDirPath(ctx, s.store, ch); root != "" {
		return root, nil
	}
	if ch.DirPath == "" {
		if s.loopDir != "" {
			return filepath.Join(s.loopDir, channelID, "work"), nil
		}
		return "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, nil
}

// resolveWorkflowConfigPaths returns dirPath and parentDirPath for workflow
// config resolution. For worktree channels — and threads nested under them —
// parentDirPath is set to the root project's dir so that three-layer merging
// (global → root project → worktree) can be performed. For channels/threads
// outside a worktree chain, parentDirPath is empty.
func (s *Server) resolveWorkflowConfigPaths(ctx context.Context, channelID string) (dirPath, parentDirPath string, err error) {
	if channelID == "" {
		return "", "", fmt.Errorf("channel_id is required")
	}
	if s.store == nil {
		return "", "", fmt.Errorf("channel lookup not configured")
	}
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", "", fmt.Errorf("looking up channel: %w", err)
	}
	if ch == nil {
		return "", "", fmt.Errorf("channel %s not found", channelID)
	}
	if root := worktreeRootDirPath(ctx, s.store, ch); root != "" {
		return ch.DirPath, root, nil
	}
	if ch.DirPath == "" {
		if s.loopDir != "" {
			return filepath.Join(s.loopDir, channelID, "work"), "", nil
		}
		return "", "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, "", nil
}

// readConfigFile reads a config file with read and returns a configResponse.
// A missing file gives nil content and empty raw. Any other read error is
// returned: shown as a missing file, the empty config would be saved over
// the real one.
func readConfigFile(read func(string) ([]byte, error), path string) (configResponse, error) {
	data, err := read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configResponse{Path: path}, nil
		}
		return configResponse{}, err
	}

	raw := string(data)
	hash := configHash(data, true)

	// Standardize HJSON to valid JSON for parsing.
	standardized, err := hujson.Standardize(data)
	if err != nil {
		// Return raw even if HJSON parsing fails.
		return configResponse{Path: path, Raw: raw, Hash: hash}, nil
	}

	var content map[string]any
	if err := json.Unmarshal(standardized, &content); err != nil {
		return configResponse{Path: path, Raw: raw, Hash: hash}, nil
	}

	return configResponse{Path: path, Content: content, Raw: raw, Hash: hash}, nil
}
