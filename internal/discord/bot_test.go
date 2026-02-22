package discord

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/orchestrator"
)

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
	s.bot = NewBot(s.session, "test-app-id", s.logger)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// --- Start / Stop ---

func (s *BotSuite) TestStartSuccess() {
	noop := func() {}
	s.session.On("AddHandler", mock.Anything).Return(noop).Times(5)
	s.session.On("Open").Return(nil)
	s.session.On("User", "@me", mock.Anything).Return(&discordgo.User{ID: "bot-123"}, nil)

	err := s.bot.Start(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "bot-123", s.bot.BotUserID())
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestStartOpenError() {
	noop := func() {}
	s.session.On("AddHandler", mock.Anything).Return(noop).Times(5)
	s.session.On("Open").Return(errors.New("connection failed"))

	err := s.bot.Start(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord session open")
}

func (s *BotSuite) TestStartUserError() {
	noop := func() {}
	s.session.On("AddHandler", mock.Anything).Return(noop).Times(5)
	s.session.On("Open").Return(nil)
	s.session.On("User", "@me", mock.Anything).Return(nil, errors.New("user fetch failed"))

	err := s.bot.Start(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord get bot user")
}

func (s *BotSuite) TestStop() {
	called := false
	remove := func() { called = true }
	s.session.On("AddHandler", mock.Anything).Return(remove).Times(5)
	s.session.On("Open").Return(nil)
	s.session.On("User", "@me", mock.Anything).Return(&discordgo.User{ID: "bot-123"}, nil)
	s.session.On("Close").Return(nil)

	err := s.bot.Start(context.Background())
	require.NoError(s.T(), err)

	err = s.bot.Stop()
	require.NoError(s.T(), err)
	require.True(s.T(), called)
	s.session.AssertExpectations(s.T())

	// removeHandlers should be cleared
	s.bot.mu.RLock()
	require.Nil(s.T(), s.bot.removeHandlers)
	s.bot.mu.RUnlock()
}

// --- SendMessage ---

func (s *BotSuite) TestSendMessageSimple() {
	s.session.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "ch-1",
		Content:   "hello",
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendMessageWithReply() {
	ref := &discordgo.MessageReference{MessageID: "msg-1"}
	s.session.On("ChannelMessageSendReply", "ch-1", "hello", ref, mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID:        "ch-1",
		Content:          "hello",
		ReplyToMessageID: "msg-1",
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendMessageSplit() {
	longContent := strings.Repeat("a", 2500)
	// First chunk is reply (2000 chars), second chunk is plain send (500 chars).
	ref := &discordgo.MessageReference{MessageID: "msg-1"}
	s.session.On("ChannelMessageSendReply", "ch-1", strings.Repeat("a", 2000), ref, mock.Anything).
		Return(&discordgo.Message{}, nil)
	s.session.On("ChannelMessageSend", "ch-1", strings.Repeat("a", 500), mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID:        "ch-1",
		Content:          longContent,
		ReplyToMessageID: "msg-1",
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendMessageReplyError() {
	ref := &discordgo.MessageReference{MessageID: "msg-1"}
	s.session.On("ChannelMessageSendReply", "ch-1", "hello", ref, mock.Anything).
		Return(nil, errors.New("send failed"))

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID:        "ch-1",
		Content:          "hello",
		ReplyToMessageID: "msg-1",
	})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord send reply")
}

func (s *BotSuite) TestSendMessageError() {
	s.session.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).
		Return(nil, errors.New("send failed"))

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "ch-1",
		Content:   "hello",
	})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord send message")
}

// --- SendTyping ---

func (s *BotSuite) TestSendTypingSuccess() {
	s.session.On("ChannelTyping", "ch-1", mock.Anything).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.bot.SendTyping(ctx, "ch-1")
	require.NoError(s.T(), err)
	cancel()
}

func (s *BotSuite) TestSendTypingError() {
	s.session.On("ChannelTyping", "ch-1", mock.Anything).Return(errors.New("typing failed"))

	err := s.bot.SendTyping(context.Background(), "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord typing")
}

// --- RegisterCommands ---

func (s *BotSuite) TestRegisterCommandsSuccess() {
	for _, cmd := range Commands() {
		s.session.On("ApplicationCommandCreate", "test-app-id", "", cmd, mock.Anything).
			Return(&discordgo.ApplicationCommand{Name: cmd.Name, ID: "id-1"}, nil)
	}

	err := s.bot.RegisterCommands(context.Background())
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRegisterCommandsError() {
	for _, cmd := range Commands() {
		s.session.On("ApplicationCommandCreate", "test-app-id", "", cmd, mock.Anything).
			Return(nil, errors.New("create failed"))
	}

	err := s.bot.RegisterCommands(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord register command")
}

// --- RemoveCommands ---

func (s *BotSuite) TestRemoveCommandsSuccess() {
	existing := []*discordgo.ApplicationCommand{
		{ID: "cmd-1", Name: "loop"},
	}
	s.session.On("ApplicationCommands", "test-app-id", "", mock.Anything).Return(existing, nil)
	s.session.On("ApplicationCommandDelete", "test-app-id", "", "cmd-1", mock.Anything).Return(nil)

	err := s.bot.RemoveCommands(context.Background())
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRemoveCommandsListError() {
	s.session.On("ApplicationCommands", "test-app-id", "", mock.Anything).
		Return(nil, errors.New("list failed"))

	err := s.bot.RemoveCommands(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord list commands")
}

func (s *BotSuite) TestRemoveCommandsDeleteError() {
	existing := []*discordgo.ApplicationCommand{
		{ID: "cmd-1", Name: "loop"},
	}
	s.session.On("ApplicationCommands", "test-app-id", "", mock.Anything).Return(existing, nil)
	s.session.On("ApplicationCommandDelete", "test-app-id", "", "cmd-1", mock.Anything).
		Return(errors.New("delete failed"))

	err := s.bot.RemoveCommands(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord delete command")
}

// --- OnMessage / OnInteraction ---

func (s *BotSuite) TestOnMessageRegistersHandler() {
	var received *orchestrator.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received = msg
		close(done)
	})

	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	s.session.On("GuildMember", "g-1", "user-1", mock.Anything).
		Return(nil, errors.New("not mocked"))

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Content:   "!loop hello",
			Author:    &discordgo.User{ID: "user-1", Username: "testuser"},
			Timestamp: time.Now(),
		},
	}
	s.bot.handleMessage(nil, m)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "hello", received.Content)
	require.True(s.T(), received.HasPrefix)
}

func (s *BotSuite) TestOnInteractionRegistersHandler() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int-1",
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "register",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "register", received.CommandName)
	require.Equal(s.T(), "ch-1", received.ChannelID)
	require.Equal(s.T(), "g-1", received.GuildID)
}

func (s *BotSuite) TestOnInteractionWithOptions() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "schedule",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{Name: "schedule", Type: discordgo.ApplicationCommandOptionString, Value: "0 9 * * *"},
							{Name: "prompt", Type: discordgo.ApplicationCommandOptionString, Value: "standup"},
							{Name: "type", Type: discordgo.ApplicationCommandOptionString, Value: "cron"},
						},
					},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "schedule", received.CommandName)
	require.Equal(s.T(), "0 9 * * *", received.Options["schedule"])
	require.Equal(s.T(), "standup", received.Options["prompt"])
	require.Equal(s.T(), "cron", received.Options["type"])
}

