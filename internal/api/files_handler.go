package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/config"
)

// validateFilePath validates a relative path against a root directory.
// Returns the cleaned absolute path or an error.
func (s *Server) validateFilePath(rootDir, relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("path is required")
	}

	// Reject absolute paths.
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}

	// Reject null bytes which can truncate paths on some systems.
	if strings.ContainsRune(relativePath, 0) {
		return "", fmt.Errorf("path contains invalid characters")
	}

	// Clean and join.
	cleaned := filepath.Clean(relativePath)

	// Reject paths that try to escape.
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal not allowed")
	}

	absPath := filepath.Join(rootDir, cleaned)

	// Resolve symlinks and verify the real path is still under rootDir.
	realRoot, err := s.sys.EvalSymlinks(rootDir)
	if err != nil {
		return "", fmt.Errorf("invalid root directory: %w", err)
	}

	// Ensure realRoot has a trailing separator so "/projects/foo" doesn't
	// match "/projects/foobar".
	rootPrefix := realRoot + string(filepath.Separator)

	realPath, err := s.sys.EvalSymlinks(absPath)
	if err != nil {
		// File might not exist yet (for write). Check the parent.
		parentDir := filepath.Dir(absPath)
		realParent, err2 := s.sys.EvalSymlinks(parentDir)
		if err2 != nil {
			return "", fmt.Errorf("path not found")
		}
		if realParent != realRoot && !strings.HasPrefix(realParent, rootPrefix) {
			return "", fmt.Errorf("path traversal not allowed")
		}
		return absPath, nil
	}

	if realPath != realRoot && !strings.HasPrefix(realPath, rootPrefix) {
		return "", fmt.Errorf("path traversal not allowed")
	}

	return absPath, nil
}

// allDirPaths returns the primary dir_path followed by any extra_dirs from
// project config. Tilde-prefixed extra_dirs entries (e.g. "~/dev/foo") are
// expanded to the user's home directory so downstream filesystem and git
// operations can chdir into them — the OS does not expand "~" itself.
func (s *Server) allDirPaths(ctx context.Context, channelID string) ([]string, error) {
	dirPath, parentDirPath, err := s.resolveWorkflowConfigPaths(ctx, channelID)
	if err != nil {
		return nil, err
	}
	paths := []string{dirPath}
	// Seed the merge from the daemon's global config so global-level
	// extra_dirs surface as file-tree roots — the agent container mounts
	// them (the runner seeds from currentConfig()), so the file tree must
	// list them too. Fall back to an empty base when the global config
	// can't be loaded.
	base := &config.Config{}
	loadCfg := s.configs.load
	if loadCfg == nil {
		loadCfg = config.Load
	}
	if global, loadErr := loadCfg(); loadErr == nil && global != nil {
		base = global
	}
	// Worktree channels resolve extra_dirs with three-layer merging
	// (global → parent → worktree) so the file tree shows the same roots the
	// agent gets — the parent channel's extra_dirs, not just the parent dir
	// the worktree config seeds. Regular channels (parentDirPath == "") use
	// the plain project-config load.
	var cfg *config.Config
	if parentDirPath != "" {
		cfg, err = config.LoadWorktreeProjectConfig(dirPath, parentDirPath, base)
	} else {
		cfg, err = config.LoadProjectConfig(dirPath, base)
	}
	if err == nil && len(cfg.ExtraDirs) > 0 {
		for _, p := range cfg.ExtraDirs {
			paths = append(paths, s.expandHomePath(p))
		}
	}
	return paths, nil
}

// expandHomePath expands a leading "~/" to the user's home directory.
// Returns the path unchanged if it doesn't start with "~/" or if the home
// directory cannot be resolved.
func (s *Server) expandHomePath(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := s.sys.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return filepath.Join(home, path[2:])
}

// resolveRootDir returns the root directory for file operations, supporting

// resolveRootParam applies the optional ?root=N workspace selector shared by
// the git and diff endpoints: absent or 0 keeps dirPath (the channel's
// primary dir); root>0 resolves the extra_dirs entry via resolveRootDir. On
// a bad index or resolve failure it writes a 400 and returns ("", false).
func (s *Server) resolveRootParam(w http.ResponseWriter, r *http.Request, channelID, dirPath string) (string, bool) {
	rootStr := r.URL.Query().Get("root")
	if rootStr == "" {
		return dirPath, true
	}
	rootIdx, err := strconv.Atoi(rootStr)
	if err != nil || rootIdx < 0 {
		http.Error(w, "invalid root index", http.StatusBadRequest)
		return "", false
	}
	if rootIdx == 0 {
		return dirPath, true
	}
	resolved, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return resolved, true
}

