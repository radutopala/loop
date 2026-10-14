package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/unidiff"
)

// configHistoryKeep is how many revisions each config file keeps.
const configHistoryKeep = 200

// configHistoryMaxFile is the size above which a file in one of the
// configHistoryDirs isn't recorded.
const configHistoryMaxFile = 1 << 20

// configFileName is the config file of a .loop dir.
const configFileName = "config.json"

// configHistoryDirs are the folders of a .loop dir holding the files the
// configs point at: prompt_path and command_path files, workflow prompts
// and chat component templates, and the agent container's build files.
// Their files are recorded with config.json.
var configHistoryDirs = []string{"templates", "shortcuts", "bash-shortcuts", "workflows", "review", "components", "container"}

// configHistorySkipped are the name endings of files in the
// configHistoryDirs that aren't recorded: copies Loop or editors keep,
// logs and leftovers.
var configHistorySkipped = []string{".bkp", ".bak", ".log", ".partial", ".DS_Store"}

// configHistoryPrivate marks content that isn't recorded: a private key
// stays out of the database and its backups.
var configHistoryPrivate = []string{"PRIVATE KEY-----", "AGE-SECRET-KEY-"}

// configHistoryRel reports whether rel, a slash-separated path relative to
// a .loop dir, names a file whose history is recorded: config.json or a
// file in one of the configHistoryDirs. It returns rel in the OS's form.
func configHistoryRel(rel string) (string, bool) {
	if rel == configFileName {
		return rel, true
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", false
	}
	dir, rest, ok := strings.Cut(filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))), "/")
	if !ok || rest == "" || !slices.Contains(configHistoryDirs, dir) {
		return "", false
	}
	if slices.ContainsFunc(configHistorySkipped, func(end string) bool { return strings.HasSuffix(rest, end) }) {
		return "", false
	}
	return filepath.Join(dir, filepath.FromSlash(rest)), true
}

func isConfigHistoryRel(rel string) bool {
	_, ok := configHistoryRel(filepath.ToSlash(rel))
	return ok
}

