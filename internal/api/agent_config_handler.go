package api

import (
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/config"
)

// validEfforts are the reasoning-effort levels accepted by the Claude CLI's
// --effort flag. Empty means "inherit from config".
var validEfforts = map[string]struct{}{
	"": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

// agentConfigResponse carries a channel's model/effort overrides plus the
// effective config defaults (global → project → worktree merge for the
// channel's dir), so the UI can label the "default" choice concretely.
type agentConfigResponse struct {
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	DefaultModel  string `json:"default_model"`
	DefaultEffort string `json:"default_effort"`
}

type agentConfigRequest struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// handleGetAgentConfig returns the channel's current model/effort overrides
// and the config defaults they would fall back to.
func (s *Server) handleGetAgentConfig(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	channelID := r.PathValue("id")
	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	defModel, defEffort := s.resolveClaudeDefaults(ch.DirPath, s.workspace.resolveParentDirPath(r.Context(), channelID))
	writeHTTPJSON(w, http.StatusOK, agentConfigResponse{
		Model:         ch.ModelOverride,
		Effort:        ch.EffortOverride,
		DefaultModel:  defModel,
		DefaultEffort: defEffort,
	}, s.logger)
}

// handleSetAgentConfig sets the channel's model/effort overrides. Empty
// values clear the override (inherit from config). Takes effect on the
// channel's next agent run — no restart or active-run interruption needed.
func (s *Server) handleSetAgentConfig(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	channelID := r.PathValue("id")

	var req agentConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	model := strings.TrimSpace(req.Model)
	effort := strings.TrimSpace(req.Effort)
	if _, ok := validEfforts[effort]; !ok {
		http.Error(w, "invalid effort: must be one of low, medium, high, xhigh, max (or empty)", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	if err := s.store.UpdateChannelAgentOverrides(r.Context(), channelID, model, effort); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveClaudeDefaults mirrors resolveGHUser's three-layer merge (global →
// project → worktree) for claude_model / claude_effort, so the UI can show
// what "default" resolves to for this channel's dir. Returns the global
// defaults on any config-load error.
func (s *Server) resolveClaudeDefaults(workdir, parentDirPath string) (string, string) {
	loadConfig := s.configs.load
	if loadConfig == nil {
		loadConfig = config.Load
	}
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return "", ""
	}
	merged := cfg
	switch {
	case workdir != "" && parentDirPath != "":
		loadWorktree := s.configs.loadWorktree
		if loadWorktree == nil {
			loadWorktree = config.LoadWorktreeProjectConfig
		}
		if pc, perr := loadWorktree(workdir, parentDirPath, cfg); perr == nil && pc != nil {
			merged = pc
		}
	case workdir != "":
		loadProjectConfig := s.configs.loadProject
		if loadProjectConfig == nil {
			loadProjectConfig = config.LoadProjectConfig
		}
		if pc, perr := loadProjectConfig(workdir, cfg); perr == nil && pc != nil {
			merged = pc
		}
	}
	return merged.ClaudeModel, merged.ClaudeEffort
}
