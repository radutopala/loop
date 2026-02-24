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

func (s *BotSuite) TestSendMessage() {
	tests := []struct {
		name    string
		msg     *orchestrator.OutgoingMessage
		setup   func(*MockSession)
		wantErr string
	}{
		{
			name: "simple",
			msg:  &orchestrator.OutgoingMessage{ChannelID: "ch-1", Content: "hello"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "with reply",
			msg:  &orchestrator.OutgoingMessage{ChannelID: "ch-1", Content: "hello", ReplyToMessageID: "msg-1"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSendReply", "ch-1", "hello", &discordgo.MessageReference{MessageID: "msg-1"}, mock.Anything).
					Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "split",
			msg:  &orchestrator.OutgoingMessage{ChannelID: "ch-1", Content: strings.Repeat("a", 2500), ReplyToMessageID: "msg-1"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSendReply", "ch-1", strings.Repeat("a", 2000), &discordgo.MessageReference{MessageID: "msg-1"}, mock.Anything).
					Return(&discordgo.Message{}, nil)
				ss.On("ChannelMessageSend", "ch-1", strings.Repeat("a", 500), mock.Anything).Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "reply error",
			msg:  &orchestrator.OutgoingMessage{ChannelID: "ch-1", Content: "hello", ReplyToMessageID: "msg-1"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSendReply", "ch-1", "hello", &discordgo.MessageReference{MessageID: "msg-1"}, mock.Anything).
					Return(nil, errors.New("send failed"))
			},
			wantErr: "discord send reply",
		},
		{
			name: "send error",
			msg:  &orchestrator.OutgoingMessage{ChannelID: "ch-1", Content: "hello"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).Return(nil, errors.New("send failed"))
			},
			wantErr: "discord send message",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			tc.setup(session)
			err := b.SendMessage(context.Background(), tc.msg)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertExpectations(s.T())
		})
	}
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

func (s *BotSuite) TestRemoveCommands() {
	tests := []struct {
		name    string
		setup   func(*MockSession)
		wantErr string
	}{
		{
			name: "success",
			setup: func(ss *MockSession) {
				ss.On("ApplicationCommands", "test-app-id", "", mock.Anything).
					Return([]*discordgo.ApplicationCommand{{ID: "cmd-1", Name: "loop"}}, nil)
				ss.On("ApplicationCommandDelete", "test-app-id", "", "cmd-1", mock.Anything).Return(nil)
			},
		},
		{
			name: "list error",
			setup: func(ss *MockSession) {
				ss.On("ApplicationCommands", "test-app-id", "", mock.Anything).Return(nil, errors.New("list failed"))
			},
			wantErr: "discord list commands",
		},
		{
			name: "delete error",
			setup: func(ss *MockSession) {
				ss.On("ApplicationCommands", "test-app-id", "", mock.Anything).
					Return([]*discordgo.ApplicationCommand{{ID: "cmd-1", Name: "loop"}}, nil)
				ss.On("ApplicationCommandDelete", "test-app-id", "", "cmd-1", mock.Anything).Return(errors.New("delete failed"))
			},
			wantErr: "discord delete command",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "test-app-id", slog.New(slog.NewTextHandler(discard{}, nil)))
			tc.setup(session)
			err := b.RemoveCommands(context.Background())
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertExpectations(s.T())
		})
	}
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

func (s *BotSuite) TestOnInteractionCommandParsing() {
	tests := []struct {
		name      string
		intName   string
		channelID string
		guildID   string
		options   []*discordgo.ApplicationCommandInteractionDataOption
		wantCmd   string
		wantOpts  map[string]any
	}{
		{
			name:      "subcommand",
			intName:   "loop",
			channelID: "ch-1",
			guildID:   "g-1",
			options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "register", Type: discordgo.ApplicationCommandOptionSubCommand},
			},
			wantCmd: "register",
		},
		{
			name:      "subcommand with options",
			intName:   "loop",
			channelID: "ch-1",
			options: []*discordgo.ApplicationCommandInteractionDataOption{
				{
					Name: "schedule", Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandInteractionDataOption{
						{Name: "schedule", Type: discordgo.ApplicationCommandOptionString, Value: "0 9 * * *"},
						{Name: "prompt", Type: discordgo.ApplicationCommandOptionString, Value: "standup"},
						{Name: "type", Type: discordgo.ApplicationCommandOptionString, Value: "cron"},
					},
				},
			},
			wantCmd:  "schedule",
			wantOpts: map[string]any{"schedule": "0 9 * * *", "prompt": "standup", "type": "cron"},
		},
		{
			name:      "subcommand group",
			intName:   "loop",
			channelID: "ch-1",
			guildID:   "g-1",
			options: []*discordgo.ApplicationCommandInteractionDataOption{
				{
					Name: "template", Type: discordgo.ApplicationCommandOptionSubCommandGroup,
					Options: []*discordgo.ApplicationCommandInteractionDataOption{
						{
							Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand,
							Options: []*discordgo.ApplicationCommandInteractionDataOption{
								{Name: "name", Type: discordgo.ApplicationCommandOptionString, Value: "daily-check"},
							},
						},
					},
				},
			},
			wantCmd:  "template-add",
			wantOpts: map[string]any{"name": "daily-check"},
		},
		{
			name:      "subcommand group no sub",
			intName:   "loop",
			channelID: "ch-1",
			options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "template", Type: discordgo.ApplicationCommandOptionSubCommandGroup, Options: []*discordgo.ApplicationCommandInteractionDataOption{}},
			},
			wantCmd: "template",
		},
		{
			name:      "top level command",
			intName:   "ping",
			channelID: "ch-1",
			options:   []*discordgo.ApplicationCommandInteractionDataOption{},
			wantCmd:   "ping",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			var received *orchestrator.Interaction
			done := make(chan struct{})
			b.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
				received = i
				close(done)
			})
			session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

			ic := &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					ChannelID: tc.channelID, GuildID: tc.guildID,
					Type: discordgo.InteractionApplicationCommand,
					Data: discordgo.ApplicationCommandInteractionData{Name: tc.intName, Options: tc.options},
				},
			}
			b.handleInteraction(nil, ic)
			<-done

			require.NotNil(s.T(), received)
			require.Equal(s.T(), tc.wantCmd, received.CommandName)
			require.Equal(s.T(), tc.channelID, received.ChannelID)
			require.Equal(s.T(), tc.guildID, received.GuildID)
			for k, v := range tc.wantOpts {
				require.Equal(s.T(), v, received.Options[k])
			}
			session.AssertExpectations(s.T())
		})
	}
}

