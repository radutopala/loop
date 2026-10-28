package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/gitlabapi"
	"github.com/radutopala/loop/internal/gitutil"
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

	if s.prLookup.client == nil && s.prLookup.gitlab == nil {
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
	pr, err := s.lookupBranchPR(r.Context(), dirPath, parentDirPath, branch)
	if err != nil {
		// A missing gh/glab is the most common environmental failure —
		// return present:false instead of 5xx so the UI degrades silently.
		if !errors.Is(err, githubapi.ErrGhNotInstalled) && !errors.Is(err, gitlabapi.ErrGlabNotInstalled) {
			s.logger.Warn("pr lookup failed", "channel_id", channelID, "branch", branch, "err", err)
		}
		writeHTTPJSON(w, http.StatusOK, prResponse{Present: false}, s.logger)
		return
	}

	resp := prResponse{Present: pr != nil, PR: pr}
	s.prLookup.put(dirPath, branch, resp)
	if pr != nil && s.prWatch != nil {
		s.prWatch.Watch(channelID)
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// lookupBranchPR finds branch's PR, or MR when dirPath's origin is a GitLab
// host. A dir without an origin remote has neither, so nothing is run.
func (s *Server) lookupBranchPR(ctx context.Context, dirPath, parentDirPath, branch string) (*githubapi.PRInfo, error) {
	host := gitRemoteHost(ctx, dirPath)
	if host == "" {
		return nil, nil
	}
	glabDir := s.expandHomePath(s.configs.glabConfigDir(dirPath, parentDirPath))
	if s.prLookup.gitlab != nil && s.prLookup.gitlab.IsGitLabHost(host, glabDir) {
		return s.prLookup.gitlab.LookupMR(ctx, dirPath, glabDir, branch)
	}
	if s.prLookup.client == nil {
		return nil, nil
	}
	return s.prLookup.client.LookupPR(ctx, dirPath, s.configs.ghUser(dirPath, parentDirPath), branch)
}

// gitRemoteHost returns the host of dir's origin remote, or "".
func gitRemoteHost(ctx context.Context, dir string) string {
	out, err := gitutil.Command(ctx, dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return remoteHost(strings.TrimSpace(string(out)))
}

// remoteHost extracts the host from a git remote URL, either a URL
// (https://user@host:port/path, ssh://git@host/path) or scp-like
// (git@host:path). "" when it has none, e.g. a local path.
func remoteHost(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[:j]
		}
		if j := strings.LastIndexByte(rest, '@'); j >= 0 {
			rest = rest[j+1:]
		}
		if h, _, err := net.SplitHostPort(rest); err == nil {
			return h
		}
		return rest
	}
	j := strings.IndexByte(u, ':')
	if j <= 0 || strings.ContainsRune(u[:j], '/') {
		return ""
	}
	h := u[:j]
	if k := strings.LastIndexByte(h, '@'); k >= 0 {
		h = h[k+1:]
	}
	return h
}
