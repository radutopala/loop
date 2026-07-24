package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// mockChannelStore implements ChannelStore for BotRouter tests.
type mockChannelStore struct {
	mock.Mock
}

func (m *mockChannelStore) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

type BotRouterSuite struct {
	suite.Suite
	discordBot *MockBot
	localBot   *MockBot
	store      *mockChannelStore
	router     *BotRouter
	logger     *slog.Logger
}

func TestBotRouterSuite(t *testing.T) {
	suite.Run(t, new(BotRouterSuite))
}

func (s *BotRouterSuite) SetupTest() {
	s.discordBot = new(MockBot)
	s.localBot = new(MockBot)
	s.store = new(mockChannelStore)
	s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	bots := map[types.Platform]Bot{
		types.PlatformDiscord: s.discordBot,
		types.PlatformLocal:   s.localBot,
	}
	s.router = NewBotRouter(bots, s.store, s.logger)
}

// --- Lifecycle: fan out ---

func (s *BotRouterSuite) TestStartFansOutToAllBots() {
	s.discordBot.On("Start", mock.Anything).Return(nil)
	s.localBot.On("Start", mock.Anything).Return(nil)

	err := s.router.Start(context.Background())
	require.NoError(s.T(), err)
	s.discordBot.AssertExpectations(s.T())
	s.localBot.AssertExpectations(s.T())
}

