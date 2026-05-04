package slack

import (
	"context"
	"errors"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- CreateChannel ---

func (s *BotSuite) TestCreateChannelSuccess() {
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "test-channel",
	}).Return(&goslack.Channel{GroupConversation: goslack.GroupConversation{
		Conversation: goslack.Conversation{ID: "C456"},
	}}, nil)

	id, err := s.bot.CreateChannel(context.Background(), "test-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C456", id)
}

func (s *BotSuite) TestCreateChannelError() {
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "test-channel",
	}).Return(nil, errors.New("some_other_error"))

	_, err := s.bot.CreateChannel(context.Background(), "test-channel")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack create channel")
}

func (s *BotSuite) TestCreateChannelNameTaken() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	s.session.On("GetConversations", mock.AnythingOfType("*slack.GetConversationsParameters")).Return(
		[]goslack.Channel{
			{GroupConversation: goslack.GroupConversation{
				Name:         "other-channel",
				Conversation: goslack.Conversation{ID: "C111"},
			}},
			{GroupConversation: goslack.GroupConversation{
				Name:         "existing-channel",
				Conversation: goslack.Conversation{ID: "C222"},
			}},
		}, "", nil,
	)

	id, err := s.bot.CreateChannel(context.Background(), "existing-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C222", id)
}

func (s *BotSuite) TestCreateChannelNameTakenLookupError() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	s.session.On("GetConversations", mock.AnythingOfType("*slack.GetConversationsParameters")).Return(
		nil, "", errors.New("api_error"),
	)

	_, err := s.bot.CreateChannel(context.Background(), "existing-channel")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack find existing channel")
}

func (s *BotSuite) TestCreateChannelNameTakenNotFound() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	s.session.On("GetConversations", mock.AnythingOfType("*slack.GetConversationsParameters")).Return(
		[]goslack.Channel{}, "", nil,
	)

	_, err := s.bot.CreateChannel(context.Background(), "existing-channel")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *BotSuite) TestCreateChannelNameTakenPagination() {
	nameTakenErr := goslack.SlackErrorResponse{Err: "name_taken"}
	s.session.On("CreateConversation", goslack.CreateConversationParams{
		ChannelName: "existing-channel",
	}).Return(nil, nameTakenErr)

	// First page: no match, has next cursor
	s.session.On("GetConversations", mock.MatchedBy(func(p *goslack.GetConversationsParameters) bool {
		return p.Cursor == ""
	})).Return(
		[]goslack.Channel{
			{GroupConversation: goslack.GroupConversation{
				Name:         "other-channel",
				Conversation: goslack.Conversation{ID: "C111"},
			}},
		}, "next_cursor_1", nil,
	)

	// Second page: match found
	s.session.On("GetConversations", mock.MatchedBy(func(p *goslack.GetConversationsParameters) bool {
		return p.Cursor == "next_cursor_1"
	})).Return(
		[]goslack.Channel{
			{GroupConversation: goslack.GroupConversation{
				Name:         "existing-channel",
				Conversation: goslack.Conversation{ID: "C333"},
			}},
		}, "", nil,
	)

	id, err := s.bot.CreateChannel(context.Background(), "existing-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C333", id)
}

// --- InviteUserToChannel ---

