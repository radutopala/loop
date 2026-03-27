package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/randutil"
)

type createThreadRequest struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	AuthorID  string `json:"author_id"`
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
}

type createThreadResponse struct {
	ThreadID string `json:"thread_id"`
}

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}

	var req createThreadRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// When msgHandler is set, skip storing the message in CreateThread —
	// HandleThreadCreated will store it as a user message instead.
	msg := req.Message
	if s.msgHandler != nil {
		msg = ""
	}

	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, req.Name, req.AuthorID, msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cleanSessionID := filepath.Base(req.SessionID)
	if cleanSessionID != "" && cleanSessionID != "." && cleanSessionID != ".." && s.store != nil {
		if err := s.store.UpdateSessionID(r.Context(), threadID, cleanSessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Import conversation history from the session JSONL file.
		s.importSessionMessages(r.Context(), req.ChannelID, threadID, cleanSessionID)
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(req.ChannelID, threadID)
	}

	if s.msgHandler != nil && req.Message != "" {
		go s.msgHandler.HandleThreadCreated(context.Background(), threadID, req.AuthorID, req.Message)
	}

	writeHTTPJSON(w, http.StatusCreated, createThreadResponse{ThreadID: threadID}, s.logger)
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.threads, "thread deletion not configured") {
		return
	}

	threadID := r.PathValue("id")

	if err := s.threads.DeleteThread(r.Context(), threadID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// importSessionMessages parses a Claude Code session JSONL file and inserts
// user prompts and assistant text responses as messages in the thread.
func (s *Server) importSessionMessages(ctx context.Context, parentChannelID, threadID, sessionID string) {
	// Sanitise sessionID to prevent path traversal — only the base name is
	// valid (no slashes, no ".." components).
	sessionID = filepath.Base(sessionID)
	if sessionID == "." || sessionID == ".." || sessionID == "" {
		return
	}

	if s.store == nil || s.sys == nil {
		return
	}

	// Look up the parent channel to find the project dir.
	parent, err := s.store.GetChannel(ctx, parentChannelID)
	if err != nil || parent == nil || parent.DirPath == "" {
		return
	}

	// Look up the thread to get its numeric chat_id.
	thread, err := s.store.GetChannel(ctx, threadID)
	if err != nil || thread == nil {
		return
	}

	// Build the JSONL file path.
	home, err := s.sys.UserHomeDir()
	if err != nil {
		return
	}
	encodedPath := osutil.EncodeClaudeProjectPath(parent.DirPath)
	jsonlPath := filepath.Join(home, ".claude", "projects", encodedPath, sessionID+".jsonl")

	f, err := s.sys.Open(jsonlPath)
	if err != nil {
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return
	}

	// Parse and insert messages.
	lines := strings.Split(string(data), "\n")
	baseTime := time.Now().Add(-time.Duration(len(lines)) * time.Second) // sequential timestamps

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}

		var text string
		var isBot bool
		switch entry.Type {
		case "assistant":
			text = extractTextBlocks(entry.Message.Content)
			isBot = true
		case "user":
			// Only import prompts (plain string), not tool_result arrays.
			if json.Unmarshal(entry.Message.Content, &text) != nil {
				continue
			}
		default:
			continue
		}

		if text == "" {
			continue
		}

		createdAt := baseTime.Add(time.Duration(i) * time.Second)
		if entry.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
				createdAt = t
			}
		}

		authorName := "user"
		if isBot {
			authorName = "agent"
		}

		msg := &db.Message{
			ChatID:      thread.ID,
			ChannelID:   threadID,
			MsgID:       "import-" + randutil.HexID(8),
			AuthorName:  authorName,
			Content:     text,
			IsBot:       isBot,
			IsProcessed: true, // all imported messages are historical
			CreatedAt:   createdAt,
		}
		if err := s.store.InsertMessage(ctx, msg); err != nil {
			s.logger.Warn("import session message failed", "error", err, "thread_id", threadID)
			return
		}
	}
}