func (s *BotRouterSuite) TestStartCollectsErrors() {
	s.discordBot.On("Start", mock.Anything).Return(errors.New("discord fail"))
	s.localBot.On("Start", mock.Anything).Return(nil)

	err := s.router.Start(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord fail")
}

func (s *BotRouterSuite) TestStopFansOutToAllBots() {
	s.discordBot.On("Stop").Return(nil)
	s.localBot.On("Stop").Return(nil)

	err := s.router.Stop()
	require.NoError(s.T(), err)
	s.discordBot.AssertExpectations(s.T())
	s.localBot.AssertExpectations(s.T())
}

func (s *BotRouterSuite) TestStopCollectsErrors() {
	s.discordBot.On("Stop").Return(errors.New("discord stop fail"))
	s.localBot.On("Stop").Return(nil)

	err := s.router.Stop()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord stop fail")
}

func (s *BotRouterSuite) TestRegisterCommandsFansOut() {
	s.discordBot.On("RegisterCommands", mock.Anything).Return(nil)
	s.localBot.On("RegisterCommands", mock.Anything).Return(nil)

	err := s.router.RegisterCommands(context.Background())
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestRegisterCommandsCollectsErrors() {
	s.discordBot.On("RegisterCommands", mock.Anything).Return(errors.New("fail"))
	s.localBot.On("RegisterCommands", mock.Anything).Return(nil)

	err := s.router.RegisterCommands(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "registering commands")
}

func (s *BotRouterSuite) TestRemoveCommandsFansOut() {
	s.discordBot.On("RemoveCommands", mock.Anything).Return(nil)
	s.localBot.On("RemoveCommands", mock.Anything).Return(nil)

	err := s.router.RemoveCommands(context.Background())
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestRemoveCommandsCollectsErrors() {
	s.discordBot.On("RemoveCommands", mock.Anything).Return(errors.New("fail"))
	s.localBot.On("RemoveCommands", mock.Anything).Return(nil)

	err := s.router.RemoveCommands(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing commands")
}

// --- Inbound handlers: register on all bots (pass-through) ---

func (s *BotRouterSuite) TestOnMessageRegistersOnAllBots() {
	var capturedPlatforms []types.Platform

	var discordHandler func(context.Context, *bot.IncomingMessage)
	s.discordBot.On("OnMessage", mock.Anything).Run(func(args mock.Arguments) {
		discordHandler = args.Get(0).(func(context.Context, *bot.IncomingMessage))
	}).Return()

	var localHandler func(context.Context, *bot.IncomingMessage)
	s.localBot.On("OnMessage", mock.Anything).Run(func(args mock.Arguments) {
		localHandler = args.Get(0).(func(context.Context, *bot.IncomingMessage))
	}).Return()

	s.router.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		capturedPlatforms = append(capturedPlatforms, msg.Platform)
	})

	require.NotNil(s.T(), discordHandler)
	require.NotNil(s.T(), localHandler)

	discordHandler(context.Background(), &bot.IncomingMessage{Platform: types.PlatformDiscord})
	localHandler(context.Background(), &bot.IncomingMessage{Platform: types.PlatformLocal})

	require.Len(s.T(), capturedPlatforms, 2)
	require.Contains(s.T(), capturedPlatforms, types.PlatformDiscord)
	require.Contains(s.T(), capturedPlatforms, types.PlatformLocal)
}

func (s *BotRouterSuite) TestOnInteractionRegistersOnAllBots() {
	var capturedPlatform types.Platform

	var discordHandler func(context.Context, *bot.Interaction)
	s.discordBot.On("OnInteraction", mock.Anything).Run(func(args mock.Arguments) {
		discordHandler = args.Get(0).(func(context.Context, *bot.Interaction))
	}).Return()
	s.localBot.On("OnInteraction", mock.Anything).Return()

	s.router.OnInteraction(func(_ context.Context, i *bot.Interaction) {
		capturedPlatform = i.Platform
	})

	discordHandler(context.Background(), &bot.Interaction{Platform: types.PlatformDiscord})
	require.Equal(s.T(), types.PlatformDiscord, capturedPlatform)
}

func (s *BotRouterSuite) TestOnChannelDeleteRegistersOnAllBots() {
	var called bool

	var localHandler func(context.Context, string, bool)
	s.discordBot.On("OnChannelDelete", mock.Anything).Return()
	s.localBot.On("OnChannelDelete", mock.Anything).Run(func(args mock.Arguments) {
		localHandler = args.Get(0).(func(context.Context, string, bool))
	}).Return()

	s.router.OnChannelDelete(func(_ context.Context, channelID string, isThread bool) {
		called = true
		require.Equal(s.T(), "ch-1", channelID)
		require.False(s.T(), isThread)
	})

	localHandler(context.Background(), "ch-1", false)
	require.True(s.T(), called)
}

func (s *BotRouterSuite) TestOnChannelJoinRegistersOnAllBots() {
	var capturedPlatform types.Platform

	var discordHandler func(context.Context, string, types.Platform)
	s.discordBot.On("OnChannelJoin", mock.Anything).Run(func(args mock.Arguments) {
		discordHandler = args.Get(0).(func(context.Context, string, types.Platform))
	}).Return()
	s.localBot.On("OnChannelJoin", mock.Anything).Return()

	s.router.OnChannelJoin(func(_ context.Context, channelID string, platform types.Platform) {
		capturedPlatform = platform
	})

	discordHandler(context.Background(), "ch-1", types.PlatformDiscord)
	require.Equal(s.T(), types.PlatformDiscord, capturedPlatform)
}

// --- Channel-specific routing ---

func (s *BotRouterSuite) TestSendMessageRoutesToChannelPlatform() {
	s.store.On("GetChannel", mock.Anything, "ch-discord").Return(
		&db.Channel{ChannelID: "ch-discord", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)

	err := s.router.SendMessage(context.Background(), &bot.OutgoingMessage{ChannelID: "ch-discord"})
	require.NoError(s.T(), err)
	s.discordBot.AssertCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
	s.localBot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
}

func (s *BotRouterSuite) TestSendMessageRoutesToLocalPlatform() {
	s.store.On("GetChannel", mock.Anything, "ch-local").Return(
		&db.Channel{ChannelID: "ch-local", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)

	err := s.router.SendMessage(context.Background(), &bot.OutgoingMessage{ChannelID: "ch-local"})
	require.NoError(s.T(), err)
	s.localBot.AssertCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
	s.discordBot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
}

func (s *BotRouterSuite) TestBotForChannelReturnsNilOnError() {
	s.store.On("GetChannel", mock.Anything, "unknown").Return(nil, errors.New("not found"))

	b := s.router.botForChannel(context.Background(), "unknown")
	require.Nil(s.T(), b)
}

func (s *BotRouterSuite) TestBotForChannelReturnsNilOnNilChannel() {
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)

	b := s.router.botForChannel(context.Background(), "gone")
	require.Nil(s.T(), b)
}

func (s *BotRouterSuite) TestBotForChannelReturnsNilOnUnregisteredPlatform() {
	s.store.On("GetChannel", mock.Anything, "ch-slack").Return(
		&db.Channel{ChannelID: "ch-slack", Platform: types.PlatformSlack}, nil,
	)

	b := s.router.botForChannel(context.Background(), "ch-slack")
	require.Nil(s.T(), b)
}

// --- Remaining channel-specific methods ---

func (s *BotRouterSuite) TestSendTyping() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("SendTyping", mock.Anything, "ch-1").Return(nil)

	err := s.router.SendTyping(context.Background(), "ch-1")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestSendStopButton() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("SendStopButton", mock.Anything, "ch-1", "run-1").Return("msg-1", nil)

	msgID, err := s.router.SendStopButton(context.Background(), "ch-1", "run-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "msg-1", msgID)
}

func (s *BotRouterSuite) TestRemoveStopButton() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("RemoveStopButton", mock.Anything, "ch-1", "msg-1").Return(nil)

	err := s.router.RemoveStopButton(context.Background(), "ch-1", "msg-1")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestSendApproval() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil,
	)
	prompt := bot.ApprovalPrompt{ID: "req-1", Kind: "execve", Target: "git push"}
	s.localBot.On("SendApproval", mock.Anything, "ch-1", prompt).Return("msg-1", nil)

	msgID, err := s.router.SendApproval(context.Background(), "ch-1", prompt)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "msg-1", msgID)
}

