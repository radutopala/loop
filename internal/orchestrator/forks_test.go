package orchestrator

import (
	"context"
	"errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// recordRemovals points s.orch's session file deletes at a recorder, failing
// each with removeErr, and returns the paths removed.
func (s *OrchestratorSuite) recordRemovals(removeErr error) *[]string {
	var removed []string
	s.orch.sessionFiles = sessionFiles{
		userHomeDir: func() (string, error) { return "/home/u", nil },
		removeAll: func(path string) error {
			removed = append(removed, path)
			return removeErr
		},
	}
	return &removed
}

func (s *OrchestratorSuite) TestSessionFilesRemove() {
	tests := []struct {
		name      string
		homeErr   error
		removeErr error
		sessionID string
		want      []string
		wantErr   string
	}{
		{name: "transcript and dir", sessionID: "fork-1",
			want: []string{"/home/u/.claude/projects/-project/fork-1.jsonl", "/home/u/.claude/projects/-project/fork-1"}},
		{name: "id can't leave the project dir", sessionID: "../../x",
			want: []string{"/home/u/.claude/projects/-project/x.jsonl", "/home/u/.claude/projects/-project/x"}},
		{name: "remove fails", sessionID: "fork-1", removeErr: errors.New("busy"), wantErr: "busy",
			want: []string{"/home/u/.claude/projects/-project/fork-1.jsonl", "/home/u/.claude/projects/-project/fork-1"}},
		{name: "no home", sessionID: "fork-1", homeErr: errors.New("no home"), wantErr: "no home"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			var removed []string
			f := sessionFiles{
				userHomeDir: func() (string, error) { return "/home/u", tc.homeErr },
				removeAll: func(path string) error {
					removed = append(removed, path)
					return tc.removeErr
				},
			}
			err := f.remove("/project", tc.sessionID)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tc.want, removed)
		})
	}
}

// TestDropFork covers the guards on deleting a hidden thread's fork: only a
// hidden thread's own fork that no other channel points to goes, and a
// failure is only logged.
func (s *OrchestratorSuite) TestDropFork() {
	learnThread := &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn, DirPath: "/project", SessionID: "fork-1"}
	files := []string{"/home/u/.claude/projects/-project/fork-1.jsonl", "/home/u/.claude/projects/-project/fork-1"}
	tests := []struct {
		name      string
		h         *db.Channel
		sessionID string
		inUse     bool
		inUseErr  error
		removeErr error
		check     bool
		want      []string
	}{
		{name: "deleted", h: learnThread, sessionID: "fork-1", check: true, want: files},
		{name: "delete fails", h: learnThread, sessionID: "fork-1", check: true, removeErr: errors.New("busy"), want: files},
		{name: "no session", h: learnThread},
		{name: "no dir", h: &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn}, sessionID: "fork-1"},
		{name: "not hidden", h: &db.Channel{ChannelID: "t1", DirPath: "/project"}, sessionID: "fork-1"},
		{name: "parent's session a thread waits to fork", h: &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn, DirPath: "/project", SessionID: "fork-1", ForkPending: true}, sessionID: "fork-1"},
		{name: "in use elsewhere", h: learnThread, sessionID: "fork-1", check: true, inUse: true},
		{name: "in-use check fails", h: learnThread, sessionID: "fork-1", check: true, inUseErr: errors.New("db down")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			removed := s.recordRemovals(tc.removeErr)
			if tc.check {
				s.store.On("SessionInUse", s.ctx, tc.sessionID, tc.h.ChannelID).Return(tc.inUse, tc.inUseErr)
			}
			s.orch.dropFork(s.ctx, tc.h, tc.sessionID)
			require.Equal(s.T(), tc.want, *removed)
			s.store.AssertExpectations(s.T())
		})
	}
}

