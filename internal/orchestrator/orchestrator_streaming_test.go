package orchestrator

import (
	"errors"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// --- Streaming tests ---

func (s *OrchestratorSuite) TestHandleMessageStreamingSkipsDuplicate() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)

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
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)

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
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)

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

func (s *OrchestratorSuite) TestHandleMessageStreamingDisabledNoOnTurn() {
	// streamingEnabled is false by default (set in SetupTest)
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
		// OnTurn should NOT be set when streaming is disabled
		return req.OnTurn == nil
	})).Return(&agent.AgentResponse{
		Response:  "Hello!",
		SessionID: "sess-3",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Hello!" && out.ReplyToMessageID == "msg1"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// Only 1 SendMessage call (the final response)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.store.AssertExpectations(s.T())
	s.runner.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingBroadcastsViaEvents() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
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
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
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

func (s *OrchestratorSuite) TestHandleMessageStreamingAskUserQuestion() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
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

	askInput := `{"questions":[{"question":"Pick one","header":"Choice","options":[{"label":"X"}]}]}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnToolUse("toolu_q", "AskUserQuestion", askInput)
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "sess-ask"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-ask").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Once()
	eb.On("BroadcastAskUser", "ch1", mock.MatchedBy(func(d events.AskUserQuestionEventData) bool {
		return len(d.Questions) == 1 && d.Questions[0].Header == "Choice"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertCalled(s.T(), "BroadcastAskUser", "ch1", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingExitPlanMode() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "plan it", MessageID: "msg-plan", IsBotMention: true, Timestamp: time.Now().UTC(),
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
		if req.OnToolUse == nil {
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
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingTodoWrite() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID: "ch1", GuildID: "g1", AuthorID: "user1", AuthorName: "Alice",
		Content: "do tasks", MessageID: "msg-todo", IsBotMention: true, Timestamp: time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Maybe()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)

	todoInput := `{"todos":[{"content":"Fix bug","status":"in_progress","activeForm":"Fixing bug"}]}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnToolUse("toolu_t", "TodoWrite", todoInput)
		req.OnTurn("done")
		return true
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "sess-todo"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-todo").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return()
	eb.On("BroadcastToolUse", "ch1", mock.Anything).Once()
	eb.On("BroadcastTodoWrite", "ch1", mock.MatchedBy(func(d events.TodoWriteEventData) bool {
		return len(d.Todos) == 1 && d.Todos[0].Content == "Fix bug"
	})).Once()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertCalled(s.T(), "BroadcastTodoWrite", "ch1", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingOnActivityBroadcasts() {
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
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
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
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
	cfgStream := s.orch.cfg.Load()
	cfgStream.StreamingEnabled = true
	s.orch.cfg.Store(cfgStream)
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