func (s *BotSuite) TestOnInteractionRespondError() {
	var received *orchestrator.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
		received = i
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
	s.bot.OnInteraction(func(_ context.Context, _ *orchestrator.Interaction) {
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
	s.bot.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
		received = i
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
	s.bot.OnInteraction(func(_ context.Context, _ *orchestrator.Interaction) {
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
	s.bot.OnInteraction(func(_ context.Context, _ *orchestrator.Interaction) {})

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
	s.bot.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
		received = i
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

func (s *BotSuite) TestHandleMessageIgnored() {
	tests := []struct {
		name string
		msg  *discordgo.Message
	}{
		{"nil author", &discordgo.Message{Author: nil}},
		{"bot message", &discordgo.Message{Author: &discordgo.User{ID: "bot-123"}, Content: "just a normal response"}},
		{
			"bot reply to self", &discordgo.Message{
				ID: "msg-2", ChannelID: "ch-2", Content: "The last commit is abc123",
				Author: &discordgo.User{ID: "bot-123", Username: "LoopBot"},
				Mentions: []*discordgo.User{{ID: "bot-123"}},
				MessageReference:  &discordgo.MessageReference{MessageID: "msg-1"},
				ReferencedMessage: &discordgo.Message{Author: &discordgo.User{ID: "bot-123"}},
			},
		},
		{
			"non-triggered", &discordgo.Message{
				GuildID: "g-1", Author: &discordgo.User{ID: "user-1"}, Content: "just a random message",
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			called := false
			b.OnMessage(func(_ context.Context, _ *orchestrator.IncomingMessage) { called = true })
			b.handleMessage(nil, &discordgo.MessageCreate{Message: tc.msg})
			require.False(s.T(), called)
		})
	}
}

func (s *BotSuite) TestHandleMessageTriggered() {
	tests := []struct {
		name        string
		msg         *discordgo.Message
		wantContent string
		wantMention bool
		wantDM      bool
	}{
		{
			name: "bot self-mention",
			msg: &discordgo.Message{
				ID: "msg-1", ChannelID: "ch-2", GuildID: "g-1", Content: "<@bot-123> check the last commit",
				Author: &discordgo.User{ID: "bot-123", Username: "LoopBot"}, Mentions: []*discordgo.User{{ID: "bot-123"}},
			},
			wantContent: "check the last commit", wantMention: true,
		},
		{
			name: "bot self-mention content fallback",
			msg: &discordgo.Message{
				ID: "msg-1", ChannelID: "ch-2", GuildID: "g-1", Content: "<@bot-123> check the last commit",
				Author: &discordgo.User{ID: "bot-123", Username: "LoopBot"}, Mentions: []*discordgo.User{},
			},
			wantContent: "check the last commit", wantMention: true,
		},
		{
			name: "DM always triggered",
			msg: &discordgo.Message{
				ID: "msg-1", ChannelID: "dm-ch-1", GuildID: "", Content: "hello in DM",
				Author: &discordgo.User{ID: "user-1", Username: "testuser"},
			},
			wantContent: "hello in DM", wantDM: true,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			session.On("GuildMember", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, errors.New("not mocked")).Maybe()
			var received *orchestrator.IncomingMessage
			done := make(chan struct{})
			b.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
				received = msg
				close(done)
			})
			b.handleMessage(nil, &discordgo.MessageCreate{Message: tc.msg})
			<-done
			require.NotNil(s.T(), received)
			require.Equal(s.T(), tc.wantContent, received.Content)
			require.Equal(s.T(), tc.wantMention, received.IsBotMention)
			require.Equal(s.T(), tc.wantDM, received.IsDM)
		})
	}
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
	handler := func(_ context.Context, _ *orchestrator.Interaction) {
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

func (s *TriggerSuite) TestParseIncomingMessage() {
	tests := []struct {
		name         string
		msg          *discordgo.Message
		wantNil      bool
		wantContent  string
		wantMention  bool
		wantReply    bool
		wantPrefix   bool
		wantDM       bool
		wantChannel  string
		wantGuild    string
		wantAuthorID string
		wantAuthor   string
	}{
		{
			name: "mention",
			msg: &discordgo.Message{
				ID: "msg-1", ChannelID: "ch-1", GuildID: "g-1", Content: "<@bot-1> hello there",
				Author: &discordgo.User{ID: "user-1", Username: "alice"}, Mentions: []*discordgo.User{{ID: "bot-1"}},
			},
			wantContent: "hello there", wantMention: true,
			wantChannel: "ch-1", wantGuild: "g-1", wantAuthorID: "user-1", wantAuthor: "alice",
		},
		{
			name:        "prefix",
			msg:         &discordgo.Message{ID: "msg-1", GuildID: "g-1", Content: "!loop what is Go?", Author: &discordgo.User{ID: "user-1", Username: "bob"}},
			wantContent: "what is Go?", wantPrefix: true, wantGuild: "g-1",
		},
		{
			name: "reply",
			msg: &discordgo.Message{
				ID: "msg-2", GuildID: "g-1", Content: "thanks", Author: &discordgo.User{ID: "user-1", Username: "carol"},
				MessageReference: &discordgo.MessageReference{MessageID: "msg-1"}, ReferencedMessage: &discordgo.Message{Author: &discordgo.User{ID: "bot-1"}},
			},
			wantContent: "thanks", wantReply: true, wantGuild: "g-1",
		},
		{
			name:    "no trigger",
			msg:     &discordgo.Message{ID: "msg-1", GuildID: "g-1", Content: "just chatting", Author: &discordgo.User{ID: "user-1", Username: "dave"}},
			wantNil: true,
		},
		{
			name: "DM",
			msg: &discordgo.Message{
				ID: "msg-1", ChannelID: "dm-ch-1", GuildID: "", Content: "hello from DM",
				Author: &discordgo.User{ID: "user-1", Username: "eve"},
			},
			wantContent: "hello from DM", wantDM: true,
			wantChannel: "dm-ch-1", wantAuthorID: "user-1", wantAuthor: "eve",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			msg := parseIncomingMessage(&discordgo.MessageCreate{Message: tc.msg}, "bot-1")
			if tc.wantNil {
				require.Nil(s.T(), msg)
				return
			}
			require.NotNil(s.T(), msg)
			require.Equal(s.T(), tc.wantContent, msg.Content)
			require.Equal(s.T(), tc.wantMention, msg.IsBotMention)
			require.Equal(s.T(), tc.wantReply, msg.IsReplyToBot)
			require.Equal(s.T(), tc.wantPrefix, msg.HasPrefix)
			require.Equal(s.T(), tc.wantDM, msg.IsDM)
			if tc.wantChannel != "" {
				require.Equal(s.T(), tc.wantChannel, msg.ChannelID)
			}
			if tc.wantGuild != "" {
				require.Equal(s.T(), tc.wantGuild, msg.GuildID)
			}
			if tc.wantAuthorID != "" {
				require.Equal(s.T(), tc.wantAuthorID, msg.AuthorID)
				require.Equal(s.T(), tc.wantAuthor, msg.AuthorName)
			}
		})
	}
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

func (s *BotSuite) TestHandleInteractionPendingStorage() {
	tests := []struct {
		name       string
		respondErr error
		wantStored bool
	}{
		{"stores on success", nil, true},
		{"not stored on error", errors.New("respond failed"), false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.OnInteraction(func(_ context.Context, _ *orchestrator.Interaction) {})
			session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(tc.respondErr)
			interaction := &discordgo.Interaction{
				ChannelID: "ch-1", Type: discordgo.InteractionApplicationCommand,
				Data: discordgo.ApplicationCommandInteractionData{
					Name:    "loop",
					Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand}},
				},
			}
			b.handleInteraction(nil, &discordgo.InteractionCreate{Interaction: interaction})
			b.mu.RLock()
			pending, ok := b.pendingInteractions["ch-1"]
			b.mu.RUnlock()
			require.Equal(s.T(), tc.wantStored, ok)
			if tc.wantStored {
				require.Same(s.T(), interaction, pending)
			}
		})
	}
}

// --- SendMessage with pending interaction ---

func (s *BotSuite) TestSendMessageWithPendingInteraction() {
	tests := []struct {
		name    string
		content string
		setup   func(*MockSession, *discordgo.Interaction)
		wantErr string
	}{
		{
			name: "success", content: "hello from interaction",
			setup: func(ss *MockSession, i *discordgo.Interaction) {
				content := "hello from interaction"
				ss.On("InteractionResponseEdit", i, &discordgo.WebhookEdit{Content: &content}, mock.Anything).Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "split", content: strings.Repeat("a", 2500),
			setup: func(ss *MockSession, i *discordgo.Interaction) {
				ss.On("InteractionResponseEdit", i, &discordgo.WebhookEdit{Content: new(strings.Repeat("a", 2000))}, mock.Anything).Return(&discordgo.Message{}, nil)
				ss.On("FollowupMessageCreate", i, true, &discordgo.WebhookParams{Content: strings.Repeat("a", 500)}, mock.Anything).Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "edit error", content: "hello",
			setup: func(ss *MockSession, i *discordgo.Interaction) {
				content := "hello"
				ss.On("InteractionResponseEdit", i, &discordgo.WebhookEdit{Content: &content}, mock.Anything).Return(nil, errors.New("edit failed"))
			},
			wantErr: "discord interaction edit",
		},
		{
			name: "followup error", content: strings.Repeat("a", 2500),
			setup: func(ss *MockSession, i *discordgo.Interaction) {
				ss.On("InteractionResponseEdit", i, &discordgo.WebhookEdit{Content: new(strings.Repeat("a", 2000))}, mock.Anything).Return(&discordgo.Message{}, nil)
				ss.On("FollowupMessageCreate", i, true, &discordgo.WebhookParams{Content: strings.Repeat("a", 500)}, mock.Anything).Return(nil, errors.New("followup failed"))
			},
			wantErr: "discord followup create",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			interaction := &discordgo.Interaction{ChannelID: "ch-1"}
			b.mu.Lock()
			b.pendingInteractions["ch-1"] = interaction
			b.mu.Unlock()
			tc.setup(session, interaction)
			err := b.SendMessage(context.Background(), &orchestrator.OutgoingMessage{ChannelID: "ch-1", Content: tc.content})
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertExpectations(s.T())
		})
	}
}

// --- CreateChannel ---

func (s *BotSuite) TestCreateChannel() {
	tests := []struct {
		name      string
		setup     func(*MockSession)
		wantID    string
		wantErr   string
	}{
		{
			name: "success",
			setup: func(ss *MockSession) {
				ss.On("GuildChannels", "g-1", mock.Anything).Return([]*discordgo.Channel{}, nil)
				ss.On("GuildChannelCreate", "g-1", "loop", discordgo.ChannelTypeGuildText, mock.Anything).
					Return(&discordgo.Channel{ID: "new-ch-1"}, nil)
			},
			wantID: "new-ch-1",
		},
		{
			name: "create error",
			setup: func(ss *MockSession) {
				ss.On("GuildChannels", "g-1", mock.Anything).Return([]*discordgo.Channel{}, nil)
				ss.On("GuildChannelCreate", "g-1", "loop", discordgo.ChannelTypeGuildText, mock.Anything).
					Return(nil, errors.New("create failed"))
			},
			wantErr: "discord create channel",
		},
		{
			name: "existing",
			setup: func(ss *MockSession) {
				ss.On("GuildChannels", "g-1", mock.Anything).Return([]*discordgo.Channel{
					{ID: "ch-other", Name: "other", Type: discordgo.ChannelTypeGuildText},
					{ID: "ch-loop", Name: "loop", Type: discordgo.ChannelTypeGuildText},
				}, nil)
			},
			wantID: "ch-loop",
		},
		{
			name: "existing wrong type",
			setup: func(ss *MockSession) {
				ss.On("GuildChannels", "g-1", mock.Anything).Return([]*discordgo.Channel{
					{ID: "ch-voice", Name: "loop", Type: discordgo.ChannelTypeGuildVoice},
				}, nil)
				ss.On("GuildChannelCreate", "g-1", "loop", discordgo.ChannelTypeGuildText, mock.Anything).
					Return(&discordgo.Channel{ID: "new-ch-1"}, nil)
			},
			wantID: "new-ch-1",
		},
		{
			name: "list error",
			setup: func(ss *MockSession) {
				ss.On("GuildChannels", "g-1", mock.Anything).Return(nil, errors.New("list failed"))
			},
			wantErr: "discord list channels",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			tc.setup(session)
			channelID, err := b.CreateChannel(context.Background(), "g-1", "loop")
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.wantID, channelID)
			}
			session.AssertExpectations(s.T())
		})
	}
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

