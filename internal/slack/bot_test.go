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

func (m *MockSession) UpdateMessage(channelID, timestamp string, options ...goslack.MsgOption) (string, string, string, error) {
	args := m.Called(channelID, timestamp, options)
	return args.String(0), args.String(1), args.String(2), args.Error(3)
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

func (s *BotSuite) TestSendMessage() {
	longContent := strings.Repeat("a", maxMessageLen+100)

	tests := []struct {
		name      string
		msg       *orchestrator.OutgoingMessage
		postErr   error
		wantErr   string
		wantCalls int
	}{
		{"plain", &orchestrator.OutgoingMessage{ChannelID: "C123", Content: "hello world"}, nil, "", 1},
		{"thread", &orchestrator.OutgoingMessage{ChannelID: "C123:1111.2222", Content: "reply in thread"}, nil, "", 1},
		{"reply_to", &orchestrator.OutgoingMessage{ChannelID: "C123", Content: "replying", ReplyToMessageID: "9999.0000"}, nil, "", 1},
		{"split", &orchestrator.OutgoingMessage{ChannelID: "C123", Content: longContent}, nil, "", 2},
		{"error", &orchestrator.OutgoingMessage{ChannelID: "C123", Content: "hello"}, errors.New("channel_not_found"), "slack send message", 1},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)
			}

			err := bot.SendMessage(context.Background(), tt.msg)
			if tt.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertNumberOfCalls(s.T(), "PostMessage", tt.wantCalls)
		})
	}
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
	s.bot.OnInteraction(func(_ context.Context, _ *orchestrator.Interaction) {})
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

func (s *BotSuite) TestCreateThreadSuccess() {
	tests := []struct {
		name     string
		channel  string
		thread   string
		authorID string
		message  string
	}{
		{
			name:     "with explicit message",
			channel:  "C123",
			thread:   "my thread",
			authorID: "U456",
			message:  "<@U123BOT> do something",
		},
		{
			name:     "with mention (empty message)",
			channel:  "C123",
			thread:   "my thread",
			authorID: "U456",
			message:  "",
		},
		{
			name:     "default (no author, no message)",
			channel:  "C123",
			thread:   "my thread",
			authorID: "",
			message:  "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"
			bot.botUsername = "loopbot"

			session.On("PostMessage", tt.channel, mock.Anything).Return(tt.channel, "9999.0000", nil)

			id, err := bot.CreateThread(context.Background(), tt.channel, tt.thread, tt.authorID, tt.message)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "C123:9999.0000", id)
		})
	}
}

func (s *BotSuite) TestCreateThreadError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("not_in_channel"))

	_, err := s.bot.CreateThread(context.Background(), "C123", "thread", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack create thread")
}

// --- CreateSimpleThread ---

func (s *BotSuite) TestCreateSimpleThread() {
	tests := []struct {
		name    string
		parent  string
		thName  string
		msg     string
		postTS  string
		postErr error
		wantID  string
		wantErr string
	}{
		{"with_message", "C123", "task output", "First turn", "1111.2222", nil, "C123:1111.2222", ""},
		{"empty_message", "C123", "task name", "", "2222.3333", nil, "C123:2222.3333", ""},
		{"composite_parent", "C123:old.ts", "task", "content", "3333.4444", nil, "C123:3333.4444", ""},
		{"error", "C123", "task", "content", "", errors.New("post failed"), "", "slack create simple thread"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"
			bot.botUsername = "loopbot"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", tt.postTS, nil)
			}

			id, err := bot.CreateSimpleThread(context.Background(), tt.parent, tt.thName, tt.msg)
			if tt.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantID, id)
			}
		})
	}
}

// --- PostMessage ---

