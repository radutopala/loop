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
	"github.com/radutopala/loop/internal/events"
)

// --- Streaming tests ---

func (s *OrchestratorSuite) TestHandleMessageStreamingSkipsDuplicate() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello bot",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Let me check...")
		req.OnTurn("") // empty text should be skipped
		req.OnTurn("Here is the answer.")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Here is the answer.", // Same as last OnTurn — final skipped
		SessionID: "sess-1",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-1").Return(nil)

	// Both OnTurn calls go to channel with reply-to
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.ReplyToMessageID == "msg1" &&
			(out.Content == "Let me check..." || out.Content == "Here is the answer.")
	})).Return(nil).Twice()

	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// 2 SendMessage (two OnTurn calls), final skipped (duplicate)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.store.AssertExpectations(s.T())
	s.runner.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingSendsFinalWhenDifferent() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Intermediate turn")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Final different response",
		SessionID: "sess-2",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-2").Return(nil)

	// OnTurn + final (different) both go to channel with reply-to
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.ReplyToMessageID == "msg1" &&
			(out.Content == "Intermediate turn" || out.Content == "Final different response")
	})).Return(nil).Twice()

	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// 2 SendMessage (1 OnTurn + 1 final)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingSendErrorIsLogged() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Streamed turn")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Streamed turn",
		SessionID: "sess-senderr",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-senderr").Return(nil)

	// Streaming SendMessage fails — should be logged, not fatal
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "Streamed turn" && out.ReplyToMessageID == "msg1"
	})).Return(errors.New("send failed")).Once()

	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingBroadcastsViaEvents() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("partial response")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "final response",
		SessionID: "sess-1",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	// Expect event broadcasts: each turn as message.created + final (different) as message.created
	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()

	s.orch.HandleMessage(s.ctx, msg)

	// 3 BroadcastMessageCreated calls: user message, intermediate turn, final response
	eb.AssertNumberOfCalls(s.T(), "BroadcastMessageCreated", 3)
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingOnToolUseBroadcasts() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnToolUse("toolu_b", "Bash", "go test ./...")
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "done",
		SessionID: "sess-tu",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-tu").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.MatchedBy(func(d events.ToolUseEventData) bool {
		return d.ToolName == "Bash" && d.Input == "go test ./..."
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertCalled(s.T(), "BroadcastToolUse", "ch1", mock.Anything)
	eb.AssertExpectations(s.T())
}

// TestHandleMessageStreamingAskUserQuestion verifies that an
// AskUserQuestion tool-use cancels the run, parks the channel on the ask
// flag, broadcasts a clean `completed` status (not `error`), and skips the
// final response delivery so the ask card lands as the only end-of-turn
// artifact. The user's answer arrives later via /api/channels/{id}/ask/resolve
// as a priority-bumped continuation.
func (s *OrchestratorSuite) TestHandleMessageStreamingAskUserQuestion() {
	cfgStream := s.orch.cfg.Load()
	// Non-zero timeout so the context cancellation we observe is provably
	// from runCancel(), not a 0-duration deadline.
	cfgStream.ContainerTimeout = time.Minute
	s.orch.cfg.Store(cfgStream)
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "ask me", MessageID: "msg-ask", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil).Maybe()

	askInput := `{"questions":[{"question":"Pick one","header":"Choice","options":[{"label":"X"}]}]}`
	var capturedCtx context.Context
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnToolUse("toolu_q", "AskUserQuestion", askInput)
		return true
	})).Run(func(args mock.Arguments) {
		capturedCtx = args.Get(0).(context.Context)
	}).Return((*agent.AgentResponse)(nil), context.Canceled)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Return().Once()
	eb.On("BroadcastAskUser", "ch1", mock.MatchedBy(func(d events.AskUserQuestionEventData) bool {
		return len(d.Questions) == 1 && d.Questions[0].Header == "Choice"
	})).Return().Once()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.MsgID == "msg-ask"
	})).Return().Once()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.Error == "" && d.MsgID == "msg-ask"
	})).Return().Once()

	s.orch.HandleMessage(s.ctx, msg)

	require.NotNil(s.T(), capturedCtx)
	require.ErrorIs(s.T(), capturedCtx.Err(), context.Canceled)
	eb.AssertCalled(s.T(), "BroadcastAskUser", "ch1", mock.Anything)
	require.True(s.T(), s.orch.IsChannelAsked("ch1"))
	eb.AssertExpectations(s.T())
}

