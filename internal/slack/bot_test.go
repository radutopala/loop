package slack

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/orchestrator"
)

// Compile-time check that SlackBot implements orchestrator.Bot.
var _ orchestrator.Bot = (*SlackBot)(nil)

// MockSession mocks the SlackSession interface.
type MockSession struct {
	mock.Mock
}

func (m *MockSession) PostMessage(channelID string, options ...goslack.MsgOption) (string, string, error) {
	args := m.Called(channelID, options)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockSession) DeleteMessage(channel, messageTimestamp string) (string, string, error) {
	args := m.Called(channel, messageTimestamp)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockSession) AuthTest() (*goslack.AuthTestResponse, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*goslack.AuthTestResponse), args.Error(1)
}

func (m *MockSession) CreateConversation(params goslack.CreateConversationParams) (*goslack.Channel, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*goslack.Channel), args.Error(1)
}

func (m *MockSession) AddReaction(name string, item goslack.ItemRef) error {
	args := m.Called(name, item)
	return args.Error(0)
}

func (m *MockSession) RemoveReaction(name string, item goslack.ItemRef) error {
	args := m.Called(name, item)
	return args.Error(0)
}

func (m *MockSession) GetConversationReplies(params *goslack.GetConversationRepliesParameters) ([]goslack.Message, bool, string, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.String(2), args.Error(3)
	}
	return args.Get(0).([]goslack.Message), args.Bool(1), args.String(2), args.Error(3)
}

func (m *MockSession) InviteUsersToConversation(channelID string, users ...string) (*goslack.Channel, error) {
	args := m.Called(channelID, users)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*goslack.Channel), args.Error(1)
}

func (m *MockSession) GetConversations(params *goslack.GetConversationsParameters) ([]goslack.Channel, string, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.String(1), args.Error(2)
	}
	return args.Get(0).([]goslack.Channel), args.String(1), args.Error(2)
}

func (m *MockSession) GetUsers(options ...goslack.GetUsersOption) ([]goslack.User, error) {
	args := m.Called(options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]goslack.User), args.Error(1)
}

func (m *MockSession) SetUserPresence(presence string) error {
	return m.Called(presence).Error(0)
}

func (m *MockSession) SetTopicOfConversation(channelID, topic string) (*goslack.Channel, error) {
	args := m.Called(channelID, topic)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*goslack.Channel), args.Error(1)
}

func (m *MockSession) GetConversationInfo(input *goslack.GetConversationInfoInput) (*goslack.Channel, error) {
	args := m.Called(input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*goslack.Channel), args.Error(1)
}

// MockSocketModeClient mocks the SocketModeClient interface.
type MockSocketModeClient struct {
	mock.Mock
	events chan socketmode.Event
}

func newMockSocketClient() *MockSocketModeClient {
	return &MockSocketModeClient{
		events: make(chan socketmode.Event, 10),
	}
}

func (m *MockSocketModeClient) RunContext(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (m *MockSocketModeClient) Ack(req socketmode.Request, payload ...any) {
	m.Called(req, payload)
}

func (m *MockSocketModeClient) Events() <-chan socketmode.Event {
	return m.events
}

// BotSuite is the test suite for SlackBot.
type BotSuite struct {
	suite.Suite
	session      *MockSession
	socketClient *MockSocketModeClient
	bot          *SlackBot
}

func TestBotSuite(t *testing.T) {
	suite.Run(t, new(BotSuite))
}

func (s *BotSuite) SetupTest() {
	s.session = new(MockSession)
	s.socketClient = newMockSocketClient()
	s.bot = NewBot(s.session, s.socketClient, testLogger())
	s.bot.botUserID = "U123BOT"
	s.bot.botUsername = "loopbot"
}

func testLogger() *slog.Logger {
	return slog.Default()
}

// --- NewSocketModeAdapter ---

func (s *BotSuite) TestNewSocketModeAdapter() {
	api := goslack.New("xoxb-fake")
	smClient := socketmode.New(api)
	adapter := NewSocketModeAdapter(smClient)
	require.NotNil(s.T(), adapter)

	// Verify Events() returns a non-nil channel.
	require.NotNil(s.T(), adapter.Events())

	// RunContext with an already-cancelled context returns immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = adapter.RunContext(ctx)

	// Ack with an empty request (exercises the passthrough).
	adapter.Ack(socketmode.Request{})
}

// --- Start / Stop ---

func (s *BotSuite) TestStartSuccess() {
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())

	session.On("AuthTest").Return(&goslack.AuthTestResponse{
		UserID: "U123BOT",
		User:   "loopbot",
	}, nil)
	session.On("SetUserPresence", "auto").Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Start(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "U123BOT", bot.BotUserID())

	cancel()
	time.Sleep(10 * time.Millisecond) // allow goroutines to stop
	session.AssertExpectations(s.T())
}

func (s *BotSuite) TestStartPresenceError() {
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())

	session.On("AuthTest").Return(&goslack.AuthTestResponse{
		UserID: "U123BOT",
		User:   "loopbot",
	}, nil)
	session.On("SetUserPresence", "auto").Return(errors.New("presence_error"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Start(ctx)
	require.NoError(s.T(), err) // presence error is non-fatal
	require.Equal(s.T(), "U123BOT", bot.BotUserID())

	cancel()
	time.Sleep(10 * time.Millisecond)
	session.AssertExpectations(s.T())
}

func (s *BotSuite) TestStartAuthError() {
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())

	session.On("AuthTest").Return(nil, errors.New("invalid_auth"))

	err := bot.Start(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack auth test")
}

func (s *BotSuite) TestStop() {
	ctx, cancel := context.WithCancel(context.Background())
	s.bot.cancel = cancel
	err := s.bot.Stop()
	require.NoError(s.T(), err)
	// Context should be cancelled
	select {
	case <-ctx.Done():
	default:
		s.Fail("context should be cancelled")
	}
}

func (s *BotSuite) TestStopNilCancel() {
	err := s.bot.Stop()
	require.NoError(s.T(), err)
}

// --- SendMessage ---

func (s *BotSuite) TestSendMessagePlain() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "C123",
		Content:   "hello world",
	})
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "PostMessage", 1)
}

func (s *BotSuite) TestSendMessageThread() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "C123:1111.2222",
		Content:   "reply in thread",
	})
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "PostMessage", 1)
}