func (s *BotSuite) TestOnInteractionSubcommandGroup() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "template",
						Type: discordgo.ApplicationCommandOptionSubCommandGroup,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name: "add",
								Type: discordgo.ApplicationCommandOptionSubCommand,
								Options: []*discordgo.ApplicationCommandInteractionDataOption{
									{Name: "name", Type: discordgo.ApplicationCommandOptionString, Value: "daily-check"},
								},
							},
						},
					},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "template-add", received.CommandName)
	require.Equal(s.T(), "daily-check", received.Options["name"])
	require.Equal(s.T(), "ch-1", received.ChannelID)
	require.Equal(s.T(), "g-1", received.GuildID)
}

func (s *BotSuite) TestOnInteractionSubcommandGroupNoSub() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:    "template",
						Type:    discordgo.ApplicationCommandOptionSubCommandGroup,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{},
					},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "template", received.CommandName)
}

func (s *BotSuite) TestOnInteractionTopLevelCommand() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// A top-level command with no subcommands (e.g. a simple /ping command).
	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    "ping",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "ping", received.CommandName)
}

func (s *BotSuite) TestOnInteractionRespondError() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("respond failed"))

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "register",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	// Interaction should still be processed even if acknowledge fails.
	require.NotNil(s.T(), received)
	require.Equal(s.T(), "register", received.CommandName)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestOnInteractionIgnoresUnhandledType() {
	called := false
	s.bot.OnInteraction(func(_ context.Context, _ any) {
		called = true
	})

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionModalSubmit,
		},
	}
	s.bot.handleInteraction(nil, ic)

	require.False(s.T(), called)
}

func (s *BotSuite) TestHandleComponentInteractionStopButton() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.MatchedBy(func(resp *discordgo.InteractionResponse) bool {
		return resp.Type == discordgo.InteractionResponseDeferredMessageUpdate
	}), mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Type:      discordgo.InteractionMessageComponent,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user-1"},
				Roles: []string{"role-1"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "stop:target-ch",
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "stop", received.CommandName)
	require.Equal(s.T(), "target-ch", received.Options["channel_id"])
	require.Equal(s.T(), "ch-1", received.ChannelID)
	require.Equal(s.T(), "g-1", received.GuildID)
	require.Equal(s.T(), "user-1", received.AuthorID)
	require.Equal(s.T(), []string{"role-1"}, received.AuthorRoles)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleComponentInteractionNonStopIgnored() {
	called := false
	s.bot.OnInteraction(func(_ context.Context, _ any) {
		called = true
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "other:something",
			},
		},
	}
	s.bot.handleInteraction(nil, ic)

	require.False(s.T(), called)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleComponentInteractionAckError() {
	s.bot.OnInteraction(func(_ context.Context, _ any) {})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("ack failed"))

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "stop:ch-1",
			},
		},
	}
	// Should not panic, just log error and continue
	s.bot.handleInteraction(nil, ic)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleComponentInteractionDMUser() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "dm-ch",
			Type:      discordgo.InteractionMessageComponent,
			User:      &discordgo.User{ID: "dm-user"},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "stop:dm-ch",
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.Equal(s.T(), "dm-user", received.AuthorID)
	require.Empty(s.T(), received.AuthorRoles)
}

// --- handleMessage edge cases ---

func (s *BotSuite) TestHandleMessageIgnoresNilAuthor() {
	called := false
	s.bot.OnMessage(func(_ context.Context, _ *orchestrator.IncomingMessage) {
		called = true
	})

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{Author: nil},
	}
	s.bot.handleMessage(nil, m)
	require.False(s.T(), called)
}

func (s *BotSuite) TestHandleMessageIgnoresBotMessages() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	called := false
	s.bot.OnMessage(func(_ context.Context, _ *orchestrator.IncomingMessage) {
		called = true
	})

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			Author:  &discordgo.User{ID: "bot-123"},
			Content: "just a normal response",
		},
	}
	s.bot.handleMessage(nil, m)
	require.False(s.T(), called)
}