// TestAfterHiddenRun covers which forks a hidden thread's run leaves behind:
// an explanation's always goes, a learn thread keeps its latest for replies
// and drops the one it replaced, and a deleted thread's goes.
func (s *OrchestratorSuite) TestAfterHiddenRun() {
	learnThread := &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn, DirPath: "/project", SessionID: "fork-old"}
	explainThread := &db.Channel{ChannelID: "explain-1", Kind: db.ChannelKindExplain, DirPath: "/project"}
	forked := &agent.AgentRequest{SessionID: "sess-parent", ForkSession: true}
	tests := []struct {
		name     string
		h        *db.Channel
		req      *agent.AgentRequest
		ran      string
		stored   bool
		fresh    *db.Channel
		freshErr error
		want     []string // sessions dropped
	}{
		{name: "not a hidden thread", h: &db.Channel{ChannelID: "ch1"}, req: forked, ran: "fork-new", stored: true},
		{name: "no thread", req: forked, ran: "fork-new"},
		{name: "no request", h: learnThread, ran: "fork-new"},
		{name: "learn pass replaces the old fork", h: learnThread, req: forked, ran: "fork-new", stored: true, fresh: learnThread, want: []string{"fork-old"}},
		{name: "reload fails, still replaced", h: learnThread, req: forked, ran: "fork-new", stored: true, freshErr: errors.New("db down"), want: []string{"fork-old"}},
		{name: "learn pass fails, its fork goes", h: learnThread, req: forked, ran: "fork-new", fresh: learnThread, want: []string{"fork-new"}},
		{name: "reply resumed the thread's own fork", h: learnThread, req: &agent.AgentRequest{SessionID: "fork-old"}, ran: "fork-old", stored: true, fresh: learnThread},
		{name: "unknown session", h: learnThread, req: forked, stored: true, fresh: learnThread},
		{name: "explanation's fork goes", h: explainThread, req: forked, ran: "fork-new", stored: true, fresh: explainThread, want: []string{"fork-new"}},
		{name: "deleted during the run", h: learnThread, req: forked, ran: "fork-new", stored: true, want: []string{"fork-new"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			removed := s.recordRemovals(nil)
			if tc.h != nil {
				s.store.On("GetChannel", s.ctx, tc.h.ChannelID).Return(tc.fresh, tc.freshErr).Maybe()
			}
			s.store.On("SessionInUse", s.ctx, mock.Anything, mock.Anything).Return(false, nil).Maybe()

			s.orch.afterHiddenRun(s.ctx, tc.h, tc.req, tc.ran, tc.stored)

			var dropped []string
			for i := 0; i < len(*removed); i += 2 {
				dropped = append(dropped, (*removed)[i+1][len("/home/u/.claude/projects/-project/"):])
			}
			require.Equal(s.T(), tc.want, dropped)
		})
	}
}

// TestStopHiddenThread checks a deleted hidden thread's run is cancelled and
// its fork deleted on the thread's drain.
func (s *OrchestratorSuite) TestStopHiddenThread() {
	cancelled := false
	s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() { cancelled = true }))
	var spawned func()
	s.orch.drainSpawn = func(fn func()) { spawned = fn }
	removed := s.recordRemovals(nil)
	s.store.On("SessionInUse", mock.Anything, "fork-1", "learn-1").Return(false, nil)

	s.orch.StopHiddenThread(&db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn, DirPath: "/project", SessionID: "fork-1"})

	require.True(s.T(), cancelled)
	require.Empty(s.T(), *removed, "the fork goes on the thread's drain")
	require.NotNil(s.T(), spawned)
	spawned()
	require.Equal(s.T(), []string{"/home/u/.claude/projects/-project/fork-1.jsonl", "/home/u/.claude/projects/-project/fork-1"}, *removed)
}