func (s *BotSuite) TestSendMessageWithReplyTo() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID:        "C123",
		Content:          "replying",
		ReplyToMessageID: "9999.0000",
	})
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "PostMessage", 1)
}

func (s *BotSuite) TestSendMessageSplit() {
	var longContent strings.Builder
	for range maxMessageLen + 100 {
		longContent.WriteString("a")
	}

	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "C123",
		Content:   longContent.String(),
	})
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "PostMessage", 2)
}

func (s *BotSuite) TestSendMessageError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("channel_not_found"))

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "C123",
		Content:   "hello",
	})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack send message")
}

// --- SendTyping ---

func (s *BotSuite) TestSendTypingSuccess() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(nil)
	s.session.On("RemoveReaction", reactionEmoji, ref).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.bot.SendTyping(ctx, "C123")
	require.NoError(s.T(), err)
	s.session.AssertCalled(s.T(), "AddReaction", reactionEmoji, ref)

	cancel()
	time.Sleep(20 * time.Millisecond)
	s.session.AssertCalled(s.T(), "RemoveReaction", reactionEmoji, ref)
}

func (s *BotSuite) TestSendTypingNoTrackedMessage() {
	err := s.bot.SendTyping(context.Background(), "C999")
	require.NoError(s.T(), err)
	// Should be a no-op, no calls made.
	s.session.AssertNotCalled(s.T(), "AddReaction")
}

func (s *BotSuite) TestSendTypingCompositeID() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(nil)
	s.session.On("RemoveReaction", reactionEmoji, ref).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.bot.SendTyping(ctx, "C123:9999.0000")
	require.NoError(s.T(), err)
	s.session.AssertCalled(s.T(), "AddReaction", reactionEmoji, ref)
}

func (s *BotSuite) TestSendTypingAddReactionError() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(errors.New("not_in_channel"))

	err := s.bot.SendTyping(context.Background(), "C123")
	require.NoError(s.T(), err) // non-fatal
}

func (s *BotSuite) TestSendTypingAlreadyReacted() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(errors.New("already_reacted"))
	s.session.On("RemoveReaction", reactionEmoji, ref).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.bot.SendTyping(ctx, "C123")
	require.NoError(s.T(), err)

	// Cleanup goroutine should still be set up.
	cancel()
	time.Sleep(20 * time.Millisecond)
	s.session.AssertCalled(s.T(), "RemoveReaction", reactionEmoji, ref)
}

// --- RegisterCommands / RemoveCommands ---

func (s *BotSuite) TestRegisterCommandsNoOp() {
	err := s.bot.RegisterCommands(context.Background())
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestRemoveCommandsNoOp() {
	err := s.bot.RemoveCommands(context.Background())
	require.NoError(s.T(), err)
}

// --- Handler Registration ---

func (s *BotSuite) TestOnMessage() {
	s.bot.OnMessage(func(_ context.Context, _ *orchestrator.IncomingMessage) {})
	require.Len(s.T(), s.bot.messageHandlers, 1)
}

func (s *BotSuite) TestOnInteraction() {
	s.bot.OnInteraction(func(_ context.Context, _ any) {})
	require.Len(s.T(), s.bot.interactionHandlers, 1)
}

func (s *BotSuite) TestOnChannelDelete() {
	s.bot.OnChannelDelete(func(_ context.Context, _ string, _ bool) {})
	require.Len(s.T(), s.bot.channelDeleteHandlers, 1)
}

func (s *BotSuite) TestOnChannelJoin() {
	s.bot.OnChannelJoin(func(_ context.Context, _ string) {})
	require.Len(s.T(), s.bot.channelJoinHandlers, 1)
}

// --- BotUserID ---

func (s *BotSuite) TestBotUserID() {
	require.Equal(s.T(), "U123BOT", s.bot.BotUserID())
}

// --- CreateChannel ---

func (s *BotSuite) TestCreateChannelSuccess() {
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "test-channel",
	}).Return(&goslack.Channel{GroupConversation: goslack.GroupConversation{
		Conversation: goslack.Conversation{ID: "C456"},
	}}, nil)

	id, err := s.bot.CreateChannel(context.Background(), "", "test-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C456", id)
}

func (s *BotSuite) TestCreateChannelError() {
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "test-channel",
	}).Return(nil, errors.New("some_other_error"))

	_, err := s.bot.CreateChannel(context.Background(), "", "test-channel")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack create channel")
}

func (s *BotSuite) TestCreateChannelNameTaken() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	s.session.On("GetConversations", mock.AnythingOfType("*slack.GetConversationsParameters")).Return(
		[]goslack.Channel{
			{GroupConversation: goslack.GroupConversation{
				Name:         "other-channel",
				Conversation: goslack.Conversation{ID: "C111"},
			}},
			{GroupConversation: goslack.GroupConversation{
				Name:         "existing-channel",
				Conversation: goslack.Conversation{ID: "C222"},
			}},
		}, "", nil,
	)

	id, err := s.bot.CreateChannel(context.Background(), "", "existing-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C222", id)
}

func (s *BotSuite) TestCreateChannelNameTakenLookupError() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	s.session.On("GetConversations", mock.AnythingOfType("*slack.GetConversationsParameters")).Return(
		nil, "", errors.New("api_error"),
	)

	_, err := s.bot.CreateChannel(context.Background(), "", "existing-channel")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack find existing channel")
}

func (s *BotSuite) TestCreateChannelNameTakenNotFound() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	s.session.On("GetConversations", mock.AnythingOfType("*slack.GetConversationsParameters")).Return(
		[]goslack.Channel{}, "", nil,
	)

	_, err := s.bot.CreateChannel(context.Background(), "", "existing-channel")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *BotSuite) TestCreateChannelNameTakenPagination() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	// First page: no match, has next cursor
	s.session.On("GetConversations", mock.MatchedBy(func(p *goslack.GetConversationsParameters) bool {
		return p.Cursor == ""
	})).Return(
		[]goslack.Channel{
			{GroupConversation: goslack.GroupConversation{
				Name:         "other-channel",
				Conversation: goslack.Conversation{ID: "C111"},
			}},
		}, "next_cursor_1", nil,
	)

	// Second page: match found
	s.session.On("GetConversations", mock.MatchedBy(func(p *goslack.GetConversationsParameters) bool {
		return p.Cursor == "next_cursor_1"
	})).Return(
		[]goslack.Channel{
			{GroupConversation: goslack.GroupConversation{
				Name:         "existing-channel",
				Conversation: goslack.Conversation{ID: "C333"},
			}},
		}, "", nil,
	)

	id, err := s.bot.CreateChannel(context.Background(), "", "existing-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C333", id)
}

