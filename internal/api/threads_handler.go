package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/transcript"
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
	if !requireConfigured(w, s.threads, "thread deletion not configured") ||
		!requireConfigured(w, s.store, "thread deletion not configured") {
		return
	}

	threadID := r.PathValue("id")
	if err := s.deleteThread(r.Context(), threadID); err != nil {
		if errors.Is(err, ErrChannelLocked) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteThread deletes threadID through s.threads, stops the runs of its
// hidden learn and explain threads, which go with it, and removes their MCP
// configs unless the parent channel's project keeps them, then tells the app
// windows it's gone. The thread and its hidden threads are noted first, while
// they still exist to find.
func (s *Server) deleteThread(ctx context.Context, threadID string) error {
	threads := s.lookupThreads(ctx, []string{threadID})
	hidden := s.hiddenThreads(ctx, threadID)
	if err := s.threads.DeleteThread(ctx, threadID); err != nil {
		return err
	}
	s.stopHiddenThreads(ctx, hidden)
	if len(threads) == 1 {
		s.removeMCPConfigs(s.threadOwner(ctx, threads[0]), append(threads, hidden...))
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelDeleted(threadID)
	}
	return nil
}

// threadOwner returns the parent channel of thread, whose project config
// decides keep_mcp_configs (a worktree thread's DirPath is the worktree, not
// the project). A failed lookup falls back to the thread itself.
func (s *Server) threadOwner(ctx context.Context, thread *db.Channel) *db.Channel {
	parent, err := s.store.GetChannel(ctx, thread.ParentID)
	if err != nil {
		s.logger.Warn("thread cleanup: looking up parent channel", "thread_id", thread.ChannelID, "error", err)
	}
	if parent == nil {
		return thread
	}
	return parent
}

// importSessionMessages parses a Claude Code session JSONL file and inserts
// user prompts and assistant text responses as messages in the thread. The
// transcript is looked up under the parent channel's project dir.
func (s *Server) importSessionMessages(ctx context.Context, parentChannelID, threadID, sessionID string) {
	if _, ok := transcript.CleanSessionID(sessionID); !ok || s.store == nil || s.sys == nil {
		return
	}

	// Look up the parent channel to find the project dir.
	parent, err := s.store.GetChannel(ctx, parentChannelID)
	if err != nil || parent == nil || parent.DirPath == "" {
		return
	}
	s.importSessionMessagesFrom(ctx, parent.DirPath, threadID, sessionID)
}

// importSessionMessagesFrom is importSessionMessages for a transcript under
// projectDir, e.g. a worktree's, whose transcripts live apart from the
// parent channel's.
func (s *Server) importSessionMessagesFrom(ctx context.Context, projectDir, threadID, sessionID string) {
	s.importSessionMessagesUntil(ctx, projectDir, threadID, sessionID, "")
}

// importSessionMessagesUntil is importSessionMessagesFrom for a fork cut at
// transcript entry cut: it imports the transcript up to and including that
// entry. "", or an entry the transcript doesn't have, imports all of it.
func (s *Server) importSessionMessagesUntil(ctx context.Context, projectDir, threadID, sessionID, cut string) {
	if _, ok := transcript.CleanSessionID(sessionID); !ok || s.store == nil || s.sys == nil {
		return
	}

	// Look up the thread to get its numeric chat_id.
	thread, err := s.store.GetChannel(ctx, threadID)
	if err != nil || thread == nil {
		return
	}

	entries, lines, err := s.readTranscript(projectDir, sessionID)
	if err != nil {
		return
	}
	for i, e := range entries {
		if cut != "" && e.UUID == cut {
			entries = entries[:i+1]
			break
		}
	}

	baseTime := time.Now().Add(-time.Duration(lines) * time.Second) // sequential timestamps
	for _, entry := range entries {
		var text string
		var isBot bool
		switch entry.Type {
		case "assistant":
			text = extractTextBlocks(entry.Message.Content)
			isBot = true
		case "user":
			// Only import prompts (plain string), not tool_result arrays.
			var ok bool
			if text, ok = entry.Prompt(); !ok {
				continue
			}
		default:
			continue
		}

		if text == "" {
			continue
		}

		createdAt := baseTime.Add(time.Duration(entry.Line) * time.Second)
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
			// Where the message sits in the session, to fork at it.
			SessionID:      sessionID,
			TranscriptUUID: entry.UUID,
		}
		if err := s.store.InsertMessage(ctx, msg); err != nil {
			s.logger.Warn("import session message failed", "error", err, "thread_id", threadID)
			return
		}
	}
}

