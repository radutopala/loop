package discord

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- BotUserID ---

func (s *BotSuite) TestBotUserIDEmpty() {
	require.Equal(s.T(), "", s.bot.BotUserID())
}

func (s *BotSuite) TestIsBotUser() {
	s.bot.botUserID = "bot-123"
	require.True(s.T(), s.bot.IsBotUser("bot-123"))
	require.False(s.T(), s.bot.IsBotUser("other"))
}

// --- Trigger detection (table-driven) ---

type TriggerSuite struct {
	suite.Suite
	session *MockSession
	bot     *DiscordBot
}

func TestTriggerSuite(t *testing.T) {
	suite.Run(t, new(TriggerSuite))
}

func (s *TriggerSuite) SetupTest() {
	s.session = new(MockSession)
	s.bot = &DiscordBot{
		session: s.session,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
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
		channel  *discordgo.Channel
		chanErr  error
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
					ChannelID:         "ch-1",
					MessageReference:  &discordgo.MessageReference{MessageID: "ref-1"},
					ReferencedMessage: &discordgo.Message{Author: &discordgo.User{ID: "other"}},
				},
			},
			botID:    "bot-1",
			channel:  &discordgo.Channel{ID: "ch-1", Type: discordgo.ChannelTypeGuildText},
			expected: false,
		},
		{
			name: "no message reference, not a thread",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{ChannelID: "ch-1"},
			},
			botID:    "bot-1",
			channel:  &discordgo.Channel{ID: "ch-1", Type: discordgo.ChannelTypeGuildText},
			expected: false,
		},
		{
			name: "no referenced message",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{
					ChannelID:        "ch-1",
					MessageReference: &discordgo.MessageReference{MessageID: "ref-1"},
				},
			},
			botID:    "bot-1",
			channel:  &discordgo.Channel{ID: "ch-1", Type: discordgo.ChannelTypeGuildText},
			expected: false,
		},
		{
			name: "referenced message no author",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{
					ChannelID:         "ch-1",
					MessageReference:  &discordgo.MessageReference{MessageID: "ref-1"},
					ReferencedMessage: &discordgo.Message{Author: nil},
				},
			},
			botID:    "bot-1",
			channel:  &discordgo.Channel{ID: "ch-1", Type: discordgo.ChannelTypeGuildText},
			expected: false,
		},
		{
			name: "message in bot-owned thread",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{ChannelID: "thread-1"},
			},
			botID:    "bot-1",
			channel:  &discordgo.Channel{ID: "thread-1", Type: discordgo.ChannelTypeGuildPublicThread, OwnerID: "bot-1"},
			expected: true,
		},
		{
			name: "message in user-owned thread",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{ChannelID: "thread-2"},
			},
			botID:    "bot-1",
			channel:  &discordgo.Channel{ID: "thread-2", Type: discordgo.ChannelTypeGuildPublicThread, OwnerID: "user-1"},
			expected: false,
		},
		{
			name: "channel lookup error falls back to false",
			msg: &discordgo.MessageCreate{
				Message: &discordgo.Message{ChannelID: "ch-1"},
			},
			botID:    "bot-1",
			chanErr:  errors.New("not found"),
			expected: false,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := &DiscordBot{
				session: session,
				logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			if tc.channel != nil || tc.chanErr != nil {
				session.On("Channel", tc.msg.ChannelID, mock.Anything).
					Return(tc.channel, tc.chanErr)
			}
			require.Equal(s.T(), tc.expected, b.isReplyToBot(tc.msg, tc.botID))
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
			session := new(MockSession)
			b := &DiscordBot{
				session: session,
				logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			session.On("Channel", mock.Anything, mock.Anything).
				Maybe().
				Return(&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil)
			msg := b.parseIncomingMessage(&discordgo.MessageCreate{Message: tc.msg}, "bot-1")
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