// --- InviteUserToChannel ---

func (s *BotSuite) TestInviteUserToChannelSuccess() {
	s.session.On("InviteUsersToConversation", "C456", []string{"U789"}).
		Return(&goslack.Channel{}, nil)

	err := s.bot.InviteUserToChannel(context.Background(), "C456", "U789")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestInviteUserToChannelError() {
	s.session.On("InviteUsersToConversation", "C456", []string{"U789"}).
		Return(nil, errors.New("not_in_channel"))

	err := s.bot.InviteUserToChannel(context.Background(), "C456", "U789")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack invite user to channel")
}

// --- GetOwnerUserID ---

func (s *BotSuite) TestGetOwnerUserIDSuccess() {
	s.session.On("GetUsers", mock.Anything).Return([]goslack.User{
		{ID: "U111", Name: "bot", IsBot: true, IsOwner: false},
		{ID: "U222", Name: "member", IsBot: false, IsOwner: false},
		{ID: "U333", Name: "owner", IsBot: false, IsOwner: true},
	}, nil)

	id, err := s.bot.GetOwnerUserID(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "U333", id)
}

func (s *BotSuite) TestGetOwnerUserIDNotFound() {
	s.session.On("GetUsers", mock.Anything).Return([]goslack.User{
		{ID: "U111", Name: "member", IsBot: false, IsOwner: false},
	}, nil)

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no workspace owner found")
}

func (s *BotSuite) TestGetOwnerUserIDError() {
	s.session.On("GetUsers", mock.Anything).Return(nil, errors.New("api_error"))

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack get users")
}

func (s *BotSuite) TestGetOwnerUserIDSkipsBot() {
	// A bot that is also marked as owner should be skipped.
	s.session.On("GetUsers", mock.Anything).Return([]goslack.User{
		{ID: "U111", Name: "bot-owner", IsBot: true, IsOwner: true},
	}, nil)

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no workspace owner found")
}

// --- SetChannelTopic ---

func (s *BotSuite) TestSetChannelTopicSuccess() {
	s.session.On("SetTopicOfConversation", "C123", "/home/user/dev/loop").
		Return(&goslack.Channel{}, nil)

	err := s.bot.SetChannelTopic(context.Background(), "C123", "/home/user/dev/loop")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSetChannelTopicError() {
	s.session.On("SetTopicOfConversation", "C123", "/path").
		Return(nil, errors.New("topic_error"))

	err := s.bot.SetChannelTopic(context.Background(), "C123", "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack set channel topic")
}

// --- CreateThread ---

func (s *BotSuite) TestCreateThreadWithMessage() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "9999.0000", nil)

	id, err := s.bot.CreateThread(context.Background(), "C123", "my thread", "U456", "<@U123BOT> do something")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:9999.0000", id)
}

func (s *BotSuite) TestCreateThreadWithMention() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "9999.0000", nil)

	id, err := s.bot.CreateThread(context.Background(), "C123", "my thread", "U456", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:9999.0000", id)
}

func (s *BotSuite) TestCreateThreadDefault() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "9999.0000", nil)

	id, err := s.bot.CreateThread(context.Background(), "C123", "my thread", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:9999.0000", id)
}

func (s *BotSuite) TestCreateThreadError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("not_in_channel"))

	_, err := s.bot.CreateThread(context.Background(), "C123", "thread", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack create thread")
}

// --- CreateSimpleThread ---

func (s *BotSuite) TestCreateSimpleThreadWithMessage() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1111.2222", nil)

	id, err := s.bot.CreateSimpleThread(context.Background(), "C123", "task output", "First turn")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:1111.2222", id)
}

func (s *BotSuite) TestCreateSimpleThreadEmptyMessageUsesName() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "2222.3333", nil)

	id, err := s.bot.CreateSimpleThread(context.Background(), "C123", "task name", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:2222.3333", id)
}

func (s *BotSuite) TestCreateSimpleThreadCompositeParent() {
	// When parent is a composite ID, only the channel part is used
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "3333.4444", nil)

	id, err := s.bot.CreateSimpleThread(context.Background(), "C123:old.ts", "task", "content")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:3333.4444", id)
}

func (s *BotSuite) TestCreateSimpleThreadError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("post failed"))

	_, err := s.bot.CreateSimpleThread(context.Background(), "C123", "task", "content")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack create simple thread")
}

// --- PostMessage ---

func (s *BotSuite) TestPostMessagePlain() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.PostMessage(context.Background(), "C123", "hello")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestPostMessageComposite() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.PostMessage(context.Background(), "C123:1111.2222", "reply")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestPostMessageTextMentionConversion() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	err := s.bot.PostMessage(context.Background(), "C123", "hey @loopbot do this")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestPostMessageError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("channel_not_found"))

	err := s.bot.PostMessage(context.Background(), "C123", "hello")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack post message")
}

// --- DeleteThread ---

func (s *BotSuite) TestDeleteThreadSuccess() {
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("C123", "1111.2222", nil)

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestDeleteThreadInvalidID() {
	err := s.bot.DeleteThread(context.Background(), "C123")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid thread ID")
}

func (s *BotSuite) TestDeleteThreadError() {
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("", "", errors.New("message_not_found"))

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack delete thread")
}

// --- GetChannelName ---

func (s *BotSuite) TestGetChannelNameSuccess() {
	s.session.On("GetConversationInfo", &goslack.GetConversationInfoInput{
		ChannelID: "C123",
	}).Return(&goslack.Channel{
		GroupConversation: goslack.GroupConversation{
			Name: "general",
		},
	}, nil)

	name, err := s.bot.GetChannelName(context.Background(), "C123")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "general", name)
}

func (s *BotSuite) TestGetChannelNameCompositeID() {
	s.session.On("GetConversationInfo", &goslack.GetConversationInfoInput{
		ChannelID: "C123",
	}).Return(&goslack.Channel{
		GroupConversation: goslack.GroupConversation{
			Name: "general",
		},
	}, nil)

	name, err := s.bot.GetChannelName(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "general", name)
}