// --- RenameThread ---

func (s *BotSuite) TestRenameThreadSuccess() {
	s.session.On("ChannelEdit", "thread-1", &discordgo.ChannelEdit{Name: "new name"}, mock.Anything).
		Return(&discordgo.Channel{ID: "thread-1"}, nil)

	err := s.bot.RenameThread(context.Background(), "thread-1", "new name")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRenameThreadError() {
	s.session.On("ChannelEdit", "thread-1", &discordgo.ChannelEdit{Name: "new name"}, mock.Anything).
		Return(nil, errors.New("edit failed"))

	err := s.bot.RenameThread(context.Background(), "thread-1", "new name")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord rename thread")
}

// --- handleThreadCreate ---

func (s *BotSuite) TestHandleThreadCreate() {
	tests := []struct {
		name     string
		channel  *discordgo.Channel
		wantJoin bool
		joinErr  error
	}{
		{"public thread", &discordgo.Channel{ID: "thread-1", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "ch-1"}, true, nil},
		{"private thread", &discordgo.Channel{ID: "thread-2", Type: discordgo.ChannelTypeGuildPrivateThread, ParentID: "ch-1"}, true, nil},
		{"ignores non-thread", &discordgo.Channel{ID: "ch-1", Type: discordgo.ChannelTypeGuildText}, false, nil},
		{"join error", &discordgo.Channel{ID: "thread-1", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "ch-1"}, true, errors.New("join failed")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			if tc.wantJoin {
				session.On("ThreadJoin", tc.channel.ID, mock.Anything).Return(tc.joinErr)
			}
			b.handleThreadCreate(nil, &discordgo.ThreadCreate{Channel: tc.channel})
			session.AssertExpectations(s.T())
		})
	}
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

func (s *BotSuite) TestGetChannelParentID() {
	tests := []struct {
		name       string
		channelID  string
		channel    *discordgo.Channel
		err        error
		wantParent string
		wantErr    string
	}{
		{
			name: "thread", channelID: "thread-1",
			channel: &discordgo.Channel{ID: "thread-1", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "ch-1"},
			wantParent: "ch-1",
		},
		{
			name: "not thread", channelID: "ch-1",
			channel: &discordgo.Channel{ID: "ch-1", Type: discordgo.ChannelTypeGuildText},
		},
		{
			name: "error", channelID: "ch-1", err: errors.New("api error"), wantErr: "discord get channel",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			session.On("Channel", tc.channelID, mock.Anything).Return(tc.channel, tc.err)
			parentID, err := b.GetChannelParentID(context.Background(), tc.channelID)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tc.wantParent, parentID)
		})
	}
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

