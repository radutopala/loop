package discord

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/types"
)

// --- CreateChannel ---

func (s *BotSuite) TestCreateChannel() {
	tests := []struct {
		name    string
		setup   func(*MockSession)
		wantID  string
		wantErr string
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			tc.setup(session)
			channelID, err := b.CreateChannel(context.Background(), "loop")
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

func (s *BotSuite) TestInviteUserToChannelRegularChannelNoOp() {
	s.session.On("Channel", "ch-1", mock.Anything).Return(&discordgo.Channel{
		ID:   "ch-1",
		Type: discordgo.ChannelTypeGuildText,
	}, nil)
	err := s.bot.InviteUserToChannel(context.Background(), "ch-1", "user-1")
	require.NoError(s.T(), err)
	s.session.AssertNotCalled(s.T(), "ThreadMemberAdd", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BotSuite) TestInviteUserToChannelThread() {
	s.session.On("Channel", "thread-1", mock.Anything).Return(&discordgo.Channel{
		ID:   "thread-1",
		Type: discordgo.ChannelTypeGuildPublicThread,
	}, nil)
	s.session.On("ThreadMemberAdd", "thread-1", "user-1", mock.Anything).Return(nil)
	err := s.bot.InviteUserToChannel(context.Background(), "thread-1", "user-1")
	require.NoError(s.T(), err)
	s.session.AssertCalled(s.T(), "ThreadMemberAdd", "thread-1", "user-1", mock.Anything)
}

func (s *BotSuite) TestInviteUserToChannelChannelError() {
	s.session.On("Channel", "ch-1", mock.Anything).Return(nil, errors.New("not found"))
	err := s.bot.InviteUserToChannel(context.Background(), "ch-1", "user-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord get channel")
}

func (s *BotSuite) TestInviteUserToChannelThreadMemberAddError() {
	s.session.On("Channel", "thread-1", mock.Anything).Return(&discordgo.Channel{
		ID:   "thread-1",
		Type: discordgo.ChannelTypeGuildPublicThread,
	}, nil)
	s.session.On("ThreadMemberAdd", "thread-1", "user-1", mock.Anything).Return(errors.New("api error"))
	err := s.bot.InviteUserToChannel(context.Background(), "thread-1", "user-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord add thread member")
}

func (s *BotSuite) TestGetOwnerUserIDSuccess() {
	s.session.On("Guild", "", mock.Anything).
		Return(&discordgo.Guild{OwnerID: "U-OWNER-123"}, nil)

	id, err := s.bot.GetOwnerUserID(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "U-OWNER-123", id)
}

func (s *BotSuite) TestGetOwnerUserIDError() {
	s.session.On("Guild", "", mock.Anything).
		Return(nil, errors.New("api error"))

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discord get guild")
}

// --- HandleIncomingMessage / HandleThreadCreated (no-ops) ---

func (s *BotSuite) TestHandleIncomingMessageNoOp() {
	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "hello", "")
}

func (s *BotSuite) TestHandleThreadCreatedPostsMessage() {
	s.bot.botUserID = "bot-123"
	s.session.On("ChannelMessageSend", "thread-1", "<@bot-123> do the task", mock.Anything).
		Return(&discordgo.Message{}, nil)

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "do the task")
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleThreadCreatedStripsExistingMention() {
	s.bot.botUserID = "bot-123"
	s.session.On("ChannelMessageSend", "thread-1", "<@bot-123> do the task", mock.Anything).
		Return(&discordgo.Message{}, nil)

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "<@bot-123> do the task")
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleThreadCreatedStripsTextMention() {
	s.bot.botUserID = "bot-123"
	s.bot.botUsername = "LoopBot"
	s.session.On("ChannelMessageSend", "thread-1", "<@bot-123> do the task", mock.Anything).
		Return(&discordgo.Message{}, nil)

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "@LoopBot do the task")
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleThreadCreatedEmptyMessage() {
	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "")
	s.session.AssertNotCalled(s.T(), "ChannelMessageSend", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BotSuite) TestHandleThreadCreatedSendError() {
	s.bot.botUserID = "bot-123"
	s.session.On("ChannelMessageSend", "thread-1", "<@bot-123> hi", mock.Anything).
		Return(nil, errors.New("send failed"))

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "hi")
	s.session.AssertExpectations(s.T())
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
		{"with message", "", "", "Do the task", "<@bot-123> Do the task"},
		{"strips text mention", "LoopBot", "", "@LoopBot Do the task", "<@bot-123> Do the task"},
		{"strips discord mention", "", "", "<@bot-123> Do the task", "<@bot-123> Do the task"},
		{"message and mention user", "", "user-42", "Do the task", "<@bot-123> Do the task <@user-42>"},
		{"mention user no message", "", "user-42", "", "<@user-42>"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
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

	threadID, err := s.bot.CreateThread(context.Background(), "ch-1", "my-thread", "", "Do the task")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestCreateThreadMentionSendError() {
	s.bot.botUserID = "bot-123"
	s.session.On("ThreadStart", "ch-1", "my-thread", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(&discordgo.Channel{ID: "thread-1"}, nil)
	s.session.On("ChannelMessageSend", "thread-1", "<@user-42>", mock.Anything).
		Return(nil, errors.New("send failed"))

	threadID, err := s.bot.CreateThread(context.Background(), "ch-1", "my-thread", "user-42", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestCreateThreadError() {
	s.session.On("ThreadStart", "ch-1", "my-thread", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
		Return(nil, errors.New("thread create failed"))

	threadID, err := s.bot.CreateThread(context.Background(), "ch-1", "my-thread", "", "Do the task")
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
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
	handler := func(ctx context.Context, channelID string, platform types.Platform) {}
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
			channel:    &discordgo.Channel{ID: "thread-1", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "ch-1"},
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
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
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			b.botUserID = "bot-123"
			session.On("Channel", mock.Anything, mock.Anything).
				Maybe().
				Return(&discordgo.Channel{Type: discordgo.ChannelTypeGuildText}, nil)
			session.On("GuildMember", "g-1", "user-1", mock.Anything).Return(tc.member, tc.memberErr)
			var received *bot.IncomingMessage
			done := make(chan struct{})
			b.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
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
