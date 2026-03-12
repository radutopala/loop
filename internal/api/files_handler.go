package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// File might not exist yet (for write). Check the parent.
		parentDir := filepath.Dir(absPath)
		realParent, err2 := filepath.EvalSymlinks(parentDir)
		if err2 != nil {
			return "", fmt.Errorf("path not found")
		}
		if !strings.HasPrefix(realParent, realRoot) {
			return "", fmt.Errorf("path traversal not allowed")
		}
		return absPath, nil
	}

	if !strings.HasPrefix(realPath, realRoot) {
		return "", fmt.Errorf("path traversal not allowed")
	}

	return absPath, nil
}

type fileEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

type listFilesResponse struct {
	Entries []fileEntry `json:"entries"`
}

// Package-level vars for testability (osReadFile and osStat are in memory_files_handler.go).
var (
	osReadDir   = os.ReadDir
	osWriteFile = os.WriteFile
)

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
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

	entries, err := listDir(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, listFilesResponse{Entries: entries}, s.logger)
}

func listDir(absPath string) ([]fileEntry, error) {
	dirEntries, err := osReadDir(absPath)
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
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
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

	info, err := osStat(absPath)
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

	data, err := osReadFile(absPath)
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
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
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
	if info, err := osStat(absPath); err == nil {
		perm = info.Mode().Perm()
	}

	if err := osWriteFile(absPath, body, perm); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

var osRemove = os.Remove

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
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

	info, err := osStat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to stat file", http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		http.Error(w, "cannot delete directories", http.StatusBadRequest)
		return
	}

	if err := osRemove(absPath); err != nil {
		http.Error(w, "failed to delete file", http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}
