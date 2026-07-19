package discord

import (
	"context"
	"log/slog"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/orchestrator"
)

// Compile-time check that DiscordBot implements orchestrator.Bot.
var _ orchestrator.Bot = (*DiscordBot)(nil)

// --- Mock DiscordSession ---

type MockSession struct {
	mock.Mock
}

func (m *MockSession) Open() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSession) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSession) AddHandler(handler any) func() {
	args := m.Called(handler)
	return args.Get(0).(func())
}

func (m *MockSession) User(userID string, options ...discordgo.RequestOption) (*discordgo.User, error) {
	args := m.Called(userID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.User), args.Error(1)
}

func (m *MockSession) ChannelMessageSend(channelID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	args := m.Called(channelID, content, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Message), args.Error(1)
}

func (m *MockSession) ChannelMessageSendReply(channelID string, content string, reference *discordgo.MessageReference, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	args := m.Called(channelID, content, reference, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Message), args.Error(1)
}

func (m *MockSession) ChannelTyping(channelID string, options ...discordgo.RequestOption) error {
	args := m.Called(channelID, options)
	return args.Error(0)
}

func (m *MockSession) ApplicationCommandCreate(appID string, guildID string, cmd *discordgo.ApplicationCommand, options ...discordgo.RequestOption) (*discordgo.ApplicationCommand, error) {
	args := m.Called(appID, guildID, cmd, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.ApplicationCommand), args.Error(1)
}

func (m *MockSession) ApplicationCommands(appID string, guildID string, options ...discordgo.RequestOption) ([]*discordgo.ApplicationCommand, error) {
	args := m.Called(appID, guildID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*discordgo.ApplicationCommand), args.Error(1)
}

func (m *MockSession) ApplicationCommandDelete(appID string, guildID string, cmdID string, options ...discordgo.RequestOption) error {
	args := m.Called(appID, guildID, cmdID, options)
	return args.Error(0)
}

func (m *MockSession) InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error {
	args := m.Called(interaction, resp, options)
	return args.Error(0)
}

func (m *MockSession) InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	args := m.Called(interaction, newresp, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Message), args.Error(1)
}

func (m *MockSession) FollowupMessageCreate(interaction *discordgo.Interaction, wait bool, data *discordgo.WebhookParams, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	args := m.Called(interaction, wait, data, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Message), args.Error(1)
}

func (m *MockSession) GuildChannelCreate(guildID string, name string, ctype discordgo.ChannelType, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	args := m.Called(guildID, name, ctype, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Channel), args.Error(1)
}

func (m *MockSession) Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	args := m.Called(channelID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Channel), args.Error(1)
}

func (m *MockSession) ThreadStart(channelID string, name string, typ discordgo.ChannelType, archiveDuration int, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	args := m.Called(channelID, name, typ, archiveDuration, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Channel), args.Error(1)
}

func (m *MockSession) ThreadJoin(id string, options ...discordgo.RequestOption) error {
	args := m.Called(id, options)
	return args.Error(0)
}

func (m *MockSession) ChannelDelete(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	args := m.Called(channelID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Channel), args.Error(1)
}

func (m *MockSession) GuildChannels(guildID string, options ...discordgo.RequestOption) ([]*discordgo.Channel, error) {
	args := m.Called(guildID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*discordgo.Channel), args.Error(1)
}

func (m *MockSession) ChannelEdit(channelID string, data *discordgo.ChannelEdit, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	args := m.Called(channelID, data, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Channel), args.Error(1)
}

func (m *MockSession) GuildMember(guildID string, userID string, options ...discordgo.RequestOption) (*discordgo.Member, error) {
	args := m.Called(guildID, userID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Member), args.Error(1)
}

func (m *MockSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	args := m.Called(channelID, data, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Message), args.Error(1)
}

func (m *MockSession) ChannelMessageDelete(channelID string, messageID string, options ...discordgo.RequestOption) error {
	args := m.Called(channelID, messageID, options)
	return args.Error(0)
}

func (m *MockSession) Guild(guildID string, options ...discordgo.RequestOption) (*discordgo.Guild, error) {
	args := m.Called(guildID, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Guild), args.Error(1)
}

func (m *MockSession) ThreadMemberAdd(threadID, memberID string, options ...discordgo.RequestOption) error {
	args := m.Called(threadID, memberID, options)
	return args.Error(0)
}

// --- Test Suite ---

type BotSuite struct {
	suite.Suite
	session *MockSession
	bot     *DiscordBot
	logger  *slog.Logger
}

func TestBotSuite(t *testing.T) {
	suite.Run(t, new(BotSuite))
}

func (s *BotSuite) SetupTest() {
	s.session = new(MockSession)
	s.logger = slog.New(slog.NewTextHandler(discard{}, nil))
	s.bot = NewBot(s.session, "test-app-id", "", s.logger)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestHandleIncomingMessageNoOps pins the two chat-platform no-ops: incoming
// API messages don't route back into Discord, so both must do nothing (and
// not panic) regardless of arguments.
func (s *BotSuite) TestHandleIncomingMessageNoOps() {
	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user", "text", "sess")
	s.bot.HandleIncomingMessageWithPriority(context.Background(), "ch-1", "user", "text", "sess", 5)
}