// multi-root workspaces via the "root" query parameter (0-indexed, default 0).
func (s *Server) resolveRootDir(ctx context.Context, channelID string, r *http.Request) (string, error) {
	rootIdx, _ := strconv.Atoi(r.URL.Query().Get("root")) // default 0
	if rootIdx == 0 {
		return s.workspace.resolveDirPath(ctx, "", channelID)
	}

	allPaths, err := s.allDirPaths(ctx, channelID)
	if err != nil {
		return "", err
	}
	if rootIdx < 0 || rootIdx >= len(allPaths) {
		return "", fmt.Errorf("invalid root index %d", rootIdx)
	}
	return allPaths[rootIdx], nil
}

type rootEntry struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Name  string `json:"name"`
}

type listRootsResponse struct {
	Roots []rootEntry `json:"roots"`
}

func (s *Server) handleListRoots(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	allPaths, err := s.allDirPaths(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	roots := make([]rootEntry, 0, len(allPaths))
	for i, p := range allPaths {
		if p == "" {
			continue
		}
		roots = append(roots, rootEntry{
			Index: i,
			Path:  p,
			Name:  filepath.Base(p),
		})
	}

	writeHTTPJSON(w, http.StatusOK, listRootsResponse{Roots: roots}, s.logger)
}

type fileEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

type listFilesResponse struct {
	Entries []fileEntry `json:"entries"`
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		relPath = "."
	}

	absPath, err := s.validateFilePath(dirPath, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	entries, err := listDir(s.sys.ReadDir, absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, listFilesResponse{Entries: entries}, s.logger)
}

func listDir(readDir func(string) ([]fs.DirEntry, error), absPath string) ([]fileEntry, error) {
	dirEntries, err := readDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	entries := make([]fileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		e := fileEntry{Name: de.Name()}
		if de.IsDir() {
			e.Type = "dir"
		} else {
			e.Type = "file"
			info, err := de.Info()
			if err == nil {
				e.Size = info.Size()
			}
		}
		entries = append(entries, e)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	return entries, nil
}

// fileSearchResult is a single match returned by handleSearchFiles.
type fileSearchResult struct {
	RootIndex int    `json:"root_index"`
	RelPath   string `json:"rel_path"`
	Name      string `json:"name"`
}

type fileSearchResponse struct {
	Results []fileSearchResult `json:"results"`
}

// Directories that are always skipped during file search. Hard-coded; cheap and covers the common cases.
var fileSearchSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".next":        true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
}

// fuzzyMatchSeq is a sequential, case-insensitive fuzzy match: every rune of q
// must appear in s in order. Empty q always matches.
func fuzzyMatchSeq(q, s string) bool {
	if q == "" {
		return true
	}
	qr := []rune(strings.ToLower(q))
	i := 0
	for _, c := range strings.ToLower(s) {
		if c == qr[i] {
			i++
			if i == len(qr) {
				return true
			}
		}
	}
	return false
}

// loadGitignorePatterns reads <root>/.gitignore and returns the simple patterns
// (skipping comments, blanks, and negations). Best-effort; nested .gitignore
// files are not consulted.
func loadGitignorePatterns(sys serverSystem, root string) []string {
	data, err := sys.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		// Trim leading "/" so simple patterns work with our basename matcher.
		line = strings.TrimPrefix(line, "/")
		// Trim trailing slash for directory-only patterns; we treat them the same.
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// gitignoreMatch checks whether relPath (or any ancestor segment) matches any
// of the supplied patterns. We match the basename and the full relPath against
// each pattern via filepath.Match.
func gitignoreMatch(patterns []string, relPath string) bool {
	if len(patterns) == 0 {
		return false
	}
	base := filepath.Base(relPath)
	for _, pat := range patterns {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pat, relPath); ok {
			return true
		}
	}
	return false
}

func (s *Server) handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	limit, ok := parseQueryInt(w, r, "limit", 30, 100)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")

	allPaths, err := s.allDirPaths(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := make([]fileSearchResult, 0)
	qLower := strings.ToLower(q)

	for rootIdx, rootAbs := range allPaths {
		if rootAbs == "" {
			continue
		}
		patterns := loadGitignorePatterns(s.sys, rootAbs)
		err := s.sys.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// Skip unreadable subtrees but keep walking siblings.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if path == rootAbs {
				return nil
			}
			rel, err := filepath.Rel(rootAbs, path)
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if fileSearchSkipDirs[d.Name()] {
					return fs.SkipDir
				}
				if gitignoreMatch(patterns, rel) {
					return fs.SkipDir
				}
				return nil
			}
			if gitignoreMatch(patterns, rel) {
				return nil
			}
			if !fuzzyMatchSeq(q, rel) {
				return nil
			}
			results = append(results, fileSearchResult{
				RootIndex: rootIdx,
				RelPath:   filepath.ToSlash(rel),
				Name:      d.Name(),
			})
			return nil
		})
		if err != nil {
			s.logger.Warn("file search walk error", "root", rootAbs, "err", err)
		}
	}

	// Rank: (a) basename starts with q, (b) relPath contains q, (c) fuzzy-only.
	sort.SliceStable(results, func(i, j int) bool {
		return rankFileSearchResult(results[i], qLower) < rankFileSearchResult(results[j], qLower)
	})
	if len(results) > limit {
		results = results[:limit]
	}

	writeHTTPJSON(w, http.StatusOK, fileSearchResponse{Results: results}, s.logger)
}