func (s *BotSuite) TestHandleMessageBotSelfMentionProcessed() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	var received *orchestrator.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received = msg
		close(done)
	})

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "ch-2",
			Content:   "<@bot-123> check the last commit",
			Author:    &discordgo.User{ID: "bot-123", Username: "LoopBot"},
			Mentions:  []*discordgo.User{{ID: "bot-123"}},
		},
	}
	s.bot.handleMessage(nil, m)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "check the last commit", received.Content)
	require.True(s.T(), received.IsBotMention)
}

func (s *BotSuite) TestHandleMessageBotSelfMentionContentFallback() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	var received *orchestrator.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received = msg
		close(done)
	})

	// Mentions slice is empty but content contains <@bot-123>.
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "ch-2",
			Content:   "<@bot-123> check the last commit",
			Author:    &discordgo.User{ID: "bot-123", Username: "LoopBot"},
			Mentions:  []*discordgo.User{},
		},
	}
	s.bot.handleMessage(nil, m)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "check the last commit", received.Content)
	require.True(s.T(), received.IsBotMention)
}

func (s *BotSuite) TestHandleMessageBotReplyToSelfNotTriggered() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	called := false
	s.bot.OnMessage(func(_ context.Context, _ *orchestrator.IncomingMessage) {
		called = true
	})

	// Bot response that is a reply to its own message. Discord auto-populates
	// Mentions with the referenced message author, but the content does NOT
	// contain <@bot-123>. This must NOT trigger a runner (prevents recursion).
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-2",
			ChannelID: "ch-2",
			Content:   "The last commit is abc123",
			Author:    &discordgo.User{ID: "bot-123", Username: "LoopBot"},
			Mentions:  []*discordgo.User{{ID: "bot-123"}},
			MessageReference: &discordgo.MessageReference{
				MessageID: "msg-1",
			},
			ReferencedMessage: &discordgo.Message{
				Author: &discordgo.User{ID: "bot-123"},
			},
		},
	}
	s.bot.handleMessage(nil, m)
	require.False(s.T(), called)
}

func (s *BotSuite) TestHandleMessageIgnoresNonTriggered() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	called := false
	s.bot.OnMessage(func(_ context.Context, _ *orchestrator.IncomingMessage) {
		called = true
	})

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID: "g-1", // Must set GuildID so it's not treated as a DM
			Author:  &discordgo.User{ID: "user-1"},
			Content: "just a random message",
		},
	}
	s.bot.handleMessage(nil, m)
	require.False(s.T(), called)
}

func (s *BotSuite) TestHandleMessageDMAlwaysTriggered() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	var received *orchestrator.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received = msg
		close(done)
	})

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "dm-ch-1",
			GuildID:   "", // DM
			Content:   "hello in DM",
			Author:    &discordgo.User{ID: "user-1", Username: "testuser"},
		},
	}
	s.bot.handleMessage(nil, m)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "hello in DM", received.Content)
	require.True(s.T(), received.IsDM)
}

func (s *BotSuite) TestHandleMessageMultipleHandlers() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	handler := func(_ context.Context, _ *orchestrator.IncomingMessage) {
		mu.Lock()
		count++
		mu.Unlock()
		wg.Done()
	}
	s.bot.OnMessage(handler)
	s.bot.OnMessage(handler)

	wg.Add(2)
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:      "msg-1",
			Author:  &discordgo.User{ID: "user-1", Username: "u"},
			Content: "!loop hello",
		},
	}
	s.bot.handleMessage(nil, m)
	wg.Wait()
	mu.Lock()
	require.Equal(s.T(), 2, count)
	mu.Unlock()
}

func (s *BotSuite) TestHandleInteractionMultipleHandlers() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	handler := func(_ context.Context, _ any) {
		mu.Lock()
		count++
		mu.Unlock()
		wg.Done()
	}
	s.bot.OnInteraction(handler)
	s.bot.OnInteraction(handler)

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	wg.Add(2)
	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	wg.Wait()
	mu.Lock()
	require.Equal(s.T(), 2, count)
	mu.Unlock()
}

// --- BotUserID ---

func (s *BotSuite) TestBotUserIDEmpty() {
	require.Equal(s.T(), "", s.bot.BotUserID())
}

// --- Trigger detection (table-driven) ---

type TriggerSuite struct {
	suite.Suite
}

func TestTriggerSuite(t *testing.T) {
	suite.Run(t, new(TriggerSuite))
}

func (s *TriggerSuite) TestIsBotMention() {
	tests := []struct {
		name     string
		mentions []*discordgo.User
		botID    string
		expected bool
	}{
		{
			name:     "mentioned",
			mentions: []*discordgo.User{{ID: "bot-1"}},
			botID:    "bot-1",
			expected: true,
		},
		{
			name:     "not mentioned",
			mentions: []*discordgo.User{{ID: "other"}},
			botID:    "bot-1",
			expected: false,
		},
		{
			name:     "no mentions",
			mentions: nil,
			botID:    "bot-1",
			expected: false,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			m := &discordgo.MessageCreate{
				Message: &discordgo.Message{Mentions: tc.mentions},
			}
			require.Equal(s.T(), tc.expected, isBotMention(m, tc.botID))
		})
	}
}

