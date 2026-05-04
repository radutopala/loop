package slack

import (
	"context"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/types"
)

// --- handleMemberJoinedChannel tests ---

func (s *BotSuite) TestHandleMemberJoinedChannelBotJoins() {
	received := make(chan string, 1)
	s.bot.OnChannelJoin(func(_ context.Context, channelID string, _ types.Platform) {
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
	s.bot.OnChannelJoin(func(_ context.Context, _ string, _ types.Platform) {
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
	s.bot.OnChannelJoin(func(_ context.Context, channelID string, _ types.Platform) {
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

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
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
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
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
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
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

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
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