func (s *BotSuite) TestInviteUserToChannelSuccess() {
	s.session.On("InviteUsersToConversation", "C456", []string{"U789"}).
		Return(&goslack.Channel{}, nil)

	err := s.bot.InviteUserToChannel(context.Background(), "C456", "U789")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestInviteUserToChannelError() {
	s.session.On("InviteUsersToConversation", "C456", []string{"U789"}).
		Return(nil, errors.New("not_in_channel"))

	err := s.bot.InviteUserToChannel(context.Background(), "C456", "U789")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack invite user to channel")
}

func (s *BotSuite) TestInviteUserToChannelSkipsThreads() {
	// Thread IDs contain ":" — inviting to threads is a no-op in Slack
	err := s.bot.InviteUserToChannel(context.Background(), "C456:1234567890.123456", "U789")
	require.NoError(s.T(), err)
	s.session.AssertNotCalled(s.T(), "InviteUsersToConversation", mock.Anything, mock.Anything)
}

// --- GetOwnerUserID ---

func (s *BotSuite) TestGetOwnerUserIDSuccess() {
	s.session.On("GetUsers", mock.Anything).Return([]goslack.User{
		{ID: "U111", Name: "bot", IsBot: true, IsOwner: false},
		{ID: "U222", Name: "member", IsBot: false, IsOwner: false},
		{ID: "U333", Name: "owner", IsBot: false, IsOwner: true},
	}, nil)

	id, err := s.bot.GetOwnerUserID(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "U333", id)
}

func (s *BotSuite) TestGetOwnerUserIDNotFound() {
	s.session.On("GetUsers", mock.Anything).Return([]goslack.User{
		{ID: "U111", Name: "member", IsBot: false, IsOwner: false},
	}, nil)

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no workspace owner found")
}

func (s *BotSuite) TestGetOwnerUserIDError() {
	s.session.On("GetUsers", mock.Anything).Return(nil, errors.New("api_error"))

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack get users")
}

func (s *BotSuite) TestGetOwnerUserIDSkipsBot() {
	// A bot that is also marked as owner should be skipped.
	s.session.On("GetUsers", mock.Anything).Return([]goslack.User{
		{ID: "U111", Name: "bot-owner", IsBot: true, IsOwner: true},
	}, nil)

	_, err := s.bot.GetOwnerUserID(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no workspace owner found")
}

// --- HandleIncomingMessage / HandleThreadCreated (no-ops) ---

func (s *BotSuite) TestHandleIncomingMessageNoOp() {
	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "hello", "")
}

func (s *BotSuite) TestHandleThreadCreatedPostsMessage() {
	s.bot.botUserID = "U123BOT"
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "9999.1111", nil)

	s.bot.HandleThreadCreated(context.Background(), "C123:1234.5678", "user-1", "do the task")
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleThreadCreatedStripsExistingMention() {
	s.bot.botUserID = "U123BOT"
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "9999.1111", nil)

	s.bot.HandleThreadCreated(context.Background(), "C123:1234.5678", "user-1", "<@U123BOT> do the task")
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleThreadCreatedStripsTextMention() {
	s.bot.botUserID = "U123BOT"
	s.bot.botUsername = "LoopBot"
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "9999.1111", nil)

	s.bot.HandleThreadCreated(context.Background(), "C123:1234.5678", "user-1", "@LoopBot do the task")
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestHandleThreadCreatedEmptyMessage() {
	s.bot.HandleThreadCreated(context.Background(), "C123:1234.5678", "user-1", "")
	s.session.AssertNotCalled(s.T(), "PostMessage", mock.Anything, mock.Anything)
}

func (s *BotSuite) TestHandleThreadCreatedNoThreadTS() {
	s.bot.HandleThreadCreated(context.Background(), "C123", "user-1", "hi")
	s.session.AssertNotCalled(s.T(), "PostMessage", mock.Anything, mock.Anything)
}

func (s *BotSuite) TestHandleThreadCreatedSendError() {
	s.bot.botUserID = "U123BOT"
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("send failed"))

	s.bot.HandleThreadCreated(context.Background(), "C123:1234.5678", "user-1", "hi")
	s.session.AssertExpectations(s.T())
}

// --- SetChannelTopic ---

func (s *BotSuite) TestSetChannelTopicSuccess() {
	s.session.On("SetTopicOfConversation", "C123", "/home/user/dev/loop").
		Return(&goslack.Channel{}, nil)

	err := s.bot.SetChannelTopic(context.Background(), "C123", "/home/user/dev/loop")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSetChannelTopicError() {
	s.session.On("SetTopicOfConversation", "C123", "/path").
		Return(nil, errors.New("topic_error"))

	err := s.bot.SetChannelTopic(context.Background(), "C123", "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack set channel topic")
}

// --- CreateThread ---

func (s *BotSuite) TestCreateThreadSuccess() {
	tests := []struct {
		name     string
		channel  string
		thread   string
		authorID string
		message  string
	}{
		{
			name:     "with explicit message",
			channel:  "C123",
			thread:   "my thread",
			authorID: "U456",
			message:  "<@U123BOT> do something",
		},
		{
			name:     "with mention and message",
			channel:  "C123",
			thread:   "my thread",
			authorID: "U456",
			message:  "Check the status",
		},
		{
			name:     "message only",
			channel:  "C123",
			thread:   "my thread",
			authorID: "",
			message:  "Do the task",
		},
		{
			name:     "mention user no message",
			channel:  "C123",
			thread:   "my thread",
			authorID: "U456",
			message:  "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"
			bot.botUsername = "loopbot"

			session.On("PostMessage", tt.channel, mock.Anything).Return(tt.channel, "9999.0000", nil)

			id, err := bot.CreateThread(context.Background(), tt.channel, tt.thread, tt.authorID, tt.message)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "C123:9999.0000", id)
		})
	}
}