func (s *BotSuite) TestPostMessage() {
	tests := []struct {
		name      string
		channelID string
		text      string
		postErr   error
		wantErr   string
	}{
		{"plain", "C123", "hello", nil, ""},
		{"composite", "C123:1111.2222", "reply", nil, ""},
		{"mention_conversion", "C123", "hey @loopbot do this", nil, ""},
		{"error", "C123", "hello", errors.New("channel_not_found"), "slack post message"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"
			bot.botUsername = "loopbot"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)
			}

			err := bot.PostMessage(context.Background(), tt.channelID, tt.text)
			if tt.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
		})
	}
}

// --- DeleteThread ---

func (s *BotSuite) TestDeleteThreadSuccess() {
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.2222"}},
		{Msg: goslack.Msg{Timestamp: "1111.3333"}},
	}, false, "", nil)
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("C123", "1111.2222", nil)
	s.session.On("DeleteMessage", "C123", "1111.3333").Return("C123", "1111.3333", nil)

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "DeleteMessage", 2)
}

func (s *BotSuite) TestDeleteThreadPaginated() {
	// First page returns one message and a cursor
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.2222"}},
	}, false, "cursor1", nil)
	// Second page returns another message and empty cursor
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
		Cursor:    "cursor1",
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.3333"}},
	}, false, "", nil)
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("C123", "1111.2222", nil)
	s.session.On("DeleteMessage", "C123", "1111.3333").Return("C123", "1111.3333", nil)

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "DeleteMessage", 2)
}

func (s *BotSuite) TestDeleteThreadInvalidID() {
	err := s.bot.DeleteThread(context.Background(), "C123")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid thread ID")
}

func (s *BotSuite) TestDeleteThreadGetRepliesError() {
	s.session.On("GetConversationReplies", mock.Anything).Return(nil, false, "", errors.New("fetch error"))

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack get thread replies")
}

func (s *BotSuite) TestDeleteThreadDeleteMessageError() {
	s.session.On("GetConversationReplies", mock.Anything).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.2222"}},
	}, false, "", nil)
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("", "", errors.New("delete failed"))

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err) // individual delete errors are logged, not returned
}

// --- RenameThread ---

func (s *BotSuite) TestRenameThreadSuccess() {
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
		Limit:     1,
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Text: "🧵 task #1 (`5m`) agent reply"}},
	}, false, "", nil)
	s.session.On("UpdateMessage", "C123", "1111.2222", mock.Anything).Return("C123", "1111.2222", "", nil)

	err := s.bot.RenameThread(context.Background(), "C123:1111.2222", "💨 task #1 (`5m`) prompt")
	require.NoError(s.T(), err)

	// Verify the updated text has 💨 and no [EPHEMERAL]
	call := s.session.Calls[1] // UpdateMessage call
	opts := call.Arguments[2].([]goslack.MsgOption)
	require.NotEmpty(s.T(), opts)
}