// TestAskUserQuestionBroadcastOrder is a regression test for a FE-side race:
// when agent.status non-running lands BEFORE messages.processed, the FE's
// refetchHead (triggered by status) can fetch /timeline before the
// is_processed=1 DB write is committed and overwrite the optimistic local
// flip with stale rows — leaving the trigger labeled "queued" forever.
// The fix is to broadcast messages.processed FIRST in processClaimedMessage's
// error path, then the deferred agent.status from executeAgentRun.
func (s *OrchestratorSuite) TestAskUserQuestionBroadcastOrder() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.ContainerTimeout = time.Minute
	s.orch.cfg.Store(cfgStream)
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "ask me", MessageID: "msg-ask-order", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	// Recent must contain the trigger row so markTriggerProcessed does NOT
	// early-return (the empty-recent shortcut would skip the very broadcast
	// whose ordering we want to test).
	trigger := &db.Message{ID: 42, ChannelID: "ch1", MsgID: "msg-ask-order", IsTriggered: true}
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{trigger}, nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{42}).Return(nil)

	askInput := `{"questions":[{"question":"Pick","header":"Choice","options":[{"label":"X"}]}]}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnToolUse("toolu_q", "AskUserQuestion", askInput)
		return true
	})).Return((*agent.AgentResponse)(nil), context.Canceled)

	// Record call order across the two broadcasts we care about.
	var order []string
	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Return().Once()
	eb.On("BroadcastAskUser", "ch1", mock.Anything).Return().Once()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running"
	})).Run(func(_ mock.Arguments) { order = append(order, "running") }).Return().Once()
	eb.On("BroadcastMessagesProcessed", "ch1", mock.MatchedBy(func(d events.MessagesProcessedData) bool {
		return len(d.MsgIDs) == 1 && d.MsgIDs[0] == "msg-ask-order"
	})).Run(func(_ mock.Arguments) { order = append(order, "processed") }).Return().Once()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed"
	})).Run(func(_ mock.Arguments) { order = append(order, "completed") }).Return().Once()

	s.orch.HandleMessage(s.ctx, msg)

	require.Equal(s.T(), []string{"running", "processed", "completed"}, order,
		"messages.processed must fire BEFORE agent.status non-running so FE refetchHead sees the committed is_processed=1")
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingExitPlanMode() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	// User picked the plan pill (Mode="plan") → req.PlanMode=true →
	// the runner completes normally because the prompt-injected plan-mode
	// system prompt halts the model at ExitPlanMode. No cancellation here.
	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "plan it", MessageID: "msg-plan", Mode: "plan", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	exitInput := `{"plan":"# Plan\nStep 1","planFilePath":"/tmp/p.md"}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil || !req.PlanMode {
			return false
		}
		req.OnToolUse("toolu_p", "ExitPlanMode", exitInput)
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "sess-plan"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-plan").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Once()
	eb.On("BroadcastExitPlan", "ch1", mock.MatchedBy(func(d events.ExitPlanModeEventData) bool {
		return d.Plan == "# Plan\nStep 1"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertCalled(s.T(), "BroadcastExitPlan", "ch1", mock.Anything)
	// ExitPlanMode parks the channel so the drain holds queued rows
	// until the user clicks approve / reject / deny.
	require.True(s.T(), s.orch.IsChannelPlanned("ch1"))
	eb.AssertExpectations(s.T())
}

// TestHandleMessageStreamingExitPlanModeSelfInitiated covers scenario 2:
// the user did NOT pick the plan pill (Mode unset), so req.PlanMode=false.
// The agent volunteers EnterPlanMode → ExitPlanMode mid-turn under
// --dangerously-skip-permissions. The orchestrator must cancel the run on
// ExitPlanMode so subsequent tools don't execute past the implicit gate,
// broadcast a clean `completed` status (not `error`), and skip the
// "Run stopped." chat message that the manual stop-button path emits.
func (s *OrchestratorSuite) TestHandleMessageStreamingExitPlanModeSelfInitiated() {
	cfgStream := s.orch.cfg.Load()
	// Non-zero timeout so a context cancellation we observe in the captured
	// context is provably from runCancel(), not from a 0-duration deadline.
	cfgStream.ContainerTimeout = time.Minute
	s.orch.cfg.Store(cfgStream)
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "do stuff", MessageID: "msg-self-plan", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	exitInput := `{"plan":"# Plan\nStep 1","planFilePath":"/tmp/p.md"}`
	// Capture the run context so we can assert it was cancelled.
	var capturedCtx context.Context
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil || req.PlanMode {
			return false
		}
		req.OnToolUse("toolu_p", "ExitPlanMode", exitInput)
		return true
	})).Run(func(args mock.Arguments) {
		capturedCtx = args.Get(0).(context.Context)
	}).Return((*agent.AgentResponse)(nil), context.Canceled)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Return().Once()
	eb.On("BroadcastExitPlan", "ch1", mock.MatchedBy(func(d events.ExitPlanModeEventData) bool {
		return d.Plan == "# Plan\nStep 1"
	})).Return().Once()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.MsgID == "msg-self-plan"
	})).Return().Once()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.Error == "" && d.MsgID == "msg-self-plan"
	})).Return().Once()

	s.orch.HandleMessage(s.ctx, msg)

	require.NotNil(s.T(), capturedCtx)
	require.ErrorIs(s.T(), capturedCtx.Err(), context.Canceled)
	eb.AssertCalled(s.T(), "BroadcastExitPlan", "ch1", mock.Anything)
	// Same pause flag as scenario 1 — the drain must hold queued messages
	// regardless of whether the agent halted naturally or was cancelled.
	require.True(s.T(), s.orch.IsChannelPlanned("ch1"))
	// No "Run stopped." message — the plan card itself is the UI artifact.
	s.bot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out != nil && out.Content == "Run stopped."
	}))
	// No error broadcast — we treat the cancellation as a clean end-of-turn.
	eb.AssertNotCalled(s.T(), "BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error"
	}))
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingTaskCreate() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "do tasks", MessageID: "msg-task", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	taskInput := `{"subject":"Fix bug","activeForm":"Fixing bug","description":""}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil || req.OnToolResult == nil {
			return false
		}
		req.OnToolUse("toolu_t", "TaskCreate", taskInput)
		req.OnToolResult("toolu_t", "Task #1 created successfully: Fix bug", false)
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "sess-task"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-task").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Once()
	eb.On("BroadcastToolResult", "ch1", mock.Anything).Once()
	eb.On("BroadcastAgentTasks", "ch1", mock.MatchedBy(func(d events.AgentTasksEventData) bool {
		return len(d.Tasks) == 1 && d.Tasks[0].Subject == "Fix bug" && d.Tasks[0].ID == "1"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertCalled(s.T(), "BroadcastAgentTasks", "ch1", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingTaskUpdate() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	// Seed the registry so applyUpdate finds the task.
	_, _ = s.orch.tasks.applyCreate("ch1",
		`{"subject":"Fix bug","activeForm":"Fixing bug"}`,
		"Task #1 created successfully: Fix bug",
	)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "do tasks", MessageID: "msg-upd", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	updateInput := `{"taskId":"1","status":"in_progress"}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnToolUse("toolu_u", "TaskUpdate", updateInput)
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "sess-upd"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-upd").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Once()
	eb.On("BroadcastAgentTasks", "ch1", mock.MatchedBy(func(d events.AgentTasksEventData) bool {
		return len(d.Tasks) == 1 && d.Tasks[0].Status == "in_progress"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertCalled(s.T(), "BroadcastAgentTasks", "ch1", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingOnActivityBroadcasts() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnActivity == nil {
			return false
		}
		req.OnActivity("model", "claude-opus-4-6")
		req.OnActivity("subagent_started", "Deep analysis")
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{
		Response:   "done",
		SessionID:  "sess-act",
		DurationMs: 5000,
		NumTurns:   2,
		StopReason: "end_turn",
		Model:      "claude-opus-4-6",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-act").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentActivity", "ch1", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "model" && d.Model == "claude-opus-4-6"
	})).Once()
	eb.On("BroadcastAgentActivity", "ch1", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "subagent_started" && d.Description == "Deep analysis"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertNumberOfCalls(s.T(), "BroadcastAgentActivity", 2)
	// Verify completed status includes result metadata
	eb.AssertCalled(s.T(), "BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.DurationMs == 5000 && d.NumTurns == 2 && d.StopReason == "end_turn" && d.Model == "claude-opus-4-6"
	}))
	eb.AssertExpectations(s.T())
}

