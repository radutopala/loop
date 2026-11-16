package orchestrator

import (
	"context"
	"errors"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/promptedit"
	"github.com/radutopala/loop/internal/types"
)

// TestPromptEdit covers an edit by voice: queued in the channel's
// prompt-edit thread, it returns the draft its run's reply makes, or why
// the run failed.
func (s *OrchestratorSuite) TestPromptEdit() {
	parent := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
	thread := &db.Channel{ChannelID: "prompt-edit-1", GuildID: "g1", Name: "prompt-edit: api", Kind: db.ChannelKindPromptEdit}
	req := promptedit.Request{Draft: "fix it", Instruction: "shorter"}

	s.Run("no instruction", func() {
		s.SetupTest()
		_, err := s.orch.PromptEdit(s.ctx, parent, promptedit.Request{Draft: "d"})
		require.ErrorIs(s.T(), err, promptedit.ErrNoInstruction)
		s.store.AssertNotCalled(s.T(), "GetHiddenThread", mock.Anything, mock.Anything, mock.Anything)
	})
	s.Run("thread fails", func() {
		s.SetupTest()
		s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindPromptEdit).Return(nil, errors.New("db down"))
		_, err := s.orch.PromptEdit(s.ctx, parent, req)
		require.EqualError(s.T(), err, "db down")
	})
	tests := []struct {
		name    string
		reply   string
		runErr  error
		want    *promptedit.Result
		wantErr string
	}{
		{name: "edits", reply: "Fix it.", want: &promptedit.Result{Text: "Fix it.", From: 0, To: 7}},
		{name: "run fails", runErr: errors.New("agent error: rate limited"), wantErr: "agent error: rate limited"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
			s.orch.SetEventBroadcaster(eb)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindPromptEdit).Return(thread, nil)
			s.store.On("IsChannelActive", s.ctx, "prompt-edit-1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "prompt-edit-1").Return(&db.Channel{ID: 9, ChannelID: "prompt-edit-1", Kind: db.ChannelKindPromptEdit}, nil)
			var trigger *db.Message
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
				trigger = m
				return !m.IsBot
			})).Return(nil)
			// The drain stands in for the run: it ends with the reply.
			s.orch.drainSpawn = func(func()) {
				s.orch.promptEditDone(&bot.IncomingMessage{MessageID: trigger.MsgID, AuthorID: promptEditAuthorID}, tc.reply, tc.runErr)
			}

			got, err := s.orch.PromptEdit(s.ctx, parent, req)

			require.Equal(s.T(), promptEditAuthorID, trigger.AuthorID)
			require.Equal(s.T(), promptEditAuthorName, trigger.AuthorName)
			require.True(s.T(), strings.HasPrefix(trigger.Content, "<draft>\nfix it\n</draft>"), trigger.Content)
			_, waiting := s.orch.promptEdits.Load(trigger.MsgID)
			require.False(s.T(), waiting)
			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

// TestPromptEditGivenUp covers an edit whose caller went away before its
// run ended: a queued one leaves the queue, a running one is stopped.
func (s *OrchestratorSuite) TestPromptEditGivenUp() {
	parent := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
	thread := &db.Channel{ChannelID: "prompt-edit-1", Name: "prompt-edit: api", Kind: db.ChannelKindPromptEdit}
	tests := []struct {
		name      string
		deleted   bool
		deleteErr error
		running   bool
		cancelled bool
	}{
		{name: "queued", deleted: true},
		{name: "running", running: true, cancelled: true},
		{name: "already gone", deleteErr: errors.New("db down")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx, cancel := context.WithCancel(s.ctx)
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
			s.orch.SetEventBroadcaster(eb)
			s.store.On("GetHiddenThread", ctx, "ch1", db.ChannelKindPromptEdit).Return(thread, nil)
			s.store.On("IsChannelActive", ctx, "prompt-edit-1").Return(true, nil)
			s.store.On("GetChannel", ctx, "prompt-edit-1").Return(&db.Channel{ID: 9, ChannelID: "prompt-edit-1", Kind: db.ChannelKindPromptEdit}, nil)
			var runID string
			s.store.On("InsertMessage", ctx, mock.MatchedBy(func(m *db.Message) bool {
				runID = m.MsgID
				return true
			})).Return(nil)
			s.store.On("DeleteQueuedMessage", mock.Anything, "prompt-edit-1", mock.Anything).Return(tc.deleted, tc.deleteErr)
			if tc.deleted {
				eb.On("BroadcastMessageDeleted", "prompt-edit-1", mock.MatchedBy(func(d events.MessageDeletedData) bool { return d.MsgID == runID })).Return().Once()
			}
			runCancelled := false
			s.orch.drainSpawn = func(func()) {
				if tc.running {
					s.orch.activeRunMsgIDs.Store("prompt-edit-1", runID)
				} else {
					s.orch.activeRunMsgIDs.Store("prompt-edit-1", "another")
				}
				s.orch.activeRuns.Store("prompt-edit-1", context.CancelFunc(func() { runCancelled = true }))
				cancel()
			}

			_, err := s.orch.PromptEdit(ctx, parent, promptedit.Request{Instruction: "write a test"})

			require.ErrorIs(s.T(), err, context.Canceled)
			require.Equal(s.T(), tc.cancelled, runCancelled)
			_, waiting := s.orch.promptEdits.Load(runID)
			require.False(s.T(), waiting)
			eb.AssertExpectations(s.T())
		})
	}
	s.Run("queued, no events", func() {
		s.SetupTest()
		s.store.On("DeleteQueuedMessage", s.ctx, "prompt-edit-1", "run-1").Return(true, nil)
		s.orch.cancelPromptEdit(s.ctx, "run-1", &promptEditRun{threadID: "prompt-edit-1"})
		s.store.AssertExpectations(s.T())
	})
}