func (s *BotSuite) TestHandleMessageRolePopulation() {
	tests := []struct {
		name      string
		member    *discordgo.Member
		memberErr error
		wantRoles []string
	}{
		{"success", &discordgo.Member{Roles: []string{"role-admin"}}, nil, []string{"role-admin"}},
		{"fetch error", nil, errors.New("not found"), nil},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			session.On("GuildMember", "g-1", "user-1", mock.Anything).Return(tc.member, tc.memberErr)
			var received *orchestrator.IncomingMessage
			done := make(chan struct{})
			b.OnMessage(func(_ context.Context, msg *orchestrator.IncomingMessage) {
				received = msg
				close(done)
			})
			b.handleMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
				ID: "msg-1", ChannelID: "ch-1", GuildID: "g-1", Content: "!loop hello",
				Author: &discordgo.User{ID: "user-1", Username: "testuser"}, Timestamp: time.Now(),
			}})
			<-done
			require.NotNil(s.T(), received)
			require.Equal(s.T(), tc.wantRoles, received.AuthorRoles)
		})
	}
}

// --- handleInteraction with AuthorID/AuthorRoles ---

func (s *BotSuite) TestHandleInteractionAuthor() {
	tests := []struct {
		name      string
		member    *discordgo.Member
		user      *discordgo.User
		guildID   string
		wantID    string
		wantRoles []string
	}{
		{
			name:    "guild member",
			member:  &discordgo.Member{User: &discordgo.User{ID: "user-guild"}, Roles: []string{"role-a", "role-b"}},
			guildID: "g-1", wantID: "user-guild", wantRoles: []string{"role-a", "role-b"},
		},
		{
			name: "DM user", user: &discordgo.User{ID: "user-dm"}, wantID: "user-dm",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			var received *orchestrator.Interaction
			done := make(chan struct{})
			b.OnInteraction(func(_ context.Context, i *orchestrator.Interaction) {
				received = i
				close(done)
			})
			session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			ic := &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					ChannelID: "ch-1", GuildID: tc.guildID,
					Type: discordgo.InteractionApplicationCommand, Member: tc.member, User: tc.user,
					Data: discordgo.ApplicationCommandInteractionData{
						Name:    "loop",
						Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand}},
					},
				},
			}
			b.handleInteraction(nil, ic)
			<-done
			require.NotNil(s.T(), received)
			require.Equal(s.T(), tc.wantID, received.AuthorID)
			require.Equal(s.T(), tc.wantRoles, received.AuthorRoles)
		})
	}
}

