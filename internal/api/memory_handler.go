package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

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

	dirPath, err := s.resolveDirPath(r.Context(), req.DirPath, req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results, err := s.memoryIndexer.Search(r.Context(), dirPath, req.Query, req.TopK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(memorySearchResponse{Results: results})
}

func (s *Server) handleMemoryIndex(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.memoryIndexer, "memory indexer not configured") {
		return
	}

	var req memoryIndexRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	dirPath, err := s.resolveDirPath(r.Context(), req.DirPath, req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	count, err := s.memoryIndexer.Index(r.Context(), dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(memoryIndexResponse{Count: count})
}

// resolveDirPath returns the dir_path from the request, resolving via channel_id lookup if needed.
func (s *Server) resolveDirPath(ctx context.Context, dirPath, channelID string) (string, error) {
	if dirPath != "" {
		return dirPath, nil
	}
	if channelID == "" {
		return "", fmt.Errorf("dir_path or channel_id is required")
	}
	if s.store == nil {
		return "", fmt.Errorf("channel lookup not configured")
	}
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up channel: %w", err)
	}
	if ch == nil {
		return "", fmt.Errorf("channel %s not found", channelID)
	}
	if ch.DirPath == "" {
		// Fall back to the default work dir for channels without a project dir.
		if s.loopDir != "" {
			return filepath.Join(s.loopDir, channelID, "work"), nil
		}
		return "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, nil
}