func (s *TriggerSuite) TestIsReplyToBot() {
	tests := []struct {
		name     string
		msg      *discordgo.MessageCreate
		botID    string
		expected bool
	}{
		{
			name: "reply to bot",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{
					MessageReference:  &discordgo.MessageReference{MessageID: "ref-1"},
					ReferencedMessage: &discordgo.Message{Author: &discordgo.User{ID: "bot-1"}},
				},
			},
			botID:    "bot-1",
			expected: true,
		},
		{
			name: "reply to other user",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{
					MessageReference:  &discordgo.MessageReference{MessageID: "ref-1"},
					ReferencedMessage: &discordgo.Message{Author: &discordgo.User{ID: "other"}},
				},
			},
			botID:    "bot-1",
			expected: false,
		},
		{
			name: "no message reference",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{},
			},
			botID:    "bot-1",
			expected: false,
		},
		{
			name: "no referenced message",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{
					MessageReference: &discordgo.MessageReference{MessageID: "ref-1"},
				},
			},
			botID:    "bot-1",
			expected: false,
		},
		{
			name: "referenced message no author",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{
					MessageReference:  &discordgo.MessageReference{MessageID: "ref-1"},
					ReferencedMessage: &discordgo.Message{Author: nil},
				},
			},
			botID:    "bot-1",
			expected: false,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, isReplyToBot(tc.msg, tc.botID))
		})
	}
}

// --- parseIncomingMessage ---

func (s *TriggerSuite) TestParseIncomingMessageMention() {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Content:   "<@bot-1> hello there",
			Author:    &discordgo.User{ID: "user-1", Username: "alice"},
			Mentions:  []*discordgo.User{{ID: "bot-1"}},
			Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	msg := parseIncomingMessage(m, "bot-1")
	require.NotNil(s.T(), msg)
	require.Equal(s.T(), "hello there", msg.Content)
	require.True(s.T(), msg.IsBotMention)
	require.Equal(s.T(), "ch-1", msg.ChannelID)
	require.Equal(s.T(), "g-1", msg.GuildID)
	require.Equal(s.T(), "user-1", msg.AuthorID)
	require.Equal(s.T(), "alice", msg.AuthorName)
	require.Equal(s.T(), "msg-1", msg.MessageID)
}

func (s *TriggerSuite) TestParseIncomingMessagePrefix() {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:      "msg-1",
			Content: "!loop what is Go?",
			Author:  &discordgo.User{ID: "user-1", Username: "bob"},
		},
	}
	msg := parseIncomingMessage(m, "bot-1")
	require.NotNil(s.T(), msg)
	require.Equal(s.T(), "what is Go?", msg.Content)
	require.True(s.T(), msg.HasPrefix)
	require.False(s.T(), msg.IsBotMention)
}

func (s *TriggerSuite) TestParseIncomingMessageReply() {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:                "msg-2",
			Content:           "thanks",
			Author:            &discordgo.User{ID: "user-1", Username: "carol"},
			MessageReference:  &discordgo.MessageReference{MessageID: "msg-1"},
			ReferencedMessage: &discordgo.Message{Author: &discordgo.User{ID: "bot-1"}},
		},
	}
	msg := parseIncomingMessage(m, "bot-1")
	require.NotNil(s.T(), msg)
	require.Equal(s.T(), "thanks", msg.Content)
	require.True(s.T(), msg.IsReplyToBot)
	require.False(s.T(), msg.IsBotMention)
	require.False(s.T(), msg.HasPrefix)
}

func (s *TriggerSuite) TestParseIncomingMessageNoTrigger() {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:      "msg-1",
			GuildID: "g-1",
			Content: "just chatting",
			Author:  &discordgo.User{ID: "user-1", Username: "dave"},
		},
	}
	msg := parseIncomingMessage(m, "bot-1")
	require.Nil(s.T(), msg)
}

func (s *TriggerSuite) TestParseIncomingMessageDM() {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "dm-ch-1",
			GuildID:   "", // DMs have empty GuildID
			Content:   "hello from DM",
			Author:    &discordgo.User{ID: "user-1", Username: "eve"},
			Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	msg := parseIncomingMessage(m, "bot-1")
	require.NotNil(s.T(), msg)
	require.Equal(s.T(), "hello from DM", msg.Content)
	require.True(s.T(), msg.IsDM)
	require.False(s.T(), msg.IsBotMention)
	require.False(s.T(), msg.IsReplyToBot)
	require.False(s.T(), msg.HasPrefix)
	require.Equal(s.T(), "dm-ch-1", msg.ChannelID)
	require.Equal(s.T(), "", msg.GuildID)
	require.Equal(s.T(), "user-1", msg.AuthorID)
	require.Equal(s.T(), "eve", msg.AuthorName)
}

// --- SendTyping refresh goroutine ---

func (s *BotSuite) TestSendTypingRefreshes() {
	s.bot.typingInterval = 20 * time.Millisecond

	typingCount := make(chan struct{}, 10)
	s.session.On("ChannelTyping", "ch-1", mock.Anything).Run(func(_ mock.Arguments) {
		typingCount <- struct{}{}
	}).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.bot.SendTyping(ctx, "ch-1")
	require.NoError(s.T(), err)

	// Wait for initial call.
	<-typingCount
	// Wait for at least one refresh.
	<-typingCount

	cancel()
	time.Sleep(30 * time.Millisecond)
}

func (s *BotSuite) TestSendTypingRefreshError() {
	s.bot.typingInterval = 20 * time.Millisecond
	// First call succeeds (initial), subsequent calls fail (refresh).
	s.session.On("ChannelTyping", "ch-1", mock.Anything).Return(nil).Once()
	s.session.On("ChannelTyping", "ch-1", mock.Anything).Return(errors.New("refresh failed"))

	ctx, cancel := context.WithCancel(context.Background())

	err := s.bot.SendTyping(ctx, "ch-1")
	require.NoError(s.T(), err)

	// Let the goroutine fire and hit the error path.
	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond)
}

// --- Pending interaction stored on successful defer ---

func (s *BotSuite) TestHandleInteractionStoresPendingInteraction() {
	s.bot.OnInteraction(func(_ context.Context, _ any) {})
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	interaction := &discordgo.Interaction{
		ChannelID: "ch-1",
		Type:      discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "loop",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand},
			},
		},
	}
	ic := &discordgo.InteractionCreate{Interaction: interaction}
	s.bot.handleInteraction(nil, ic)

	s.bot.mu.RLock()
	pending, ok := s.bot.pendingInteractions["ch-1"]
	s.bot.mu.RUnlock()
	require.True(s.T(), ok)
	require.Same(s.T(), interaction, pending)
}

