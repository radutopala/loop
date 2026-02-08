package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- Mocks ---

type MockStore struct {
	mock.Mock
}

func (m *MockStore) UpsertChannel(ctx context.Context, ch *db.Channel) error {
	args := m.Called(ctx, ch)
	return args.Error(0)
}

func (m *MockStore) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) GetChannelByDirPath(ctx context.Context, dirPath string) (*db.Channel, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) SetChannelActive(ctx context.Context, channelID string, active bool) error {
	args := m.Called(ctx, channelID, active)
	return args.Error(0)
}

func (m *MockStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	args := m.Called(ctx, channelID, sessionID)
	return args.Error(0)
}

func (m *MockStore) GetRegisteredChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *MockStore) InsertMessage(ctx context.Context, msg *db.Message) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func (m *MockStore) GetUnprocessedMessages(ctx context.Context, channelID string) ([]*db.Message, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	args := m.Called(ctx, ids)
	return args.Error(0)
}

func (m *MockStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) CreateScheduledTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	args := m.Called(ctx, task)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) GetDueTasks(ctx context.Context, now time.Time) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) UpdateScheduledTask(ctx context.Context, task *db.ScheduledTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
}

func (m *MockStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error {
	return m.Called(ctx, id, enabled).Error(0)
}

func (m *MockStore) GetScheduledTask(ctx context.Context, id int64) (*db.ScheduledTask, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) InsertTaskRunLog(ctx context.Context, trl *db.TaskRunLog) (int64, error) {
	args := m.Called(ctx, trl)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) UpdateTaskRunLog(ctx context.Context, trl *db.TaskRunLog) error {
	args := m.Called(ctx, trl)
	return args.Error(0)
}

func (m *MockStore) Close() error {
	args := m.Called()
	return args.Error(0)
}

type MockBot struct {
	mock.Mock
}

func (m *MockBot) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockBot) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockBot) SendMessage(ctx context.Context, msg *OutgoingMessage) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func (m *MockBot) SendTyping(ctx context.Context, channelID string) error {
	args := m.Called(ctx, channelID)
	return args.Error(0)
}

func (m *MockBot) RegisterCommands(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockBot) RemoveCommands(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockBot) OnMessage(handler func(ctx context.Context, msg *IncomingMessage)) {
	m.Called(handler)
}

func (m *MockBot) OnInteraction(handler func(ctx context.Context, i any)) {
	m.Called(handler)
}

func (m *MockBot) BotUserID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockBot) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	args := m.Called(ctx, guildID, name)
	return args.String(0), args.Error(1)
}

type MockRunner struct {
	mock.Mock
}

func (m *MockRunner) Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*agent.AgentResponse), args.Error(1)
}

func (m *MockRunner) Cleanup(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

type MockScheduler struct {
	mock.Mock
}

func (m *MockScheduler) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockScheduler) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockScheduler) AddTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	args := m.Called(ctx, task)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockScheduler) RemoveTask(ctx context.Context, taskID int64) error {
	args := m.Called(ctx, taskID)
	return args.Error(0)
}

func (m *MockScheduler) ListTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockScheduler) SetTaskEnabled(ctx context.Context, taskID int64, enabled bool) error {
	return m.Called(ctx, taskID, enabled).Error(0)
}

func (m *MockScheduler) ToggleTask(ctx context.Context, taskID int64) (bool, error) {
	args := m.Called(ctx, taskID)
	return args.Bool(0), args.Error(1)
}

func (m *MockScheduler) EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string) error {
	return m.Called(ctx, taskID, schedule, taskType, prompt).Error(0)
}

// --- Test Suite ---

type OrchestratorSuite struct {
	suite.Suite
	store     *MockStore
	bot       *MockBot
	runner    *MockRunner
	scheduler *MockScheduler
	orch      *Orchestrator
	ctx       context.Context
}

func TestOrchestratorSuite(t *testing.T) {
	suite.Run(t, new(OrchestratorSuite))
}

func (s *OrchestratorSuite) SetupTest() {
	s.store = new(MockStore)
	s.bot = new(MockBot)
	s.runner = new(MockRunner)
	s.scheduler = new(MockScheduler)
	s.ctx = context.Background()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger)
}

func (s *OrchestratorSuite) TestNew() {
	require.NotNil(s.T(), s.orch)
	require.NotNil(s.T(), s.orch.queue)
}

