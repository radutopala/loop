package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/osutil"
)

type sessionEntry struct {
	SessionID    string    `json:"session_id"`
	LastModified time.Time `json:"last_modified"`
	LastMessage  string    `json:"last_message,omitempty"`
}

type listSessionsResponse struct {
	CurrentSessionID   string         `json:"current_session_id"`
	Sessions           []sessionEntry `json:"sessions"`
	ImportedSessionIDs []string       `json:"imported_session_ids"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	if ch.DirPath == "" {
		writeHTTPJSON(w, http.StatusOK, listSessionsResponse{
			CurrentSessionID: ch.SessionID,
			Sessions:         []sessionEntry{},
		}, s.logger)
		return
	}

	encodedPath := osutil.EncodeClaudeProjectPath(ch.DirPath)

	home, err := s.sys.UserHomeDir()
	if err != nil {
		http.Error(w, "failed to determine home directory", http.StatusInternalServerError)
		return
	}

	projectDir := filepath.Join(home, ".claude", "projects", encodedPath)

	entries, err := s.sys.ReadDir(projectDir)
	if err != nil {
		// Directory doesn't exist — return empty sessions.
		writeHTTPJSON(w, http.StatusOK, listSessionsResponse{
			CurrentSessionID: ch.SessionID,
			Sessions:         []sessionEntry{},
		}, s.logger)
		return
	}

	var sessions []sessionEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".jsonl")
		lastMsg := readLastMessageText(s.sys, filepath.Join(projectDir, name))
		sessions = append(sessions, sessionEntry{
			SessionID:    sessionID,
			LastModified: info.ModTime(),
			LastMessage:  lastMsg,
		})
	}

	// Sort by ModTime descending (newest first).
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastModified.After(sessions[j].LastModified)
	})

	if sessions == nil {
		sessions = []sessionEntry{}
	}

	// Collect session IDs already associated with any channel or thread in the DB.
	var importedIDs []string
	if allChannels, err := s.store.ListChannels(r.Context()); err == nil {
		for _, c := range allChannels {
			if c.SessionID != "" {
				importedIDs = append(importedIDs, c.SessionID)
			}
		}
	}
	if importedIDs == nil {
		importedIDs = []string{}
	}

	writeHTTPJSON(w, http.StatusOK, listSessionsResponse{
		CurrentSessionID:   ch.SessionID,
		Sessions:           sessions,
		ImportedSessionIDs: importedIDs,
	}, s.logger)
}

// tailReadSize is the number of bytes to read from the end of a session file
// when extracting the last assistant message.
const tailReadSize = 32 * 1024

// maxLastMessageLen is the maximum length of the last_message field.
const maxLastMessageLen = 200

// readLastMessageText reads the tail of a JSONL session file and returns
// the text from the last assistant or user message, truncated to maxLastMessageLen.
func readLastMessageText(sys interface {
	Open(string) (*os.File, error)
}, path string) string {
	f, err := sys.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	return findLastMessageFromReader(f, tailReadSize)
}

// statSeekReader is a reader that can stat (for size), seek, and read.
type statSeekReader interface {
	io.ReadSeeker
	Stat() (os.FileInfo, error)
}

// findLastMessageFromReader reads the tail of a file and extracts the last message.
func findLastMessageFromReader(r statSeekReader, maxBytes int64) string {
	info, err := r.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - maxBytes
	if offset > 0 {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return findLastMessage(data)
}

// findLastMessage scans JSONL lines in reverse and returns the text from
// the last assistant or user message, truncated to maxLastMessageLen.
func findLastMessage(data []byte) string {
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		text := extractMessageText(line)
		if text != "" {
			if len(text) > maxLastMessageLen {
				return text[:maxLastMessageLen] + "..."
			}
			return text
		}
	}
	return ""
}

// extractMessageText parses a JSONL line and returns the text content from
// an assistant message (text blocks) or a user prompt (plain string content).
// Returns "" for tool_result user messages, system events, and other types.
func extractMessageText(line string) string {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return ""
	}

	switch entry.Type {
	case "assistant":
		return extractTextBlocks(entry.Message.Content)
	case "user":
		// User content is either a plain string (prompt) or an array (tool_result).
		// Only return text for plain string prompts.
		var text string
		if json.Unmarshal(entry.Message.Content, &text) == nil && text != "" {
			return text
		}
		return ""
	default:
		return ""
	}
}

// extractTextBlocks returns concatenated text from content blocks.
func extractTextBlocks(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}
