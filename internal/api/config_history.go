package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/unidiff"
)

// configHistoryKeep is how many revisions each config file keeps.
const configHistoryKeep = 200

// ConfigHistoryStore stores the revisions of Loop's config files.
type ConfigHistoryStore interface {
	InsertConfigRevision(ctx context.Context, rev *db.ConfigRevision, keep int) (bool, error)
	ListConfigRevisions(ctx context.Context, path string) ([]*db.ConfigRevision, error)
	GetConfigRevision(ctx context.Context, id int64) (*db.ConfigRevision, error)
}

// configHistory records the revisions of the global and project configs.
// last holds each path's newest recorded hash, so an unchanged file costs
// no database round trip.
type configHistory struct {
	store ConfigHistoryStore
	mu    sync.Mutex
	last  map[string]string
}

// WithConfigHistory records the history of the global and project configs
// in store and serves the history routes.
func WithConfigHistory(store ConfigHistoryStore) Option {
	return func(s *Server) { s.history.store = store }
}

// lockConfig takes path's config lock for an edit by source. Whatever the
// file holds when the lock is taken is recorded first, as an external edit
// when Loop didn't write it, then the edit's result is recorded as source's
// when the returned unlock runs.
func (s *Server) lockConfig(path, source string) func() {
	unlock := s.configLocks.lock(path)
	s.recordConfig(path, db.ConfigSourceExternal)
	return func() {
		s.recordConfig(path, source)
		unlock()
	}
}

// recordConfig stores the config file at path as its newest revision,
// written by source, when its content changed. A missing file records
// nothing. Callers hold path's config lock.
func (s *Server) recordConfig(path, source string) {
	if s.history.store == nil {
		return
	}
	data, err := s.sys.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logger.Warn("reading config for its history", "path", path, "error", err)
		}
		return
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	h := &s.history
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last[path] == hash {
		return
	}
	rev := &db.ConfigRevision{Path: path, Content: string(data), Hash: hash, Source: source}
	if _, err := h.store.InsertConfigRevision(context.Background(), rev, configHistoryKeep); err != nil {
		s.logger.Warn("recording config revision", "path", path, "error", err)
		return
	}
	if h.last == nil {
		h.last = map[string]string{}
	}
	h.last[path] = hash
}

// RunConfigHistory records edits Loop didn't make to the global config and
// each channel's project config, checking every interval until ctx is done.
func (s *Server) RunConfigHistory(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.scanConfigHistory(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// scanConfigHistory records the current content of the global config and
// of every channel's project config.
func (s *Server) scanConfigHistory(ctx context.Context) {
	if s.history.store == nil {
		return
	}
	var paths []string
	if global, err := s.globalConfigPath(); err == nil {
		paths = append(paths, global)
	}
	if s.store != nil {
		channels, err := s.store.ListChannels(ctx)
		if err != nil {
			s.logger.Warn("listing channels for the config history", "error", err)
		}
		for _, ch := range channels {
			if dir, err := s.resolveProjectConfigDirPath(ctx, ch.ChannelID); err == nil {
				paths = append(paths, filepath.Join(dir, ".loop", "config.json"))
			}
		}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		unlock := s.configLocks.lock(path)
		s.recordConfig(path, db.ConfigSourceExternal)
		unlock()
	}
}

// globalConfigPath is the path of the global ~/.loop/config.json.
func (s *Server) globalConfigPath() (string, error) {
	if s.loopDir == "" {
		return "", errors.New("loop directory not configured")
	}
	return filepath.Join(s.loopDir, "config.json"), nil
}

type configRevisionSummary struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	Added     int       `json:"added"`
	Removed   int       `json:"removed"`
}

type configHistoryResponse struct {
	Path      string                  `json:"path"`
	Revisions []configRevisionSummary `json:"revisions"`
}

type configRevisionResponse struct {
	*db.ConfigRevision
	// Diff is the unified diff from the revision before it.
	Diff string `json:"diff"`
}

// handleGetConfigHistory lists the global config's revisions, newest first.
func (s *Server) handleGetConfigHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigHistory(w) {
		return
	}
	path, err := s.globalConfigPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeConfigHistory(w, r, path)
}