// --- Start tests ---

func (s *OrchestratorSuite) TestStartSuccess() {
	s.bot.On("OnMessage", mock.AnythingOfType("func(context.Context, *orchestrator.IncomingMessage)")).Return()
	s.bot.On("OnInteraction", mock.AnythingOfType("func(context.Context, interface {})")).Return()
	s.bot.On("RegisterCommands", s.ctx).Return(nil)
	s.bot.On("Start", s.ctx).Return(nil)
	s.scheduler.On("Start", s.ctx).Return(nil)

	err := s.orch.Start(s.ctx)
	require.NoError(s.T(), err)
	s.bot.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestStartRegisterCommandsError() {
	s.bot.On("OnMessage", mock.Anything).Return()
	s.bot.On("OnInteraction", mock.Anything).Return()
	s.bot.On("RegisterCommands", s.ctx).Return(errors.New("register failed"))

	err := s.orch.Start(s.ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "registering commands")
}

func (s *OrchestratorSuite) TestStartBotError() {
	s.bot.On("OnMessage", mock.Anything).Return()
	s.bot.On("OnInteraction", mock.Anything).Return()
	s.bot.On("RegisterCommands", s.ctx).Return(nil)
	s.bot.On("Start", s.ctx).Return(errors.New("bot failed"))

	err := s.orch.Start(s.ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting bot")
}

func (s *OrchestratorSuite) TestStartSchedulerError() {
	s.bot.On("OnMessage", mock.Anything).Return()
	s.bot.On("OnInteraction", mock.Anything).Return()
	s.bot.On("RegisterCommands", s.ctx).Return(nil)
	s.bot.On("Start", s.ctx).Return(nil)
	s.scheduler.On("Start", s.ctx).Return(errors.New("scheduler failed"))

	err := s.orch.Start(s.ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting scheduler")
}

// --- Stop tests ---

func (s *OrchestratorSuite) TestStopSuccess() {
	s.scheduler.On("Stop").Return(nil)
	s.bot.On("Stop").Return(nil)
	s.runner.On("Cleanup", mock.Anything).Return(nil)

	err := s.orch.Stop()
	require.NoError(s.T(), err)
}

func (s *OrchestratorSuite) TestStopWithErrors() {
	s.scheduler.On("Stop").Return(errors.New("sched err"))
	s.bot.On("Stop").Return(errors.New("bot err"))
	s.runner.On("Cleanup", mock.Anything).Return(errors.New("runner err"))

	err := s.orch.Stop()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "scheduler")
	require.Contains(s.T(), err.Error(), "bot")
	require.Contains(s.T(), err.Error(), "runner cleanup")
}

// --- HandleMessage tests ---

func (s *OrchestratorSuite) TestHandleMessageUnregisteredChannel() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
		Content:   "hello",
	})

	s.store.AssertExpectations(s.T())
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageIsChannelActiveError() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, errors.New("db err"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
	})

	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageGetChannelError() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("channel err"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
		GuildID:   "g1",
	})

	s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageGetChannelNil() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
		GuildID:   "g1",
	})

	s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageInsertMessageError() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(errors.New("insert err"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID:    "ch1",
		IsBotMention: true,
	})

	// Should not proceed to triggered flow
	s.store.AssertNotCalled(s.T(), "GetRecentMessages", mock.Anything, mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageNotTriggered() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
		GuildID:   "g1",
		Content:   "just a message",
		// Not triggered: IsBotMention=false, IsReplyToBot=false, HasPrefix=false, IsDM=false
	})

	s.store.AssertNotCalled(s.T(), "GetRecentMessages", mock.Anything, mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageTriggeredFullFlow() {
	msg := &IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello bot",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	recentMsgs := []*db.Message{
		{ID: 2, AuthorName: "Alice", Content: "hello bot", IsBot: false},
		{ID: 1, AuthorName: "Bot", Content: "hi", IsBot: true},
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	// First GetChannel (in HandleMessage)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true, SessionID: "session123"}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return(recentMsgs, nil)
	// Second GetChannel (in processTriggeredMessage for session data) — returns same object
	s.runner.On("Run", s.ctx, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "ch1" && req.SessionID == "session123" && len(req.Messages) == 2
	})).Return(&agent.AgentResponse{
		Response:  "Hello Alice!",
		SessionID: "session456",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "session456").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "Hello Alice!" && out.ReplyToMessageID == "msg1"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{2, 1}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	s.runner.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageTriggeredWithNilSession() {
	msg := &IncomingMessage{
		ChannelID:  "ch1",
		GuildID:    "g1",
		AuthorName: "Alice",
		Content:    "hello",
		MessageID:  "msg1",
		HasPrefix:  true,
		Timestamp:  time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "" && len(req.Messages) == 0
	})).Return(&agent.AgentResponse{
		Response:  "Hi!",
		SessionID: "new-session",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "new-session").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageGetRecentMessagesError() {
	msg := &IncomingMessage{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return(nil, errors.New("db err"))

	s.orch.HandleMessage(s.ctx, msg)

	s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageGetSessionError() {
	msg := &IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	// First GetChannel (in HandleMessage) succeeds
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	// Second GetChannel (for session data in processTriggeredMessage) returns error
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("session err")).Once()

	s.orch.HandleMessage(s.ctx, msg)

	s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageRunnerError() {
	msg := &IncomingMessage{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(nil, errors.New("runner err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Sorry, I encountered an error processing your request."
	})).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageAgentResponseError() {
	msg := &IncomingMessage{
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
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Error: "agent broke",
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Agent error: agent broke"
	})).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.bot.AssertCalled(s.T(), "SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Agent error: agent broke"
	}))
}