// --- PostMessage ---

func (s *BotSuite) TestPostMessage() {
	tests := []struct {
		name        string
		content     string
		botUserID   string
		botUsername string
		expectedMsg string
		sendErr     error
		wantErr     string
	}{
		{name: "success", content: "hello", expectedMsg: "hello"},
		{name: "converts text mention", content: "@LoopBot check the last commit", botUserID: "bot-123", botUsername: "LoopBot", expectedMsg: "<@bot-123> check the last commit"},
		{name: "converts text mention case insensitive", content: "@loopbot check commits", botUserID: "bot-123", botUsername: "LoopBot", expectedMsg: "<@bot-123> check commits"},
		{name: "error", content: "hello", expectedMsg: "hello", sendErr: errors.New("send failed"), wantErr: "discord post message"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			if tc.botUserID != "" {
				b.botUserID = tc.botUserID
				b.botUsername = tc.botUsername
			}
			var ret *discordgo.Message
			if tc.sendErr == nil {
				ret = &discordgo.Message{}
			}
			session.On("ChannelMessageSend", "ch-1", tc.expectedMsg, mock.Anything).Return(ret, tc.sendErr)
			err := b.PostMessage(context.Background(), "ch-1", tc.content)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertExpectations(s.T())
		})
	}
}

// --- CreateSimpleThread tests ---