func (s *BotSuite) TestCreateThreadError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("not_in_channel"))

	_, err := s.bot.CreateThread(context.Background(), "C123", "thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack create thread")
}

// --- CreateSimpleThread ---

func (s *BotSuite) TestCreateSimpleThread() {
	tests := []struct {
		name    string
		parent  string
		thName  string
		msg     string
		postTS  string
		postErr error
		wantID  string
		wantErr string
	}{
		{"with_message", "C123", "task output", "First turn", "1111.2222", nil, "C123:1111.2222", ""},
		{"empty_message", "C123", "task name", "", "2222.3333", nil, "C123:2222.3333", ""},
		{"composite_parent", "C123:old.ts", "task", "content", "3333.4444", nil, "C123:3333.4444", ""},
		{"error", "C123", "task", "content", "", errors.New("post failed"), "", "slack create simple thread"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"
			bot.botUsername = "loopbot"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				// Parent message (name as thread title)
				session.On("PostMessage", "C123", mock.Anything).Return("C123", tt.postTS, nil)
			}

			id, err := bot.CreateSimpleThread(context.Background(), tt.parent, tt.thName, tt.msg)
			if tt.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantID, id)
			}
		})
	}
}

func (s *BotSuite) TestCreateSimpleThreadReplyError() {
	// Parent succeeds but reply fails — thread is still created, error just logged.
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())
	bot.botUserID = "U123BOT"

	session.On("PostMessage", "C123", mock.Anything).Return("C123", "1111.2222", nil).Once()
	session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("reply failed")).Once()

	id, err := bot.CreateSimpleThread(context.Background(), "C123", "thread name", "reply content")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "C123:1111.2222", id)
}

// --- PostMessage ---

func (s *BotSuite) TestPostMessage() {
	tests := []struct {
		name      string
		channelID string
		text      string
		postErr   error
		wantErr   string
	}{
		{"plain", "C123", "hello", nil, ""},
		{"composite", "C123:1111.2222", "reply", nil, ""},
		{"mention_conversion", "C123", "hey @loopbot do this", nil, ""},
		{"error", "C123", "hello", errors.New("channel_not_found"), "slack post message"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"
			bot.botUsername = "loopbot"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)
			}

			err := bot.PostMessage(context.Background(), tt.channelID, tt.text)
			if tt.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
		})
	}
}

// --- DeleteThread ---

func (s *BotSuite) TestDeleteThreadSuccess() {
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.2222"}},
		{Msg: goslack.Msg{Timestamp: "1111.3333"}},
	}, false, "", nil)
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("C123", "1111.2222", nil)
	s.session.On("DeleteMessage", "C123", "1111.3333").Return("C123", "1111.3333", nil)

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "DeleteMessage", 2)
}

func (s *BotSuite) TestDeleteThreadPaginated() {
	// First page returns one message and a cursor
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.2222"}},
	}, false, "cursor1", nil)
	// Second page returns another message and empty cursor
	s.session.On("GetConversationReplies", &goslack.GetConversationRepliesParameters{
		ChannelID: "C123",
		Timestamp: "1111.2222",
		Cursor:    "cursor1",
	}).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.3333"}},
	}, false, "", nil)
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("C123", "1111.2222", nil)
	s.session.On("DeleteMessage", "C123", "1111.3333").Return("C123", "1111.3333", nil)

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err)
	s.session.AssertNumberOfCalls(s.T(), "DeleteMessage", 2)
}

func (s *BotSuite) TestDeleteThreadInvalidID() {
	err := s.bot.DeleteThread(context.Background(), "C123")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid thread ID")
}

