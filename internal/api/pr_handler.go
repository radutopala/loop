package api

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/radutopala/loop/internal/githubapi"
)

// prResponse mirrors githubapi.PRInfo with `present` so the frontend can
// branch on hit/miss without distinguishing nil/{} JSON.
type prResponse struct {
	Present bool              `json:"present"`
	PR      *githubapi.PRInfo `json:"pr,omitempty"`
}

func (s *Server) handleChannelPR(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	if s.prLookup.client == nil {
		writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
		return
	}

	dirPath := ch.DirPath
	if dirPath == "" && s.loopDir != "" {
		dirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
		return
	}

	branch := gitBranch(r.Context(), dirPath)
	if branch == "" {
		writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
		return
	}

	// Serve from cache unless the caller demands freshness (?fresh=1 — used
	// by the FE right after an agent run completes). Lookup errors are never
	// cached, so transient network failures don't stick.
	if r.URL.Query().Get("fresh") != "1" {
		if resp, ok := s.prLookup.get(dirPath, branch); ok {
			writeHTTPJSON(w, http.StatusOK, resp, s.logger)
			return
		}
	}

	parentDirPath := s.workspace.resolveParentDirPath(r.Context(), channelID)
	ghUser := s.configs.ghUser(dirPath, parentDirPath)

	pr, err := s.prLookup.client.LookupPR(r.Context(), dirPath, ghUser, branch)
	if err != nil {
		// gh not installed is the most common environmental failure —
		// return present:false instead of 5xx so the UI degrades silently.
		if errors.Is(err, githubapi.ErrGhNotInstalled) {
			writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
			return
		}
		s.logger.Warn("pr lookup failed", "channel_id", channelID, "branch", branch, "err", err)
		writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
		return
	}

	resp := prResponse{Present: pr != nil, PR: pr}
	s.prLookup.put(dirPath, branch, resp)
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}
