package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

// --- Mocks ---

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

func (m *MockBot) SendMessage(ctx context.Context, msg *bot.OutgoingMessage) error {
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

func (m *MockBot) OnMessage(handler func(ctx context.Context, msg *bot.IncomingMessage)) {
	m.Called(handler)
}

func (m *MockBot) OnInteraction(handler func(ctx context.Context, i *bot.Interaction)) {
	m.Called(handler)
}

func (m *MockBot) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	m.Called(handler)
}

func (m *MockBot) OnChannelJoin(handler func(ctx context.Context, channelID string, platform types.Platform)) {
	m.Called(handler)
}

func (m *MockBot) BotUserID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockBot) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	return m.Called(ctx, channelID, userID).Error(0)
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

func (m *MockBot) RenameThread(ctx context.Context, threadID, name string) error {
	return m.Called(ctx, threadID, name).Error(0)
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

func (m *MockBot) HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string) {
	m.Called(ctx, channelID, authorID, content, mode)
}

func (m *MockBot) HandleIncomingMessageWithPriority(ctx context.Context, channelID, authorID, content, mode string, priority int) {
	m.Called(ctx, channelID, authorID, content, mode, priority)
}

func (m *MockBot) HandleThreadCreated(ctx context.Context, threadID, authorID, message string) {
	m.Called(ctx, threadID, authorID, message)
}

func (m *MockBot) IsBotUser(userID string) bool {
	args := m.Called(userID)
	return args.Bool(0)
}

func (m *MockBot) SendStopButton(ctx context.Context, channelID, runID string) (string, error) {
	args := m.Called(ctx, channelID, runID)
	return args.String(0), args.Error(1)
}

func (m *MockBot) RemoveStopButton(ctx context.Context, channelID, messageID string) error {
	return m.Called(ctx, channelID, messageID).Error(0)
}

func (m *MockBot) SendApproval(ctx context.Context, channelID string, prompt bot.ApprovalPrompt) (string, error) {
	args := m.Called(ctx, channelID, prompt)
	return args.String(0), args.Error(1)
}

func (m *MockBot) RemoveApproval(ctx context.Context, channelID, messageID string) error {
	return m.Called(ctx, channelID, messageID).Error(0)
}

type MockEventBroadcaster struct {
	mock.Mock
}

