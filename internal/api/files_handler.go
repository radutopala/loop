package api

import (
	"context"
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
func validateFilePath(rootDir, relativePath string) (string, error) {
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
	realRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return "", fmt.Errorf("invalid root directory: %w", err)
	}

	// Ensure realRoot has a trailing separator so "/projects/foo" doesn't
	// match "/projects/foobar".
	rootPrefix := realRoot + string(filepath.Separator)

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// File might not exist yet (for write). Check the parent.
		parentDir := filepath.Dir(absPath)
		realParent, err2 := filepath.EvalSymlinks(parentDir)
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

// allDirPaths returns the primary dir_path followed by any extra_dirs from project config.
func (s *Server) allDirPaths(ctx context.Context, channelID string) ([]string, error) {
	dirPath, err := s.resolveDirPath(ctx, "", channelID)
	if err != nil {
		return nil, err
	}
	paths := []string{dirPath}
	cfg, err := config.LoadProjectConfig(dirPath, &config.Config{})
	if err == nil && len(cfg.ExtraDirs) > 0 {
		paths = append(paths, cfg.ExtraDirs...)
	}
	return paths, nil
}

// resolveRootDir returns the root directory for file operations, supporting
// multi-root workspaces via the "root" query parameter (0-indexed, default 0).
func (s *Server) resolveRootDir(ctx context.Context, channelID string, r *http.Request) (string, error) {
	rootIdx, _ := strconv.Atoi(r.URL.Query().Get("root")) // default 0
	if rootIdx == 0 {
		return s.resolveDirPath(ctx, "", channelID)
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

	absPath, err := validateFilePath(dirPath, relPath)
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
	absPath, err := validateFilePath(dirPath, relPath)
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

	if info.Size() > maxFileSize {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	data, err := s.sys.ReadFile(absPath)
	if err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
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
	absPath, err := validateFilePath(dirPath, relPath)
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
	absPath, err := validateFilePath(dirPath, relPath)
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
func validateDirPath(rootDir, relativePath string) (string, error) {
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

	realRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return "", fmt.Errorf("invalid root directory: %w", err)
	}
	rootPrefix := realRoot + string(filepath.Separator)

	// Walk up to the first existing ancestor.
	ancestor := absPath
	for {
		realAncestor, err2 := filepath.EvalSymlinks(ancestor)
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
	absPath, err := validateDirPath(dirPath, relPath)
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
