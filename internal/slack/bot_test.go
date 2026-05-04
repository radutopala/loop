package slack

import (
	"context"
	"log/slog"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
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