func (s *BotSuite) TestRenameThreadSlackShortcode() {
	// Slack returns emoji as shortcodes (e.g. :thread: instead of 🧵)
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
		Limit:     1,
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Text: ":thread: task #1 (`5m`) agent reply"}},
	}, false, "", nil)
	s.session.On("UpdateMessage", "C123", "1111.2222", mock.Anything).Return("C123", "1111.2222", "", nil)

	err := s.bot.RenameThread(context.Background(), "C123:1111.2222", "💨 task #1 (`5m`) prompt")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestRenameThreadStripsEphemeral() {
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
		Limit:     1,
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Text: "🧵 task #1 (`5m`) [EPHEMERAL]\nNo ready work tickets."}},
	}, false, "", nil)
	s.session.On("UpdateMessage", "C123", "1111.2222", mock.Anything).Return("C123", "1111.2222", "", nil)

	err := s.bot.RenameThread(context.Background(), "C123:1111.2222", "💨 task #1 (`5m`)")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestRenameThreadErrors() {
	tests := []struct {
		name    string
		id      string
		replies []goslack.Message
		replErr error
		updErr  error
		wantErr string
	}{
		{"invalid_id", "C123", nil, nil, nil, "invalid thread ID"},
		{"get_replies_error", "C123:1111.2222", nil, errors.New("fetch error"), nil, "slack get parent message"},
		{"empty_replies", "C123:1111.2222", []goslack.Message{}, nil, nil, "parent message not found"},
		{"update_error", "C123:1111.2222", []goslack.Message{{Msg: goslack.Msg{Text: "🧵 original"}}}, nil, errors.New("update_failed"), "slack rename thread"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.id != "C123" { // has valid composite ID
				session.On("GetConversationReplies", mock.Anything).Return(tt.replies, false, "", tt.replErr)
				if tt.updErr != nil {
					session.On("UpdateMessage", "C123", "1111.2222", mock.Anything).Return("", "", "", tt.updErr)
				}
			}

			err := bot.RenameThread(context.Background(), tt.id, "new name")
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

// --- GetChannelName ---

func (s *BotSuite) TestGetChannelName() {
	tests := []struct {
		name      string
		channelID string
		infoErr   error
		wantName  string
		wantErr   bool
	}{
		{"success", "C123", nil, "general", false},
		{"composite_id", "C123:1111.2222", nil, "general", false},
		{"error", "C123", errors.New("channel_not_found"), "", true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.infoErr != nil {
				session.On("GetConversationInfo", mock.Anything).Return(nil, tt.infoErr)
			} else {
				session.On("GetConversationInfo", &goslack.GetConversationInfoInput{ChannelID: "C123"}).
					Return(&goslack.Channel{GroupConversation: goslack.GroupConversation{Name: "general"}}, nil)
			}

			name, err := bot.GetChannelName(context.Background(), tt.channelID)
			if tt.wantErr {
				require.Error(s.T(), err)
				require.Empty(s.T(), name)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantName, name)
			}
		})
	}
}

// --- GetChannelParentID ---

func (s *BotSuite) TestGetChannelParentID() {
	tests := []struct {
		name   string
		input  string
		wantID string
	}{
		{"thread", "C123:1111.2222", "C123"},
		{"not_thread", "C123", ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			parentID, err := s.bot.GetChannelParentID(context.Background(), tt.input)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantID, parentID)
		})
	}
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
	tests := []struct {
		name   string
		input  string
		expect time.Time
	}{
		{"valid", "1234567890.123456", time.Unix(1234567890, 0)},
		{"invalid", "abc", time.Time{}},
		{"empty", "", time.Time{}},
		{"non_digit", "abc.123", time.Time{}},
		{"dot_prefix", ".123456", time.Time{}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := slackTSToTime(tt.input)
			if tt.expect.IsZero() {
				require.True(s.T(), result.IsZero())
			} else {
				require.Equal(s.T(), tt.expect, result)
			}
		})
	}
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

func (s *BotSuite) TestHandleMessageIgnored() {
	tests := []struct {
		name string
		ev   *slackevents.MessageEvent
	}{
		{"subtype", &slackevents.MessageEvent{User: "U456", Text: "edited", TimeStamp: "1234567890.000001", Channel: "C123", SubType: "message_changed"}},
		{"no_trigger", &slackevents.MessageEvent{User: "U456", Text: "just a random message", TimeStamp: "1234567890.000001", Channel: "C123", ChannelType: "channel"}},
		{"self_no_mention", &slackevents.MessageEvent{User: "U123BOT", Text: "hello from bot", Channel: "C123", TimeStamp: "1234567890.000001"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			received := make(chan *orchestrator.IncomingMessage, 1)
			s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) { received <- msg })
			s.bot.handleMessage(tt.ev)

			select {
			case <-received:
				s.Fail("should not have received message")
			case <-time.After(50 * time.Millisecond):
				// expected
			}
		})
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