func (s *BotRouterSuite) TestRemoveApproval() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("RemoveApproval", mock.Anything, "ch-1", "msg-1").Return(nil)

	err := s.router.RemoveApproval(context.Background(), "ch-1", "msg-1")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestSetChannelTopic() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("SetChannelTopic", mock.Anything, "ch-1", "topic").Return(nil)

	err := s.router.SetChannelTopic(context.Background(), "ch-1", "topic")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestDeleteThread() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(
		&db.Channel{ChannelID: "thread-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("DeleteThread", mock.Anything, "thread-1").Return(nil)

	err := s.router.DeleteThread(context.Background(), "thread-1")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestRenameThread() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(
		&db.Channel{ChannelID: "thread-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("RenameThread", mock.Anything, "thread-1", "new name").Return(nil)

	err := s.router.RenameThread(context.Background(), "thread-1", "new name")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestPostMessage() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("PostMessage", mock.Anything, "ch-1", "hello").Return(nil)

	err := s.router.PostMessage(context.Background(), "ch-1", "hello")
	require.NoError(s.T(), err)
}

func (s *BotRouterSuite) TestGetChannelParentID() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("GetChannelParentID", mock.Anything, "ch-1").Return("parent-1", nil)

	parentID, err := s.router.GetChannelParentID(context.Background(), "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "parent-1", parentID)
}

func (s *BotRouterSuite) TestGetChannelName() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("GetChannelName", mock.Anything, "ch-1").Return("general", nil)

	name, err := s.router.GetChannelName(context.Background(), "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "general", name)
}

func (s *BotRouterSuite) TestCreateThread() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("CreateThread", mock.Anything, "ch-1", "thread", "user-1", "msg").Return("thread-1", nil)

	threadID, err := s.router.CreateThread(context.Background(), "ch-1", "thread", "user-1", "msg")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
}

func (s *BotRouterSuite) TestCreateSimpleThread() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("CreateSimpleThread", mock.Anything, "ch-1", "thread", "hi").Return("thread-1", nil)

	threadID, err := s.router.CreateSimpleThread(context.Background(), "ch-1", "thread", "hi")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
}

func (s *BotRouterSuite) TestInviteUserToChannel() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(
		&db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}, nil,
	)
	s.discordBot.On("InviteUserToChannel", mock.Anything, "ch-1", "user-1").Return(nil)

	err := s.router.InviteUserToChannel(context.Background(), "ch-1", "user-1")
	require.NoError(s.T(), err)
}