func (m *MockEventBroadcaster) BroadcastMessageCreated(channelID string, data events.MessageEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastMessagesProcessed(channelID string, data events.MessagesProcessedData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastMessageDeleted(channelID string, data events.MessageDeletedData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastMessageStreaming(channelID string, data events.MessageStreamingData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAgentStatus(channelID string, data events.AgentStatusEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastToolUse(channelID string, data events.ToolUseEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAgentThinking(channelID string, data events.AgentThinkingEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastToolResult(channelID string, data events.ToolResultEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAgentActivity(channelID string, data events.AgentActivityEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAskUser(channelID string, data events.AskUserQuestionEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastExitPlan(channelID string, data events.ExitPlanModeEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAgentTasks(channelID string, data events.AgentTasksEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastChannelCreated(parentChannelID, channelID string) {
	m.Called(parentChannelID, channelID)
}

func (m *MockEventBroadcaster) BroadcastChannelDeleted(channelID string) {
	m.Called(channelID)
}

func (m *MockEventBroadcaster) BroadcastAgentInstanceRegistered(channelID string, data events.AgentInstanceEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAgentInstanceUnregistered(channelID string, data events.AgentInstanceEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastAgentInstanceMetadata(channelID string, data events.AgentInstanceEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastImageBuildStatus(data events.ImageBuildStatusData) {
	m.Called(data)
}

func (m *MockEventBroadcaster) BroadcastImageUpdateAvailable(data events.ImageUpdateAvailableData) {
	m.Called(data)
}

func (m *MockEventBroadcaster) BroadcastGateApprovalRequested(channelID string, data events.GateApprovalEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastGateApprovalResolved(channelID string, data events.GateApprovalResolvedData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastReviewComment(channelID string, data events.ReviewCommentEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastReviewStatus(channelID string, data events.ReviewStatusEventData) {
	m.Called(channelID, data)
}

func (m *MockEventBroadcaster) BroadcastReviewDiff(channelID string, data events.ReviewDiffEventData) {
	m.Called(channelID, data)
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

// --- Test Suite ---

type OrchestratorSuite struct {
	suite.Suite
	store     *testutil.MockStore
	bot       *MockBot
	runner    *MockRunner
	scheduler *testutil.MockScheduler
	orch      *Orchestrator
	ctx       context.Context

	// pendingMu guards the default ClaimNextPending mock's introspection of
	// past InsertMessage calls. claimed dedupes which msg_ids have been
	// served — drainChannel calls ClaimNextPending repeatedly until nil, so
	// without dedupe it would loop forever on the same row.
	pendingMu sync.Mutex
	pending   map[string][]*db.Message
	claimed   map[string]bool
	nextRowID int64
}

func TestOrchestratorSuite(t *testing.T) {
	suite.Run(t, new(OrchestratorSuite))
}

func (s *OrchestratorSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.bot = new(MockBot)
	s.runner = new(MockRunner)
	s.scheduler = new(testutil.MockScheduler)
	s.ctx = context.Background()
	s.pending = make(map[string][]*db.Message)
	s.nextRowID = 0

	// Default expectations for stop button (non-fatal, called during processClaimedMessage)
	s.bot.On("BotUserID").Return("BOT").Maybe()
	s.bot.On("IsBotUser", mock.Anything).Return(false).Maybe()
	s.bot.On("SendStopButton", mock.Anything, mock.Anything, mock.Anything).Return("stop-msg-1", nil).Maybe()
	s.bot.On("RemoveStopButton", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// sendReply now always stores bot messages — provide defaults so tests that don't care don't break.
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.IsBot
	})).Return(nil).Maybe()

	// Default DB-pull drain plumbing. ClaimNextPending introspects past
	// InsertMessage calls on the same channel and returns the next
	// unclaimed triggered row. This lets existing tests keep their
	// `InsertMessage(...).Return(nil)` mocks intact while still having
	// the drain loop see the inserted row. Tests that need a specific
	// error from InsertMessage stay in control because their per-test
	// On("InsertMessage", ...) registers after SetupTest and is matched
	// in registration order.
	s.claimed = make(map[string]bool)
	s.store.On("ClaimNextPending", mock.Anything, mock.AnythingOfType("string")).Return(
		func(_ context.Context, ch string) *db.Message {
			s.pendingMu.Lock()
			defer s.pendingMu.Unlock()
			for _, call := range s.store.Calls {
				if call.Method != "InsertMessage" || len(call.Arguments) < 2 {
					continue
				}
				row, ok := call.Arguments.Get(1).(*db.Message)
				if !ok || !row.IsTriggered || row.ChannelID != ch {
					continue
				}
				if s.claimed[row.MsgID] {
					continue
				}
				s.claimed[row.MsgID] = true
				s.nextRowID++
				captured := *row
				captured.ID = s.nextRowID
				return &captured
			}
			return nil
		},
		nil,
	).Maybe()
	s.store.On("ReleaseRunningMessage", mock.Anything, mock.AnythingOfType("int64"), mock.AnythingOfType("bool")).Return(nil).Maybe()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{}, nil)

	// Run drains inline so mock expectations from the drain path
	// (runner.Run, MarkMessagesProcessed, …) are observed before the test
	// body's AssertExpectations calls — TearDown / Cleanup fires too late.
	s.orch.SetSynchronousDrain()
}

func (s *OrchestratorSuite) TestNew() {
	require.NotNil(s.T(), s.orch)
	require.NotNil(s.T(), s.orch.store)
	require.NotNil(s.T(), s.orch.bot)
}

func (s *OrchestratorSuite) TestSetEventBroadcaster() {
	require.Nil(s.T(), s.orch.events)
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)
	require.Same(s.T(), eb, s.orch.events)
}

// --- Start tests ---

func (s *OrchestratorSuite) TestStart() {
	tests := []struct {
		name        string
		registerErr error
		botStartErr error
		schedErr    error
		wantErr     string
	}{
		{"success", nil, nil, nil, ""},
		{"register commands error", errors.New("register failed"), nil, nil, "registering commands"},
		{"bot start error", nil, errors.New("bot failed"), nil, "starting bot"},
		{"scheduler start error", nil, nil, errors.New("scheduler failed"), "starting scheduler"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.bot.On("OnMessage", mock.Anything).Return()
			s.bot.On("OnInteraction", mock.Anything).Return()
			s.bot.On("OnChannelDelete", mock.Anything).Return()
			s.bot.On("OnChannelJoin", mock.Anything).Return()
			s.bot.On("RegisterCommands", s.ctx).Return(tc.registerErr)
			if tc.registerErr == nil {
				s.bot.On("Start", s.ctx).Return(tc.botStartErr)
			}
			if tc.registerErr == nil && tc.botStartErr == nil {
				s.scheduler.On("Start", s.ctx).Return(tc.schedErr)
			}

			err := s.orch.Start(s.ctx)
			if tc.wantErr == "" {
				require.NoError(s.T(), err)
			} else {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			}
			s.bot.AssertExpectations(s.T())
			s.scheduler.AssertExpectations(s.T())
		})
	}
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

// --- HandleChannelDelete tests ---

func (s *OrchestratorSuite) TestHandleChannelDelete() {
	tests := []struct {
		name      string
		channelID string
		isThread  bool
		setupMock func()
		setupFunc func()
	}{
		{
			name: "thread success", channelID: "thread-1", isThread: true,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "thread-1").
					Return(&db.Channel{ChannelID: "thread-1", DirPath: "/work", ParentID: "ch-1"}, nil)
				s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)
			},
		},
		{
			name: "thread MCP config error logs warning", channelID: "thread-1", isThread: true,
			setupFunc: func() {
				s.orch.removeMCPConfig = func(string, string) error { return errors.New("rm error") }
			},
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "thread-1").
					Return(&db.Channel{ChannelID: "thread-1", DirPath: "/work", ParentID: "ch-1"}, nil)
				s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)
			},
		},
		{
			name: "thread lookup error still deletes", channelID: "thread-1", isThread: true,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "thread-1").Return(nil, errors.New("db error"))
				s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)
			},
		},
		{
			name: "thread delete error", channelID: "thread-1", isThread: true,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "thread-1").Return(nil, nil)
				s.store.On("DeleteChannel", s.ctx, "thread-1").Return(errors.New("db error"))
			},
		},
		{
			name: "channel success", channelID: "ch-1", isThread: false,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").
					Return(&db.Channel{ChannelID: "ch-1", DirPath: "/work"}, nil)
				s.store.On("ListChannelIDsByParentID", s.ctx, "ch-1").
					Return([]string{"t1", "t2"}, nil)
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)
			},
		},
		{
			name: "channel MCP config error logs warning", channelID: "ch-1", isThread: false,
			setupFunc: func() {
				s.orch.removeMCPConfig = func(string, string) error { return errors.New("rm error") }
			},
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").
					Return(&db.Channel{ChannelID: "ch-1", DirPath: "/work"}, nil)
				s.store.On("ListChannelIDsByParentID", s.ctx, "ch-1").
					Return([]string{"t1"}, nil)
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)
			},
		},
		{
			name: "channel lookup error still deletes", channelID: "ch-1", isThread: false,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").Return(nil, errors.New("db error"))
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)
			},
		},
		{
			name: "channel list children error", channelID: "ch-1", isThread: false,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").
					Return(&db.Channel{ChannelID: "ch-1", DirPath: "/work"}, nil)
				s.store.On("ListChannelIDsByParentID", s.ctx, "ch-1").
					Return(nil, errors.New("db error"))
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)
			},
		},
		{
			name: "channel children error", channelID: "ch-1", isThread: false,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").
					Return(&db.Channel{ChannelID: "ch-1", DirPath: "/work"}, nil)
				s.store.On("ListChannelIDsByParentID", s.ctx, "ch-1").
					Return([]string{}, nil)
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(errors.New("db error"))
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)
			},
		},
		{
			name: "channel delete error", channelID: "ch-1", isThread: false,
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").
					Return(&db.Channel{ChannelID: "ch-1", DirPath: "/work"}, nil)
				s.store.On("ListChannelIDsByParentID", s.ctx, "ch-1").
					Return([]string{}, nil)
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(errors.New("db error"))
			},
		},
		{
			name: "thread KeepMCPConfigs skips removal", channelID: "thread-1", isThread: true,
			setupFunc: func() {
				cfgKeep := s.orch.cfg.Load()
				cfgKeep.KeepMCPConfigs = true
				s.orch.cfg.Store(cfgKeep)
				s.orch.removeMCPConfig = func(string, string) error {
					s.Fail("removeMCPConfig should not be called when KeepMCPConfigs is true")
					return nil
				}
			},
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "thread-1").
					Return(&db.Channel{ChannelID: "thread-1", DirPath: "/work", ParentID: "ch-1"}, nil)
				s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)
			},
		},
		{
			name: "channel KeepMCPConfigs skips removal", channelID: "ch-1", isThread: false,
			setupFunc: func() {
				cfgKeep := s.orch.cfg.Load()
				cfgKeep.KeepMCPConfigs = true
				s.orch.cfg.Store(cfgKeep)
				s.orch.removeMCPConfig = func(string, string) error {
					s.Fail("removeMCPConfig should not be called when KeepMCPConfigs is true")
					return nil
				}
			},
			setupMock: func() {
				s.store.On("GetChannel", s.ctx, "ch-1").
					Return(&db.Channel{ChannelID: "ch-1", DirPath: "/work"}, nil)
				s.store.On("DeleteChannelsByParentID", s.ctx, "ch-1").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "ch-1").Return(nil)
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.setupFunc != nil {
				tc.setupFunc()
			}
			tc.setupMock()
			s.orch.HandleChannelDelete(s.ctx, tc.channelID, tc.isThread)
			s.store.AssertExpectations(s.T())
		})
	}
}

