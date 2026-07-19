package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

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

// prCacheTTL bounds how long a PR lookup (hit or miss) is served from cache.
// Every lookup is a `gh` subprocess doing a network round-trip (1-3s through
// a proxy), and the Git panel fires one per channel select — mostly for
// branches with no PR at all. The cache is also invalidated eagerly when the
// branch poller sees the dir's branch/commit change, and the frontend can
// bypass it with ?fresh=1 (used right after an agent run completes, when a
// new PR is most likely).
const prCacheTTL = time.Minute

type prCacheEntry struct {
	resp prResponse
	at   time.Time
}

func prCacheKey(dir, branch string) string { return dir + "\x00" + branch }

func (s *Server) prCacheGet(dir, branch string) (prResponse, bool) {
	s.prCacheMu.Lock()
	defer s.prCacheMu.Unlock()
	e, ok := s.prCache[prCacheKey(dir, branch)]
	if !ok || s.prNow().Sub(e.at) > prCacheTTL {
		return prResponse{}, false
	}
	return e.resp, true
}

func (s *Server) prCachePut(dir, branch string, resp prResponse) {
	s.prCacheMu.Lock()
	defer s.prCacheMu.Unlock()
	if s.prCache == nil {
		s.prCache = make(map[string]prCacheEntry)
	}
	s.prCache[prCacheKey(dir, branch)] = prCacheEntry{resp: resp, at: s.prNow()}
}

// InvalidatePRCacheForDir drops every cached PR lookup for a directory. Wired
// to the branch poller so a new commit/branch (the push that precedes a PR)
// makes the next lookup fresh.
func (s *Server) InvalidatePRCacheForDir(dir string) {
	s.prCacheMu.Lock()
	defer s.prCacheMu.Unlock()
	prefix := dir + "\x00"
	for k := range s.prCache {
		if strings.HasPrefix(k, prefix) {
			delete(s.prCache, k)
		}
	}
}

// prNow returns the cache clock (injectable for tests).
func (s *Server) prNow() time.Time {
	if s.prCacheClock != nil {
		return s.prCacheClock()
	}
	return time.Now()
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

	// Serve from cache unless the caller demands freshness (?fresh=1 — used
	// by the FE right after an agent run completes). Lookup errors are never
	// cached, so transient network failures don't stick.
	if r.URL.Query().Get("fresh") != "1" {
		if resp, ok := s.prCacheGet(dirPath, branch); ok {
			writeHTTPJSON(w, http.StatusOK, resp, s.logger)
			return
		}
	}

	parentDirPath := s.workspace.resolveParentDirPath(r.Context(), channelID)
	ghUser := s.configs.ghUser(dirPath, parentDirPath)

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

	resp := prResponse{Present: pr != nil, PR: pr}
	s.prCachePut(dirPath, branch, resp)
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}