// ConfigHistoryStore stores the revisions of Loop's config files.
type ConfigHistoryStore interface {
	InsertConfigRevision(ctx context.Context, rev *db.ConfigRevision, keep int) (bool, error)
	ListConfigRevisions(ctx context.Context, path string) ([]*db.ConfigRevision, error)
	GetConfigRevision(ctx context.Context, id int64) (*db.ConfigRevision, error)
	ListConfigRevisionFiles(ctx context.Context, prefix string) ([]*db.ConfigRevisionFile, error)
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

// lockConfig takes path's config lock for an edit by source, as
// lockConfigFile does. The plugins then follow the config.
func (s *Server) lockConfig(path, source string) func() {
	unlock := s.lockConfigFile(path, source)
	return func() {
		unlock()
		s.pluginConfigChanged(context.Background())
	}
}

// lockConfigFile takes path's config lock for an edit by source. Whatever
// the file holds when the lock is taken is recorded first, as an external
// edit when Loop didn't write it, then the edit's result is recorded as
// source's when the returned unlock runs.
func (s *Server) lockConfigFile(path, source string) func() {
	unlock := s.configLocks.lock(path)
	s.recordConfig(path, db.ConfigSourceExternal)
	return func() {
		s.recordConfig(path, source)
		unlock()
	}
}

// recordConfig stores the config file at path as its newest revision,
// written by source, when its content changed. A missing file, or one that
// isn't UTF-8 text, records nothing. Callers hold path's config lock.
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
	if !utf8.Valid(data) || slices.ContainsFunc(configHistoryPrivate, func(m string) bool { return strings.Contains(string(data), m) }) {
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

// RunConfigHistory records edits Loop didn't make to the global config,
// each channel's project config and the files they point at, checking
// every interval until ctx is done.
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

// scanConfigHistory records the current content of the global config, of
// every channel's project config, and of the files in their .loop dirs'
// configHistoryDirs.
func (s *Server) scanConfigHistory(ctx context.Context) {
	if s.history.store == nil {
		return
	}
	var roots []string
	if s.loopDir != "" {
		roots = append(roots, s.loopDir)
	}
	for _, dir := range s.ProjectDirs(ctx) {
		roots = append(roots, filepath.Join(dir, ".loop"))
	}
	seen := map[string]bool{}
	for _, root := range roots {
		for _, path := range s.configHistoryFiles(root) {
			if seen[path] {
				continue
			}
			seen[path] = true
			unlock := s.configLocks.lock(path)
			s.recordConfig(path, db.ConfigSourceExternal)
			unlock()
		}
	}
}

// ProjectDirs lists the project dirs of the channels, each once: the dirs
// whose .loop dir holds a project config.
func (s *Server) ProjectDirs(ctx context.Context) []string {
	if s.store == nil {
		return nil
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		s.logger.Warn("listing channels for their project dirs", "error", err)
	}
	var dirs []string
	for _, ch := range channels {
		if dir, err := s.resolveProjectConfigDirPath(ctx, ch.ChannelID); err == nil && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// configHistoryFiles lists the files of the .loop dir root whose history
// is recorded: its config.json and the regular files, up to
// configHistoryMaxFile in size, in its configHistoryDirs, but for the
// configHistorySkipped.
func (s *Server) configHistoryFiles(root string) []string {
	paths := []string{filepath.Join(root, configFileName)}
	for _, dir := range configHistoryDirs {
		_ = s.sys.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if rel, _ := filepath.Rel(root, path); !isConfigHistoryRel(rel) {
				return nil
			}
			if info, err := d.Info(); err == nil && info.Size() <= configHistoryMaxFile {
				paths = append(paths, path)
			}
			return nil
		})
	}
	return paths
}

// globalConfigPath is the path of the global ~/.loop/config.json.
func (s *Server) globalConfigPath() (string, error) {
	if s.loopDir == "" {
		return "", errors.New("loop directory not configured")
	}
	return filepath.Join(s.loopDir, configFileName), nil
}

// configHistoryRoot finds the .loop dir whose recorded file path is: the
// loop dir, or the nearest .loop dir above path. It returns the dir and
// path relative to it, and false when path isn't a recorded file of one.
func (s *Server) configHistoryRoot(path string) (string, string, bool) {
	candidates := []string{s.loopDir}
	for d := filepath.Dir(path); d != filepath.Dir(d); d = filepath.Dir(d) {
		if filepath.Base(d) == ".loop" {
			candidates = append(candidates, d)
			break
		}
	}
	for _, root := range candidates {
		// Both are absolute, so Rel doesn't fail.
		rel, _ := filepath.Rel(root, path)
		if rel, ok := configHistoryRel(filepath.ToSlash(rel)); ok {
			return root, rel, true
		}
	}
	return "", "", false
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
	// Missing is set when the file is gone, so its newest revision can be
	// restored too.
	Missing bool `json:"missing,omitempty"`
}

// configHistoryFile is a file of a .loop dir with recorded revisions.
type configHistoryFile struct {
	// File is the file's path relative to the .loop dir, slash-separated.
	File      string    `json:"file"`
	Revisions int       `json:"revisions"`
	UpdatedAt time.Time `json:"updated_at"`
}

type configHistoryFilesResponse struct {
	Root  string              `json:"root"`
	Files []configHistoryFile `json:"files"`
}

type configRevisionResponse struct {
	*db.ConfigRevision
	// Diff is the unified diff from the revision before it.
	Diff string `json:"diff"`
}

// handleGetConfigHistory lists the revisions of the global config, or of
// the file of the loop dir the file query parameter names, newest first.
func (s *Server) handleGetConfigHistory(w http.ResponseWriter, r *http.Request) {
	if root, ok := s.globalHistoryRoot(w); ok {
		s.writeConfigHistory(w, r, root)
	}
}

// handleGetProjectConfigHistory lists the revisions of the given channel's
// project config, or of the file of its .loop dir the file query parameter
// names, newest first.
func (s *Server) handleGetProjectConfigHistory(w http.ResponseWriter, r *http.Request) {
	if root, ok := s.projectHistoryRoot(w, r); ok {
		s.writeConfigHistory(w, r, root)
	}
}

// handleGetConfigHistoryFiles lists the files of the loop dir with
// recorded revisions.
func (s *Server) handleGetConfigHistoryFiles(w http.ResponseWriter, r *http.Request) {
	if root, ok := s.globalHistoryRoot(w); ok {
		s.writeConfigHistoryFiles(w, r, root)
	}
}

// handleGetProjectConfigHistoryFiles lists the files of the given
// channel's project .loop dir with recorded revisions.
func (s *Server) handleGetProjectConfigHistoryFiles(w http.ResponseWriter, r *http.Request) {
	if root, ok := s.projectHistoryRoot(w, r); ok {
		s.writeConfigHistoryFiles(w, r, root)
	}
}

// globalHistoryRoot returns the loop dir, writing the error response when
// there's no history or no loop dir.
func (s *Server) globalHistoryRoot(w http.ResponseWriter) (string, bool) {
	if !s.requireConfigHistory(w) {
		return "", false
	}
	if _, err := s.globalConfigPath(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return "", false
	}
	return s.loopDir, true
}

// projectHistoryRoot returns the .loop dir of the channel_id query
// parameter's project, writing the error response when there's no history
// or no such channel.
func (s *Server) projectHistoryRoot(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.requireConfigHistory(w) {
		return "", false
	}
	dirPath, err := s.resolveProjectConfigDirPath(r.Context(), r.URL.Query().Get("channel_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return filepath.Join(dirPath, ".loop"), true
}

// writeConfigHistoryFiles writes the files of the .loop dir root with
// recorded revisions, by path.
func (s *Server) writeConfigHistoryFiles(w http.ResponseWriter, r *http.Request, root string) {
	files, err := s.history.store.ListConfigRevisionFiles(r.Context(), root+string(filepath.Separator))
	if err != nil {
		http.Error(w, "failed to list config files", http.StatusInternalServerError)
		return
	}
	resp := configHistoryFilesResponse{Root: root, Files: []configHistoryFile{}}
	for _, f := range files {
		// The prefix also matches files of the .loop dirs of projects
		// under root, which aren't root's.
		rel, _ := filepath.Rel(root, f.Path)
		if _, ok := configHistoryRel(filepath.ToSlash(rel)); ok {
			resp.Files = append(resp.Files, configHistoryFile{File: filepath.ToSlash(rel), Revisions: f.Revisions, UpdatedAt: f.UpdatedAt})
		}
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// writeConfigHistory writes the revisions of the file of the .loop dir
// root the file query parameter names (config.json when it's empty), each
// with the lines it added and removed.
func (s *Server) writeConfigHistory(w http.ResponseWriter, r *http.Request, root string) {
	file := r.URL.Query().Get("file")
	if file == "" {
		file = configFileName
	}
	rel, ok := configHistoryRel(file)
	if !ok {
		http.Error(w, "invalid file", http.StatusBadRequest)
		return
	}
	path := filepath.Join(root, rel)
	revs, err := s.history.store.ListConfigRevisions(r.Context(), path)
	if err != nil {
		http.Error(w, "failed to list config revisions", http.StatusInternalServerError)
		return
	}
	_, err = s.sys.Stat(path)
	resp := configHistoryResponse{Path: path, Revisions: []configRevisionSummary{}, Missing: os.IsNotExist(err)}
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
// file, as an owner edit: a trusted project config stays trusted.
func (s *Server) handleRestoreConfigRevision(w http.ResponseWriter, r *http.Request) {
	rev, ok := s.configRevision(w, r)
	if !ok {
		return
	}
	if _, err := s.globalConfigPath(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	root, rel, ok := s.configHistoryRoot(rev.Path)
	if !ok {
		http.Error(w, "not a config file", http.StatusBadRequest)
		return
	}
	dir := filepath.Dir(rev.Path)
	if err := s.sys.MkdirAll(dir, 0755); err != nil {
		http.Error(w, "failed to create "+dir, http.StatusInternalServerError)
		return
	}
	source := fmt.Sprintf("restore:%d", rev.ID)
	if rel != configFileName {
		// Not part of the trusted project config, nor read by the plugins.
		defer s.lockConfigFile(rev.Path, source)()
		if err := s.sys.WriteFile(rev.Path, []byte(rev.Content), 0644); err != nil {
			http.Error(w, "failed to write "+rel, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer s.lockConfig(rev.Path, source)()
	var (
		before []byte
		keep   bool
	)
	if root != s.loopDir {
		before, keep = s.readProjectConfigBefore(rev.Path)
	}
	content := []byte(rev.Content)
	if err := s.sys.WriteFile(rev.Path, content, 0644); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	if keep {
		s.keepProjectTrust(filepath.Dir(root), before, content)
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
