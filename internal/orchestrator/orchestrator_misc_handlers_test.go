package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// --- Stop interaction tests ---

func (s *OrchestratorSuite) TestHandleInteractionStopNoActiveRun() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "No active run to stop."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "stop",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionStopCancelsActiveRun() {
	cancelled := false
	cancelFunc := context.CancelFunc(func() { cancelled = true })
	s.orch.activeRuns.Store("ch1", cancelFunc)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		AuthorID:    "user1",
		CommandName: "stop",
	})

	require.True(s.T(), cancelled, "cancel func should have been called")
	// Verify the activeRuns entry was removed
	_, loaded := s.orch.activeRuns.Load("ch1")
	require.False(s.T(), loaded, "activeRuns entry should have been removed")
}

func (s *OrchestratorSuite) TestHandleInteractionStopWithChannelIDOption() {
	cancelled := false
	cancelFunc := context.CancelFunc(func() { cancelled = true })
	s.orch.activeRuns.Store("target-ch", cancelFunc)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		AuthorID:    "user1",
		CommandName: "stop",
		Options:     map[string]string{"channel_id": "target-ch"},
	})

	require.True(s.T(), cancelled, "cancel func should have been called for target channel")
	_, loaded := s.orch.activeRuns.Load("target-ch")
	require.False(s.T(), loaded)
}

func (s *OrchestratorSuite) TestHandleMessageSendStopButtonError() {
	s.bot.ExpectedCalls = nil // clear default
	s.bot.On("BotUserID").Return("BOT").Maybe()
	s.bot.On("IsBotUser", mock.Anything).Return(false).Maybe()
	s.bot.On("SendStopButton", mock.Anything, "ch1", "ch1").Return("", errors.New("button failed")).Once()
	s.bot.On("RemoveStopButton", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

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
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hi!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// Verify RemoveStopButton was NOT called (since stopMsgID is "")
	s.bot.AssertNotCalled(s.T(), "RemoveStopButton", mock.Anything, "ch1", "")
}

func (s *OrchestratorSuite) TestHandleMessageRemoveStopButtonError() {
	s.bot.ExpectedCalls = nil // clear default
	s.bot.On("BotUserID").Return("BOT").Maybe()
	s.bot.On("IsBotUser", mock.Anything).Return(false).Maybe()
	s.bot.On("SendStopButton", mock.Anything, "ch1", "ch1").Return("stop-msg-1", nil).Once()
	s.bot.On("RemoveStopButton", mock.Anything, "ch1", "stop-msg-1").Return(errors.New("remove failed")).Once()

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
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hi!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.bot.AssertCalled(s.T(), "RemoveStopButton", mock.Anything, "ch1", "stop-msg-1")
}

func (s *OrchestratorSuite) TestHandleMessageRunCanceledByStopButton() {
	// Set a real timeout so runCtx doesn't expire immediately.
	cfgCT := s.orch.cfg.Load()
	cfgCT.ContainerTimeout = 10 * time.Second
	s.orch.cfg.Store(cfgCT)

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

	// Simulate stop button click during runner execution.
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		// Cancel the runCtx via the activeRuns entry (simulates stop button).
		if val, ok := s.orch.activeRuns.Load("ch1"); ok {
			cancel := val.(context.CancelFunc)
			cancel()
		}
		// Wait for context cancellation to propagate.
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(nil, context.Canceled)

	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "Run stopped." && out.ReplyToMessageID == "msg1"
	})).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.bot.AssertCalled(s.T(), "SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Run stopped."
	}))
}

// --- Readme interaction tests ---

func (s *OrchestratorSuite) TestHandleInteractionReadme() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && len(out.Content) > 0
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "readme",
	})

	s.bot.AssertExpectations(s.T())
}

// --- refreshTyping test ---

func (s *OrchestratorSuite) TestRefreshTypingCancellation() {
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()

	// Use a very short interval so the ticker fires during test
	s.orch.typingInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.orch.refreshTyping(ctx, "ch1")
		close(done)
	}()

	// Wait long enough for initial call + at least one ticker fire
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Expected
	case <-time.After(time.Second):
		s.T().Fatal("refreshTyping should have returned after cancel")
	}

	// Verify SendTyping was called more than once (initial + at least 1 tick)
	require.GreaterOrEqual(s.T(), len(s.bot.Calls), 2)
}

func (s *OrchestratorSuite) TestRefreshTypingTickerError() {
	// First call succeeds, subsequent calls fail
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Once()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(errors.New("typing err"))

	s.orch.typingInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.orch.refreshTyping(ctx, "ch1")
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("refreshTyping should have returned after cancel")
	}
}

// --- buildAgentRequest test ---

func (s *OrchestratorSuite) TestBuildAgentRequest() {
	recent := []*db.Message{
		{ID: 2, AuthorName: "Alice", Content: "new msg", IsBot: false},
		{ID: 1, AuthorName: "Bot", Content: "old response", IsBot: true},
	}
	channel := &db.Channel{
		ChannelID: "ch1",
		SessionID: "sess-data",
		DirPath:   "/home/user/project",
	}

	req := s.orch.buildAgentRequest("ch1", recent, channel)

	require.Equal(s.T(), "sess-data", req.SessionID)
	require.Equal(s.T(), "ch1", req.ChannelID)
	require.Equal(s.T(), "/home/user/project", req.DirPath)
	require.Equal(s.T(), "chat", req.AgentID)
	require.Len(s.T(), req.Messages, 2)
	// Messages should be reversed (oldest first)
	require.Equal(s.T(), "assistant", req.Messages[0].Role)
	require.Equal(s.T(), "user", req.Messages[1].Role)
}

