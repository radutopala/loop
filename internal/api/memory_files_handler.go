package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/db"
)

// osReadFile is a package-level var for testing.
var osReadFile = os.ReadFile

// osStat is a package-level var for testing.
var osStat = os.Stat

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
		if _, err := osStat(f.FilePath); err == nil {
			existing = append(existing, f)
		}
	}

	writeHTTPJSON(w, http.StatusOK, memoryFilesResponse{Files: existing}, s.logger)
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

	data, err := osReadFile(path)
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