// --- IAmTheOwner tests ---

func (s *OrchestratorSuite) TestHandleInteractionIAmTheOwnerSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Owners: types.RoleGrant{Users: []string{"user1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ <@user1> is now the owner of this channel."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "iamtheowner",
		AuthorID:    "user1",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionIAmTheOwnerAlreadyConfigured() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: types.Permissions{
			Owners: types.RoleGrant{Users: []string{"existing-owner"}},
		},
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ An owner is already configured. Use `/loop allow_user` to manage permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "iamtheowner",
		AuthorID:    "user1",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionIAmTheOwnerChannelNotRegistered() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "iamtheowner",
		AuthorID:    "user1",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionIAmTheOwnerStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "iamtheowner",
		AuthorID:    "user1",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionIAmTheOwnerBlockedByCfgPerms() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"cfg-owner"}}},
	})

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: types.Permissions{},
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ An owner is already configured. Use `/loop allow_user` to manage permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "iamtheowner",
		AuthorID:    "user1",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- sendReply platform behavior ---

func (s *OrchestratorSuite) TestSendReplyLocalPlatformStoresAndBroadcasts() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	localOrch := New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{}, nil)
	eb := new(MockEventBroadcaster)
	localOrch.SetEventBroadcaster(eb)

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 10, ChannelID: "ch1"}, nil)
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 10 && m.ChannelID == "ch1" && m.Content == "Loop bot is running." && m.IsBot
	})).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "Loop bot is running." && d.IsBot && d.AuthorName == "agent"
	}))

	localOrch.sendReply(s.ctx, "ch1", "Loop bot is running.")

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestSendReplyAlwaysStoresAndBroadcasts() {
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "hello"
	})).Return(nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1"}, nil)

	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "hello" && d.IsBot && d.AuthorName == "agent"
	}))

	s.orch.sendReply(s.ctx, "ch1", "hello")

	s.bot.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestActiveChatChannelIDs() {
	// Empty initially.
	ids := s.orch.ActiveChatChannelIDs()
	require.Empty(s.T(), ids)

	// Store some active runs.
	s.orch.activeRuns.Store("ch-1", context.CancelFunc(func() {}))
	s.orch.activeRuns.Store("ch-2", context.CancelFunc(func() {}))

	ids = s.orch.ActiveChatChannelIDs()
	require.Len(s.T(), ids, 2)
	_, ok1 := ids["ch-1"]
	_, ok2 := ids["ch-2"]
	require.True(s.T(), ok1)
	require.True(s.T(), ok2)
}