// TestLearnPassRunDropsForks covers a learn pass's run end to end: it forks
// its turn cut where the turn ended, records where its own streamed turns
// end, and leaves one fork behind: the new one when it succeeds, none when
// it fails.
func (s *OrchestratorSuite) TestLearnPassRunDropsForks() {
	tests := []struct {
		name    string
		runErr  error
		wantGet string
	}{
		{name: "succeeds, the old fork goes", wantGet: "fork-old"},
		{name: "fails, its fork goes", runErr: errors.New("boom"), wantGet: "fork-new"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(&config.Config{})
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return().Maybe()
			eb.On("BroadcastAgentStatus", mock.Anything, mock.Anything).Return().Maybe()
			eb.On("BroadcastLearnPass", mock.Anything).Return().Maybe()
			s.orch.SetEventBroadcaster(eb)
			removed := s.recordRemovals(nil)
			thread := &db.Channel{ID: 9, ChannelID: "learn-1", ParentID: "ch1", DirPath: "/project", Kind: db.ChannelKindLearn, Platform: types.PlatformLocal, SessionID: "fork-old", Active: true}
			s.store.On("IsChannelActive", s.ctx, "learn-1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "learn-1").Return(thread, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", SessionID: "sess-now"}, nil)
			s.store.On("GetRecentMessages", s.ctx, "learn-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetLearnPassByTrigger", s.ctx, "learn-1", "t1").Return(&db.LearnPass{ID: 4, MessageID: "b1"}, nil)
			s.store.On("UpdateLearnPass", s.ctx, int64(4), mock.Anything, mock.Anything).Return(nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{SessionID: "sess-then", TranscriptUUID: "uuid-b1"}, nil)
			s.store.On("ListScheduledTasks", s.ctx, "ch1").Return([]*db.ScheduledTask(nil), nil)
			s.store.On("ListLearnProposals", s.ctx, "ch1").Return([]*db.LearnProposal(nil), nil)
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool { return !m.IsBot })).Return(nil)
			s.store.On("UpdateSessionID", s.ctx, "learn-1", "fork-new").Return(nil).Maybe()
			s.store.On("MarkMessagesProcessed", s.ctx, mock.Anything).Return(nil).Maybe()
			s.store.On("SessionInUse", s.ctx, tc.wantGet, "learn-1").Return(false, nil)
			s.bot.On("SendTyping", mock.Anything, "learn-1").Return(nil).Maybe()
			s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
			var got *agent.AgentRequest
			resp := &agent.AgentResponse{Response: "reviewed", SessionID: "fork-new"}
			if tc.runErr != nil {
				resp = nil
			}
			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				got = req
				req.OnTurn("reviewed", agent.TurnRef{SessionID: "fork-new", UUID: "uuid-9"})
				return true
			})).Return(resp, tc.runErr)

			s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", MessageID: "t1", AuthorID: learnAuthorID, AuthorName: learnAuthorName,
				Content: "review", HasPrefix: true, Platform: types.PlatformLocal})

			require.NotNil(s.T(), got)
			require.Equal(s.T(), "sess-then", got.SessionID)
			require.Equal(s.T(), "uuid-b1", got.ResumeAt)
			require.True(s.T(), got.ForkSession)
			var stored *db.Message
			for _, c := range s.store.Calls {
				if m, ok := c.Arguments.Get(1).(*db.Message); ok && c.Method == "InsertMessage" && m.IsBot && m.Content == "reviewed" {
					stored = m
				}
			}
			require.NotNil(s.T(), stored)
			require.Equal(s.T(), "fork-new", stored.SessionID)
			require.Equal(s.T(), "uuid-9", stored.TranscriptUUID)
			require.Equal(s.T(), []string{
				"/home/u/.claude/projects/-project/" + tc.wantGet + ".jsonl",
				"/home/u/.claude/projects/-project/" + tc.wantGet,
			}, *removed)
		})
	}
}

func (s *OrchestratorSuite) TestReviewedTurn() {
	require.Equal(s.T(), "b1", reviewedTurn(&db.Explanation{MessageID: "b1"}, nil))
	require.Equal(s.T(), "b2", reviewedTurn(nil, &db.LearnPass{MessageID: "b2"}))
	require.Empty(s.T(), reviewedTurn(nil, nil))
}

// promptTranscript is a session whose second prompt, u-2, is answered by
// a-3 after an attachment.
const promptTranscript = `{"type":"user","uuid":"u-1","parentUuid":null,"message":{"role":"user","content":"first"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","message":{"content":[{"type":"text","text":"one"}]}}
{"type":"user","uuid":"u-2","parentUuid":"a-1","message":{"role":"user","content":"second"}}
{"type":"attachment","uuid":"x-1","parentUuid":"u-2"}
{"type":"assistant","uuid":"a-3","parentUuid":"x-1","message":{"content":[{"type":"text","text":"two"}]}}`

// readTranscripts points s.orch's transcript reads at promptTranscript and
// returns the paths read.
func (s *OrchestratorSuite) readTranscripts() *[]string {
	var read []string
	s.orch.sessionFiles = sessionFiles{
		userHomeDir: func() (string, error) { return "/home/u", nil },
		readFile: func(path string) ([]byte, error) {
			read = append(read, path)
			return []byte(promptTranscript), nil
		},
	}
	return &read
}

