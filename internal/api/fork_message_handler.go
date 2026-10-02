package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/transcript"
)

// errNotForkable is why a message can't be forked at; it's the 409 body.
var errNotForkable = errors.New("this message can't be forked at: its place in the session's transcript isn't known")

// handleForkAtMessage creates a thread that continues a channel's or
// thread's conversation from one of its messages, like handleForkThread but
// cut there: the new thread's first run resumes the session as it was at
// that point. At an agent reply the fork keeps the reply. At a user message
// it starts just before it, and the message comes back as the response's
// prompt for the composer to offer again. A top-level channel's fork is a
// thread of its own; a thread's is a sibling.
func (s *Server) handleForkAtMessage(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}
	ctx := r.Context()
	channelID, msgID := r.PathValue("id"), r.PathValue("msgId")

	src, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A hidden learn or explain thread is as good as missing, as in
	// handleForkThread.
	if src == nil || db.IsHiddenKind(src.Kind) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	msg, err := s.store.GetChatMessage(ctx, channelID, msgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg == nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if src.Worktree && src.ParentID != "" {
		at, err := s.forkPointAt(ctx, src.DirPath, msg)
		if !writeForkPointError(w, err) {
			s.forkWorktreeThread(w, r, src, at)
		}
		return
	}

	// The fork's parent is the source's parent, or the source itself when
	// it's a top-level channel. Its transcripts are under the parent's
	// project dir.
	parentID := src.ParentID
	if parentID == "" {
		parentID = src.ChannelID
	}
	parent, err := s.store.GetChannel(ctx, parentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "parent project channel not found", http.StatusInternalServerError)
		return
	}
	at, err := s.forkPointAt(ctx, parent.DirPath, msg)
	if writeForkPointError(w, err) {
		return
	}

	newID, err := s.threads.CreateThread(ctx, parentID, src.Name+" (fork)", "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if at.SessionID != "" {
		if _, err := s.store.MarkSessionForkPendingAt(ctx, newID, at.SessionID, at.ResumeAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.importSessionMessagesUntil(ctx, parent.DirPath, newID, at.SessionID, at.ResumeAt)
	} else if err := s.store.UpdateSessionID(ctx, newID, ""); err != nil {
		// A fork before a session's first prompt starts fresh, not on the
		// parent's session the new thread inherited.
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(parentID, newID)
	}
	writeHTTPJSON(w, http.StatusCreated, forkThreadResponse{ThreadID: newID, Prompt: at.Prompt}, s.logger)
}

// writeForkPointError writes forkPointAt's error, if any, and reports
// whether it did.
func writeForkPointError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errNotForkable):
		http.Error(w, errNotForkable.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return true
}

// forkPointAt returns where a fork at msg picks up, reading transcripts
// under projectDir. An agent reply resumes at its own transcript entry. A
// user message resumes at the entry before its prompt, and starts fresh
// when the prompt opens the session. Its prompt's entry is recorded on it
// once its run ends; a message without one is located from the first reply
// to it that has one, walking that reply's transcript back to the prompt.
func (s *Server) forkPointAt(ctx context.Context, projectDir string, msg *db.Message) (forkPoint, error) {
	if msg.IsBot {
		if !msg.Forkable {
			return forkPoint{}, errNotForkable
		}
		return forkPoint{SessionID: msg.SessionID, ResumeAt: msg.TranscriptUUID}, nil
	}
	from := msg
	if !msg.Forkable {
		reply, err := s.store.FirstForkableReply(ctx, msg.ChannelID, msg.MsgID)
		if err != nil {
			return forkPoint{}, err
		}
		if reply == nil {
			return forkPoint{}, errNotForkable
		}
		from = reply
	}
	entries, _, err := s.readTranscript(projectDir, from.SessionID)
	if err != nil {
		return forkPoint{}, errNotForkable
	}
	prompt, ok := transcript.PromptOf(entries, from.TranscriptUUID)
	if !ok {
		return forkPoint{}, errNotForkable
	}
	at := forkPoint{ResumeAt: prompt.ParentUUID, Prompt: msg.Content}
	if prompt.ParentUUID != "" {
		at.SessionID = from.SessionID
	}
	return at, nil
}