func (s *OrchestratorSuite) TestActiveRunsMapReturnsSharedMap() {
	m := s.orch.ActiveRunsMap()
	require.NotNil(s.T(), m)
	// Verify it's the same underlying map.
	m.Store("test-ch", context.CancelFunc(func() {}))
	ids := s.orch.ActiveChatChannelIDs()
	_, ok := ids["test-ch"]
	require.True(s.T(), ok, "ActiveRunsMap should return the orchestrator's activeRuns")
	m.Delete("test-ch")
}

func (s *OrchestratorSuite) TestCancelActiveRunActive() {
	cancelled := false
	cancel := context.CancelFunc(func() { cancelled = true })
	s.orch.activeRuns.Store("ch-cancel", cancel)

	ok := s.orch.CancelActiveRun("ch-cancel")
	require.True(s.T(), ok)
	require.True(s.T(), cancelled)

	// Entry should be deleted.
	_, loaded := s.orch.activeRuns.Load("ch-cancel")
	require.False(s.T(), loaded)
}

func (s *OrchestratorSuite) TestCancelActiveRunNoRun() {
	ok := s.orch.CancelActiveRun("ch-nonexistent")
	require.False(s.T(), ok)
}

func (s *OrchestratorSuite) TestActiveRunMessageIDStored() {
	s.orch.activeRunMsgIDs.Store("ch-a", "m-1")
	require.Equal(s.T(), "m-1", s.orch.ActiveRunMessageID("ch-a"))
}