func (s *BotSuite) TestCreateSimpleThread() {
	tests := []struct {
		name     string
		title    string
		message  string
		setup    func(*MockSession)
		wantID   string
		wantErr  string
	}{
		{
			name: "success", title: "task output", message: "First turn content",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task output", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(&discordgo.Channel{ID: "thread-1"}, nil)
				ss.On("ChannelMessageSend", "thread-1", "First turn content", mock.Anything).Return(&discordgo.Message{}, nil)
			},
			wantID: "thread-1",
		},
		{
			name: "empty message", title: "task name", message: "",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task name", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(&discordgo.Channel{ID: "thread-2"}, nil)
			},
			wantID: "thread-2",
		},
		{
			name: "start error", title: "task", message: "content",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(nil, errors.New("thread start failed"))
			},
			wantErr: "discord create simple thread",
		},
		{
			name: "message send error", title: "task", message: "content",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(&discordgo.Channel{ID: "thread-3"}, nil)
				ss.On("ChannelMessageSend", "thread-3", "content", mock.Anything).Return(nil, errors.New("send failed"))
			},
			wantID: "thread-3", // message send error is logged but doesn't fail creation
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			tc.setup(session)
			threadID, err := b.CreateSimpleThread(context.Background(), "ch-1", tc.title, tc.message)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
				require.Empty(s.T(), threadID)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.wantID, threadID)
			}
			session.AssertExpectations(s.T())
		})
	}
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