func (s *BotSuite) TestHandleSlashCommandDispatched() {
	tests := []struct {
		name     string
		cmd      goslack.SlashCommand
		wantCmd  string
		wantOpts map[string]string
		wantAuth string
	}{
		{"schedule", goslack.SlashCommand{ChannelID: "C123", TeamID: "T123", Text: "schedule 0 9 * * * cron Check for updates"},
			"schedule", map[string]string{"schedule": "0 9 * * *", "type": "cron", "prompt": "Check for updates"}, ""},
		{"tasks", goslack.SlashCommand{ChannelID: "C123", TeamID: "T123", Text: "tasks"},
			"tasks", nil, ""},
		{"author_id", goslack.SlashCommand{ChannelID: "C123", TeamID: "T123", UserID: "U789", Text: "tasks"},
			"tasks", nil, "U789"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			received := make(chan *orchestrator.Interaction, 1)
			bot.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) { received <- i })
			sc.On("Ack", mock.Anything, mock.Anything).Return()

			evt := socketmode.Event{
				Type: socketmode.EventTypeSlashCommand, Data: tt.cmd, Request: &socketmode.Request{},
			}
			bot.handleSlashCommand(evt)

			select {
			case inter := <-received:
				require.Equal(s.T(), tt.wantCmd, inter.CommandName)
				for k, v := range tt.wantOpts {
					require.Equal(s.T(), v, inter.Options[k], "option %s", k)
				}
				if tt.wantAuth != "" {
					require.Equal(s.T(), tt.wantAuth, inter.AuthorID)
				}
			case <-time.After(time.Second):
				s.Fail("timeout waiting for interaction")
			}
		})
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

func (s *BotSuite) TestParseSlashCommand() {
	tests := []struct {
		name       string
		input      string
		wantCmd    string
		wantOpts   map[string]string
		wantErrSub string // non-empty means error expected
	}{
		// schedule
		{"schedule_cron", "schedule 0 9 * * * cron Check updates", "schedule", map[string]string{"schedule": "0 9 * * *", "type": "cron", "prompt": "Check updates"}, ""},
		{"schedule_interval", "schedule 1h interval Run checks", "schedule", map[string]string{"schedule": "1h", "type": "interval", "prompt": "Run checks"}, ""},
		{"schedule_too_few", "schedule foo bar", "", nil, "Usage:"},
		{"schedule_type_at_start", "schedule cron prompt text", "", nil, "Usage:"},
		{"schedule_type_at_end", "schedule daily cron", "", nil, "Usage:"},
		{"schedule_no_type", "schedule daily something prompt", "", nil, "Usage:"},
		// cancel
		{"cancel", "cancel 42", "cancel", map[string]string{"task_id": "42"}, ""},
		{"cancel_invalid_id", "cancel abc", "", nil, "Invalid task_id"},
		{"cancel_no_args", "cancel", "", nil, "Usage:"},
		// toggle
		{"toggle", "toggle 7", "toggle", map[string]string{"task_id": "7"}, ""},
		{"toggle_no_args", "toggle", "", nil, "Usage:"},
		{"toggle_invalid_id", "toggle abc", "", nil, "Invalid task_id"},
		// edit
		{"edit_all_flags", "edit 5 --schedule 1h --type interval --prompt New task prompt text", "edit", map[string]string{"task_id": "5", "schedule": "1h", "type": "interval", "prompt": "New task prompt text"}, ""},
		{"edit_no_flags", "edit 5", "", nil, "At least one of"},
		{"edit_no_args", "edit", "", nil, "Usage:"},
		{"edit_invalid_id", "edit abc", "", nil, "Invalid task_id"},
		{"edit_schedule_no_val", "edit 1 --schedule", "", nil, "--schedule requires a value"},
		{"edit_type_no_val", "edit 1 --type", "", nil, "--type requires a value"},
		{"edit_prompt_no_val", "edit 1 --prompt", "", nil, "--prompt requires a value"},
		{"edit_with_prompt", "edit 1 --prompt hello world", "edit", map[string]string{"prompt": "hello world"}, ""},
		{"edit_with_type", "edit 1 --type cron", "edit", map[string]string{"type": "cron"}, ""},
		// simple commands
		{"status", "status", "status", nil, ""},
		{"stop", "stop", "stop", nil, ""},
		{"readme", "readme", "readme", nil, ""},
		{"iamtheowner", "iamtheowner", "iamtheowner", nil, ""},
		{"tasks", "tasks", "tasks", nil, ""},
		// template
		{"template_add", "template add daily-check", "template-add", map[string]string{"name": "daily-check"}, ""},
		{"template_list", "template list", "template-list", nil, ""},
		{"template_no_args", "template", "", nil, "Usage:"},
		{"template_unknown_sub", "template foo", "", nil, "Usage:"},
		{"template_add_no_name", "template add", "", nil, "Usage:"},
		// errors
		{"empty", "", "", nil, "Available commands"},
		{"unknown", "foo", "", nil, "Unknown subcommand: foo"},
		// help text checks
		{"help_has_stop", "", "", nil, "/loop stop"},
		{"help_has_readme", "", "", nil, "/loop readme"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			inter, errText := parseSlashCommand("C123", "T123", tt.input)
			if tt.wantErrSub != "" {
				require.Contains(s.T(), errText, tt.wantErrSub)
				return
			}
			require.Empty(s.T(), errText)
			require.NotNil(s.T(), inter)
			require.Equal(s.T(), tt.wantCmd, inter.CommandName)
			require.Equal(s.T(), "C123", inter.ChannelID)
			require.Equal(s.T(), "T123", inter.GuildID)
			for k, v := range tt.wantOpts {
				require.Equal(s.T(), v, inter.Options[k], "option %s", k)
			}
		})
	}
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

