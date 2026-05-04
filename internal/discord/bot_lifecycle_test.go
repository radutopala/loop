package discord

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

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
		msg     *bot.OutgoingMessage
		setup   func(*MockSession)
		wantErr string
	}{
		{
			name: "simple",
			msg:  &bot.OutgoingMessage{ChannelID: "ch-1", Content: "hello"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "with reply",
			msg:  &bot.OutgoingMessage{ChannelID: "ch-1", Content: "hello", ReplyToMessageID: "msg-1"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSendReply", "ch-1", "hello", &discordgo.MessageReference{MessageID: "msg-1"}, mock.Anything).
					Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "split",
			msg:  &bot.OutgoingMessage{ChannelID: "ch-1", Content: strings.Repeat("a", 2500), ReplyToMessageID: "msg-1"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSendReply", "ch-1", strings.Repeat("a", 2000), &discordgo.MessageReference{MessageID: "msg-1"}, mock.Anything).
					Return(&discordgo.Message{}, nil)
				ss.On("ChannelMessageSend", "ch-1", strings.Repeat("a", 500), mock.Anything).Return(&discordgo.Message{}, nil)
			},
		},
		{
			name: "reply error",
			msg:  &bot.OutgoingMessage{ChannelID: "ch-1", Content: "hello", ReplyToMessageID: "msg-1"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSendReply", "ch-1", "hello", &discordgo.MessageReference{MessageID: "msg-1"}, mock.Anything).
					Return(nil, errors.New("send failed"))
			},
			wantErr: "discord send reply",
		},
		{
			name: "send error",
			msg:  &bot.OutgoingMessage{ChannelID: "ch-1", Content: "hello"},
			setup: func(ss *MockSession) {
				ss.On("ChannelMessageSend", "ch-1", "hello", mock.Anything).Return(nil, errors.New("send failed"))
			},
			wantErr: "discord send message",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
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
			b := NewBot(session, "test-app-id", "", slog.New(slog.NewTextHandler(discard{}, nil)))
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
