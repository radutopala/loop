package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

func (s *OrchestratorSuite) TestSwitchSession() {
	tests := []struct {
		name         string
		running      bool
		inUse        bool
		inUseErr     error
		saveErr      error
		wantDeferred bool
		wantErr      string
		wantSave     string // "update", "fork" or "" for neither
	}{
		{name: "idle switches now", wantSave: "update"},
		{name: "shared session forks", inUse: true, wantSave: "fork"},
		{name: "in-use check fails", inUseErr: errors.New("db"), wantErr: "db"},
		{name: "update fails", saveErr: errors.New("db"), wantErr: "db", wantSave: "update"},
		{name: "fork mark fails", inUse: true, saveErr: errors.New("db"), wantErr: "db", wantSave: "fork"},
		{name: "running defers", running: true, wantDeferred: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("SessionInUse", s.ctx, "sess-2", "ch1").Return(tc.inUse, tc.inUseErr).Maybe()
			s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-2").Return(tc.saveErr).Maybe()
			s.store.On("MarkSessionForkPending", s.ctx, "ch1", "sess-2").Return(true, tc.saveErr).Maybe()
			if tc.running {
				s.orch.sessionRunStarted("ch1")
			}

			deferred, err := s.orch.SwitchSession(s.ctx, "ch1", "sess-2")

			require.Equal(s.T(), tc.wantDeferred, deferred)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			switch tc.wantSave {
			case "update":
				s.store.AssertCalled(s.T(), "UpdateSessionID", s.ctx, "ch1", "sess-2")
				s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, mock.Anything, mock.Anything)
			case "fork":
				s.store.AssertCalled(s.T(), "MarkSessionForkPending", s.ctx, "ch1", "sess-2")
				s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
			default:
				s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
				s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

// TestSessionRunDone covers a switch deferred during a run: applied once the
// run ends, only in its own channel, and a failure is only logged.
func (s *OrchestratorSuite) TestSessionRunDone() {
	tests := []struct {
		name    string
		saveErr error
	}{
		{name: "applies the deferred switch"},
		{name: "failure is logged", saveErr: errors.New("db")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("SessionInUse", s.ctx, "sess-2", "ch1").Return(false, nil).Once()
			s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-2").Return(tc.saveErr).Once()
			s.orch.sessionRunStarted("ch1")
			s.orch.sessionRunStarted("ch2")
			deferred, err := s.orch.SwitchSession(s.ctx, "ch1", "sess-2")
			require.NoError(s.T(), err)
			require.True(s.T(), deferred)

			s.orch.sessionRunDone(s.ctx, "ch2")
			s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)

			s.orch.sessionRunDone(s.ctx, "ch1")
			s.orch.sessionRunDone(s.ctx, "ch1")
			s.store.AssertNumberOfCalls(s.T(), "UpdateSessionID", 1)
		})
	}
}

// TestSwitchSessionDuringRun switches a channel's session while its run is
// in progress: the switch lands after the run saves its own session, so the
// channel's next run resumes the chosen one.
func (s *OrchestratorSuite) TestSwitchSessionDuringRun() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "local-user",
		AuthorName:   "local-user",
		Content:      "hello bot",
		MessageID:    "msg-local",
		Platform:     types.PlatformLocal,
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	var saved []string
	var deferred bool
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(mock.Arguments) {
		var err error
		deferred, err = s.orch.SwitchSession(context.Background(), "ch1", "sess-picked")
		s.Require().NoError(err)
	}).Return(&agent.AgentResponse{Response: "Hello!", SessionID: "s1"}, nil)
	s.store.On("SessionInUse", s.ctx, "sess-picked", "ch1").Return(false, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", mock.Anything).Run(func(args mock.Arguments) {
		saved = append(saved, args.String(2))
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)
	s.orch.drainWG.Wait()

	require.True(s.T(), deferred)
	require.Equal(s.T(), []string{"s1", "sess-picked"}, saved)
}