func (s *BotSuite) TestHandleEventInvalidData() {
	// All should return without panic
	for _, evt := range []socketmode.Event{
		{Type: socketmode.EventTypeSlashCommand, Data: "not a slash command"},
		{Type: socketmode.EventTypeEventsAPI, Data: "not an events api event"},
		{Type: socketmode.EventType("unknown"), Data: nil},
	} {
		s.bot.handleEvent(evt)
	}
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

// --- Additional bot edge cases ---

func (s *BotSuite) TestSendTypingRemoveReactionErrors() {
	for _, removeErr := range []string{"remove error", "no_reaction"} {
		s.Run(removeErr, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			ref := goslack.NewRefToMessage("C123", "1234.5678")
			bot.lastMessageRef.Store("C123", ref)

			session.On("AddReaction", "eyes", ref).Return(nil)
			session.On("RemoveReaction", "eyes", ref).Return(errors.New(removeErr))

			ctx, cancel := context.WithCancel(context.Background())
			err := bot.SendTyping(ctx, "C123")
			require.NoError(s.T(), err)

			cancel()
			time.Sleep(50 * time.Millisecond)
			session.AssertCalled(s.T(), "RemoveReaction", "eyes", ref)
		})
	}
}

func (s *BotSuite) TestIsReplyToBotFalse() {
	tests := []struct {
		name    string
		replies []goslack.Message
		err     error
	}{
		{"api_error", nil, errors.New("api error")},
		{"empty_replies", []goslack.Message{}, nil},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			ev := &slackevents.MessageEvent{
				User: "U456", Channel: "C123",
				ThreadTimeStamp: "1234567890.000001", TimeStamp: "1234567890.000002",
			}
			session.On("GetConversationReplies", mock.Anything).Return(tt.replies, false, "", tt.err)
			require.False(s.T(), bot.isReplyToBot(ev))
		})
	}
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