// handleGetProjectConfigHistory lists the revisions of the given channel's
// project config, newest first.
func (s *Server) handleGetProjectConfigHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigHistory(w) {
		return
	}
	dirPath, err := s.resolveProjectConfigDirPath(r.Context(), r.URL.Query().Get("channel_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeConfigHistory(w, r, filepath.Join(dirPath, ".loop", "config.json"))
}

// writeConfigHistory writes path's revisions, each with the lines it added
// and removed.
func (s *Server) writeConfigHistory(w http.ResponseWriter, r *http.Request, path string) {
	revs, err := s.history.store.ListConfigRevisions(r.Context(), path)
	if err != nil {
		http.Error(w, "failed to list config revisions", http.StatusInternalServerError)
		return
	}
	resp := configHistoryResponse{Path: path, Revisions: []configRevisionSummary{}}
	for i, rev := range revs {
		added, removed := unidiff.Count(previousContent(revs, i), rev.Content)
		resp.Revisions = append(resp.Revisions, configRevisionSummary{
			ID: rev.ID, Source: rev.Source, CreatedAt: rev.CreatedAt, Added: added, Removed: removed,
		})
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// previousContent is the content of the revision before revs[i], or ""
// for the oldest one; revs is newest first.
func previousContent(revs []*db.ConfigRevision, i int) string {
	if i+1 < len(revs) {
		return revs[i+1].Content
	}
	return ""
}

// handleGetConfigRevision returns a revision with the diff from the one
// before it, or from the revision of the same file the against query
// parameter names.
func (s *Server) handleGetConfigRevision(w http.ResponseWriter, r *http.Request) {
	rev, ok := s.configRevision(w, r)
	if !ok {
		return
	}
	if v := r.URL.Query().Get("against"); v != "" {
		other, ok := s.lookupConfigRevision(w, r, v)
		if !ok {
			return
		}
		if other.Path != rev.Path {
			http.Error(w, "revisions are of different config files", http.StatusBadRequest)
			return
		}
		resp := configRevisionResponse{ConfigRevision: rev, Diff: unidiff.Diff(rev.Path, rev.Path, other.Content, rev.Content)}
		writeHTTPJSON(w, http.StatusOK, resp, s.logger)
		return
	}
	revs, err := s.history.store.ListConfigRevisions(r.Context(), rev.Path)
	if err != nil {
		http.Error(w, "failed to list config revisions", http.StatusInternalServerError)
		return
	}
	from, before := "/dev/null", ""
	for i, other := range revs {
		if other.ID == rev.ID && i+1 < len(revs) {
			from, before = rev.Path, revs[i+1].Content
		}
	}
	resp := configRevisionResponse{ConfigRevision: rev, Diff: unidiff.Diff(from, rev.Path, before, rev.Content)}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// handleRestoreConfigRevision writes a revision's content back to its
// config file, as an owner edit: a trusted project config stays trusted.
func (s *Server) handleRestoreConfigRevision(w http.ResponseWriter, r *http.Request) {
	rev, ok := s.configRevision(w, r)
	if !ok {
		return
	}
	global, err := s.globalConfigPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dir := filepath.Dir(rev.Path)
	if filepath.Base(rev.Path) != "config.json" || filepath.Base(dir) != ".loop" {
		http.Error(w, "not a config file", http.StatusBadRequest)
		return
	}
	project := rev.Path != global
	if err := s.sys.MkdirAll(dir, 0755); err != nil {
		http.Error(w, "failed to create .loop directory", http.StatusInternalServerError)
		return
	}
	defer s.lockConfig(rev.Path, fmt.Sprintf("restore:%d", rev.ID))()
	var (
		before []byte
		keep   bool
	)
	if project {
		before, keep = s.readProjectConfigBefore(rev.Path)
	}
	content := []byte(rev.Content)
	if err := s.sys.WriteFile(rev.Path, content, 0644); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	if keep {
		s.keepProjectTrust(filepath.Dir(dir), before, content)
	}
	w.WriteHeader(http.StatusNoContent)
}

// configRevision looks up the revision the request's {id} names, writing
// the error response when there's none.
func (s *Server) configRevision(w http.ResponseWriter, r *http.Request) (*db.ConfigRevision, bool) {
	if !s.requireConfigHistory(w) {
		return nil, false
	}
	return s.lookupConfigRevision(w, r, r.PathValue("id"))
}

// lookupConfigRevision looks up the revision with the given id, writing
// the error response when the id is invalid or there's no such revision.
func (s *Server) lookupConfigRevision(w http.ResponseWriter, r *http.Request, v string) (*db.ConfigRevision, bool) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		http.Error(w, "invalid revision id", http.StatusBadRequest)
		return nil, false
	}
	rev, err := s.history.store.GetConfigRevision(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to get config revision", http.StatusInternalServerError)
		return nil, false
	}
	if rev == nil {
		http.Error(w, "config revision not found", http.StatusNotFound)
		return nil, false
	}
	return rev, true
}

// requireConfigHistory writes 501 when the server records no history.
func (s *Server) requireConfigHistory(w http.ResponseWriter) bool {
	if s.history.store == nil {
		http.Error(w, "config history not configured", http.StatusNotImplemented)
		return false
	}
	return true
}
