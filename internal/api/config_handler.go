package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/radutopala/loop/internal/config"
	"github.com/tailscale/hujson"
)

type configResponse struct {
	Path    string         `json:"path"`
	Content map[string]any `json:"content"`
	Raw     string         `json:"raw"`
}

type configSaveRequest struct {
	Content string `json:"content"`
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
	resp := readConfigFile(s.sys, path)
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// handleSaveConfig writes JSON content to the global ~/.loop/config.json.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req configSaveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := hujson.Standardize([]byte(req.Content)); err != nil {
		http.Error(w, "content is not valid HJSON", http.StatusBadRequest)
		return
	}

	home, err := s.sys.UserHomeDir()
	if err != nil {
		http.Error(w, "cannot determine home directory", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(home, ".loop", "config.json")
	if err := s.sys.WriteFile(path, []byte(req.Content), 0644); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	path := filepath.Join(dirPath, ".loop", "config.json")
	resp := readConfigFile(s.sys, path)
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// handleSaveProjectConfig writes JSON content to the project-level
// .loop/config.json for the given channel.
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
	if _, err := hujson.Standardize([]byte(req.Content)); err != nil {
		http.Error(w, "content is not valid HJSON", http.StatusBadRequest)
		return
	}

	loopDir := filepath.Join(dirPath, ".loop")
	if err := s.sys.MkdirAll(loopDir, 0755); err != nil {
		http.Error(w, "failed to create .loop directory", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(loopDir, "config.json")
	if err := s.sys.WriteFile(path, []byte(req.Content), 0644); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveProjectConfigDirPath returns the directory to use for project config.
// For worktree channels it returns the parent channel's dir path, so all
// worktrees share the root project's .loop/config.json.
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
	// Worktree channels delegate config to the parent project.
	if ch.Worktree && ch.ParentID != "" {
		parent, err := s.store.GetChannel(ctx, ch.ParentID)
		if err != nil {
			return "", fmt.Errorf("looking up parent channel: %w", err)
		}
		if parent != nil && parent.DirPath != "" {
			return parent.DirPath, nil
		}
	}
	if ch.DirPath == "" {
		if s.loopDir != "" {
			return filepath.Join(s.loopDir, channelID, "work"), nil
		}
		return "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, nil
}

// readConfigFile reads a config file and returns a configResponse.
// If the file does not exist, it returns a response with nil content and empty raw.
func readConfigFile(sys serverSystem, path string) configResponse {
	data, err := sys.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configResponse{Path: path}
		}
		return configResponse{Path: path}
	}

	raw := string(data)

	// Standardize HJSON to valid JSON for parsing.
	standardized, err := hujson.Standardize(data)
	if err != nil {
		// Return raw even if HJSON parsing fails.
		return configResponse{Path: path, Raw: raw}
	}

	var content map[string]any
	if err := json.Unmarshal(standardized, &content); err != nil {
		return configResponse{Path: path, Raw: raw}
	}

	return configResponse{Path: path, Content: content, Raw: raw}
}