func (s *BotSuite) TestHandleEventsAPIChannelGroupDeleted() {
	tests := []struct {
		name      string
		eventType string
		data      any
		expectID  string
	}{
		{"channel_deleted", "channel_deleted", &slackevents.ChannelDeletedEvent{Channel: "C111"}, "C111"},
		{"group_deleted", "group_deleted", &slackevents.GroupDeletedEvent{Channel: "G222"}, "G222"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			received := make(chan string, 1)
			bot.OnChannelDelete(func(_ context.Context, channelID string, _ bool) {
				received <- channelID
			})

			sc.On("Ack", mock.Anything, mock.Anything).Return()

			evt := socketmode.Event{
				Type: socketmode.EventTypeEventsAPI,
				Data: slackevents.EventsAPIEvent{
					InnerEvent: slackevents.EventsAPIInnerEvent{Type: tt.eventType, Data: tt.data},
				},
				Request: &socketmode.Request{},
			}
			bot.handleEventsAPI(evt)

			select {
			case chID := <-received:
				require.Equal(s.T(), tt.expectID, chID)
			case <-time.After(time.Second):
				s.T().Fatal("handler not called")
			}
		})
	}
}

func (s *BotSuite) TestNotifyChannelDeleteNoHandlers() {
	// Should not panic with no handlers registered
	s.bot.notifyChannelDelete("C123")
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

func (s *BotSuite) TestParseAllowDeny() {
	tests := []struct {
		name       string
		input      string
		wantCmd    string
		wantOpts   map[string]string
		wantErrSub string
	}{
		{"allow_user_member", "allow user <@U123456> member", "allow_user", map[string]string{"target_id": "U123456", "role": "member"}, ""},
		{"allow_user_owner", "allow user <@U123456> owner", "allow_user", map[string]string{"target_id": "U123456", "role": "owner"}, ""},
		{"allow_user_default", "allow user <@U123456>", "allow_user", map[string]string{"role": "member"}, ""},
		{"allow_user_pipe", "allow user <@U123456|alice>", "allow_user", map[string]string{"target_id": "U123456"}, ""},
		{"allow_user_invalid", "allow user U123456", "", nil, "Invalid user"},
		{"allow_role_error", "allow role admin", "", nil, "Discord-only"},
		{"allow_unknown_sub", "allow unknown <@U123>", "", nil, "Usage:"},
		{"allow_too_few", "allow", "", nil, "Usage:"},
		{"deny_user_success", "deny user <@U654321>", "deny_user", map[string]string{"target_id": "U654321"}, ""},
		{"deny_user_invalid", "deny user notamention", "", nil, "Invalid user"},
		{"deny_role_error", "deny role admin", "", nil, "Discord-only"},
		{"deny_unknown_sub", "deny unknown <@U123>", "", nil, "Usage:"},
		{"deny_too_few", "deny", "", nil, "Usage:"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			inter, errText := parseSlashCommand("C123", "T123", tt.input)
			if tt.wantErrSub != "" {
				require.Contains(s.T(), errText, tt.wantErrSub)
				return
			}
			require.Empty(s.T(), errText)
			require.NotNil(s.T(), inter)
			require.Equal(s.T(), tt.wantCmd, inter.CommandName)
			for k, v := range tt.wantOpts {
				require.Equal(s.T(), v, inter.Options[k], "option %s", k)
			}
		})
	}
}

func (s *BotSuite) TestExtractUserID() {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"valid", "<@U123456>", "U123456"},
		{"with_pipe", "<@U123456|alice>", "U123456"},
		{"no_at_prefix", "U123456", ""},
		{"no_at_sign", "<U123456>", ""},
		{"no_angle", "@U123456", ""},
		{"whitespace", "  <@U123456>  ", "U123456"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.expect, extractUserID(tt.input))
		})
	}
}

// --- Stop button tests ---