func (s *BotSuite) TestGetChannelNameError() {
	s.session.On("GetConversationInfo", mock.Anything).Return(nil, errors.New("channel_not_found"))

	name, err := s.bot.GetChannelName(context.Background(), "C123")
	require.Error(s.T(), err)
	require.Empty(s.T(), name)
}

// --- GetChannelParentID ---

func (s *BotSuite) TestGetChannelParentIDThread() {
	parentID, err := s.bot.GetChannelParentID(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123", parentID)
}

func (s *BotSuite) TestGetChannelParentIDNotThread() {
	parentID, err := s.bot.GetChannelParentID(context.Background(), "C123")
	require.NoError(s.T(), err)
	require.Empty(s.T(), parentID)
}

// --- Composite ID ---

func (s *BotSuite) TestCompositeID() {
	require.Equal(s.T(), "C123:1234.5678", compositeID("C123", "1234.5678"))
}

func (s *BotSuite) TestParseCompositeIDThread() {
	ch, ts := parseCompositeID("C123:1234.5678")
	require.Equal(s.T(), "C123", ch)
	require.Equal(s.T(), "1234.5678", ts)
}

func (s *BotSuite) TestParseCompositeIDPlain() {
	ch, ts := parseCompositeID("C123")
	require.Equal(s.T(), "C123", ch)
	require.Empty(s.T(), ts)
}

// --- Slack TS to Time ---

func (s *BotSuite) TestSlackTSToTime() {
	ts := "1234567890.123456"
	t := slackTSToTime(ts)
	require.Equal(s.T(), time.Unix(1234567890, 0), t)
}

func (s *BotSuite) TestSlackTSToTimeInvalid() {
	t := slackTSToTime("abc")
	require.True(s.T(), t.IsZero())
}

func (s *BotSuite) TestSlackTSToTimeEmpty() {
	t := slackTSToTime("")
	require.True(s.T(), t.IsZero())
}

// --- Event Handling ---

func (s *BotSuite) TestHandleMessageMentionInChannel() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "<@U123BOT> hello",
		TimeStamp:   "1234567890.000001",
		Channel:     "C123",
		ChannelType: "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123", msg.ChannelID)
		require.Equal(s.T(), "hello", msg.Content)
		require.True(s.T(), msg.IsBotMention)
		require.Equal(s.T(), "U456", msg.AuthorID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageMentionInThread() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	s.session.On("GetConversationReplies", mock.AnythingOfType("*slack.GetConversationRepliesParameters")).
		Return([]goslack.Message{{Msg: goslack.Msg{User: "U123BOT"}}}, false, "", nil)

	ev := &slackevents.MessageEvent{
		User:            "U456",
		Text:            "<@U123BOT> hello",
		TimeStamp:       "1234567891.000001",
		ThreadTimeStamp: "1234567890.000001",
		Channel:         "C123",
		ChannelType:     "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
		require.True(s.T(), msg.IsBotMention)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageWithPrefix() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "!loop status",
		TimeStamp:   "1234567890.000001",
		Channel:     "C123",
		ChannelType: "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.HasPrefix)
		require.Equal(s.T(), "status", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageDM() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "hello",
		TimeStamp:   "1234567890.000001",
		Channel:     "D123",
		ChannelType: "im",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsDM)
		require.Equal(s.T(), "hello", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageIgnoredSubtype() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:      "U456",
		Text:      "edited",
		TimeStamp: "1234567890.000001",
		Channel:   "C123",
		SubType:   "message_changed",
	}
	s.bot.handleMessage(ev)

	select {
	case <-received:
		s.Fail("should not have received message with subtype")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func (s *BotSuite) TestHandleMessageNoTrigger() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "just a random message",
		TimeStamp:   "1234567890.000001",
		Channel:     "C123",
		ChannelType: "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case <-received:
		s.Fail("should not have received non-triggered message")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func (s *BotSuite) TestHandleMessageReplyToBot() {
	s.session.On("GetConversationReplies", mock.AnythingOfType("*slack.GetConversationRepliesParameters")).Return(
		[]goslack.Message{{Msg: goslack.Msg{User: "U123BOT"}}},
		false, "", nil,
	)

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:            "U456",
		Text:            "follow up",
		TimeStamp:       "1234567891.000001",
		ThreadTimeStamp: "1234567890.000001",
		Channel:         "C123",
		ChannelType:     "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsReplyToBot)
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

// --- Slash Commands ---

func (s *BotSuite) TestHandleSlashCommandSchedule() {
	received := make(chan any, 1)
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: goslack.SlashCommand{
			ChannelID: "C123",
			TeamID:    "T123",
			Text:      "schedule 0 9 * * * cron Check for updates",
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleSlashCommand(evt)

	select {
	case i := <-received:
		inter := i.(*orchestrator.Interaction)
		require.Equal(s.T(), "schedule", inter.CommandName)
		require.Equal(s.T(), "0 9 * * *", inter.Options["schedule"])
		require.Equal(s.T(), "cron", inter.Options["type"])
		require.Equal(s.T(), "Check for updates", inter.Options["prompt"])
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}

func (s *BotSuite) TestHandleSlashCommandTasks() {
	received := make(chan any, 1)
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: goslack.SlashCommand{
			ChannelID: "C123",
			TeamID:    "T123",
			Text:      "tasks",
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleSlashCommand(evt)

	select {
	case i := <-received:
		inter := i.(*orchestrator.Interaction)
		require.Equal(s.T(), "tasks", inter.CommandName)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}

func (s *BotSuite) TestHandleSlashCommandSetsAuthorID() {
	received := make(chan any, 1)
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: goslack.SlashCommand{
			ChannelID: "C123",
			TeamID:    "T123",
			UserID:    "U789",
			Text:      "tasks",
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleSlashCommand(evt)

	select {
	case i := <-received:
		inter := i.(*orchestrator.Interaction)
		require.Equal(s.T(), "U789", inter.AuthorID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}

func (s *BotSuite) TestHandleSlashCommandHelp() {
	// Empty text should send help back
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: goslack.SlashCommand{
			ChannelID: "C123",
			TeamID:    "T123",
			Text:      "",
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleSlashCommand(evt)

	time.Sleep(50 * time.Millisecond)
	s.session.AssertCalled(s.T(), "PostMessage", "C123", mock.Anything)
}

func (s *BotSuite) TestHandleEventsAPIChannelMention() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: string(slackevents.Message),
				Data: &slackevents.MessageEvent{
					User:        "U456",
					Text:        "<@U123BOT> test",
					TimeStamp:   "1234567890.000001",
					Channel:     "C123",
					ChannelType: "channel",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEvent(evt)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsBotMention)
		require.Equal(s.T(), "test", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

// --- Command Parsing ---

func (s *BotSuite) TestParseSlashCommandSchedule() {
	inter, errText := parseSlashCommand("C123", "T123", "schedule 0 9 * * * cron Check updates")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "schedule", inter.CommandName)
	require.Equal(s.T(), "0 9 * * *", inter.Options["schedule"])
	require.Equal(s.T(), "cron", inter.Options["type"])
	require.Equal(s.T(), "Check updates", inter.Options["prompt"])
}

func (s *BotSuite) TestParseSlashCommandScheduleInterval() {
	inter, errText := parseSlashCommand("C123", "T123", "schedule 1h interval Run checks")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "1h", inter.Options["schedule"])
	require.Equal(s.T(), "interval", inter.Options["type"])
	require.Equal(s.T(), "Run checks", inter.Options["prompt"])
}

func (s *BotSuite) TestParseSlashCommandScheduleTooFewArgs() {
	_, errText := parseSlashCommand("C123", "T123", "schedule foo bar")
	require.NotEmpty(s.T(), errText)
}

func (s *BotSuite) TestParseSlashCommandCancel() {
	inter, errText := parseSlashCommand("C123", "T123", "cancel 42")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "cancel", inter.CommandName)
	require.Equal(s.T(), "42", inter.Options["task_id"])
}

func (s *BotSuite) TestParseSlashCommandCancelInvalidID() {
	_, errText := parseSlashCommand("C123", "T123", "cancel abc")
	require.Contains(s.T(), errText, "Invalid task_id")
}

func (s *BotSuite) TestParseSlashCommandToggle() {
	inter, errText := parseSlashCommand("C123", "T123", "toggle 7")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "toggle", inter.CommandName)
	require.Equal(s.T(), "7", inter.Options["task_id"])
}

func (s *BotSuite) TestParseSlashCommandEdit() {
	inter, errText := parseSlashCommand("C123", "T123", "edit 5 --schedule 1h --type interval --prompt New task prompt text")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "edit", inter.CommandName)
	require.Equal(s.T(), "5", inter.Options["task_id"])
	require.Equal(s.T(), "1h", inter.Options["schedule"])
	require.Equal(s.T(), "interval", inter.Options["type"])
	require.Equal(s.T(), "New task prompt text", inter.Options["prompt"])
}

func (s *BotSuite) TestParseSlashCommandEditNoFlags() {
	_, errText := parseSlashCommand("C123", "T123", "edit 5")
	require.Contains(s.T(), errText, "At least one of")
}

func (s *BotSuite) TestParseSlashCommandStatus() {
	inter, errText := parseSlashCommand("C123", "T123", "status")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "status", inter.CommandName)
}

func (s *BotSuite) TestParseSlashCommandTemplateAdd() {
	inter, errText := parseSlashCommand("C123", "T123", "template add daily-check")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "template-add", inter.CommandName)
	require.Equal(s.T(), "daily-check", inter.Options["name"])
}

func (s *BotSuite) TestParseSlashCommandTemplateList() {
	inter, errText := parseSlashCommand("C123", "T123", "template list")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "template-list", inter.CommandName)
}

func (s *BotSuite) TestEventLoopChannelClosed() {
	session := new(MockSession)
	events := make(chan socketmode.Event, 1)
	sc := &MockSocketModeClient{events: events}
	bot := NewBot(session, sc, testLogger())
	bot.botUserID = "U123BOT"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		bot.eventLoop(ctx)
		close(done)
	}()

	// Close the events channel to trigger the !ok path.
	close(events)

	select {
	case <-done:
		// eventLoop exited correctly
	case <-time.After(time.Second):
		s.Fail("eventLoop did not exit when events channel closed")
	}
}

func (s *BotSuite) TestHandleSlashCommandInvalidData() {
	// Send a slash command event with non-SlashCommand data type
	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: "not a slash command", // wrong type
	}
	// Should return without panic
	s.bot.handleSlashCommand(evt)
}

func (s *BotSuite) TestHandleEventsAPIInvalidData() {
	// Send an events API event with non-EventsAPIEvent data type
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: "not an events api event", // wrong type
	}
	// Should return without panic
	s.bot.handleEventsAPI(evt)
}

func (s *BotSuite) TestHandleEventUnknownType() {
	// Send an event with an unknown type
	evt := socketmode.Event{
		Type: socketmode.EventType("unknown"),
		Data: nil,
	}
	// Should return without panic
	s.bot.handleEvent(evt)
}

func (s *BotSuite) TestHandleEventBothPaths() {
	// Exercise both case branches through handleEvent

	// EventsAPI path
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()
	evtAPI := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "unknown",
				Data: nil,
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEvent(evtAPI)

	// SlashCommand path (with invalid data, exercises both switch branches)
	evtCmd := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: "invalid",
	}
	s.bot.handleEvent(evtCmd)
}

func (s *BotSuite) TestEventLoopProcessesEvent() {
	// Test that eventLoop processes events from the channel
	session := new(MockSession)
	events := make(chan socketmode.Event, 10)
	sc := &MockSocketModeClient{events: events}
	bot := NewBot(session, sc, testLogger())
	bot.botUserID = "U123BOT"

	received := make(chan *orchestrator.IncomingMessage, 1)
	bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	sc.On("Ack", mock.Anything, mock.Anything).Return()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go bot.eventLoop(ctx)

	// Send a mention via message event through the channel.
	events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: string(slackevents.Message),
				Data: &slackevents.MessageEvent{
					User:        "U456",
					Text:        "<@U123BOT> hello from event loop",
					TimeStamp:   "1234567890.000001",
					Channel:     "C123",
					ChannelType: "channel",
				},
			},
		},
		Request: &socketmode.Request{},
	}

	select {
	case msg := <-received:
		require.Equal(s.T(), "hello from event loop", msg.Content)
		require.True(s.T(), msg.IsBotMention)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message from eventLoop")
	}
}