// TestHandleMessageStreamingOnCompactingPersistsRow asserts that a "compacting"
// activity event writes a kind=compacting row via InsertAgentEvent so the
// marker survives run completion / page reload, in addition to the normal
// BroadcastAgentActivity for live subscribers.
func (s *OrchestratorSuite) TestHandleMessageStreamingOnCompactingPersistsRow() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 7, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	// The compacting row must be inserted with chat_id=7 (the channel's chat id)
	// and Kind=compacting. No tool/text fields are set.
	s.store.On("InsertAgentEvent", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 7 && m.ChannelID == "ch1" && m.Kind == db.MessageKindCompacting
	})).Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnActivity == nil {
			return false
		}
		req.OnActivity("compacting", "")
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "done",
		SessionID: "sess-cmp",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-cmp").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentActivity", "ch1", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "compacting"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertCalled(s.T(), "InsertAgentEvent", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.Kind == db.MessageKindCompacting
	}))
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingOnThinkingAndToolResultBroadcasts() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnThinking == nil || req.OnToolResult == nil {
			return false
		}
		req.OnThinking("planning a step")
		req.OnToolResult("toolu_q", "ok-output", false)
		req.OnToolResult("toolu_e", "boom", true)
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{
		Response:   "done",
		SessionID:  "sess-tk",
		DurationMs: 1000,
		NumTurns:   1,
		StopReason: "end_turn",
		Model:      "claude-opus-4-6",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-tk").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentThinking", "ch1", events.AgentThinkingEventData{Text: "planning a step"}).Once()
	eb.On("BroadcastToolResult", "ch1", events.ToolResultEventData{ToolUseID: "toolu_q", Output: "ok-output"}).Once()
	eb.On("BroadcastToolResult", "ch1", events.ToolResultEventData{ToolUseID: "toolu_e", Output: "boom", IsError: true}).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertNumberOfCalls(s.T(), "BroadcastAgentThinking", 1)
	eb.AssertNumberOfCalls(s.T(), "BroadcastToolResult", 2)
	eb.AssertExpectations(s.T())
}