// --- Nil bot error paths ---

func (s *BotRouterSuite) TestChannelMethodsReturnErrorWhenNoBotFound() {
	// All channel-specific methods should return an error (not panic)
	// when botForChannel returns nil.
	s.store.On("GetChannel", mock.Anything, "unknown").Return(nil, nil)

	ctx := context.Background()

	err := s.router.SendMessage(ctx, &bot.OutgoingMessage{ChannelID: "unknown", Content: "hi"})
	require.ErrorContains(s.T(), err, "no bot found for channel unknown")

	err = s.router.SendTyping(ctx, "unknown")
	require.ErrorContains(s.T(), err, "no bot found")

	_, err = s.router.SendStopButton(ctx, "unknown", "run-1")
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.RemoveStopButton(ctx, "unknown", "msg-1")
	require.ErrorContains(s.T(), err, "no bot found")

	_, err = s.router.SendApproval(ctx, "unknown", bot.ApprovalPrompt{})
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.RemoveApproval(ctx, "unknown", "msg-1")
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.SetChannelTopic(ctx, "unknown", "topic")
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.DeleteThread(ctx, "unknown")
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.RenameThread(ctx, "unknown", "name")
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.PostMessage(ctx, "unknown", "hi")
	require.ErrorContains(s.T(), err, "no bot found")

	_, err = s.router.GetChannelParentID(ctx, "unknown")
	require.ErrorContains(s.T(), err, "no bot found")

	_, err = s.router.GetChannelName(ctx, "unknown")
	require.ErrorContains(s.T(), err, "no bot found")

	_, err = s.router.CreateThread(ctx, "unknown", "t", "u", "m")
	require.ErrorContains(s.T(), err, "no bot found")

	_, err = s.router.CreateSimpleThread(ctx, "unknown", "t", "m")
	require.ErrorContains(s.T(), err, "no bot found")

	err = s.router.InviteUserToChannel(ctx, "unknown", "user-1")
	require.ErrorContains(s.T(), err, "no bot found")
}

// --- BotFor: explicit platform routing ---

func (s *BotRouterSuite) TestBotForReturnsPlatformBot() {
	require.Equal(s.T(), s.discordBot, s.router.BotFor(types.PlatformDiscord))
	require.Equal(s.T(), s.localBot, s.router.BotFor(types.PlatformLocal))
	require.Nil(s.T(), s.router.BotFor(types.PlatformSlack))
}

// No-channel methods are no-ops on the router (use BotFor directly).

func (s *BotRouterSuite) TestBotUserIDReturnsEmpty() {
	require.Empty(s.T(), s.router.BotUserID())
}

// --- IsBotUser checks all bots ---

func (s *BotRouterSuite) TestIsBotUserChecksAllBots() {
	s.discordBot.On("IsBotUser", "discord-bot").Return(true)
	s.localBot.On("IsBotUser", "discord-bot").Maybe().Return(false)

	require.True(s.T(), s.router.IsBotUser("discord-bot"))
}

func (s *BotRouterSuite) TestIsBotUserReturnsFalseForUnknown() {
	s.discordBot.On("IsBotUser", "unknown").Return(false)
	s.localBot.On("IsBotUser", "unknown").Return(false)

	require.False(s.T(), s.router.IsBotUser("unknown"))
}

func (s *BotRouterSuite) TestIsBotUserMatchesLocalBot() {
	s.discordBot.On("IsBotUser", "local-bot").Maybe().Return(false)
	s.localBot.On("IsBotUser", "local-bot").Return(true)

	require.True(s.T(), s.router.IsBotUser("local-bot"))
}

// --- HandleIncomingMessage / HandleThreadCreated routing ---

