package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
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

func (m *MockStore) GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*db.Channel, error) {
	args := m.Called(ctx, dirPath, platform)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	args := m.Called(ctx, channelID, sessionID)
	return args.Error(0)
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

func (m *MockStore) DeleteChannel(ctx context.Context, channelID string) error {
	args := m.Called(ctx, channelID)
	return args.Error(0)
}

func (m *MockStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	args := m.Called(ctx, parentID)
	return args.Error(0)
}

func (m *MockStore) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockStore) GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID, templateName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *MockStore) UpdateChannelPermissions(ctx context.Context, channelID string, perms db.ChannelPermissions) error {
	return m.Called(ctx, channelID, perms).Error(0)
}

func (m *MockStore) UpsertMemoryFile(ctx context.Context, file *db.MemoryFile) error {
	return m.Called(ctx, file).Error(0)
}

func (m *MockStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*db.MemoryFile, error) {
	args := m.Called(ctx, dirPath)
	return args.Get(0).([]*db.MemoryFile), args.Error(1)
}

func (m *MockStore) GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error) {
	args := m.Called(ctx, filePath, dirPath)
	return args.String(0), args.Error(1)
}

func (m *MockStore) DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error {
	return m.Called(ctx, filePath, dirPath).Error(0)
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

func (m *MockBot) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	m.Called(handler)
}

func (m *MockBot) OnChannelJoin(handler func(ctx context.Context, channelID string)) {
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

func (m *MockBot) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	return m.Called(ctx, channelID, userID).Error(0)
}

func (m *MockBot) GetOwnerUserID(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
}

func (m *MockBot) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	return m.Called(ctx, channelID, topic).Error(0)
}

func (m *MockBot) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	args := m.Called(ctx, channelID, name, mentionUserID, message)
	return args.String(0), args.Error(1)
}

func (m *MockBot) DeleteThread(ctx context.Context, threadID string) error {
	return m.Called(ctx, threadID).Error(0)
}

func (m *MockBot) PostMessage(ctx context.Context, channelID, content string) error {
	return m.Called(ctx, channelID, content).Error(0)
}

func (m *MockBot) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

func (m *MockBot) GetChannelName(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

func (m *MockBot) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	args := m.Called(ctx, channelID, name, initialMessage)
	return args.String(0), args.Error(1)
}

func (m *MockBot) GetMemberRoles(ctx context.Context, guildID, userID string) ([]string, error) {
	args := m.Called(ctx, guildID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{})
}

func (s *OrchestratorSuite) TestNew() {
	require.NotNil(s.T(), s.orch)
	require.NotNil(s.T(), s.orch.queue)
}

// --- Start tests ---

func (s *OrchestratorSuite) TestStartSuccess() {
	s.bot.On("OnMessage", mock.AnythingOfType("func(context.Context, *orchestrator.IncomingMessage)")).Return()
	s.bot.On("OnInteraction", mock.AnythingOfType("func(context.Context, interface {})")).Return()
	s.bot.On("OnChannelDelete", mock.AnythingOfType("func(context.Context, string, bool)")).Return()
	s.bot.On("OnChannelJoin", mock.AnythingOfType("func(context.Context, string)")).Return()
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
	s.bot.On("OnChannelDelete", mock.Anything).Return()
	s.bot.On("OnChannelJoin", mock.Anything).Return()
	s.bot.On("RegisterCommands", s.ctx).Return(errors.New("register failed"))

	err := s.orch.Start(s.ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "registering commands")
}

