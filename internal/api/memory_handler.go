package api

import (
	"context"
	"net/http"

	"github.com/radutopala/loop/internal/memory"
)

// MemoryIndexer abstracts memory search and indexing for the memory API endpoints.
type MemoryIndexer interface {
	Search(ctx context.Context, memoryDir, query string, topK int) ([]memory.SearchResult, error)
	Index(ctx context.Context, memoryDir string) (int, error)
}

type memorySearchRequest struct {
	Query     string `json:"query"`
	TopK      int    `json:"top_k"`
	DirPath   string `json:"dir_path"`
	ChannelID string `json:"channel_id"`
}

type memorySearchResponse struct {
	Results []memory.SearchResult `json:"results"`
}

type memoryIndexRequest struct {
	DirPath   string `json:"dir_path"`
	ChannelID string `json:"channel_id"`
}

type memoryIndexResponse struct {
	Count int `json:"count"`
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.memoryIndexer, "memory indexer not configured") {
		return
	}

	var req memorySearchRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	dirPath, err := s.workspace.resolveDirPath(r.Context(), req.DirPath, req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results, err := s.memoryIndexer.Search(r.Context(), dirPath, req.Query, req.TopK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, memorySearchResponse{Results: results}, s.logger)
}

func (s *Server) handleMemoryIndex(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.memoryIndexer, "memory indexer not configured") {
		return
	}

	var req memoryIndexRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	dirPath, err := s.workspace.resolveDirPath(r.Context(), req.DirPath, req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	count, err := s.memoryIndexer.Index(r.Context(), dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, memoryIndexResponse{Count: count}, s.logger)
}
