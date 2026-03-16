package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/db"
)

type memoryFilesResponse struct {
	Files []db.MemoryFileInfo `json:"files"`
}

func (s *Server) handleListMemoryFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "store not configured") {
		return
	}

	channelID := r.URL.Query().Get("channel_id")
	dirPathParam := r.URL.Query().Get("dir_path")

	dirPath, err := s.resolveDirPath(r.Context(), dirPathParam, channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	files, err := s.store.ListDistinctMemoryFilePaths(r.Context(), dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	existing := make([]db.MemoryFileInfo, 0, len(files))
	for _, f := range files {
		if _, err := s.sys.Stat(f.FilePath); err == nil {
			existing = append(existing, f)
		}
	}

	writeHTTPJSON(w, http.StatusOK, memoryFilesResponse{Files: existing}, s.logger)
}

func (s *Server) handleSearchMemoryFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "store not configured") {
		return
	}

	channelID := r.URL.Query().Get("channel_id")
	dirPathParam := r.URL.Query().Get("dir_path")
	query := strings.ToLower(r.URL.Query().Get("q"))

	dirPath, err := s.resolveDirPath(r.Context(), dirPathParam, channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	files, err := s.store.ListDistinctMemoryFilePaths(r.Context(), dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	matched := make([]db.MemoryFileInfo, 0)
	for _, f := range files {
		if _, statErr := s.sys.Stat(f.FilePath); statErr != nil {
			continue
		}
		if query == "" || strings.Contains(strings.ToLower(f.FilePath), query) {
			matched = append(matched, f)
		}
	}

	writeHTTPJSON(w, http.StatusOK, memoryFilesResponse{Files: matched}, s.logger)
}

func (s *Server) handleReadMemoryFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	if !filepath.IsAbs(path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		http.Error(w, "only .md files are supported", http.StatusBadRequest)
		return
	}

	data, err := s.sys.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

func (s *Server) handleWriteMemoryFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	if !filepath.IsAbs(path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		http.Error(w, "only .md files are supported", http.StatusBadRequest)
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
	if info, err := s.sys.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	if err := s.sys.WriteFile(path, body, perm); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}
