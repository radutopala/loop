package api

import (
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type auditFileEntry struct {
	Date         string    `json:"date"`          // "2026-04-24"
	Size         int64     `json:"size"`          // bytes
	LastModified time.Time `json:"last_modified"` // file mtime
}

type listAuditFilesResponse struct {
	Files []auditFileEntry `json:"files"`
	Total int              `json:"total"`
}

// handleListAuditFiles returns newest-first the agentgate-YYYY-MM-DD.jsonl
// files accumulated for the channel under {policyDir}/<channel>/audit/.
// Supports ?offset=N&limit=M for infinite-scroll pagination (defaults:
// offset=0, limit=50).
func (s *Server) handleListAuditFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.auditDirResolver, "audit dir resolver not configured") {
		return
	}

	channelID := r.PathValue("id")
	dir := s.auditDirResolver.AuditDir(channelID)
	if dir == "" {
		writeHTTPJSON(w, http.StatusOK, listAuditFilesResponse{Files: []auditFileEntry{}}, s.logger)
		return
	}

	entries, err := s.sys.ReadDir(dir)
	if err != nil {
		// Directory may not exist yet (no container ever spawned on this
		// channel, or gate disabled). Return empty list rather than 500.
		writeHTTPJSON(w, http.StatusOK, listAuditFilesResponse{Files: []auditFileEntry{}}, s.logger)
		return
	}

	var files []auditFileEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "agentgate-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, "agentgate-"), ".jsonl")
		if _, parseErr := time.Parse("2006-01-02", stamp); parseErr != nil {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		files = append(files, auditFileEntry{
			Date:         stamp,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Date > files[j].Date
	})

	offset, limit := parsePaging(r, 0, 50)
	total := len(files)
	offset = min(offset, total)
	end := min(offset+limit, total)
	page := files[offset:end]
	if page == nil {
		page = []auditFileEntry{}
	}
	writeHTTPJSON(w, http.StatusOK, listAuditFilesResponse{Files: page, Total: total}, s.logger)
}

// handleDeleteAuditFile removes one audit file from disk. The date path-var is
// validated against YYYY-MM-DD so the file path cannot escape the audit dir.
func (s *Server) handleDeleteAuditFile(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.auditDirResolver, "audit dir resolver not configured") {
		return
	}

	channelID := r.PathValue("id")
	date := r.PathValue("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}

	dir := s.auditDirResolver.AuditDir(channelID)
	if dir == "" {
		http.Error(w, "audit not configured", http.StatusNotFound)
		return
	}
	path := filepath.Join(dir, "agentgate-"+date+".jsonl")

	if err := s.sys.Remove(path); err != nil {
		s.logger.Warn("delete audit file", "path", path, "error", err)
		http.Error(w, "failed to delete audit file", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parsePaging extracts ?offset= and ?limit= from the request, applying the
// given defaults on missing / malformed values and capping limit at 500.
func parsePaging(r *http.Request, defaultOffset, defaultLimit int) (int, int) {
	offset := defaultOffset
	limit := defaultLimit
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 500)
		}
	}
	return offset, limit
}