func (s *BotSuite) TestHandleInteractionDoesNotStorePendingOnError() {
	s.bot.OnInteraction(func(_ context.Context, _ any) {})
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("respond failed"))

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)

	s.bot.mu.RLock()
	_, ok := s.bot.pendingInteractions["ch-1"]
	s.bot.mu.RUnlock()
	require.False(s.T(), ok)
}

// --- SendMessage with pending interaction ---

func (s *BotSuite) TestSendMessageWithPendingInteraction() {
	interaction := &discordgo.Interaction{ChannelID: "ch-1"}
	s.bot.mu.Lock()
	s.bot.pendingInteractions["ch-1"] = interaction
	s.bot.mu.Unlock()

	content := "hello from interaction"
	s.session.On("InteractionResponseEdit", interaction, &discordgo.WebhookEdit{Content: &content}, mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "ch-1",
		Content:   content,
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())

	// Pending interaction should be consumed.
	s.bot.mu.RLock()
	_, ok := s.bot.pendingInteractions["ch-1"]
	s.bot.mu.RUnlock()
	require.False(s.T(), ok)
}

func (s *BotSuite) TestSendMessageWithPendingInteractionSplit() {
	interaction := &discordgo.Interaction{ChannelID: "ch-1"}
	s.bot.mu.Lock()
	s.bot.pendingInteractions["ch-1"] = interaction
	s.bot.mu.Unlock()

	longContent := strings.Repeat("a", 2500)
	secondChunk := strings.Repeat("a", 500)

	s.session.On("InteractionResponseEdit", interaction, &discordgo.WebhookEdit{Content: new(strings.Repeat("a", 2000))}, mock.Anything).
		Return(&discordgo.Message{}, nil)
	s.session.On("FollowupMessageCreate", interaction, true, &discordgo.WebhookParams{Content: secondChunk}, mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "ch-1",
		Content:   longContent,
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendMessageWithPendingInteractionEditError() {
	interaction := &discordgo.Interaction{ChannelID: "ch-1"}
	s.bot.mu.Lock()
	s.bot.pendingInteractions["ch-1"] = interaction
	s.bot.mu.Unlock()

	content := "hello"
	s.session.On("InteractionResponseEdit", interaction, &discordgo.WebhookEdit{Content: &content}, mock.Anything).
		Return(nil, errors.New("edit failed"))

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "ch-1",
		Content:   content,
	})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord interaction edit")
}

func (s *BotSuite) TestSendMessageWithPendingInteractionFollowupError() {
	interaction := &discordgo.Interaction{ChannelID: "ch-1"}
	s.bot.mu.Lock()
	s.bot.pendingInteractions["ch-1"] = interaction
	s.bot.mu.Unlock()

	longContent := strings.Repeat("a", 2500)
	secondChunk := strings.Repeat("a", 500)

	s.session.On("InteractionResponseEdit", interaction, &discordgo.WebhookEdit{Content: new(strings.Repeat("a", 2000))}, mock.Anything).
		Return(&discordgo.Message{}, nil)
	s.session.On("FollowupMessageCreate", interaction, true, &discordgo.WebhookParams{Content: secondChunk}, mock.Anything).
		Return(nil, errors.New("followup failed"))

	err := s.bot.SendMessage(context.Background(), &orchestrator.OutgoingMessage{
		ChannelID: "ch-1",
		Content:   longContent,
	})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord followup create")
}

// --- CreateChannel ---