func (s *BotSuite) TestStartRunContextError() {
	// Test that Start logs socket mode errors
	session := new(MockSession)
	session.On("AuthTest").Return(&goslack.AuthTestResponse{
		UserID: "U123BOT",
		User:   "loopbot",
	}, nil)
	session.On("SetUserPresence", "auto").Return(nil)

	// Create a mock socket client that returns an error from RunContext
	errSc := &errorSocketModeClient{
		events: make(chan socketmode.Event),
		runErr: errors.New("socket error"),
	}

	bot := NewBot(session, errSc, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	err := bot.Start(ctx)
	require.NoError(s.T(), err)

	// Give the goroutine time to execute RunContext and log the error.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Give time for cleanup.
	time.Sleep(50 * time.Millisecond)
}

// errorSocketModeClient is a SocketModeClient that returns an error from RunContext.
type errorSocketModeClient struct {
	events chan socketmode.Event
	runErr error
}

func (e *errorSocketModeClient) RunContext(_ context.Context) error {
	return e.runErr
}

func (e *errorSocketModeClient) Ack(_ socketmode.Request, _ ...any) {}

func (e *errorSocketModeClient) Events() <-chan socketmode.Event {
	return e.events
}

func (s *BotSuite) TestParseSlashCommandEmpty() {
	inter, errText := parseSlashCommand("C123", "T123", "")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Available commands")
}

func (s *BotSuite) TestParseSlashCommandUnknown() {
	inter, errText := parseSlashCommand("C123", "T123", "foo")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Unknown subcommand: foo")
}

// --- Additional command parsing edge cases ---

func (s *BotSuite) TestParseCancelWrongArgCount() {
	inter, errText := parseSlashCommand("C123", "T123", "cancel")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseToggleWrongArgCount() {
	inter, errText := parseSlashCommand("C123", "T123", "toggle")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseToggleInvalidID() {
	inter, errText := parseSlashCommand("C123", "T123", "toggle abc")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Invalid task_id")
}

func (s *BotSuite) TestParseEditNoArgs() {
	inter, errText := parseSlashCommand("C123", "T123", "edit")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseEditInvalidID() {
	inter, errText := parseSlashCommand("C123", "T123", "edit abc")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Invalid task_id")
}

func (s *BotSuite) TestParseEditScheduleNoValue() {
	inter, errText := parseSlashCommand("C123", "T123", "edit 1 --schedule")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "--schedule requires a value")
}

func (s *BotSuite) TestParseEditTypeNoValue() {
	inter, errText := parseSlashCommand("C123", "T123", "edit 1 --type")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "--type requires a value")
}

func (s *BotSuite) TestParseEditPromptNoValue() {
	inter, errText := parseSlashCommand("C123", "T123", "edit 1 --prompt")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "--prompt requires a value")
}

func (s *BotSuite) TestParseEditWithPrompt() {
	inter, _ := parseSlashCommand("C123", "T123", "edit 1 --prompt hello world")
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "edit", inter.CommandName)
	require.Equal(s.T(), "hello world", inter.Options["prompt"])
}