func (s *OrchestratorSuite) TestActiveRunMessageIDNotPresent() {
	require.Equal(s.T(), "", s.orch.ActiveRunMessageID("ch-missing"))
}

// --- ResumeChannel / drainChannel error paths ---
//
// These build a fresh orchestrator so the default SetupTest ClaimNextPending /
// ReleaseRunningMessage mocks (which return rows from past InsertMessage calls)
// don't override the per-test expectations.
func (s *OrchestratorSuite) TestResumeChannelDrainsEmpty() {
	store := new(testutil.MockStore)
	store.On("ClaimNextPending", mock.Anything, "empty-ch").Return(nil, nil).Once()
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()

	orch.ResumeChannel(context.Background(), "empty-ch")
	store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestDrainChannelClaimError() {
	store := new(testutil.MockStore)
	store.On("ClaimNextPending", mock.Anything, "err-ch").Return(nil, errors.New("db gone")).Once()
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()

	orch.ResumeChannel(context.Background(), "err-ch")
	store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestDrainChannelReleaseError() {
	// Force processClaimedMessage to exit early (GetRecentMessages error in
	// prepareAgentRequest) so we only need to mock the few methods drainChannel
	// itself touches. ReleaseRunningMessage then returns an error so we exercise
	// the log-and-continue branch in drainChannel.
	store := new(testutil.MockStore)
	row := &db.Message{ID: 99, ChannelID: "rel-ch", MsgID: "m-rel", IsTriggered: true, AuthorID: "u", AuthorName: "u", Content: "hi"}
	store.On("ClaimNextPending", mock.Anything, "rel-ch").Return(row, nil).Once()
	store.On("ClaimNextPending", mock.Anything, "rel-ch").Return(nil, nil).Once()
	store.On("ReleaseRunningMessage", mock.Anything, int64(99), true).Return(errors.New("release failed")).Once()
	store.On("GetRecentMessages", mock.Anything, "rel-ch", mock.Anything).Return(nil, errors.New("recent failed")).Once()

	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()
	orch.ResumeChannel(context.Background(), "rel-ch")
	store.AssertExpectations(s.T())
}

// TestDrainChannelChannelDeleted exercises the nil-channel guard added to
// prepareAgentRequest: if GetChannel returns (nil, nil) — the channel was
// deleted between message enqueue and drain — processClaimedMessage must
// return early (no crash, no agent run) and the message must still be
// released so the drain loop moves on.
func (s *OrchestratorSuite) TestDrainChannelChannelDeleted() {
	store := new(testutil.MockStore)
	row := &db.Message{ID: 42, ChannelID: "gone-ch", MsgID: "m-gone", IsTriggered: true, AuthorID: "u", AuthorName: "u", Content: "hi"}
	store.On("ClaimNextPending", mock.Anything, "gone-ch").Return(row, nil).Once()
	store.On("ClaimNextPending", mock.Anything, "gone-ch").Return(nil, nil).Once()
	store.On("GetRecentMessages", mock.Anything, "gone-ch", mock.Anything).Return([]*db.Message{}, nil).Once()
	store.On("GetChannel", mock.Anything, "gone-ch").Return(nil, nil).Once()
	store.On("ReleaseRunningMessage", mock.Anything, int64(42), true).Return(nil).Once()

	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()
	orch.ResumeChannel(context.Background(), "gone-ch")
	store.AssertExpectations(s.T())
}

// TestDrainChannelSkipsWhenPlanned verifies the pause flag set by
// ExitPlanMode short-circuits drainChannel: ClaimNextPending must NOT be
// invoked while the flag is present.
func (s *OrchestratorSuite) TestDrainChannelSkipsWhenPlanned() {
	store := new(testutil.MockStore)
	// No ClaimNextPending expectation — if the guard regresses, the mock
	// call panics with "unexpected call".
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()
	store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil)
	store.On("DeletePausedChannel", mock.Anything, "planned-ch", db.PausedKindPlan).Return(nil)

	orch.markPlannedChannel(context.Background(), "planned-ch", events.ExitPlanModeEventData{})
	require.True(s.T(), orch.IsChannelPlanned("planned-ch"))

	orch.ResumeChannel(context.Background(), "planned-ch")
	// Should have returned immediately without touching the store.
	store.AssertNotCalled(s.T(), "ClaimNextPending")

	// After clearing, the next drain proceeds and finds nothing pending.
	orch.ClearPlannedChannel("planned-ch")
	require.False(s.T(), orch.IsChannelPlanned("planned-ch"))
	store.On("ClaimNextPending", mock.Anything, "planned-ch").Return(nil, nil).Once()
	orch.ResumeChannel(context.Background(), "planned-ch")
	store.AssertExpectations(s.T())
}

// TestDrainChannelSkipsWhenAsked verifies the pause flag set by
// AskUserQuestion short-circuits drainChannel: ClaimNextPending must NOT be
// invoked while the flag is present.
func (s *OrchestratorSuite) TestDrainChannelSkipsWhenAsked() {
	store := new(testutil.MockStore)
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()
	store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil)
	store.On("DeletePausedChannel", mock.Anything, "asked-ch", db.PausedKindAsk).Return(nil)

	orch.markAskedChannel(context.Background(), "asked-ch", "", events.AskUserQuestionEventData{})
	require.True(s.T(), orch.IsChannelAsked("asked-ch"))

	orch.ResumeChannel(context.Background(), "asked-ch")
	store.AssertNotCalled(s.T(), "ClaimNextPending")

	orch.ClearAskedChannel("asked-ch")
	require.False(s.T(), orch.IsChannelAsked("asked-ch"))
	store.On("ClaimNextPending", mock.Anything, "asked-ch").Return(nil, nil).Once()
	orch.ResumeChannel(context.Background(), "asked-ch")
	store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestCurrentConfigReloads() {
	s.orch.cfg.Store(&config.Config{KeepMCPConfigs: false})
	s.orch.configLoad = func() (*config.Config, error) {
		return &config.Config{KeepMCPConfigs: true}, nil
	}

	cfg := s.orch.currentConfig()
	require.True(s.T(), cfg.KeepMCPConfigs)
	// Verify it was also stored as the fallback.
	require.True(s.T(), s.orch.cfg.Load().KeepMCPConfigs)
}

func (s *OrchestratorSuite) TestCurrentConfigFallbackOnError() {
	s.orch.cfg.Store(&config.Config{KeepMCPConfigs: true})
	s.orch.configLoad = func() (*config.Config, error) {
		return nil, errors.New("reload failed")
	}

	cfg := s.orch.currentConfig()
	require.True(s.T(), cfg.KeepMCPConfigs)
}

func (s *OrchestratorSuite) TestCurrentConfigNilLoader() {
	s.orch.cfg.Store(&config.Config{KeepMCPConfigs: true})
	s.orch.configLoad = nil

	cfg := s.orch.currentConfig()
	require.True(s.T(), cfg.KeepMCPConfigs)
}

// TestDefaultDrainSpawnTracksOnDrainWG covers the closure installed by New —
// the production path that schedules drains onto drainWG so WaitDrains can
// observe them. Most tests swap this out via SetSynchronousDrain, leaving the
// default closure body uncovered.
func (s *OrchestratorSuite) TestDefaultDrainSpawnTracksOnDrainWG() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)

	ran := make(chan struct{})
	orch.drainSpawn(func() { close(ran) })

	select {
	case <-ran:
	case <-time.After(time.Second):
		s.T().Fatal("default drainSpawn closure did not invoke fn")
	}
	orch.WaitDrains()
}