// readTranscript parses session sessionID's transcript, kept under the
// project dir Claude Code uses for projectDir. It returns the entries that
// parse and the transcript's line count; lines that don't parse are skipped.
func (s *Server) readTranscript(projectDir, sessionID string) ([]transcript.Entry, int, error) {
	home, err := s.sys.UserHomeDir()
	if err != nil {
		return nil, 0, err
	}
	path, err := transcript.Path(home, projectDir, sessionID)
	if err != nil {
		return nil, 0, err
	}
	f, err := s.sys.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, err
	}
	entries, lines := transcript.Parse(data)
	return entries, lines, nil
}

type forkThreadResponse struct {
	ThreadID     string `json:"thread_id"`
	WorktreePath string `json:"worktree_path,omitempty"`
	// Prompt is the message a fork at a user message starts before, for
	// the composer to offer again.
	Prompt string `json:"prompt,omitempty"`
}

// forkPoint is where a fork picks up its source's conversation.
type forkPoint struct {
	// SessionID is the session the fork continues; "" starts it fresh.
	SessionID string
	// ResumeAt is the transcript entry the fork keeps the session up to;
	// "" keeps all of it.
	ResumeAt string
	// Prompt is returned to the caller as forkThreadResponse.Prompt.
	Prompt string
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
	// A hidden learn or explain thread is as good as missing: forking it
	// would surface its session as a visible thread.
	if src == nil || src.ParentID == "" || db.IsHiddenKind(src.Kind) {
		http.Error(w, "thread not found", http.StatusBadRequest)
		return
	}

	if src.Worktree {
		s.forkWorktreeThread(w, r, src, forkPoint{SessionID: src.SessionID})
		return
	}

	newID, err := s.threads.CreateThread(r.Context(), src.ParentID, src.Name+" (fork)", "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if src.SessionID != "" {
		if _, err := s.store.MarkSessionForkPending(r.Context(), newID, src.SessionID); err != nil {
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

// forkWorktreeThread is the worktree-thread arm of handleForkThread and
// handleForkAtMessage: new worktree branched from what the SOURCE worktree
// has checked out (its branch, or its commit when detached), new thread
// continuing the source's conversation from at. The source's transcript
// lives under its own worktree's project dir, not the parent channel's, so
// that's where it's copied and imported from.
func (s *Server) forkWorktreeThread(w http.ResponseWriter, r *http.Request, src *db.Channel, at forkPoint) {
	parent, err := s.store.GetChannel(r.Context(), src.ParentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "parent project channel not found", http.StatusInternalServerError)
		return
	}

	// Fork from whatever the source has checked out, not from
	// worktree/<dir basename>: its branch is often renamed.
	srcBranch, err := s.worktreeCreator.HeadRef(r.Context(), src.DirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := "wt-" + randutil.HexID(4)
	result, err := s.worktreeCreator.Create(r.Context(), parent.DirPath, srcBranch, name, "")
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
	if err := s.store.UpsertChannel(r.Context(), ch); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	staged := false
	if at.SessionID != "" {
		if err := s.copySessionFile(src.DirPath, result.WorktreePath, at.SessionID); err != nil {
			s.logger.Warn("fork session transcript unavailable; starting thread fresh",
				"thread_id", newID, "session_id", at.SessionID, "error", err)
		} else {
			staged = true
		}
	}
	if staged {
		if _, err := s.store.MarkSessionForkPendingAt(r.Context(), newID, at.SessionID, at.ResumeAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.importSessionMessagesUntil(r.Context(), src.DirPath, newID, at.SessionID, at.ResumeAt)
	} else {
		// The new thread inherited the parent channel's session, which isn't
		// this conversation and whose transcript isn't in the new worktree's
		// project dir. Clear it so the fork starts clean rather than
		// resuming it. UpsertChannel keeps a stored id over an empty one,
		// so this takes an explicit update.
		if err := s.store.UpdateSessionID(r.Context(), newID, ""); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(src.ParentID, newID)
	}
	writeHTTPJSON(w, http.StatusCreated, forkThreadResponse{
		ThreadID:     newID,
		WorktreePath: result.WorktreePath,
		Prompt:       at.Prompt,
	}, s.logger)
}
