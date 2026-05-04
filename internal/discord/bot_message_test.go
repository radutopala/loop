package discord

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

// --- OnMessage / OnInteraction ---

func (s *BotSuite) TestOnMessageRegistersHandler() {
	var received *bot.IncomingMessage
	done := make(chan struct{})
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
		close(done)
	})

	s.bot.mu.Lock()
	s.bot.botUserID = "bot-123"
	s.bot.mu.Unlock()

	s.session.On("Channel", mock.Anything, mock.Anything).
		Maybe().
		Return(&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil)
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			var received *bot.Interaction
			done := make(chan struct{})
			b.OnInteraction(func(_ context.Context, i *bot.Interaction) {
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
	var received *bot.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i *bot.Interaction) {
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
	s.bot.OnInteraction(func(_ context.Context, _ *bot.Interaction) {
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
	var received *bot.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i *bot.Interaction) {
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
	s.bot.OnInteraction(func(_ context.Context, _ *bot.Interaction) {
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
	s.bot.OnInteraction(func(_ context.Context, _ *bot.Interaction) {})

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
	var received *bot.Interaction
	done := make(chan struct{})
	s.bot.OnInteraction(func(_ context.Context, i *bot.Interaction) {
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
				Author:            &discordgo.User{ID: "bot-123", Username: "LoopBot"},
				Mentions:          []*discordgo.User{{ID: "bot-123"}},
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			session.On("Channel", mock.Anything, mock.Anything).
				Maybe().
				Return(&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil)
			called := false
			b.OnMessage(func(_ context.Context, _ *bot.IncomingMessage) { called = true })
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			session.On("Channel", mock.Anything, mock.Anything).
				Maybe().
				Return(&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil)
			session.On("GuildMember", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, errors.New("not mocked")).Maybe()
			var received *bot.IncomingMessage
			done := make(chan struct{})
			b.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
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
	s.session.On("Channel", mock.Anything, mock.Anything).
		Maybe().
		Return(&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	handler := func(_ context.Context, _ *bot.IncomingMessage) {
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
	handler := func(_ context.Context, _ *bot.Interaction) {
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