// TestPromptEditThreadRuns checks what runs in a prompt-edit thread: only
// the edits PromptEdit queued there and still waits for, each read-only in
// a fresh session on the channel's model, with the edit's own message.
func (s *OrchestratorSuite) TestPromptEditThreadRuns() {
	thread := &db.Channel{ChannelID: "prompt-edit-1", ParentID: "ch1", DirPath: "/project", Kind: db.ChannelKindPromptEdit, SessionID: "old"}
	parent := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project", ModelOverride: "opus", EffortOverride: "high"}
	edit, err := promptedit.Prepare(promptedit.Request{Draft: "fix it", Instruction: "shorter"})
	require.NoError(s.T(), err)
	tests := []struct {
		name    string
		run     *promptEditRun // registered under run-1
		author  string
		wantErr string
	}{
		{name: "a user's message", run: &promptEditRun{edit: edit, threadID: "prompt-edit-1"}, author: "user-1", wantErr: "prompt-edit thread prompt-edit-1: only edits by voice run here"},
		{name: "an edit given up on", author: promptEditAuthorID, wantErr: "prompt-edit thread prompt-edit-1: the edit was dropped before it ran"},
		{name: "an edit queued in another thread", run: &promptEditRun{edit: edit, threadID: "prompt-edit-2"}, author: promptEditAuthorID, wantErr: "the edit was dropped before it ran"},
		{name: "the edit", run: &promptEditRun{edit: edit, threadID: "prompt-edit-1"}, author: promptEditAuthorID},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.run != nil {
				s.orch.promptEdits.Store("run-1", tc.run)
			}
			s.store.On("GetRecentMessages", s.ctx, "prompt-edit-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "prompt-edit-1").Return(thread, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(parent, nil)
			msg := &bot.IncomingMessage{ChannelID: "prompt-edit-1", MessageID: "run-1", AuthorID: tc.author, AuthorName: promptEditAuthorName, Content: edit.Prompt}

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, msg, "")

			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), promptedit.AgentID, req.AgentID)
			require.True(s.T(), req.ReadOnly)
			require.Empty(s.T(), req.SessionID)
			require.False(s.T(), req.ForkSession)
			require.Empty(s.T(), req.ResumeAt)
			require.Equal(s.T(), "opus", req.Model)
			require.Equal(s.T(), "high", req.Effort)
			require.Equal(s.T(), promptedit.SystemPrompt(), req.SystemPrompt)
			require.Equal(s.T(), edit.Prompt, req.Prompt)
			require.False(s.T(), req.Live)
			require.False(s.T(), req.Suggest)
		})
	}
	s.Run("forgotten between admitting and shaping", func() {
		s.SetupTest()
		err := s.orch.applyPromptEditRequest(nil, &bot.IncomingMessage{MessageID: "run-1"}, parent)
		require.ErrorIs(s.T(), err, errPromptEditDropped)
	})
}

// TestPromptEditDone checks that only the edit's own run hands it a reply.
func (s *OrchestratorSuite) TestPromptEditDone() {
	run := &promptEditRun{threadID: "prompt-edit-1", done: make(chan promptEditOutcome, 1)}
	s.orch.promptEdits.Store("run-1", run)
	s.orch.promptEditDone(&bot.IncomingMessage{MessageID: "run-1", AuthorID: "user-1"}, "x", nil)
	s.orch.promptEditDone(&bot.IncomingMessage{MessageID: "run-2", AuthorID: promptEditAuthorID}, "x", nil)
	require.Empty(s.T(), run.done)
	s.orch.promptEditDone(&bot.IncomingMessage{MessageID: "run-1", AuthorID: promptEditAuthorID}, "new", nil)
	require.Equal(s.T(), promptEditOutcome{reply: "new"}, <-run.done)
	_, ok := s.orch.promptEdits.Load("run-1")
	require.False(s.T(), ok)
}

// TestHandleMessagePromptEditTriggerNeverAutoCreates covers an edit whose
// thread was deleted meanwhile: it fails, rather than auto-creating a plain
// channel where the run would be unrestricted.
func (s *OrchestratorSuite) TestHandleMessagePromptEditTriggerNeverAutoCreates() {
	s.store.On("IsChannelActive", s.ctx, "prompt-edit-1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "prompt-edit-1").Return("", nil)
	run := &promptEditRun{threadID: "prompt-edit-1", done: make(chan promptEditOutcome, 1)}
	s.orch.promptEdits.Store("run-1", run)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "prompt-edit-1",
		MessageID: "run-1",
		AuthorID:  promptEditAuthorID,
		Content:   "edit",
		HasPrefix: true,
		Platform:  types.PlatformLocal,
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	out := <-run.done
	require.EqualError(s.T(), out.err, "the edit's thread was deleted")
}