func rankFileSearchResult(r fileSearchResult, qLower string) int {
	if qLower == "" {
		return 2
	}
	nameLower := strings.ToLower(r.Name)
	if strings.HasPrefix(nameLower, qLower) {
		return 0
	}
	if strings.Contains(strings.ToLower(r.RelPath), qLower) {
		return 1
	}
	return 2
}

const maxFileSize = 5 * 1024 * 1024 // 5MB

func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	relPath := r.URL.Query().Get("path")
	absPath, err := s.validateFilePath(dirPath, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := s.sys.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to stat file", http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}

	// Video files: stream straight from disk with Range support so the editor's
	// <video> player can seek. This runs before the maxFileSize check and never
	// buffers the whole file into memory — videos are routinely larger than the
	// text cap. We open through s.sys (an *os.File, hence an io.ReadSeeker)
	// rather than calling http.ServeFile, which is a path-injection sink: the
	// user-supplied path would reach it even though validateFilePath already
	// contains the request to the channel dir.
	if mime := videoMIMEByExt(absPath); mime != "" {
		f, openErr := s.sys.Open(absPath)
		if openErr != nil {
			http.Error(w, "failed to read file", http.StatusInternalServerError)
			return
		}
		defer f.Close() //nolint:errcheck
		w.Header().Set("Content-Type", mime)
		http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), f)
		return
	}

	if info.Size() > maxFileSize {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	data, err := s.sys.ReadFile(absPath)
	if err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}

	// Image files: serve the bytes with a real Content-Type so the editor's
	// <img src=...> branch can render them directly.
	if mime := imageMIMEByExt(absPath); mime != "" {
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		w.Write(data) //nolint:errcheck
		return
	}

	// Binary detection: check first 512 bytes for null bytes.
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			w.Header().Set("X-File-Binary", "true")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

// imageMIMEByExt returns the MIME type for known image extensions, or "" for
// non-image files. Extension match only — we don't sniff content because the
// editor will trust whatever Content-Type the browser receives.
func imageMIMEByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// videoMIMEByExt returns the MIME type for known video extensions, or "" for
// non-video files. Extension match only (same approach as imageMIMEByExt).
func videoMIMEByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	}
	return ""
}

type writeFileResponse struct {
	OK bool `json:"ok"`
}

