package api

import (
	"context"
	"encoding/json"
	"errors"
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
		if errors.Is(err, ErrChannelLocked) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
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

type forkThreadResponse struct {
	ThreadID     string `json:"thread_id"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

// handleForkThread creates a sibling of the given thread that continues its
// conversation: the new thread copies the source's Claude session id (history
// imported for display; the orchestrator forks the session on the first
// message because the id is now shared) — a "branch this conversation" for
// threads. For WORKTREE threads it additionally creates a new git worktree
// branched from the source worktree's branch, so the fork continues from the
// source's committed code state; base_branch is set to the source's branch so
// the fork's diff shows its own delta.
func (s *Server) handleForkThread(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}
	threadID := r.PathValue("id")

	src, err := s.store.GetChannel(r.Context(), threadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if src == nil || src.ParentID == "" {
		http.Error(w, "thread not found", http.StatusBadRequest)
		return
	}

	if src.Worktree {
		s.forkWorktreeThread(w, r, src)
		return
	}

	newID, err := s.threads.CreateThread(r.Context(), src.ParentID, src.Name+" (fork)", "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if src.SessionID != "" {
		if err := s.store.MarkSessionForkPending(r.Context(), newID, src.SessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.importSessionMessages(r.Context(), src.ParentID, newID, src.SessionID)
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(src.ParentID, newID)
	}
	writeHTTPJSON(w, http.StatusCreated, forkThreadResponse{ThreadID: newID}, s.logger)
}

// forkWorktreeThread is the worktree-thread arm of handleForkThread: new
// worktree branched from the SOURCE worktree's branch (its committed state),
// new thread carrying the source's session.
func (s *Server) forkWorktreeThread(w http.ResponseWriter, r *http.Request, src *db.Channel) {
	parent, err := s.store.GetChannel(r.Context(), src.ParentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "parent project channel not found", http.StatusInternalServerError)
		return
	}

	// The worktree branch convention is worktree/<dir basename> (see
	// worktree.Creator.Create).
	srcBranch := "worktree/" + filepath.Base(src.DirPath)
	name := "wt-" + randutil.HexID(4)
	result, err := s.worktreeCreator.Create(r.Context(), parent.DirPath, srcBranch, name, src.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newID, err := s.threads.CreateThread(r.Context(), src.ParentID, name+" (fork of "+srcBranch+")", "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ch, err := s.store.GetChannel(r.Context(), newID)
	if err != nil || ch == nil {
		http.Error(w, "failed to get created thread", http.StatusInternalServerError)
		return
	}
	ch.DirPath = result.WorktreePath
	ch.Worktree = true
	ch.BaseBranch = srcBranch
	// A fork whose transcript never reached the worktree's project dir is
	// not a fork — drop the inherited id so the thread starts clean rather
	// than resuming a conversation that isn't on disk.
	if src.SessionID != "" && !result.SessionStaged {
		s.logger.Warn("fork session transcript unavailable; starting thread fresh",
			"thread_id", newID, "session_id", src.SessionID)
		ch.SessionID = ""
	}
	if err := s.store.UpsertChannel(r.Context(), ch); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if src.SessionID != "" && result.SessionStaged {
		if err := s.store.MarkSessionForkPending(r.Context(), newID, src.SessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.importSessionMessages(r.Context(), src.ParentID, newID, src.SessionID)
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(src.ParentID, newID)
	}
	writeHTTPJSON(w, http.StatusCreated, forkThreadResponse{
		ThreadID:     newID,
		WorktreePath: result.WorktreePath,
	}, s.logger)
}