func (s *BotSuite) TestDeleteThreadGetRepliesError() {
	s.session.On("GetConversationReplies", mock.Anything).Return(nil, false, "", errors.New("fetch error"))

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack get thread replies")
}

func (s *BotSuite) TestDeleteThreadDeleteMessageError() {
	s.session.On("GetConversationReplies", mock.Anything).Return([]goslack.Message{
		{Msg: goslack.Msg{Timestamp: "1111.2222"}},
	}, false, "", nil)
	s.session.On("DeleteMessage", "C123", "1111.2222").Return("", "", errors.New("delete failed"))

	err := s.bot.DeleteThread(context.Background(), "C123:1111.2222")
	require.NoError(s.T(), err) // individual delete errors are logged, not returned
}

// --- RenameThread ---

func (s *BotSuite) TestRenameThreadSuccess() {
	s.session.On("UpdateMessage", "C123", "1111.2222", mock.Anything).Return("C123", "1111.2222", "", nil)

	err := s.bot.RenameThread(context.Background(), "C123:1111.2222", "💨 task #1 (`5m`) prompt")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRenameThreadInvalidID() {
	err := s.bot.RenameThread(context.Background(), "C123", "new name")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid thread ID")
}

func (s *BotSuite) TestRenameThreadUpdateError() {
	s.session.On("UpdateMessage", "C123", "1111.2222", mock.Anything).Return("", "", "", errors.New("update_failed"))

	err := s.bot.RenameThread(context.Background(), "C123:1111.2222", "new name")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack rename thread")
}

// --- GetChannelName ---

func (s *BotSuite) TestGetChannelName() {
	tests := []struct {
		name      string
		channelID string
		infoErr   error
		wantName  string
		wantErr   bool
	}{
		{"success", "C123", nil, "general", false},
		{"composite_id", "C123:1111.2222", nil, "general", false},
		{"error", "C123", errors.New("channel_not_found"), "", true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.infoErr != nil {
				session.On("GetConversationInfo", mock.Anything).Return(nil, tt.infoErr)
			} else {
				session.On("GetConversationInfo", &goslack.GetConversationInfoInput{ChannelID: "C123"}).
					Return(&goslack.Channel{GroupConversation: goslack.GroupConversation{Name: "general"}}, nil)
			}

			name, err := bot.GetChannelName(context.Background(), tt.channelID)
			if tt.wantErr {
				require.Error(s.T(), err)
				require.Empty(s.T(), name)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantName, name)
			}
		})
	}
}

// --- GetChannelParentID ---

func (s *BotSuite) TestGetChannelParentID() {
	tests := []struct {
		name   string
		input  string
		wantID string
	}{
		{"thread", "C123:1111.2222", "C123"},
		{"not_thread", "C123", ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			parentID, err := s.bot.GetChannelParentID(context.Background(), tt.input)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantID, parentID)
		})
	}
}

// --- Composite ID ---

func (s *BotSuite) TestCompositeID() {
	require.Equal(s.T(), "C123:1234.5678", compositeID("C123", "1234.5678"))
}

func (s *BotSuite) TestParseCompositeIDThread() {
	ch, ts := parseCompositeID("C123:1234.5678")
	require.Equal(s.T(), "C123", ch)
	require.Equal(s.T(), "1234.5678", ts)
}

func (s *BotSuite) TestParseCompositeIDPlain() {
	ch, ts := parseCompositeID("C123")
	require.Equal(s.T(), "C123", ch)
	require.Empty(s.T(), ts)
}

// --- Slack TS to Time ---

func (s *BotSuite) TestSlackTSToTime() {
	tests := []struct {
		name   string
		input  string
		expect time.Time
	}{
		{"valid", "1234567890.123456", time.Unix(1234567890, 0)},
		{"invalid", "abc", time.Time{}},
		{"empty", "", time.Time{}},
		{"non_digit", "abc.123", time.Time{}},
		{"dot_prefix", ".123456", time.Time{}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := slackTSToTime(tt.input)
			if tt.expect.IsZero() {
				require.True(s.T(), result.IsZero())
			} else {
				require.Equal(s.T(), tt.expect, result)
			}
		})
	}
}