func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	relPath := r.URL.Query().Get("path")
	absPath, err := s.validateFilePath(dirPath, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxFileSize+1))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	if len(body) > maxFileSize {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Preserve original file permissions if the file exists.
	perm := os.FileMode(0644)
	if info, err := s.sys.Stat(absPath); err == nil {
		perm = info.Mode().Perm()
	}

	if err := s.sys.WriteFile(absPath, body, perm); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	relPath := r.URL.Query().Get("path")
	absPath, err := s.validateFilePath(dirPath, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := s.sys.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to stat path", http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		if err := s.sys.RemoveAll(absPath); err != nil {
			http.Error(w, "failed to delete directory", http.StatusInternalServerError)
			return
		}
	} else {
		if err := s.sys.Remove(absPath); err != nil {
			http.Error(w, "failed to delete file", http.StatusInternalServerError)
			return
		}
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

// validateDirPath is like validateFilePath but allows non-existent intermediate
// parents (for MkdirAll). It walks up to the first existing ancestor to verify
// the path stays under rootDir.
func (s *Server) validateDirPath(rootDir, relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	if strings.ContainsRune(relativePath, 0) {
		return "", fmt.Errorf("path contains invalid characters")
	}
	cleaned := filepath.Clean(relativePath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal not allowed")
	}
	absPath := filepath.Join(rootDir, cleaned)

	realRoot, err := s.sys.EvalSymlinks(rootDir)
	if err != nil {
		return "", fmt.Errorf("invalid root directory: %w", err)
	}
	rootPrefix := realRoot + string(filepath.Separator)

	// Walk up to the first existing ancestor.
	ancestor := absPath
	for {
		realAncestor, err2 := s.sys.EvalSymlinks(ancestor)
		if err2 == nil {
			if realAncestor != realRoot && !strings.HasPrefix(realAncestor, rootPrefix) {
				return "", fmt.Errorf("path traversal not allowed")
			}
			return absPath, nil
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("path not found")
		}
		ancestor = parent
	}
}

func (s *Server) handleCreateDir(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	relPath := r.URL.Query().Get("path")
	absPath, err := s.validateDirPath(dirPath, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.sys.MkdirAll(absPath, 0o755); err != nil {
		http.Error(w, "failed to create directory", http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

// filesExistsRequest is a batch existence check.
// Each candidate is either a relative path (tried against every root)
// or an absolute path (matched against root prefixes).
type filesExistsRequest struct {
	Paths []string `json:"paths"`
}

type filesExistsResult struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	RootIndex int    `json:"root_index"`
	RelPath   string `json:"rel_path"`
}

type filesExistsResponse struct {
	Results []filesExistsResult `json:"results"`
}

// resolveAndStat returns true if path exists as a regular file under any root.
// For relative input, every root is tried in order. For absolute input, the
// first root that prefixes the path is used.
func (s *Server) resolveAndStat(roots []string, path string) (bool, int, string) {
	if path == "" {
		return false, 0, ""
	}
	if filepath.IsAbs(path) {
		realPath, err := s.sys.EvalSymlinks(path)
		if err != nil {
			return false, 0, ""
		}
		info, err := s.sys.Stat(realPath)
		if err != nil || info.IsDir() {
			return false, 0, ""
		}
		for i, root := range roots {
			realRoot, err := s.sys.EvalSymlinks(root)
			if err != nil {
				continue
			}
			rootPrefix := realRoot + string(filepath.Separator)
			if realPath != realRoot && !strings.HasPrefix(realPath, rootPrefix) {
				continue
			}
			// realPath is realRoot or starts with rootPrefix → string trim is safe.
			rel := strings.TrimPrefix(strings.TrimPrefix(realPath, realRoot), string(filepath.Separator))
			return true, i, filepath.ToSlash(rel)
		}
		return false, 0, ""
	}
	for i, root := range roots {
		absPath, err := s.validateFilePath(root, path)
		if err != nil {
			continue
		}
		info, err := s.sys.Stat(absPath)
		if err != nil || info.IsDir() {
			continue
		}
		return true, i, filepath.ToSlash(filepath.Clean(path))
	}
	return false, 0, ""
}

const maxExistsBatch = 200

func (s *Server) handleFilesExists(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	roots, err := s.allDirPaths(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req filesExistsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Paths) > maxExistsBatch {
		req.Paths = req.Paths[:maxExistsBatch]
	}

	results := make([]filesExistsResult, 0, len(req.Paths))
	for _, p := range req.Paths {
		exists, idx, rel := s.resolveAndStat(roots, p)
		results = append(results, filesExistsResult{Path: p, Exists: exists, RootIndex: idx, RelPath: rel})
	}

	writeHTTPJSON(w, http.StatusOK, filesExistsResponse{Results: results}, s.logger)
}