func (s *BotSuite) TestCreateChannelSuccess() {
	s.session.On("GuildChannels", "g-1", mock.Anything).
		Return([]*discordgo.Channel{}, nil)
	s.session.On("GuildChannelCreate", "g-1", "loop", discordgo.ChannelTypeGuildText, mock.Anything).
		Return(&discordgo.Channel{ID: "new-ch-1"}, nil)

	channelID, err := s.bot.CreateChannel(context.Background(), "g-1", "loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestCreateChannelError() {
	s.session.On("GuildChannels", "g-1", mock.Anything).
		Return([]*discordgo.Channel{}, nil)
	s.session.On("GuildChannelCreate", "g-1", "loop", discordgo.ChannelTypeGuildText, mock.Anything).
		Return(nil, errors.New("create failed"))

	channelID, err := s.bot.CreateChannel(context.Background(), "g-1", "loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord create channel")
	require.Empty(s.T(), channelID)
}

func (s *BotSuite) TestCreateChannelExisting() {
	s.session.On("GuildChannels", "g-1", mock.Anything).
		Return([]*discordgo.Channel{
			{ID: "ch-other", Name: "other", Type: discordgo.ChannelTypeGuildText},
			{ID: "ch-loop", Name: "loop", Type: discordgo.ChannelTypeGuildText},
		}, nil)

	channelID, err := s.bot.CreateChannel(context.Background(), "g-1", "loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch-loop", channelID)
	s.session.AssertNotCalled(s.T(), "GuildChannelCreate")
}

func (s *BotSuite) TestCreateChannelExistingWrongType() {
	// A voice channel with the same name should not match.
	s.session.On("GuildChannels", "g-1", mock.Anything).
		Return([]*discordgo.Channel{
			{ID: "ch-voice", Name: "loop", Type: discordgo.ChannelTypeGuildVoice},
		}, nil)
	s.session.On("GuildChannelCreate", "g-1", "loop", discordgo.ChannelTypeGuildText, mock.Anything).
		Return(&discordgo.Channel{ID: "new-ch-1"}, nil)

	channelID, err := s.bot.CreateChannel(context.Background(), "g-1", "loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
}

func (s *BotSuite) TestCreateChannelListError() {
	s.session.On("GuildChannels", "g-1", mock.Anything).
		Return(nil, errors.New("list failed"))

	_, err := s.bot.CreateChannel(context.Background(), "g-1", "loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord list channels")
}

// --- InviteUserToChannel ---

func (s *BotSuite) TestInviteUserToChannelNoOp() {
	err := s.bot.InviteUserToChannel(context.Background(), "ch-1", "user-1")
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestGetOwnerUserIDNoOp() {
	id, err := s.bot.GetOwnerUserID(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

// --- SetChannelTopic ---

func (s *BotSuite) TestSetChannelTopicSuccess() {
	s.session.On("ChannelEdit", "ch-1", &discordgo.ChannelEdit{Topic: "/home/user/dev/loop"}, mock.Anything).
		Return(&discordgo.Channel{}, nil)

	err := s.bot.SetChannelTopic(context.Background(), "ch-1", "/home/user/dev/loop")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSetChannelTopicError() {
	s.session.On("ChannelEdit", "ch-1", &discordgo.ChannelEdit{Topic: "/path"}, mock.Anything).
		Return(nil, errors.New("edit_error"))

	err := s.bot.SetChannelTopic(context.Background(), "ch-1", "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord set channel topic")
}

// --- CreateThread ---

func (s *BotSuite) TestCreateThreadSuccess() {
	cases := []struct {
		name        string
		botUsername string
		mentionUser string
		message     string
		expectedMsg string
	}{
		{"default", "", "", "", "Tag me to get started!"},
		{"with mention user", "", "user-42", "", "Hey <@user-42>, tag me to get started!"},
		{"with message", "", "", "Do the task", "<@bot-123> Do the task"},
		{"strips text mention", "LoopBot", "", "@LoopBot Do the task", "<@bot-123> Do the task"},
		{"strips discord mention", "", "", "<@bot-123> Do the task", "<@bot-123> Do the task"},
		{"message and mention user", "", "user-42", "Do the task", "<@bot-123> Do the task <@user-42>"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			b.botUsername = tc.botUsername

			session.On("ThreadStart", "ch-1", "my-thread", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
				Return(&discordgo.Channel{ID: "thread-1"}, nil)
			session.On("ChannelMessageSend", "thread-1", tc.expectedMsg, mock.Anything).
				Return(&discordgo.Message{}, nil)

			threadID, err := b.CreateThread(context.Background(), "ch-1", "my-thread", tc.mentionUser, tc.message)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "thread-1", threadID)
			session.AssertExpectations(s.T())
		})
	}
}

func (s *BotSuite) TestCreateThreadMessageSendError() {
	s.bot.botUserID = "bot-123"
	s.session.On("ThreadStart", "ch-1", "my-thread", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(&discordgo.Channel{ID: "thread-1"}, nil)
	s.session.On("ChannelMessageSend", "thread-1", mock.Anything, mock.Anything).
		Return(nil, errors.New("send failed"))

	threadID, err := s.bot.CreateThread(context.Background(), "ch-1", "my-thread", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestCreateThreadError() {
	s.session.On("ThreadStart", "ch-1", "my-thread", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(nil, errors.New("thread create failed"))

	threadID, err := s.bot.CreateThread(context.Background(), "ch-1", "my-thread", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord create thread")
	require.Empty(s.T(), threadID)
}

// --- DeleteThread ---

func (s *BotSuite) TestDeleteThreadSuccess() {
	s.session.On("ChannelDelete", "thread-1", mock.Anything).
		Return(&discordgo.Channel{ID: "thread-1"}, nil)

	err := s.bot.DeleteThread(context.Background(), "thread-1")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestDeleteThreadError() {
	s.session.On("ChannelDelete", "thread-1", mock.Anything).
		Return(nil, errors.New("delete failed"))

	err := s.bot.DeleteThread(context.Background(), "thread-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord delete thread")
}

// --- handleThreadCreate ---

func (s *BotSuite) TestHandleThreadCreateJoins() {
	cases := []struct {
		name      string
		threadID  string
		chanType  discordgo.ChannelType
	}{
		{"public thread", "thread-1", discordgo.ChannelTypeGuildPublicThread},
		{"private thread", "thread-2", discordgo.ChannelTypeGuildPrivateThread},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			session.On("ThreadJoin", tc.threadID, mock.Anything).Return(nil)

			c := &discordgo.ThreadCreate{
				Channel: &discordgo.Channel{
					ID:       tc.threadID,
					Type:     tc.chanType,
					ParentID: "ch-1",
				},
			}
			b.handleThreadCreate(nil, c)
			session.AssertExpectations(s.T())
		})
	}
}

func (s *BotSuite) TestHandleThreadCreateIgnoresNonThread() {
	c := &discordgo.ThreadCreate{
		Channel: &discordgo.Channel{
			ID:   "ch-1",
			Type: discordgo.ChannelTypeGuildText,
		},
	}
	s.bot.handleThreadCreate(nil, c)
	s.session.AssertNotCalled(s.T(), "ThreadJoin", mock.Anything, mock.Anything)
}

func (s *BotSuite) TestHandleThreadCreateJoinError() {
	s.session.On("ThreadJoin", "thread-1", mock.Anything).Return(errors.New("join failed"))

	c := &discordgo.ThreadCreate{
		Channel: &discordgo.Channel{
			ID:       "thread-1",
			Type:     discordgo.ChannelTypeGuildPublicThread,
			ParentID: "ch-1",
		},
	}
	s.bot.handleThreadCreate(nil, c)
	s.session.AssertExpectations(s.T())
}

// --- handleThreadDelete ---

func (s *BotSuite) TestHandleThreadDelete() {
	called := make(chan struct{}, 1)
	s.bot.OnChannelDelete(func(ctx context.Context, channelID string, isThread bool) {
		require.Equal(s.T(), "thread-1", channelID)
		require.True(s.T(), isThread)
		called <- struct{}{}
	})

	c := &discordgo.ThreadDelete{
		Channel: &discordgo.Channel{
			ID:       "thread-1",
			Type:     discordgo.ChannelTypeGuildPublicThread,
			ParentID: "ch-1",
		},
	}
	s.bot.handleThreadDelete(nil, c)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("handler not called")
	}
}

// --- handleChannelDelete ---

func (s *BotSuite) TestHandleChannelDeleteNotifiesHandlers() {
	called := make(chan struct{}, 1)
	s.bot.OnChannelDelete(func(ctx context.Context, channelID string, isThread bool) {
		require.Equal(s.T(), "ch-1", channelID)
		require.False(s.T(), isThread)
		called <- struct{}{}
	})

	c := &discordgo.ChannelDelete{
		Channel: &discordgo.Channel{
			ID:   "ch-1",
			Type: discordgo.ChannelTypeGuildText,
		},
	}
	s.bot.handleChannelDelete(nil, c)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("handler not called")
	}
}

func (s *BotSuite) TestHandleChannelDeleteIgnoresThreads() {
	s.bot.OnChannelDelete(func(ctx context.Context, channelID string, isThread bool) {
		s.T().Fatal("should not be called for threads")
	})

	c := &discordgo.ChannelDelete{
		Channel: &discordgo.Channel{
			ID:       "thread-1",
			Type:     discordgo.ChannelTypeGuildPublicThread,
			ParentID: "ch-1",
		},
	}
	s.bot.handleChannelDelete(nil, c)
}

// --- OnChannelDelete ---

func (s *BotSuite) TestOnChannelDeleteRegistersHandler() {
	handler := func(ctx context.Context, channelID string, isThread bool) {}
	s.bot.OnChannelDelete(handler)

	s.bot.mu.RLock()
	require.Len(s.T(), s.bot.channelDeleteHandlers, 1)
	s.bot.mu.RUnlock()
}

// --- OnChannelJoin ---

func (s *BotSuite) TestOnChannelJoinRegistersHandler() {
	handler := func(ctx context.Context, channelID string) {}
	s.bot.OnChannelJoin(handler)

	s.bot.mu.RLock()
	require.Len(s.T(), s.bot.channelJoinHandlers, 1)
	s.bot.mu.RUnlock()
}

// --- GetChannelName ---

func (s *BotSuite) TestGetChannelNameSuccess() {
	s.session.On("Channel", "ch-1", mock.Anything).Return(&discordgo.Channel{
		ID:   "ch-1",
		Name: "general",
	}, nil)

	name, err := s.bot.GetChannelName(context.Background(), "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "general", name)
}

func (s *BotSuite) TestGetChannelNameError() {
	s.session.On("Channel", "ch-1", mock.Anything).Return(nil, errors.New("api error"))

	name, err := s.bot.GetChannelName(context.Background(), "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord get channel name")
	require.Empty(s.T(), name)
}

// --- GetChannelParentID ---

func (s *BotSuite) TestGetChannelParentIDThread() {
	s.session.On("Channel", "thread-1", mock.Anything).Return(&discordgo.Channel{
		ID:       "thread-1",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		ParentID: "ch-1",
	}, nil)

	parentID, err := s.bot.GetChannelParentID(context.Background(), "thread-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch-1", parentID)
}

func (s *BotSuite) TestGetChannelParentIDNotThread() {
	s.session.On("Channel", "ch-1", mock.Anything).Return(&discordgo.Channel{
		ID:   "ch-1",
		Type: discordgo.ChannelTypeGuildText,
	}, nil)

	parentID, err := s.bot.GetChannelParentID(context.Background(), "ch-1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), parentID)
}

func (s *BotSuite) TestGetChannelParentIDError() {
	s.session.On("Channel", "ch-1", mock.Anything).Return(nil, errors.New("api error"))

	parentID, err := s.bot.GetChannelParentID(context.Background(), "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord get channel")
	require.Empty(s.T(), parentID)
}

// --- GetMemberRoles ---

func (s *BotSuite) TestGetMemberRolesSuccess() {
	s.session.On("GuildMember", "g-1", "user-1", mock.Anything).
		Return(&discordgo.Member{Roles: []string{"role-1", "role-2"}}, nil)

	roles, err := s.bot.GetMemberRoles(context.Background(), "g-1", "user-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"role-1", "role-2"}, roles)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestGetMemberRolesError() {
	s.session.On("GuildMember", "g-1", "user-1", mock.Anything).
		Return(nil, errors.New("api error"))

	roles, err := s.bot.GetMemberRoles(context.Background(), "g-1", "user-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord get member roles")
	require.Nil(s.T(), roles)
	s.session.AssertExpectations(s.T())
}

// --- handleMessage with role population ---

func (s *BotSuite) TestHandleMessagePopulatesRoles() {
	var received *orchestrator.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received = msg
		close(done)
	})

	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	// Override GuildMember to return roles for this test.
	s.session.On("GuildMember", "g-1", "user-1", mock.Anything).
		Return(&discordgo.Member{Roles: []string{"role-admin"}}, nil)

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-2",
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Content:   "!loop hello",
			Author:    &discordgo.User{ID: "user-1", Username: "testuser"},
			Timestamp: time.Now(),
		},
	}
	s.bot.handleMessage(nil, m)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), []string{"role-admin"}, received.AuthorRoles)
}

func (s *BotSuite) TestHandleMessageRoleFetchError() {
	var received *orchestrator.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
		received = msg
		close(done)
	})

	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	// GuildMember returns error — roles should be nil.
	s.session.On("GuildMember", "g-1", "user-1", mock.Anything).
		Return(nil, errors.New("not found"))

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-3",
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Content:   "!loop hello",
			Author:    &discordgo.User{ID: "user-1", Username: "testuser"},
			Timestamp: time.Now(),
		},
	}
	s.bot.handleMessage(nil, m)
	<-done

	require.NotNil(s.T(), received)
	require.Nil(s.T(), received.AuthorRoles)
}

