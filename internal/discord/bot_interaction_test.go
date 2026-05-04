package discord

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.OnInteraction(func(_ context.Context, _ *bot.Interaction) {})
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			interaction := &discordgo.Interaction{ChannelID: "ch-1"}
			b.mu.Lock()
			b.pendingInteractions["ch-1"] = interaction
			b.mu.Unlock()
			tc.setup(session, interaction)
			err := b.SendMessage(context.Background(), &bot.OutgoingMessage{ChannelID: "ch-1", Content: tc.content})
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