func (s *OrchestratorSuite) TestStartBotError() {
	s.bot.On("OnMessage", mock.Anything).Return()
	s.bot.On("OnInteraction", mock.Anything).Return()
	s.bot.On("OnChannelDelete", mock.Anything).Return()
	s.bot.On("OnChannelJoin", mock.Anything).Return()
	s.bot.On("RegisterCommands", s.ctx).Return(nil)
	s.bot.On("Start", s.ctx).Return(errors.New("bot failed"))

	err := s.orch.Start(s.ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting bot")
}

func (s *OrchestratorSuite) TestStartSchedulerError() {
	s.bot.On("OnMessage", mock.Anything).Return()
	s.bot.On("OnInteraction", mock.Anything).Return()
	s.bot.On("OnChannelDelete", mock.Anything).Return()
	s.bot.On("OnChannelJoin", mock.Anything).Return()
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
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
		Content:   "hello",
	})

	s.store.AssertExpectations(s.T())
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadResolved() {
	msg := &IncomingMessage{
		ChannelID:    "thread1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello in thread",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	// Thread is not directly active
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil).Once()
	// Resolve thread: parent found
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	// Parent is active
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	// Get parent channel for inheritance
	parentPerms := db.ChannelPermissions{
		Owners:  db.ChannelRoleGrant{Users: []string{"user1"}},
		Members: db.ChannelRoleGrant{Users: []string{"member1"}},
	}
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", GuildID: "g1", DirPath: "/project", Platform: types.PlatformDiscord, SessionID: "sess-parent", Permissions: parentPerms, Active: true,
	}, nil)
	// Upsert thread channel with dir_path, platform, session_id, and permissions inherited from parent
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread1" && ch.ParentID == "ch1" && ch.GuildID == "g1" &&
			ch.DirPath == "/project" && ch.Platform == "discord" && ch.SessionID == "sess-parent" &&
			len(ch.Permissions.Owners.Users) == 1 && ch.Permissions.Owners.Users[0] == "user1" &&
			len(ch.Permissions.Members.Users) == 1 && ch.Permissions.Members.Users[0] == "member1" &&
			ch.Active
	})).Return(nil)
	// Now the thread is a channel — normal flow continues
	s.store.On("GetChannel", s.ctx, "thread1").Return(&db.Channel{
		ID: 2, ChannelID: "thread1", GuildID: "g1", DirPath: "/project", ParentID: "ch1", SessionID: "sess-parent", Permissions: parentPerms, Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "thread1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "thread1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ForkSession && req.SessionID == "sess-parent"
	})).Return(&agent.AgentResponse{
		Response:  "Hi from thread!",
		SessionID: "sess-forked",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread1", "sess-forked").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.ChannelID == "thread1" && out.Content == "Hi from thread!"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageThreadAlreadyUpserted() {
	// Second message in a thread — thread is already in DB with dir_path
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "thread1").Return(&db.Channel{
		ID: 2, ChannelID: "thread1", GuildID: "g1", DirPath: "/project", ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		GuildID:   "g1",
		Content:   "just context",
	})

	// No GetChannelParentID call — thread was already active
	s.bot.AssertNotCalled(s.T(), "GetChannelParentID", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadInactiveParent() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	// Should not upsert or proceed
	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadParentIDError() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("", errors.New("api error"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadParentActiveCheckError() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, errors.New("db error"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadGetParentChannelError() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db error"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadGetParentChannelNil() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadUpsertError() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", GuildID: "g1", DirPath: "/project", Active: true,
	}, nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("upsert error"))

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	// Should not proceed to GetChannel for thread
	s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageDMAutoCreatesChannel() {
	msg := &IncomingMessage{
		ChannelID:  "dm-ch1",
		GuildID:    "",
		AuthorID:   "user1",
		AuthorName: "Alice",
		Content:    "hello",
		MessageID:  "msg1",
		IsDM:       true,
		Timestamp:  time.Now().UTC(),
	}

	// Channel is not active
	s.store.On("IsChannelActive", s.ctx, "dm-ch1").Return(false, nil)
	// Not a thread
	s.bot.On("GetChannelParentID", s.ctx, "dm-ch1").Return("", nil)
	// Auto-create DM channel
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "dm-ch1" && ch.Name == "DM" && ch.Platform == "discord" && ch.Active
	})).Return(nil)
	// Normal flow continues
	s.store.On("GetChannel", s.ctx, "dm-ch1").Return(&db.Channel{
		ID: 1, ChannelID: "dm-ch1", Active: true, Platform: types.PlatformDiscord,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "dm-ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "dm-ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hello from DM!",
		SessionID: "sess-dm",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "dm-ch1", "sess-dm").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.ChannelID == "dm-ch1" && out.Content == "Hello from DM!"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageDMAutoCreateFails() {
	msg := &IncomingMessage{
		ChannelID: "dm-ch1",
		GuildID:   "",
		Content:   "hello",
		IsDM:      true,
	}

	s.store.On("IsChannelActive", s.ctx, "dm-ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "dm-ch1").Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("upsert error"))

	s.orch.HandleMessage(s.ctx, msg)

	// Should not proceed
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageNonDMUnregisteredChannelDropped() {
	// Non-triggered message to an unregistered channel (not a thread either) should be dropped
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)

	s.orch.HandleMessage(s.ctx, &IncomingMessage{
		ChannelID: "ch1",
		Content:   "hello",
		// No trigger flags set
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageMentionAutoCreatesChannel() {
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

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("general", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.GuildID == "g1" && ch.Name == "general" && ch.Platform == "discord" && ch.Active
	})).Return(nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true, Platform: types.PlatformDiscord,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hello!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "Hello!"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageMentionAutoCreateFails() {
	msg := &IncomingMessage{
		ChannelID:    "ch1",
		Content:      "hello bot",
		IsBotMention: true,
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("general", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("upsert error"))

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessagePrefixAutoCreatesChannel() {
	msg := &IncomingMessage{
		ChannelID:  "ch1",
		AuthorID:   "user1",
		AuthorName: "Alice",
		Content:    "do something",
		MessageID:  "msg1",
		HasPrefix:  true,
		Timestamp:  time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("dev-ops", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.Name == "dev-ops" && ch.Active
	})).Return(nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true, Platform: types.PlatformDiscord,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Done!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

// --- HandleChannelJoin tests ---

func (s *OrchestratorSuite) TestHandleChannelJoinSuccess() {
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("project-x", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.Name == "project-x" && ch.Platform == "discord" && ch.Active
	})).Return(nil)

	s.orch.HandleChannelJoin(s.ctx, "ch1")

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelJoinNameLookupFails() {
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("", errors.New("api error"))
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.Name == "channel" && ch.Platform == "discord" && ch.Active
	})).Return(nil)

	s.orch.HandleChannelJoin(s.ctx, "ch1")

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelJoinEmptyName() {
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.Name == "channel" && ch.Active
	})).Return(nil)

	s.orch.HandleChannelJoin(s.ctx, "ch1")

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelJoinUpsertError() {
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("project-x", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("db error"))

	s.orch.HandleChannelJoin(s.ctx, "ch1")

	s.store.AssertExpectations(s.T())
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return(recentMsgs, nil)
	// Second GetChannel (in processTriggeredMessage for session data) — returns same object
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "ch1" && req.SessionID == "session123" && len(req.Messages) == 2 && req.Prompt == "Alice: hello bot"
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "" && len(req.Messages) == 0 && req.Prompt == "Alice: hello"
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner err"))
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
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
		{ID: 5, AuthorName: "Alice", Content: "hello"},
	}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	// Second GetChannel (for session data in processTriggeredMessage) succeeds
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "unknown",
	})
	// Should not panic, just log warning
}

func (s *OrchestratorSuite) TestHandleInteractionSchedule() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
			strings.Contains(out.Content, nextRun.Add(10*time.Minute).Local().Format("2006-01-02 15:04 MST")) &&
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("EditTask", s.ctx, int64(42), new("0 9 * * *"), (*string)(nil), (*string)(nil)).Return(nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("EditTask", s.ctx, int64(10), (*string)(nil), new("interval"), new("new prompt")).Return(nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("EditTask", s.ctx, int64(42), (*string)(nil), (*string)(nil), new("new")).Return(errors.New("edit err"))
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
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Loop bot is running."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "status",
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

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddSuccess() {
	templates := []config.TaskTemplate{
		{Name: "daily-check", Description: "Daily check", Schedule: "0 9 * * *", Type: "cron", Prompt: "check stuff"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.TemplateName == "daily-check" && task.Schedule == "0 9 * * *" && task.Type == db.TaskTypeCron && task.Prompt == "check stuff"
	})).Return(int64(10), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Template 'daily-check' loaded (task ID: 10)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-check"},
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(&db.ScheduledTask{ID: 5, TemplateName: "daily-check"}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Template 'daily-check' already loaded (task ID: 5)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "daily-check"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateAddUnknown() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Unknown template: nonexistent"
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to check existing templates."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-check").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.Anything).Return(int64(0), errors.New("sched error"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to add template task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return strings.Contains(out.Content, "Available templates:") &&
			strings.Contains(out.Content, "**daily-check**") &&
			strings.Contains(out.Content, "**weekly-report**") &&
			strings.Contains(out.Content, "[cron]") &&
			strings.Contains(out.Content, "`0 9 * * *`") &&
			strings.Contains(out.Content, "Daily check")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "template-list",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTemplateListEmpty() {
	// s.orch already has nil templates from SetupTest
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "No templates configured."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute, LoopDir: tmpDir})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily-from-file").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.Prompt == "Do daily stuff" && task.TemplateName == "daily-from-file"
	})).Return(int64(20), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Template 'daily-from-file' loaded (task ID: 20)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
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
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, types.PlatformDiscord, config.Config{TaskTemplates: templates, ContainerTimeout: 5 * time.Minute})

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "bad-template").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return strings.Contains(out.Content, "Failed to resolve template prompt")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "template-add",
		Options:     map[string]string{"name": "bad-template"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- HandleChannelDelete tests ---

func (s *OrchestratorSuite) TestHandleChannelDeleteThread() {
	s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)

	s.orch.HandleChannelDelete(s.ctx, "thread-1", true)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelDeleteThreadError() {
	s.store.On("DeleteChannel", s.ctx, "thread-1").Return(errors.New("db error"))

	s.orch.HandleChannelDelete(s.ctx, "thread-1", true)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelDeleteChannel() {
	s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
	s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)

	s.orch.HandleChannelDelete(s.ctx, "ch-1", false)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelDeleteChannelChildrenError() {
	s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(errors.New("db error"))
	s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)

	s.orch.HandleChannelDelete(s.ctx, "ch-1", false)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleChannelDeleteChannelError() {
	s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
	s.store.On("DeleteChannel", s.ctx, "ch-1").Return(errors.New("db error"))

	s.orch.HandleChannelDelete(s.ctx, "ch-1", false)
	s.store.AssertExpectations(s.T())
}

// --- Permissions tests ---

func (s *OrchestratorSuite) TestConfigPermissionsForEmptyConfig() {
	// Default zero-value config → empty permissions.
	s.orch.cfg = config.Config{}
	perms := s.orch.configPermissionsFor("")
	require.True(s.T(), perms.IsEmpty())
}

func (s *OrchestratorSuite) TestConfigPermissionsForGlobalNoProjectConfig() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
	}
	// Empty dirPath → global permissions returned directly.
	perms := s.orch.configPermissionsFor("")
	require.Equal(s.T(), []string{"U1"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestConfigPermissionsForWithDirPath() {
	orig := config.TestSetReadFile(func(path string) ([]byte, error) {
		return nil, os.ErrNotExist
	})
	defer config.TestSetReadFile(orig)

	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
	}
	// Non-existent project config → LoadProjectConfig returns global config → global permissions returned.
	perms := s.orch.configPermissionsFor("/some/project")
	require.Equal(s.T(), []string{"U1"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestConfigPermissionsForLoadError() {
	orig := config.TestSetReadFile(func(path string) ([]byte, error) {
		return nil, errors.New("permission denied")
	})
	defer config.TestSetReadFile(orig)

	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
	}
	// Read error (not ErrNotExist) → LoadProjectConfig returns error → global permissions returned as fallback.
	perms := s.orch.configPermissionsFor("/some/project")
	require.Equal(s.T(), []string{"U1"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestConfigPermissionsForProjectOverridesGlobal() {
	orig := config.TestSetReadFile(func(path string) ([]byte, error) {
		if path == "/project/.loop/config.json" {
			return []byte(`{"permissions":{"owners":{"users":["U2"]}}}`), nil
		}
		return nil, os.ErrNotExist
	})
	defer config.TestSetReadFile(orig)

	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
	}
	perms := s.orch.configPermissionsFor("/project")
	require.Equal(s.T(), []string{"U2"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestResolveRole() {
	tests := []struct {
		name        string
		cfgPerms    config.PermissionsConfig
		dbPerms     db.ChannelPermissions
		authorID    string
		authorRoles []string
		expected    types.Role
	}{
		{
			name:     "bootstrap: both empty → owner",
			cfgPerms: config.PermissionsConfig{},
			dbPerms:  db.ChannelPermissions{},
			expected: types.RoleOwner,
		},
		{
			name:     "cfg owner only",
			cfgPerms: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  db.ChannelPermissions{},
			authorID: "U1",
			expected: types.RoleOwner,
		},
		{
			name:     "db owner only",
			cfgPerms: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  db.ChannelPermissions{Owners: db.ChannelRoleGrant{Users: []string{"U2"}}},
			authorID: "U2",
			expected: types.RoleOwner,
		},
		{
			name:     "cfg member only",
			cfgPerms: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}, Members: config.RoleGrant{Users: []string{"U2"}}},
			dbPerms:  db.ChannelPermissions{},
			authorID: "U2",
			expected: types.RoleMember,
		},
		{
			name:     "db member wins when cfg empty",
			cfgPerms: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  db.ChannelPermissions{Members: db.ChannelRoleGrant{Users: []string{"U3"}}},
			authorID: "U3",
			expected: types.RoleMember,
		},
		{
			name:     "denied when not in any list",
			cfgPerms: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  db.ChannelPermissions{},
			authorID: "U99",
			expected: "",
		},
		{
			name:        "cfg owner by role",
			cfgPerms:    config.PermissionsConfig{Owners: config.RoleGrant{Roles: []string{"admin"}}},
			dbPerms:     db.ChannelPermissions{},
			authorID:    "U5",
			authorRoles: []string{"admin"},
			expected:    types.RoleOwner,
		},
		{
			name:     "db owner beats cfg member",
			cfgPerms: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"U1"}}, Members: config.RoleGrant{Users: []string{"U2"}}},
			dbPerms:  db.ChannelPermissions{Owners: db.ChannelRoleGrant{Users: []string{"U2"}}},
			authorID: "U2",
			expected: types.RoleOwner,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			role := resolveRole(tc.cfgPerms, tc.dbPerms, tc.authorID, tc.authorRoles)
			require.Equal(s.T(), tc.expected, role)
		})
	}
}

func (s *OrchestratorSuite) TestHandleMessagePermissionDenied() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"allowed-user"}}},
	}

	msg := &IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "denied-user",
		Content:      "hello bot",
		IsBotMention: true,
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// No runner call or response sent.
	s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
	s.bot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessagePermissionAllowed() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"allowed-user"}}},
	}

	msg := &IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "allowed-user",
		AuthorName:   "Alice",
		Content:      "hello bot",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response: "Hello!", SessionID: "s1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleInteractionPermissionDenied() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"allowed-user"}}},
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ You don't have permission to use this command."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "denied-user",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	s.scheduler.AssertNotCalled(s.T(), "ListTasks", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleInteractionPermissionAllowed() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"allowed-user"}}},
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "allowed-user",
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionPermissionByRole() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Roles: []string{"admin-role"}}},
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "some-user",
		AuthorRoles: []string{"admin-role"},
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionGetChannelNil() {
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{Owners: config.RoleGrant{Users: []string{"allowed-user"}}},
	}

	// Channel not found — dirPath will be empty, permissions come from global.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ You don't have permission to use this command."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "denied-user",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionPermCmdRequiresOwner() {
	// Member user cannot manage permissions.
	s.orch.cfg = config.Config{
		Permissions: config.PermissionsConfig{
			Owners:  config.RoleGrant{Users: []string{"owner-user"}},
			Members: config.RoleGrant{Users: []string{"member-user"}},
		},
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ Only owners can manage permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		AuthorID:    "member-user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", db.ChannelPermissions{
		Members: db.ChannelRoleGrant{Users: []string{"U99"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ <@U99> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserOwner() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", db.ChannelPermissions{
		Owners: db.ChannelRoleGrant{Users: []string{"U99"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ <@U99> granted owner role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserChannelNotRegistered() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", db.ChannelPermissions{
		Owners: db.ChannelRoleGrant{Roles: []string{"R1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> granted owner role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyUserSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{
			Owners:  db.ChannelRoleGrant{Users: []string{"owner-user"}},
			Members: db.ChannelRoleGrant{Users: []string{"U99"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.MatchedBy(func(p db.ChannelPermissions) bool {
		// U99 removed from Members; owner-user remains in Owners
		return len(p.Owners.Users) == 1 && p.Owners.Users[0] == "owner-user" &&
			len(p.Members.Users) == 0
	})).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ <@U99> removed from channel permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_user",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyRoleSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{
			Owners: db.ChannelRoleGrant{Users: []string{"owner-user"}, Roles: []string{"R1"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.MatchedBy(func(p db.ChannelPermissions) bool {
		// R1 removed from Owners.Roles; owner-user remains in Owners.Users
		return len(p.Owners.Users) == 1 && p.Owners.Users[0] == "owner-user" &&
			len(p.Owners.Roles) == 0
	})).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> removed from channel permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_role",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestAppendUnique() {
	// Value not present — appends it.
	result := appendUnique([]string{"a", "b"}, "c")
	require.Equal(s.T(), []string{"a", "b", "c"}, result)

	// Value already present — no duplicate added.
	result = appendUnique([]string{"a", "b", "c"}, "b")
	require.Equal(s.T(), []string{"a", "b", "c"}, result)
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserDefaultRole() {
	// When "role" option is absent, it defaults to "member".
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", db.ChannelPermissions{
		Members: db.ChannelRoleGrant{Users: []string{"U99"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ <@U99> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleChannelNotRegistered() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleMember() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", db.ChannelPermissions{
		Members: db.ChannelRoleGrant{Roles: []string{"R1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleDefaultRole() {
	// When "role" option is absent, it defaults to "member".
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", db.ChannelPermissions{
		Members: db.ChannelRoleGrant{Roles: []string{"R1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyUserChannelNotRegistered() {
	// Bootstrap mode (no config perms, no db perms) → everyone is RoleOwner.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyUserStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{
			Owners: db.ChannelRoleGrant{Users: []string{"owner-user"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_user",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyRoleChannelNotRegistered() {
	// Bootstrap mode (no config perms, no db perms) → everyone is RoleOwner.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_role",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyRoleStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: db.ChannelPermissions{
			Owners: db.ChannelRoleGrant{Users: []string{"owner-user"}, Roles: []string{"R1"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_role",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- Streaming tests ---

func (s *OrchestratorSuite) TestHandleMessageStreamingSkipsDuplicate() {
	s.orch.cfg.StreamingEnabled = true

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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
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
	s.orch.cfg.StreamingEnabled = true

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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.ReplyToMessageID == "msg1" &&
			(out.Content == "Intermediate turn" || out.Content == "Final different response")
	})).Return(nil).Twice()

	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// 2 SendMessage (1 OnTurn + 1 final)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageStreamingDisabledNoOnTurn() {
	// streamingEnabled is false by default (set in SetupTest)
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

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		// OnTurn should NOT be set when streaming is disabled
		return req.OnTurn == nil
	})).Return(&agent.AgentResponse{
		Response:  "Hello!",
		SessionID: "sess-3",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess-3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *OutgoingMessage) bool {
		return out.Content == "Hello!" && out.ReplyToMessageID == "msg1"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// Only 1 SendMessage call (the final response)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.store.AssertExpectations(s.T())
	s.runner.AssertExpectations(s.T())
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"truncated with ellipsis", "hello world", 8, "hello..."},
		{"maxLen 3 gives ellipsis only", "hello", 3, "hel"},
		{"maxLen 2 no ellipsis", "hello", 2, "he"},
		{"maxLen 1", "hello", 1, "h"},
		{"maxLen 4 truncates", "hello world", 4, "h..."},
		{"empty string", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxLen)
			require.Equal(t, tt.want, got)
		})
	}
}