// --- handleInteraction with AuthorID/AuthorRoles ---

func (s *BotSuite) TestHandleInteractionPopulatesAuthorGuild() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int-guild",
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Type:      discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user-guild"},
				Roles: []string{"role-a", "role-b"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "user-guild", received.AuthorID)
	require.Equal(s.T(), []string{"role-a", "role-b"}, received.AuthorRoles)
}

func (s *BotSuite) TestHandleInteractionPopulatesAuthorDM() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received = i.(*orchestrator.Interaction)
		close(done)
	})

	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int-dm",
			ChannelID: "dm-ch",
			Type:      discordgo.InteractionApplicationCommand,
			User:      &discordgo.User{ID: "user-dm"},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "loop",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			},
		},
	}
	s.bot.handleInteraction(nil, ic)
	<-done

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "user-dm", received.AuthorID)
	require.Nil(s.T(), received.AuthorRoles)
}

// --- PostMessage ---

func (s *BotSuite) TestPostMessageSuccess() {
	s.session.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.PostMessage(context.Background(), "ch-1", "hello")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestPostMessageConvertsTextMention() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.botUsername = "LoopBot"
	s.bot.mu.Unlock()

	s.session.On("ChannelMessageSend", "ch-1", "<@bot-123> check the last commit", mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.PostMessage(context.Background(), "ch-1", "@LoopBot check the last commit")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestPostMessageConvertsTextMentionCaseInsensitive() {
	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.botUsername = "LoopBot"
	s.bot.mu.Unlock()

	s.session.On("ChannelMessageSend", "ch-1", "<@bot-123> check commits", mock.Anything).
		Return(&discordgo.Message{}, nil)

	err := s.bot.PostMessage(context.Background(), "ch-1", "@loopbot check commits")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestPostMessageError() {
	s.session.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).
		Return(nil, errors.New("send failed"))

	err := s.bot.PostMessage(context.Background(), "ch-1", "hello")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord post message")
}

// --- CreateSimpleThread tests ---

func (s *BotSuite) TestCreateSimpleThreadSuccess() {
	s.session.On("ThreadStart", "ch-1", "task output", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(&discordgo.Channel{ID: "thread-1"}, nil)
	s.session.On("ChannelMessageSend", "thread-1", "First turn content", mock.Anything).
		Return(&discordgo.Message{}, nil)

	threadID, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "task output", "First turn content")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestCreateSimpleThreadEmptyMessage() {
	s.session.On("ThreadStart", "ch-1", "task name", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(&discordgo.Channel{ID: "thread-2"}, nil)

	threadID, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "task name", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-2", threadID)
	// ChannelMessageSend should not be called for empty message
	s.session.AssertNotCalled(s.T(), "ChannelMessageSend", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BotSuite) TestCreateSimpleThreadStartError() {
	s.session.On("ThreadStart", "ch-1", "task", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(nil, errors.New("thread start failed"))

	threadID, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "task", "content")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord create simple thread")
	require.Empty(s.T(), threadID)
}

func (s *BotSuite) TestCreateSimpleThreadMessageSendError() {
	s.session.On("ThreadStart", "ch-1", "task", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(&discordgo.Channel{ID: "thread-3"}, nil)
	s.session.On("ChannelMessageSend", "thread-3", "content", mock.Anything).
		Return(nil, errors.New("send failed"))

	// Message send error is logged but does not fail the thread creation
	threadID, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "task", "content")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-3", threadID)
	s.session.AssertExpectations(s.T())
}

// --- Stop button tests ---

func (s *BotSuite) TestSendStopButtonSuccess() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		return data.Content == "Processing..." && len(data.Components) == 1
	}), mock.Anything).Return(&discordgo.Message{ID: "stop-msg-1"}, nil)

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "run-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "stop-msg-1", msgID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendStopButtonError() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.Anything, mock.Anything).Return(nil, errors.New("send failed"))

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "run-1")
	require.Error(s.T(), err)
	require.Equal(s.T(), "", msgID)
}