func (s *OrchestratorSuite) TestWaitDrainsReturnsImmediatelyWhenIdle() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	done := make(chan struct{})
	go func() {
		orch.WaitDrains()
		close(done)
	}()
	require.Eventually(s.T(), func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func (s *OrchestratorSuite) TestListAskedChannelsSnapshotsEntries() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	require.Empty(s.T(), orch.ListAskedChannels())

	s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil).Maybe()
	q1 := events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{Question: "pick A or B"}}}
	q2 := events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{Question: "pick C or D"}}}
	orch.markAskedChannel(context.Background(), "ch-1", "", q1)
	orch.markAskedChannel(context.Background(), "ch-2", "plan", q2)

	entries := orch.ListAskedChannels()
	require.Len(s.T(), entries, 2)
	byID := map[string]events.AskUserQuestionEventData{}
	for _, e := range entries {
		byID[e.ChannelID] = e.Data
	}
	require.Equal(s.T(), q1, byID["ch-1"])
	require.Equal(s.T(), q2, byID["ch-2"])
}

// TestListAskedChannelsIgnoresMalformedEntries covers the defensive type
// assertions in ListAskedChannels. The askedChannels map is private, but a
// caller using sync.Map.Store directly could in principle insert a wrong-type
// value — the function must skip those rather than panic. We use the
// exported field via package-internal access since the test is in the same
// package.
func (s *OrchestratorSuite) TestListAskedChannelsIgnoresMalformedEntries() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)

	s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil).Maybe()
	// Valid entry.
	orch.markAskedChannel(context.Background(), "ch-valid", "", events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{Question: "q"}}})
	// Wrong-typed value (string instead of AskUserQuestionEventData).
	orch.askedChannels.Store("ch-bad-val", "not the right type")
	// Wrong-typed key (int instead of string).
	orch.askedChannels.Store(42, events.AskUserQuestionEventData{})

	entries := orch.ListAskedChannels()
	require.Len(s.T(), entries, 1)
	require.Equal(s.T(), "ch-valid", entries[0].ChannelID)
}