func (s *BotSuite) TestParseEditWithType() {
	inter, _ := parseSlashCommand("C123", "T123", "edit 1 --type cron")
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "edit", inter.CommandName)
	require.Equal(s.T(), "cron", inter.Options["type"])
}

func (s *BotSuite) TestParseTemplateNoArgs() {
	inter, errText := parseSlashCommand("C123", "T123", "template")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseTemplateUnknownSubcommand() {
	inter, errText := parseSlashCommand("C123", "T123", "template foo")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseTemplateAddNoName() {
	inter, errText := parseSlashCommand("C123", "T123", "template add")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseScheduleTypeAtStart() {
	// Type keyword at position 0 (before schedule)
	inter, errText := parseSlashCommand("C123", "T123", "schedule cron prompt text")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseScheduleTypeAtEnd() {
	// Type keyword at the last position (no prompt)
	inter, errText := parseSlashCommand("C123", "T123", "schedule daily cron")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseScheduleNoTypeKeyword() {
	// No type keyword found
	inter, errText := parseSlashCommand("C123", "T123", "schedule daily something prompt")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Usage:")
}

// --- Additional bot edge cases ---

func (s *BotSuite) TestSlackTSToTimeNonDigit() {
	t := slackTSToTime("abc.123")
	require.True(s.T(), t.IsZero())
}

func (s *BotSuite) TestSlackTSToTimeDotPrefix() {
	// Timestamp starting with a dot (empty parts[0])
	t := slackTSToTime(".123456")
	require.True(s.T(), t.IsZero())
}

func (s *BotSuite) TestSendTypingRemoveReactionError() {
	channelID := "C123"
	ref := goslack.NewRefToMessage(channelID, "1234.5678")
	s.bot.lastMessageRef.Store(channelID, ref)

	s.session.On("AddReaction", "eyes", ref).Return(nil)
	s.session.On("RemoveReaction", "eyes", ref).Return(errors.New("remove error"))

	ctx, cancel := context.WithCancel(context.Background())
	err := s.bot.SendTyping(ctx, channelID)
	require.NoError(s.T(), err)

	cancel()
	time.Sleep(50 * time.Millisecond)
	s.session.AssertCalled(s.T(), "RemoveReaction", "eyes", ref)
}

func (s *BotSuite) TestHandleMessageSubtypeSkipped() {
	// Messages with subtypes should be skipped
	ev := &slackevents.MessageEvent{
		SubType: "message_changed",
		User:    "U456",
		Text:    "<@U123BOT> test",
		Channel: "C123",
	}
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case <-received:
		s.Fail("should not receive message with subtype")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func (s *BotSuite) TestHandleMessageNotTriggered() {
	// Message that doesn't match any trigger
	ev := &slackevents.MessageEvent{
		User:      "U456",
		Text:      "just some random text",
		Channel:   "C123",
		TimeStamp: "1234567890.000001",
	}

	s.session.On("GetConversationReplies", mock.Anything).
		Return([]goslack.Message{}, false, "", nil).Maybe()

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case <-received:
		s.Fail("should not receive non-triggered message")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func (s *BotSuite) TestHandleMessagePrefixTrigger() {
	ev := &slackevents.MessageEvent{
		User:      "U456",
		Text:      "!loop hello world",
		Channel:   "C123",
		TimeStamp: "1234567890.000001",
	}

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.HasPrefix)
		require.Equal(s.T(), "hello world", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout")
	}
}

func (s *BotSuite) TestHandleMessageDMTrigger() {
	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "hello",
		Channel:     "D123",
		ChannelType: "im",
		TimeStamp:   "1234567890.000001",
	}

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsDM)
		require.Equal(s.T(), "hello", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout")
	}
}

func (s *BotSuite) TestIsReplyToBotError() {
	ev := &slackevents.MessageEvent{
		User:            "U456",
		Channel:         "C123",
		ThreadTimeStamp: "1234567890.000001",
		TimeStamp:       "1234567890.000002",
	}
	s.session.On("GetConversationReplies", mock.Anything).
		Return(nil, false, "", errors.New("api error"))

	result := s.bot.isReplyToBot(ev)
	require.False(s.T(), result)
}

func (s *BotSuite) TestIsReplyToBotEmptyReplies() {
	ev := &slackevents.MessageEvent{
		User:            "U456",
		Channel:         "C123",
		ThreadTimeStamp: "1234567890.000001",
		TimeStamp:       "1234567890.000002",
	}
	s.session.On("GetConversationReplies", mock.Anything).
		Return([]goslack.Message{}, false, "", nil)

	result := s.bot.isReplyToBot(ev)
	require.False(s.T(), result)
}

func (s *BotSuite) TestHandleEventsAPIUnknownInnerEvent() {
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "unknown_type",
				Data: nil,
			},
		},
		Request: &socketmode.Request{},
	}
	// Should not panic
	s.bot.handleEventsAPI(evt)
}

func (s *BotSuite) TestHandleEventsAPIMessageEvent() {
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	// Use a DM to exercise the message event path.
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: string(slackevents.Message),
				Data: &slackevents.MessageEvent{
					User:        "U456",
					Text:        "hello via events api",
					TimeStamp:   "1234567890.000001",
					Channel:     "D123",
					ChannelType: "im",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEventsAPI(evt)

	select {
	case msg := <-received:
		require.Equal(s.T(), "hello via events api", msg.Content)
		require.True(s.T(), msg.IsDM)
	case <-time.After(time.Second):
		s.Fail("timeout")
	}
}

func (s *BotSuite) TestHandleEventsAPINilRequest() {
	// No Request to Ack
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "unknown_type",
				Data: nil,
			},
		},
		Request: nil,
	}
	s.bot.handleEventsAPI(evt)
}

