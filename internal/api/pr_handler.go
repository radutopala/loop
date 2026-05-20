package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/githubapi"
)

// GitHubLookup is the subset of githubapi.Client the PR endpoint needs.
// It is an interface so tests can stub `gh` invocations without a real
// binary on PATH.
type GitHubLookup interface {
	LookupPR(ctx context.Context, workdir, ghUser, branch string) (*githubapi.PRInfo, error)
}

// SetGitHubLookup wires the GitHub PR lookup client used by
// GET /api/channels/{id}/pr. The endpoint returns an empty response when
// no client is configured — useful in tests and headless environments.
func (s *Server) SetGitHubLookup(g GitHubLookup) {
	s.githubLookup = g
}

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

	if s.githubLookup == nil {
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

	parentDirPath := s.resolveParentDirPath(r.Context(), channelID)
	ghUser := s.resolveGHUser(dirPath, parentDirPath)

	pr, err := s.githubLookup.LookupPR(r.Context(), dirPath, ghUser, branch)
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
	if pr == nil {
		writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
		return
	}

	writeHTTPJSON(w, http.StatusOK, prResponse{Present: true, PR: pr}, s.logger)
}

// resolveGHUser returns the gh CLI user for the channel's workdir. For
// worktree channels (parentDirPath != "") the merge is three-layered:
// global → parent project → worktree, so the parent project's
// github.gh_user setting applies even when the worktree's own
// .loop/config.json is empty. Falls back to "" (use gh's active account)
// on any load error.
func (s *Server) resolveGHUser(workdir, parentDirPath string) string {
	loadConfig := s.loadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return ""
	}
	merged := cfg
	switch {
	case workdir != "" && parentDirPath != "":
		loadWorktree := s.loadWorktreeProjectConfig
		if loadWorktree == nil {
			loadWorktree = config.LoadWorktreeProjectConfig
		}
		if pc, perr := loadWorktree(workdir, parentDirPath, cfg); perr == nil && pc != nil {
			merged = pc
		}
	case workdir != "":
		loadProjectConfig := s.loadProjectConfig
		if loadProjectConfig == nil {
			loadProjectConfig = config.LoadProjectConfig
		}
		if pc, perr := loadProjectConfig(workdir, cfg); perr == nil && pc != nil {
			merged = pc
		}
	}
	return merged.GitHub.GHUser
}

// resolveReviewEnabled mirrors resolveGHUser for the review.enabled flag.
// The layering is global → project → worktree; the worktree's own
// .loop/config.json wins, falling through to the parent project and then
// to global when an inner layer doesn't set Enabled explicitly. Returns
// false on any config-load error so a broken config doesn't silently
// expose the panel.
func (s *Server) resolveReviewEnabled(workdir, parentDirPath string) bool {
	loadConfig := s.loadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return false
	}
	merged := cfg
	switch {
	case workdir != "" && parentDirPath != "":
		loadWorktree := s.loadWorktreeProjectConfig
		if loadWorktree == nil {
			loadWorktree = config.LoadWorktreeProjectConfig
		}
		if pc, perr := loadWorktree(workdir, parentDirPath, cfg); perr == nil && pc != nil {
			merged = pc
		}
	case workdir != "":
		loadProjectConfig := s.loadProjectConfig
		if loadProjectConfig == nil {
			loadProjectConfig = config.LoadProjectConfig
		}
		if pc, perr := loadProjectConfig(workdir, cfg); perr == nil && pc != nil {
			merged = pc
		}
	}
	return merged.Review.Enabled
}