func (s *OrchestratorSuite) TestListPlannedChannelsSnapshotsEntries() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	require.Empty(s.T(), orch.ListPlannedChannels())

	s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil).Maybe()
	p1 := events.ExitPlanModeEventData{Plan: "# Plan one"}
	p2 := events.ExitPlanModeEventData{Plan: "# Plan two", PlanFilePath: "/tmp/plan.md"}
	orch.markPlannedChannel(context.Background(), "ch-1", p1)
	orch.markPlannedChannel(context.Background(), "ch-2", p2)

	entries := orch.ListPlannedChannels()
	require.Len(s.T(), entries, 2)
	byID := map[string]events.ExitPlanModeEventData{}
	for _, e := range entries {
		byID[e.ChannelID] = e.Data
	}
	require.Equal(s.T(), p1, byID["ch-1"])
	require.Equal(s.T(), p2, byID["ch-2"])
}

// TestListPlannedChannelsIgnoresMalformedEntries covers the defensive type
// assertions in ListPlannedChannels, mirroring the asked-channels case.
func (s *OrchestratorSuite) TestListPlannedChannelsIgnoresMalformedEntries() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)

	s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil).Maybe()
	// Valid entry.
	orch.markPlannedChannel(context.Background(), "ch-valid", events.ExitPlanModeEventData{Plan: "p"})
	// Wrong-typed value (string instead of ExitPlanModeEventData).
	orch.plannedChannels.Store("ch-bad-val", "not the right type")
	// Wrong-typed key (int instead of string).
	orch.plannedChannels.Store(42, events.ExitPlanModeEventData{})

	entries := orch.ListPlannedChannels()
	require.Len(s.T(), entries, 1)
	require.Equal(s.T(), "ch-valid", entries[0].ChannelID)
}