func (s *BotRouterSuite) TestHandleIncomingMessageRoutesToCorrectBot() {
	s.store.On("GetChannel", mock.Anything, "ch-local").Return(
		&db.Channel{ChannelID: "ch-local", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("HandleIncomingMessage", mock.Anything, "ch-local", "user-1", "hello", "").Return()

	s.router.HandleIncomingMessage(context.Background(), "ch-local", "user-1", "hello", "")
	s.localBot.AssertCalled(s.T(), "HandleIncomingMessage", mock.Anything, "ch-local", "user-1", "hello", "")
}

func (s *BotRouterSuite) TestHandleIncomingMessageNilBotNoPanic() {
	router := NewBotRouter(map[types.Platform]Bot{}, s.store, s.logger)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil)

	router.HandleIncomingMessage(context.Background(), "ch-1", "", "test", "")
}

func (s *BotRouterSuite) TestHandleIncomingMessageWithPriorityRoutesToCorrectBot() {
	s.store.On("GetChannel", mock.Anything, "ch-local").Return(
		&db.Channel{ChannelID: "ch-local", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-local", "user-1", "urgent", "", 5).Return()

	s.router.HandleIncomingMessageWithPriority(context.Background(), "ch-local", "user-1", "urgent", "", 5)
	s.localBot.AssertCalled(s.T(), "HandleIncomingMessageWithPriority", mock.Anything, "ch-local", "user-1", "urgent", "", 5)
}

func (s *BotRouterSuite) TestHandleIncomingMessageWithPriorityNilBotNoPanic() {
	router := NewBotRouter(map[types.Platform]Bot{}, s.store, s.logger)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil)

	router.HandleIncomingMessageWithPriority(context.Background(), "ch-1", "", "test", "", 3)
}

func (s *BotRouterSuite) TestHandleIncomingMessageDelayedRoutesToCorrectBot() {
	s.store.On("GetChannel", mock.Anything, "ch-local").Return(
		&db.Channel{ChannelID: "ch-local", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("HandleIncomingMessageDelayed", mock.Anything, "ch-local", "user-1", "later", "", int64(123)).Return()

	s.router.HandleIncomingMessageDelayed(context.Background(), "ch-local", "user-1", "later", "", 123)
	s.localBot.AssertCalled(s.T(), "HandleIncomingMessageDelayed", mock.Anything, "ch-local", "user-1", "later", "", int64(123))
}

func (s *BotRouterSuite) TestHandleIncomingMessageDelayedNilBotNoPanic() {
	router := NewBotRouter(map[types.Platform]Bot{}, s.store, s.logger)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil)

	router.HandleIncomingMessageDelayed(context.Background(), "ch-1", "", "test", "", 99)
}

func (s *BotRouterSuite) TestHandleThreadCreatedRoutesToCorrectBot() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(
		&db.Channel{ChannelID: "thread-1", Platform: types.PlatformLocal}, nil,
	)
	s.localBot.On("HandleThreadCreated", mock.Anything, "thread-1", "user-1", "start task").Return()

	s.router.HandleThreadCreated(context.Background(), "thread-1", "user-1", "start task")
	s.localBot.AssertCalled(s.T(), "HandleThreadCreated", mock.Anything, "thread-1", "user-1", "start task")
}

func (s *BotRouterSuite) TestHandleThreadCreatedNilBotNoPanic() {
	router := NewBotRouter(map[types.Platform]Bot{}, s.store, s.logger)
	s.store.On("GetChannel", mock.Anything, "t-1").Return(nil, nil)

	router.HandleThreadCreated(context.Background(), "t-1", "", "test")
}

// --- Empty bots (edge case) ---

func (s *BotRouterSuite) TestEmptyBotsLifecycle() {
	router := NewBotRouter(map[types.Platform]Bot{}, s.store, s.logger)

	require.NoError(s.T(), router.Start(context.Background()))
	require.NoError(s.T(), router.Stop())
	require.NoError(s.T(), router.RegisterCommands(context.Background()))
	require.NoError(s.T(), router.RemoveCommands(context.Background()))
	require.False(s.T(), router.IsBotUser("any"))
	require.Empty(s.T(), router.BotUserID())

	// botForChannel returns nil with no bots; BotFor returns nil.
	s.store.On("GetChannel", mock.Anything, "ch").Return(nil, nil)
	require.Nil(s.T(), router.botForChannel(context.Background(), "ch"))
	require.Nil(s.T(), router.BotFor(types.PlatformLocal))
}