// --- handleMemberJoinedChannel tests ---

func (s *BotSuite) TestHandleMemberJoinedChannelBotJoins() {
	received := make(chan string, 1)
	s.bot.OnChannelJoin(func(_ context.Context, channelID string) {
		received <- channelID
	})

	ev := &slackevents.MemberJoinedChannelEvent{
		User:    "U123BOT",
		Channel: "C456",
	}
	s.bot.handleMemberJoinedChannel(ev)

	select {
	case chID := <-received:
		require.Equal(s.T(), "C456", chID)
	case <-time.After(time.Second):
		s.T().Fatal("handler not called")
	}
}

func (s *BotSuite) TestHandleMemberJoinedChannelOtherUser() {
	called := false
	s.bot.OnChannelJoin(func(_ context.Context, _ string) {
		called = true
	})

	ev := &slackevents.MemberJoinedChannelEvent{
		User:    "U999OTHER",
		Channel: "C456",
	}
	s.bot.handleMemberJoinedChannel(ev)

	// Give goroutine a chance to run (it shouldn't)
	time.Sleep(50 * time.Millisecond)
	require.False(s.T(), called, "handler should not be called for other users")
}

func (s *BotSuite) TestHandleEventsAPIMemberJoinedChannel() {
	received := make(chan string, 1)
	s.bot.OnChannelJoin(func(_ context.Context, channelID string) {
		received <- channelID
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "member_joined_channel",
				Data: &slackevents.MemberJoinedChannelEvent{
					User:    "U123BOT",
					Channel: "C789",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEventsAPI(evt)

	select {
	case chID := <-received:
		require.Equal(s.T(), "C789", chID)
	case <-time.After(time.Second):
		s.T().Fatal("handler not called")
	}
}

// --- Channel deletion/archive event tests ---

func (s *BotSuite) TestHandleEventsAPIChannelDeleted() {
	received := make(chan string, 1)
	s.bot.OnChannelDelete(func(_ context.Context, channelID string, isThread bool) {
		require.False(s.T(), isThread)
		received <- channelID
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "channel_deleted",
				Data: &slackevents.ChannelDeletedEvent{
					Channel: "C111",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEventsAPI(evt)

	select {
	case chID := <-received:
		require.Equal(s.T(), "C111", chID)
	case <-time.After(time.Second):
		s.T().Fatal("handler not called")
	}
}

func (s *BotSuite) TestHandleEventsAPIGroupDeleted() {
	received := make(chan string, 1)
	s.bot.OnChannelDelete(func(_ context.Context, channelID string, _ bool) {
		received <- channelID
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "group_deleted",
				Data: &slackevents.GroupDeletedEvent{
					Channel: "G222",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEventsAPI(evt)

	select {
	case chID := <-received:
		require.Equal(s.T(), "G222", chID)
	case <-time.After(time.Second):
		s.T().Fatal("handler not called")
	}
}

func (s *BotSuite) TestNotifyChannelDeleteNoHandlers() {
	// Should not panic with no handlers registered
	s.bot.notifyChannelDelete("C123")
}

func (s *BotSuite) TestHandleMessageSelfMentionSkipped() {
	// Bot's own messages (without self-mention) should be skipped
	ev := &slackevents.MessageEvent{
		User:      "U123BOT",
		Text:      "hello from bot",
		Channel:   "C123",
		TimeStamp: "1234567890.000001",
	}

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case <-received:
		s.Fail("should not receive bot's own message")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func (s *BotSuite) TestHandleMessageInThread() {
	// Thread reply to a bot-started thread (no @mention, just a reply).
	ev := &slackevents.MessageEvent{
		User:            "U456",
		Text:            "follow up in thread",
		Channel:         "C123",
		TimeStamp:       "1234567890.000003",
		ThreadTimeStamp: "1234567890.000001",
		ChannelType:     "channel",
	}

	// isReplyToBot returns true — parent message was from the bot.
	s.session.On("GetConversationReplies", mock.Anything).
		Return([]goslack.Message{{Msg: goslack.Msg{User: "U123BOT"}}}, false, "", nil)

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
		require.True(s.T(), msg.IsReplyToBot)
	case <-time.After(time.Second):
		s.Fail("timeout")
	}
}

func (s *BotSuite) TestHandleMessageSelfMentionCreatesThread() {
	// Bot self-mention in a channel (e.g. from CreateThread) should be
	// processed by handleMessage and use the message's own TS as thread TS.
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U123BOT",
		Text:        "<@U123BOT> Review the codebase",
		Channel:     "C123",
		TimeStamp:   "1234567890.000001",
		ChannelType: "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
		require.Equal(s.T(), "Review the codebase", msg.Content)
		require.True(s.T(), msg.IsBotMention)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for self-mention message")
	}
}

func (s *BotSuite) TestHandleMessageSelfMentionInThread() {
	// Bot self-mention inside a thread should use the thread TS, not the message TS.
	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})

	// isReplyToBot is called for threaded messages.
	s.session.On("GetConversationReplies", mock.AnythingOfType("*slack.GetConversationRepliesParameters")).
		Return([]goslack.Message{{Msg: goslack.Msg{User: "U123BOT"}}}, false, "", nil)

	ev := &slackevents.MessageEvent{
		User:            "U123BOT",
		Text:            "<@U123BOT> do something",
		Channel:         "C123",
		TimeStamp:       "1234567891.000001",
		ThreadTimeStamp: "1234567890.000001",
		ChannelType:     "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
		require.Equal(s.T(), "do something", msg.Content)
		require.True(s.T(), msg.IsBotMention)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for self-mention in thread")
	}
}

func (s *BotSuite) TestHandleMessageMentionDM() {
	// @mentions in DMs should be dispatched with both IsDM and IsBotMention.
	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "<@U123BOT> hello",
		Channel:     "D123",
		TimeStamp:   "1234567890.000001",
		ChannelType: "im",
	}

	received := make(chan *orchestrator.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received <- msg
	})
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsDM)
		require.True(s.T(), msg.IsBotMention)
		require.Equal(s.T(), "hello", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for DM mention")
	}
}

// --- GetMemberRoles ---

func (s *BotSuite) TestGetMemberRolesReturnsNil() {
	roles, err := s.bot.GetMemberRoles(context.Background(), "team-1", "user-1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), roles)
}

// --- parseAllow / parseDeny / extractUserID ---

func (s *BotSuite) TestParseAllowUserMember() {
	inter, errText := parseSlashCommand("C123", "T123", "allow user <@U123456> member")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "allow_user", inter.CommandName)
	require.Equal(s.T(), "U123456", inter.Options["target_id"])
	require.Equal(s.T(), "member", inter.Options["role"])
}

func (s *BotSuite) TestParseAllowUserOwner() {
	inter, errText := parseSlashCommand("C123", "T123", "allow user <@U123456> owner")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "allow_user", inter.CommandName)
	require.Equal(s.T(), "U123456", inter.Options["target_id"])
	require.Equal(s.T(), "owner", inter.Options["role"])
}

func (s *BotSuite) TestParseAllowUserDefaultMember() {
	// No role specified — defaults to "member".
	inter, errText := parseSlashCommand("C123", "T123", "allow user <@U123456>")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "allow_user", inter.CommandName)
	require.Equal(s.T(), "member", inter.Options["role"])
}

func (s *BotSuite) TestParseAllowUserWithPipe() {
	// Slack mentions with |username suffix.
	inter, errText := parseSlashCommand("C123", "T123", "allow user <@U123456|alice>")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "U123456", inter.Options["target_id"])
}