func (s *BotSuite) TestSendStopButton() {
	tests := []struct {
		name      string
		channelID string
		postErr   error
		wantID    string
		wantErr   bool
	}{
		{"success", "C123", nil, "1234567890.123456", false},
		{"error", "C123", errors.New("post failed"), "", true},
		{"thread", "C123:1111111111.000", nil, "1234567890.999", false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", tt.wantID, nil)
			}

			msgID, err := bot.SendStopButton(context.Background(), tt.channelID, "run-1")
			if tt.wantErr {
				require.Error(s.T(), err)
				require.Empty(s.T(), msgID)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantID, msgID)
			}
		})
	}
}

func (s *BotSuite) TestRemoveStopButton() {
	tests := []struct {
		name      string
		channelID string
		delErr    error
		wantErr   bool
	}{
		{"success", "C123", nil, false},
		{"error", "C123", errors.New("delete failed"), true},
		{"thread", "C123:1111111111.000", nil, false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.delErr != nil {
				session.On("DeleteMessage", "C123", "1234567890.123456").Return("", "", tt.delErr)
			} else {
				session.On("DeleteMessage", "C123", "1234567890.123456").Return("C123", "1234567890.123456", nil)
			}

			err := bot.RemoveStopButton(context.Background(), tt.channelID, "1234567890.123456")
			if tt.wantErr {
				require.Error(s.T(), err)
			} else {
				require.NoError(s.T(), err)
			}
		})
	}
}

// --- handleInteractive tests ---

func (s *BotSuite) TestHandleInteractiveStopAction() {
	received := make(chan *orchestrator.Interaction, 1)
	s.bot.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			Channel: goslack.Channel{
				GroupConversation: goslack.GroupConversation{
					Conversation: goslack.Conversation{ID: "C123"},
				},
			},
			Team: goslack.Team{ID: "T123"},
			User: goslack.User{ID: "U456"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{
					{ActionID: "stop:target-ch"},
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleInteractive(evt)

	select {
	case inter := <-received:
		require.Equal(s.T(), "stop", inter.CommandName)
		require.Equal(s.T(), "target-ch", inter.Options["channel_id"])
		require.Equal(s.T(), "C123", inter.ChannelID)
		require.Equal(s.T(), "T123", inter.GuildID)
		require.Equal(s.T(), "U456", inter.AuthorID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}

func (s *BotSuite) TestHandleInteractiveIgnored() {
	tests := []struct {
		name string
		evt  socketmode.Event
	}{
		{"non_block_actions", socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    goslack.InteractionCallback{Type: goslack.InteractionTypeDialogSubmission},
			Request: &socketmode.Request{},
		}},
		{"non_stop_action", socketmode.Event{
			Type: socketmode.EventTypeInteractive,
			Data: goslack.InteractionCallback{
				Type:           goslack.InteractionTypeBlockActions,
				ActionCallback: goslack.ActionCallbacks{BlockActions: []*goslack.BlockAction{{ActionID: "other:something"}}},
			},
			Request: &socketmode.Request{},
		}},
		{"invalid_data", socketmode.Event{
			Type: socketmode.EventTypeInteractive,
			Data: "not-a-callback",
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			called := false
			bot.OnInteraction(func(_ context.Context, _ *orchestrator.Interaction) { called = true })
			sc.On("Ack", mock.Anything, mock.Anything).Return()

			bot.handleInteractive(tt.evt)
			require.False(s.T(), called)
		})
	}
}

func (s *BotSuite) TestHandleEventInteractiveType() {
	received := make(chan *orchestrator.Interaction, 1)
	s.bot.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			Channel: goslack.Channel{
				GroupConversation: goslack.GroupConversation{
					Conversation: goslack.Conversation{ID: "C123"},
				},
			},
			Team: goslack.Team{ID: "T123"},
			User: goslack.User{ID: "U456"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{
					{ActionID: "stop:ch-1"},
				},
			},
		},
		Request: &socketmode.Request{},
	}
	// Call handleEvent directly to cover the EventTypeInteractive branch
	s.bot.handleEvent(evt)

	select {
	case inter := <-received:
		require.Equal(s.T(), "stop", inter.CommandName)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}
