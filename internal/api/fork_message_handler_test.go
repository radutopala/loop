package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/testutil"
)

// forkTranscript is a session with two turns: a prompt answered by two
// replies, then a second prompt answered by one.
var forkTranscript = []string{
	`{"type":"queue-operation"}`,
	`{"type":"user","uuid":"u-1","parentUuid":null,"message":{"role":"user","content":"first prompt"}}`,
	`{"type":"assistant","uuid":"a-1","parentUuid":"u-1","message":{"content":[{"type":"text","text":"looking"}]}}`,
	`{"type":"user","uuid":"r-1","parentUuid":"a-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
	`{"type":"assistant","uuid":"a-2","parentUuid":"r-1","message":{"content":[{"type":"text","text":"done"}]}}`,
	`{"type":"user","uuid":"u-2","parentUuid":"a-2","message":{"role":"user","content":"second prompt"}}`,
	`{"type":"attachment","uuid":"x-1","parentUuid":"u-2"}`,
	`{"type":"assistant","uuid":"a-3","parentUuid":"x-1","message":{"content":[{"type":"text","text":"again"}]}}`,
}

// useForkTranscript points the server's home at a temp dir holding
// forkTranscript as session sess-1 of project dir.
func (s *ServerSuite) useForkTranscript(dir string) {
	home := s.T().TempDir()
	project := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(dir))
	require.NoError(s.T(), os.MkdirAll(project, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(project, "sess-1.jsonl"), []byte(strings.Join(forkTranscript, "\n")), 0o644))
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(home, nil)
	s.srv.sys = &realOpenSys{sys}
}

// importedContents returns the contents of the messages imported into
// threadID, in order.
func (s *ServerSuite) importedContents(threadID string) []string {
	var got []string
	for _, c := range s.store.Calls {
		if c.Method == "InsertMessage" {
			if m := c.Arguments.Get(1).(*db.Message); m.ChannelID == threadID {
				got = append(got, m.Content)
			}
		}
	}
	return got
}

// importedRefs returns the session and transcript entry of each message
// imported into threadID, in order.
func (s *ServerSuite) importedRefs(threadID string) []string {
	var got []string
	for _, c := range s.store.Calls {
		if c.Method == "InsertMessage" {
			if m := c.Arguments.Get(1).(*db.Message); m.ChannelID == threadID {
				got = append(got, m.SessionID+" "+m.TranscriptUUID)
			}
		}
	}
	return got
}

func (s *ServerSuite) TestForkAtMessage() {
	reply := &db.Message{ChannelID: "t1", MsgID: "b3", IsBot: true, SessionID: "sess-1", TranscriptUUID: "a-3", Forkable: true}
	tests := []struct {
		name       string
		src        *db.Channel
		msg        *db.Message
		reply      *db.Message // the first forkable reply to a user message
		wantParent string
		wantAt     forkPoint
		wantImport []string
	}{
		{
			name:       "at a reply in a thread",
			src:        &db.Channel{ChannelID: "t1", ParentID: "ch1", Name: "research"},
			msg:        &db.Message{ChannelID: "t1", MsgID: "b2", IsBot: true, SessionID: "sess-1", TranscriptUUID: "a-2", Forkable: true},
			wantParent: "ch1",
			wantAt:     forkPoint{SessionID: "sess-1", ResumeAt: "a-2"},
			wantImport: []string{"first prompt", "looking", "done"},
		},
		{
			name:       "before a later prompt in a channel",
			src:        &db.Channel{ChannelID: "ch1", Name: "research", DirPath: "/proj"},
			msg:        &db.Message{ChannelID: "ch1", MsgID: "m2", Content: "second prompt", IsProcessed: true},
			reply:      reply,
			wantParent: "ch1",
			wantAt:     forkPoint{SessionID: "sess-1", ResumeAt: "a-2", Prompt: "second prompt"},
			wantImport: []string{"first prompt", "looking", "done"},
		},
		{
			name:       "before a recorded prompt",
			src:        &db.Channel{ChannelID: "ch1", Name: "research", DirPath: "/proj"},
			msg:        &db.Message{ChannelID: "ch1", MsgID: "m2", Content: "second prompt", IsProcessed: true, SessionID: "sess-1", TranscriptUUID: "u-2", Forkable: true},
			wantParent: "ch1",
			wantAt:     forkPoint{SessionID: "sess-1", ResumeAt: "a-2", Prompt: "second prompt"},
			wantImport: []string{"first prompt", "looking", "done"},
		},
		{
			name:       "before the first prompt starts fresh",
			src:        &db.Channel{ChannelID: "t1", ParentID: "ch1", Name: "research"},
			msg:        &db.Message{ChannelID: "t1", MsgID: "m1", Content: "first prompt", IsProcessed: true},
			reply:      &db.Message{ChannelID: "t1", MsgID: "b1", IsBot: true, SessionID: "sess-1", TranscriptUUID: "a-1", Forkable: true},
			wantParent: "ch1",
			wantAt:     forkPoint{Prompt: "first prompt"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.useForkTranscript("/proj")
			s.srv.SetEventsHub(NewEventsHub(s.srv.logger))
			s.store.On("GetChannel", mock.Anything, tc.src.ChannelID).Return(tc.src, nil)
			s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/proj"}, nil)
			s.store.On("GetChannel", mock.Anything, "t2").Return(&db.Channel{ID: 9, ChannelID: "t2", ParentID: "ch1"}, nil)
			s.store.On("GetChatMessage", mock.Anything, tc.src.ChannelID, tc.msg.MsgID).Return(tc.msg, nil)
			if tc.reply != nil {
				s.store.On("FirstForkableReply", mock.Anything, tc.src.ChannelID, tc.msg.MsgID).Return(tc.reply, nil)
			}
			s.threads.On("CreateThread", mock.Anything, tc.wantParent, "research (fork)", "", "").Return("t2", nil)
			s.store.On("MarkSessionForkPendingAt", mock.Anything, "t2", tc.wantAt.SessionID, tc.wantAt.ResumeAt).Return(true, nil)
			s.store.On("UpdateSessionID", mock.Anything, "t2", "").Return(nil)
			s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

			rec := s.testRequest("POST", "/api/channels/"+tc.src.ChannelID+"/messages/"+tc.msg.MsgID+"/fork", "")
			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			var resp forkThreadResponse
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
			require.Equal(s.T(), forkThreadResponse{ThreadID: "t2", Prompt: tc.wantAt.Prompt}, resp)
			require.Equal(s.T(), tc.wantImport, s.importedContents("t2"))
			if tc.wantImport != nil {
				// Imported messages keep their place in the session, to fork at.
				require.Equal(s.T(), []string{"sess-1 u-1", "sess-1 a-1", "sess-1 a-2"}, s.importedRefs("t2"))
			}
			if tc.wantAt.SessionID == "" {
				// A fresh fork drops the session the thread inherited.
				s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, "t2", "")
				s.store.AssertNotCalled(s.T(), "MarkSessionForkPendingAt", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			} else {
				s.store.AssertCalled(s.T(), "MarkSessionForkPendingAt", mock.Anything, "t2", tc.wantAt.SessionID, tc.wantAt.ResumeAt)
				s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

func (s *ServerSuite) TestForkAtMessageErrors() {
	thread := &db.Channel{ChannelID: "t1", ParentID: "ch1", Name: "research"}
	botMsg := &db.Message{ChannelID: "t1", MsgID: "b2", IsBot: true, SessionID: "sess-1", TranscriptUUID: "a-2", Forkable: true}
	userMsg := &db.Message{ChannelID: "t1", MsgID: "m2", Content: "second prompt"}
	tests := []struct {
		name     string
		src      *db.Channel
		srcErr   error
		msg      *db.Message
		msgErr   error
		reply    *db.Message
		replyErr error
		parent   *db.Channel
		noParent bool
		parErr   error
		noFile   bool
		create   error
		mark     error
		clear    error
		want     int
	}{
		{name: "channel lookup fails", srcErr: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "no such channel", want: http.StatusNotFound},
		{name: "hidden thread", src: &db.Channel{ChannelID: "t1", ParentID: "ch1", Kind: db.ChannelKindLearn}, want: http.StatusNotFound},
		{name: "message lookup fails", src: thread, msgErr: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "no such message", src: thread, want: http.StatusNotFound},
		{name: "parent lookup fails", src: thread, msg: botMsg, parErr: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "no parent", src: thread, msg: botMsg, noParent: true, want: http.StatusInternalServerError},
		{name: "parent without a dir", src: thread, msg: botMsg, parent: &db.Channel{ChannelID: "ch1"}, want: http.StatusInternalServerError},
		{name: "reply without a transcript ref", src: thread, msg: &db.Message{ChannelID: "t1", MsgID: "b0", IsBot: true}, want: http.StatusConflict},
		{name: "reply lookup fails", src: thread, msg: userMsg, replyErr: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "prompt without a reply", src: thread, msg: userMsg, want: http.StatusConflict},
		{name: "transcript gone", src: thread, msg: userMsg, reply: &db.Message{SessionID: "sess-1", TranscriptUUID: "a-3"}, noFile: true, want: http.StatusConflict},
		{name: "reply not in the transcript", src: thread, msg: userMsg, reply: &db.Message{SessionID: "sess-1", TranscriptUUID: "a-9"}, want: http.StatusConflict},
		{name: "thread creation fails", src: thread, msg: botMsg, create: errors.New("nope"), want: http.StatusInternalServerError},
		{name: "marking the fork fails", src: thread, msg: botMsg, mark: errors.New("db down"), want: http.StatusInternalServerError},
		{name: "clearing the session fails", src: thread, msg: userMsg, reply: &db.Message{SessionID: "sess-1", TranscriptUUID: "a-1"}, clear: errors.New("db down"), want: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.useForkTranscript("/proj")
			if tc.noFile {
				s.useForkTranscript("/elsewhere")
			}
			parent := tc.parent
			if parent == nil && tc.parErr == nil && !tc.noParent {
				parent = &db.Channel{ChannelID: "ch1", DirPath: "/proj"}
			}
			s.store.On("GetChannel", mock.Anything, "t1").Return(tc.src, tc.srcErr)
			s.store.On("GetChannel", mock.Anything, "ch1").Return(parent, tc.parErr)
			s.store.On("GetChannel", mock.Anything, "t2").Return(&db.Channel{ID: 9, ChannelID: "t2"}, nil)
			s.store.On("GetChatMessage", mock.Anything, "t1", "m1").Return(tc.msg, tc.msgErr)
			s.store.On("FirstForkableReply", mock.Anything, "t1", mock.Anything).Return(tc.reply, tc.replyErr)
			s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("t2", tc.create)
			s.store.On("MarkSessionForkPendingAt", mock.Anything, "t2", mock.Anything, mock.Anything).Return(false, tc.mark)
			s.store.On("UpdateSessionID", mock.Anything, "t2", "").Return(tc.clear)
			s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

			rec := s.testRequest("POST", "/api/channels/t1/messages/m1/fork", "")
			require.Equal(s.T(), tc.want, rec.Code, rec.Body.String())
		})
	}
}

func (s *ServerSuite) TestForkAtMessageNotConfigured() {
	s.srv.store = nil
	rec := s.testRequest("POST", "/api/channels/t1/messages/m1/fork", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)

	s.srv.store = s.store
	s.srv.threads = nil
	rec = s.testRequest("POST", "/api/channels/t1/messages/m1/fork", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// TestForkAtMessageWorktree: a worktree thread's fork gets its own worktree,
// as with handleForkThread, cut at the message; one that can't be forked at
// stops before any worktree is made.
func (s *ServerSuite) TestForkAtMessageWorktree() {
	dir := initGitRepo(s.T())
	src := dir + "/.worktrees/src-wt"
	cmd := exec.Command("git", "worktree", "add", "-b", "worktree/src-wt", src)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))
	s.srv.sys = s.sys
	s.sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()

	s.store.On("GetChannel", mock.Anything, "wt1").Return(&db.Channel{
		ChannelID: "wt1", ParentID: "ch1", Name: "src", Worktree: true, DirPath: src, SessionID: "sess-2",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.store.On("GetChatMessage", mock.Anything, "wt1", "b0").Return(&db.Message{ChannelID: "wt1", MsgID: "b0", IsBot: true}, nil)
	s.store.On("GetChatMessage", mock.Anything, "wt1", "b2").Return(&db.Message{
		ChannelID: "wt1", MsgID: "b2", IsBot: true, SessionID: "sess-1", TranscriptUUID: "a-2", Forkable: true,
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("wt2", nil)
	s.store.On("GetChannel", mock.Anything, "wt2").Return(&db.Channel{ChannelID: "wt2", ParentID: "ch1"}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)
	s.store.On("MarkSessionForkPendingAt", mock.Anything, "wt2", "sess-1", "a-2").Return(true, nil)

	rec := s.testRequest("POST", "/api/channels/wt1/messages/b0/fork", "")
	require.Equal(s.T(), http.StatusConflict, rec.Code)
	s.threads.AssertNotCalled(s.T(), "CreateThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	rec = s.testRequest("POST", "/api/channels/wt1/messages/b2/fork", "")
	require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
	var resp forkThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "wt2", resp.ThreadID)
	require.Contains(s.T(), resp.WorktreePath, ".worktrees/")
	// The message's session is staged, not the thread's current one.
	s.store.AssertCalled(s.T(), "MarkSessionForkPendingAt", mock.Anything, "wt2", "sess-1", "a-2")
	s.sys.AssertCalled(s.T(), "ReadFile", filepath.Join("/home/testuser", ".claude", "projects", osutil.EncodeClaudeProjectPath(src), "sess-1.jsonl"))
}

func (s *ServerSuite) TestReadTranscriptInvalidSessionID() {
	s.sys.On("UserHomeDir").Return("/home/testuser", nil)
	_, _, err := s.srv.readTranscript("/proj", "..")
	require.Error(s.T(), err)
}