func (s *BotSuite) TestParseAllowUserInvalidMention() {
	_, errText := parseSlashCommand("C123", "T123", "allow user U123456")
	require.Contains(s.T(), errText, "Invalid user")
}

func (s *BotSuite) TestParseAllowRoleError() {
	_, errText := parseSlashCommand("C123", "T123", "allow role admin")
	require.Contains(s.T(), errText, "Discord-only")
}

func (s *BotSuite) TestParseAllowUnknownSubcommand() {
	_, errText := parseSlashCommand("C123", "T123", "allow unknown <@U123>")
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseAllowTooFewArgs() {
	_, errText := parseSlashCommand("C123", "T123", "allow")
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseDenyUserSuccess() {
	inter, errText := parseSlashCommand("C123", "T123", "deny user <@U654321>")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "deny_user", inter.CommandName)
	require.Equal(s.T(), "U654321", inter.Options["target_id"])
}

func (s *BotSuite) TestParseDenyUserInvalidMention() {
	_, errText := parseSlashCommand("C123", "T123", "deny user notamention")
	require.Contains(s.T(), errText, "Invalid user")
}

func (s *BotSuite) TestParseDenyRoleError() {
	_, errText := parseSlashCommand("C123", "T123", "deny role admin")
	require.Contains(s.T(), errText, "Discord-only")
}

func (s *BotSuite) TestParseDenyUnknownSubcommand() {
	_, errText := parseSlashCommand("C123", "T123", "deny unknown <@U123>")
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestParseDenyTooFewArgs() {
	_, errText := parseSlashCommand("C123", "T123", "deny")
	require.Contains(s.T(), errText, "Usage:")
}

func (s *BotSuite) TestExtractUserIDValid() {
	require.Equal(s.T(), "U123456", extractUserID("<@U123456>"))
}

func (s *BotSuite) TestExtractUserIDWithPipe() {
	require.Equal(s.T(), "U123456", extractUserID("<@U123456|alice>"))
}

func (s *BotSuite) TestExtractUserIDInvalid() {
	require.Equal(s.T(), "", extractUserID("U123456"))
	require.Equal(s.T(), "", extractUserID("<U123456>"))
	require.Equal(s.T(), "", extractUserID("@U123456"))
}

func (s *BotSuite) TestExtractUserIDWhitespace() {
	require.Equal(s.T(), "U123456", extractUserID("  <@U123456>  "))
}