func (s *BotSuite) TestRemoveStopButtonSuccess() {
	s.session.On("ChannelMessageDelete", "ch1", "stop-msg-1", mock.Anything).Return(nil)

	err := s.bot.RemoveStopButton(context.Background(), "ch1", "stop-msg-1")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRemoveStopButtonError() {
	s.session.On("ChannelMessageDelete", "ch1", "stop-msg-1", mock.Anything).Return(errors.New("delete failed"))

	err := s.bot.RemoveStopButton(context.Background(), "ch1", "stop-msg-1")
	require.Error(s.T(), err)
}

func (s *BotSuite) TestSendStopButtonCustomID() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		if len(data.Components) != 1 {
			return false
		}
		row, ok := data.Components[0].(discordgo.ActionsRow)
		if !ok || len(row.Components) != 1 {
			return false
		}
		btn, ok := row.Components[0].(discordgo.Button)
		if !ok {
			return false
		}
		return btn.CustomID == "stop:my-channel" && btn.Style == discordgo.DangerButton && btn.Label == "Stop"
	}), mock.Anything).Return(&discordgo.Message{ID: "msg-1"}, nil)

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "my-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "msg-1", msgID)
	s.session.AssertExpectations(s.T())
}

// --- Verify Bot interface compliance ---

func (s *BotSuite) TestBotInterfaceCompliance() {
	var _ Bot = (*DiscordBot)(nil)
}