func (s *OrchestratorSuite) TestHandleMessageUpdateSessionIDError() {
	msg := &IncomingMessage{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "data",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "data").Return(errors.New("session err"))
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// Should still send message and mark processed
	s.bot.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageSendResponseError() {
	msg := &IncomingMessage{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response: "ok",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(errors.New("send err"))
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageMarkProcessedError() {
	msg := &IncomingMessage{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
		{ID: 5, AuthorName: "Alice", Content: "hello"},
	}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response: "ok",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{5}).Return(errors.New("mark err"))

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageTypingError() {
	msg := &IncomingMessage{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(errors.New("typing err"))
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response: "ok",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// Should still complete despite typing error
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageInsertBotResponseError() {
	msg := &IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	// First GetChannel (in HandleMessage) succeeds
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
	// First InsertMessage (user message) succeeds
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil).Once()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	// Second GetChannel (for session data in processTriggeredMessage) succeeds
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response: "ok",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	// Third GetChannel (for bot response insert) returns error
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("channel err")).Once()
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageInsertBotResponseInsertError() {
	msg := &IncomingMessage{
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
	// First InsertMessage (user message) succeeds
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil).Once()
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response: "ok",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	// Second InsertMessage (bot response) fails
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(errors.New("insert err")).Once()
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// Should still mark messages processed despite bot response insert error
	s.store.AssertExpectations(s.T())
}

// --- HandleInteraction tests ---

func (s *OrchestratorSuite) TestHandleInteractionInvalidType() {
	s.orch.HandleInteraction(s.ctx, "not an interaction")
	// Should not panic, just log error
}

func (s *OrchestratorSuite) TestHandleInteractionUnknownCommand() {
	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "unknown",
	})
	// Should not panic, just log warning
}

func (s *OrchestratorSuite) TestHandleInteractionRegister() {
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.GuildID == "g1" && ch.Active
	})).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Channel registered successfully."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "register",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionRegisterError() {
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to register channel."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "register",
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionUnregister() {
	s.store.On("SetChannelActive", s.ctx, "ch1", false).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Channel unregistered successfully."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "unregister",
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionUnregisterError() {
	s.store.On("SetChannelActive", s.ctx, "ch1", false).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to unregister channel."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "unregister",
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionSchedule() {
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.Prompt == "do stuff" && task.Schedule == "0 * * * *" && task.Type == db.TaskTypeCron
	})).Return(int64(42), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task scheduled (ID: 42)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "schedule",
		Options: map[string]string{
			"schedule": "0 * * * *",
			"prompt":   "do stuff",
			"type":     "cron",
		},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionScheduleInterval() {
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.Type == db.TaskTypeInterval && task.Schedule == "5m"
	})).Return(int64(43), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task scheduled (ID: 43)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "schedule",
		Options: map[string]string{
			"schedule": "5m",
			"prompt":   "ping",
			"type":     "interval",
		},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionScheduleError() {
	s.scheduler.On("AddTask", s.ctx, mock.Anything).Return(int64(0), errors.New("sched err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to schedule task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "schedule",
		Options: map[string]string{
			"schedule": "0 * * * *",
			"prompt":   "do stuff",
			"type":     "cron",
		},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasks() {
	nextRun := time.Now().Add(30 * time.Minute)
	tasks := []*db.ScheduledTask{
		{ID: 1, Prompt: "task1", Schedule: "0 * * * *", Type: db.TaskTypeCron, Enabled: true, NextRunAt: nextRun},
		{ID: 2, Prompt: "task2", Schedule: "5m", Type: db.TaskTypeInterval, Enabled: false, NextRunAt: nextRun.Add(5 * time.Minute)},
		{ID: 3, Prompt: "task3", Schedule: "10m", Type: db.TaskTypeOnce, Enabled: true, NextRunAt: nextRun.Add(10 * time.Minute)},
	}
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return(tasks, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "Scheduled tasks:") &&
			strings.Contains(out.Content, "ID 1") &&
			strings.Contains(out.Content, "[cron]") &&
			strings.Contains(out.Content, "[enabled]") &&
			strings.Contains(out.Content, "`0 * * * *`") &&
			strings.Contains(out.Content, "task1") &&
			strings.Contains(out.Content, "[disabled]") &&
			strings.Contains(out.Content, "`5m`") &&
			strings.Contains(out.Content, "[once]") &&
			strings.Contains(out.Content, "UTC") &&
			!strings.Contains(out.Content, "`10m`") &&
			strings.Contains(out.Content, "next: in ")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasksEmpty() {
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasksError() {
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return(nil, errors.New("list err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to list tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionCancel() {
	s.scheduler.On("RemoveTask", s.ctx, int64(42)).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task 42 cancelled."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "cancel",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionCancelInvalidID() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "cancel",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionCancelError() {
	s.scheduler.On("RemoveTask", s.ctx, int64(42)).Return(errors.New("remove err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to cancel task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "cancel",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleEnable() {
	s.scheduler.On("ToggleTask", s.ctx, int64(42)).Return(true, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task 42 enabled."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleDisable() {
	s.scheduler.On("ToggleTask", s.ctx, int64(42)).Return(false, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task 42 disabled."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleInvalidID() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleError() {
	s.scheduler.On("ToggleTask", s.ctx, int64(42)).Return(false, errors.New("toggle err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to toggle task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditSuccess() {
	schedule := "0 9 * * *"
	s.scheduler.On("EditTask", s.ctx, int64(42), &schedule, (*string)(nil), (*string)(nil)).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task 42 updated."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "42", "schedule": "0 9 * * *"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditWithTypeAndPrompt() {
	taskType := "interval"
	prompt := "new prompt"
	s.scheduler.On("EditTask", s.ctx, int64(10), (*string)(nil), &taskType, &prompt).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Task 10 updated."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "10", "type": "interval", "prompt": "new prompt"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditInvalidID() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditNoFields() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "At least one of schedule, type, or prompt is required."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "42"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditError() {
	prompt := "new"
	s.scheduler.On("EditTask", s.ctx, int64(42), (*string)(nil), (*string)(nil), &prompt).Return(errors.New("edit err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to edit task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "42", "prompt": "new"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionStatus() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Loop bot is running."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "status",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAsk() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", s.ctx, mock.Anything).Return(&agent.AgentResponse{
		Response: "answer",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "ask",
		Options:     map[string]string{"prompt": "what is Go?"},
	})

	s.runner.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAskEmptyPrompt() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Please provide a prompt."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "ask",
		Options:     map[string]string{},
	})

	s.bot.AssertExpectations(s.T())
}

// --- refreshTyping test ---

func (s *OrchestratorSuite) TestRefreshTypingCancellation() {
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil)

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
	require.Len(s.T(), req.Messages, 2)
	// Messages should be reversed (oldest first)
	require.Equal(s.T(), "assistant", req.Messages[0].Role)
	require.Equal(s.T(), "user", req.Messages[1].Role)
}

func (s *OrchestratorSuite) TestBuildAgentRequestNilSession() {
	req := s.orch.buildAgentRequest("ch1", nil, nil)

	require.Equal(s.T(), "", req.SessionID)
	require.Equal(s.T(), "", req.DirPath)
	require.Empty(s.T(), req.Messages)
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