func (s *OrchestratorSuite) TestSessionFilesPromptOf() {
	tests := []struct {
		name      string
		homeErr   error
		readErr   error
		sessionID string
		uuid      string
		want      string
		wantErr   string
	}{
		{name: "reply's prompt", sessionID: "sess-1", uuid: "a-3", want: "u-2"},
		{name: "not in the transcript", sessionID: "sess-1", uuid: "a-9", wantErr: "isn't in the transcript"},
		{name: "unreadable", sessionID: "sess-1", uuid: "a-3", readErr: errors.New("gone"), wantErr: "gone"},
		{name: "invalid session", sessionID: "..", uuid: "a-3", wantErr: "invalid session id"},
		{name: "no home", sessionID: "sess-1", uuid: "a-3", homeErr: errors.New("no home"), wantErr: "no home"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			f := sessionFiles{
				userHomeDir: func() (string, error) { return "/home/u", tc.homeErr },
				readFile: func(path string) ([]byte, error) {
					require.Equal(s.T(), "/home/u/.claude/projects/-project/sess-1.jsonl", path)
					return []byte(promptTranscript), tc.readErr
				},
			}
			got, err := f.promptOf("/project", tc.sessionID, tc.uuid)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

// TestRecordPrompt covers which runs record their prompt's transcript
// entry, and that failing to is only logged.
func (s *OrchestratorSuite) TestRecordPrompt() {
	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	reply := agent.TurnRef{SessionID: "sess-1", UUID: "a-3"}
	tests := []struct {
		name     string
		ch       *db.Channel
		reply    agent.TurnRef
		setErr   error
		wantRead bool
		wantSet  bool
	}{
		{name: "records", ch: ch, reply: reply, wantRead: true, wantSet: true},
		{name: "store fails", ch: ch, reply: reply, setErr: errors.New("db down"), wantRead: true, wantSet: true},
		{name: "prompt not found", ch: ch, reply: agent.TurnRef{SessionID: "sess-1", UUID: "a-9"}, wantRead: true},
		{name: "no reply", ch: ch},
		{name: "reply without a uuid", ch: ch, reply: agent.TurnRef{SessionID: "sess-1"}},
		{name: "no channel", reply: reply},
		{name: "no dir", ch: &db.Channel{ChannelID: "ch1"}, reply: reply},
		{name: "hidden thread", ch: &db.Channel{ChannelID: "e1", DirPath: "/project", Kind: db.ChannelKindExplain}, reply: reply},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			read := s.readTranscripts()
			s.store.On("SetPromptTranscriptRef", s.ctx, "ch1", "m2", "sess-1", "u-2").Return(tc.setErr)

			s.orch.recordPrompt(s.ctx, tc.ch, "m2", tc.reply)

			require.Equal(s.T(), tc.wantRead, len(*read) > 0)
			if tc.wantSet {
				s.store.AssertCalled(s.T(), "SetPromptTranscriptRef", s.ctx, "ch1", "m2", "sess-1", "u-2")
			} else {
				s.store.AssertNotCalled(s.T(), "SetPromptTranscriptRef", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

// TestHandleMessageRecordsPrompt: once a run ends, the user message that
// started it records its prompt's entry, found from the run's first reply
// that has a uuid.
func (s *OrchestratorSuite) TestHandleMessageRecordsPrompt() {
	read := s.readTranscripts()
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/project", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req := args.Get(1).(*agent.AgentRequest)
		req.OnTurn("starting", agent.TurnRef{SessionID: "sess-1"})
		req.OnTurn("two", agent.TurnRef{SessionID: "sess-1", UUID: "a-3"})
		req.OnTurn("more", agent.TurnRef{SessionID: "sess-1", UUID: "a-4"})
	}).Return(&agent.AgentResponse{Response: "more", SessionID: "sess-1"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-1").Return(nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, mock.Anything).Return(nil)
	s.store.On("SetPromptTranscriptRef", mock.Anything, "ch1", "m2", "sess-1", "u-2").Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "ch1", MessageID: "m2", GuildID: "g1", AuthorName: "user",
		Content: "second", IsBotMention: true,
	})

	s.store.AssertCalled(s.T(), "SetPromptTranscriptRef", mock.Anything, "ch1", "m2", "sess-1", "u-2")
	require.Equal(s.T(), []string{"/home/u/.claude/projects/-project/sess-1.jsonl"}, *read)
}