func (s *OrchestratorSuite) TestBuildAgentRequestNilSession() {
	req := s.orch.buildAgentRequest("ch1", nil, nil)

	require.Equal(s.T(), "", req.SessionID)
	require.Equal(s.T(), "", req.DirPath)
	require.Equal(s.T(), "chat", req.AgentID)
	require.Empty(s.T(), req.Messages)
}

func (s *OrchestratorSuite) TestFormatMessageContent() {
	tests := []struct {
		name       string
		authorName string
		content    string
		expected   string
	}{
		{"normal message", "Alice", "hello bot", "Alice: hello bot"},
		{"slash command", "Alice", "/loop 5m check status", "/loop 5m check status"},
		{"slash with spaces", "Bob", "/commit -m fix", "/commit -m fix"},
		{"empty author", "", "hello", ": hello"},
		{"empty content", "Alice", "", "Alice: "},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.expected, formatMessageContent(tt.authorName, tt.content))
		})
	}
}

func (s *OrchestratorSuite) TestBuildAgentRequestSlashCommandNoPrefix() {
	recent := []*db.Message{
		{ID: 2, AuthorName: "Alice", Content: "/loop 5m check", IsBot: false},
		{ID: 1, AuthorName: "Bot", Content: "old response", IsBot: true},
	}
	channel := &db.Channel{ChannelID: "ch1", SessionID: "sess-1"}

	req := s.orch.buildAgentRequest("ch1", recent, channel)

	require.Len(s.T(), req.Messages, 2)
	require.Equal(s.T(), "Bot: old response", req.Messages[0].Content)
	require.Equal(s.T(), "/loop 5m check", req.Messages[1].Content) // no author prefix
}

func (s *OrchestratorSuite) TestFormatDuration() {
	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{"negative", -5 * time.Second, "due now"},
		{"zero", 0, "due now"},
		{"seconds", 45 * time.Second, "in 45s"},
		{"one minute", time.Minute, "in 1m"},
		{"minutes", 15 * time.Minute, "in 15m"},
		{"one hour", time.Hour, "in 1h"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "in 2h30m"},
		{"hours no minutes", 3 * time.Hour, "in 3h"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, formatDuration(tc.d))
		})
	}
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddSuccess() {
	templates := []config.TaskTemplate{
		{Name: "daily-check", Description: "Daily check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.TemplateName == "daily-check" && task.Schedule == "0 9 * * *" && task.Type == db.TaskTypeCron && task.Prompt == "check stuff" && task.AutoDeleteSec == 0
	})).Return(int64(10), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Template 'daily-check' loaded (task ID: 10)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-check"},
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddWithAutoDelete() {
	templates := []config.TaskTemplate{
		{Name: "ephemeral-check", Description: "Ephemeral check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff", AutoDeleteSec: 300},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "ephemeral-check").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.TemplateName == "ephemeral-check" && task.AutoDeleteSec == 300
	})).Return(int64(11), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Template 'ephemeral-check' loaded (task ID: 11)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "ephemeral-check"},
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddIdempotent() {
	templates := []config.TaskTemplate{
		{Name: "daily-check", Description: "Daily check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(&db.ScheduledTask{ID: 5, TemplateName: "daily-check"}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Template 'daily-check' already loaded (task ID: 5)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-check"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddUnknown() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Unknown template: nonexistent"
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "nonexistent"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddStoreError() {
	templates := []config.TaskTemplate{
		{Name: "daily-check", Description: "Daily check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to check existing templates."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-check"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddSchedulerError() {
	templates := []config.TaskTemplate{
		{Name: "daily-check", Description: "Daily check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.Anything).Return(int64(0), errors.New("sched error"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to add template task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-check"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateList() {
	templates := []config.TaskTemplate{
		{Name: "daily-check", Description: "Daily check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff"},
		{Name: "weekly-report", Description: "Weekly report", Schedule: "0 17 * * 5", Type: "cron", Prompt: "generate report"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Available templates:") &&
			strings.Contains(out.Content, "**daily-check**") &&
			strings.Contains(out.Content, "**weekly-report**") &&
			strings.Contains(out.Content, "[cron]") &&
			strings.Contains(out.Content, "`0 9 * * *`") &&
			strings.Contains(out.Content, "Daily check")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-list",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateListEmpty() {
	// s.orch already has nil templates from SetupTest
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "No templates configured."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-list",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddWithPromptPath() {
	tmpDir := s.T().TempDir()

	// Write a template prompt file
	templatesDir := tmpDir + "/templates"
	require.NoError(s.T(), os.MkdirAll(templatesDir, 0755))
	require.NoError(s.T(), os.WriteFile(templatesDir+"/daily.md", []byte("Do daily stuff"), 0644))

	templates := []config.TaskTemplate{
		{Name: "daily-from-file", Description: "Daily from file", Schedule: "0 9 * * *", Type: "cron", PromptPath: "daily.md"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute, LoopDir: tmpDir}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-from-file").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.Prompt == "Do daily stuff" && task.TemplateName == "daily-from-file"
	})).Return(int64(20), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Template 'daily-from-file' loaded (task ID: 20)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-from-file"},
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddResolvePromptError() {
	templates := []config.TaskTemplate{
		{Name: "bad-template", Description: "Bad template", Schedule: "0 9 * * *", Type: "cron"},
		// Neither prompt nor prompt_path set
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "bad-template").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Failed to resolve template prompt")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "bad-template"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddWithWorktreeFields() {
	templates := []config.TaskTemplate{
		{Name: "wt-task", Schedule: "0 * * * *", Type: "cron", Prompt: "do stuff", Worktree: true, OriginBranch: "main", UpdateBeforeRun: true},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute}, nil)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "wt-task").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.Worktree && task.OriginBranch == "main" && task.UpdateBeforeRun
	})).Return(int64(20), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Template 'wt-task' loaded (task ID: 20)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "wt-task"},
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}