// TestRestoreParkedChannels verifies persisted ask/plan parks are reloaded
// into the in-memory maps at startup — including the ask's composer mode —
// and that malformed payloads are skipped rather than fatal.
func (s *OrchestratorSuite) TestRestoreParkedChannels() {
	store := new(testutil.MockStore)
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)

	store.On("ListPausedChannels", mock.Anything).Return([]*db.PausedChannel{
		{ChannelID: "ch-ask", Kind: db.PausedKindAsk, Mode: "plan", Data: `{"questions":[{"question":"q","header":"H"}]}`},
		{ChannelID: "ch-plan", Kind: db.PausedKindPlan, Data: `{"plan":"# P"}`},
		{ChannelID: "ch-bad-ask", Kind: db.PausedKindAsk, Data: `not json`},
		{ChannelID: "ch-bad-plan", Kind: db.PausedKindPlan, Data: `not json`},
		{ChannelID: "ch-unknown", Kind: "other", Data: `{}`},
	}, nil)

	orch.RestoreParkedChannels(context.Background())

	require.True(s.T(), orch.IsChannelAsked("ch-ask"))
	require.Equal(s.T(), "plan", orch.AskedChannelMode("ch-ask"))
	require.True(s.T(), orch.IsChannelPlanned("ch-plan"))
	require.False(s.T(), orch.IsChannelAsked("ch-bad-ask"))
	require.False(s.T(), orch.IsChannelPlanned("ch-bad-plan"))
	require.False(s.T(), orch.IsChannelAsked("ch-unknown"))
	require.False(s.T(), orch.IsChannelPlanned("ch-unknown"))
	store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestRestoreParkedChannelsListError() {
	store := new(testutil.MockStore)
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	store.On("ListPausedChannels", mock.Anything).Return(nil, errors.New("db closed"))

	orch.RestoreParkedChannels(context.Background()) // must not panic
	require.Empty(s.T(), orch.ListAskedChannels())
	require.Empty(s.T(), orch.ListPlannedChannels())
}

// TestAskedChannelModeDefaults covers the empty cases: no park at all, and a
// wrong-typed stored value.
func (s *OrchestratorSuite) TestAskedChannelModeDefaults() {
	orch := New(s.store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	require.Equal(s.T(), "", orch.AskedChannelMode("nope"))
	orch.askedModes.Store("weird", 42)
	require.Equal(s.T(), "", orch.AskedChannelMode("weird"))
}

// TestParkPersistenceErrorsAreLoggedNotFatal covers the error branches of the
// persistence calls in mark/clear: a failing store must not break parking.
func (s *OrchestratorSuite) TestParkPersistenceErrorsAreLoggedNotFatal() {
	store := new(testutil.MockStore)
	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(errors.New("disk full"))
	store.On("DeletePausedChannel", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("disk full"))

	orch.markAskedChannel(context.Background(), "ch-1", "plan", events.AskUserQuestionEventData{})
	require.True(s.T(), orch.IsChannelAsked("ch-1"))
	orch.ClearAskedChannel("ch-1")
	require.False(s.T(), orch.IsChannelAsked("ch-1"))

	orch.markPlannedChannel(context.Background(), "ch-2", events.ExitPlanModeEventData{Plan: "p"})
	require.True(s.T(), orch.IsChannelPlanned("ch-2"))
	orch.ClearPlannedChannel("ch-2")
	require.False(s.T(), orch.IsChannelPlanned("ch-2"))
}
