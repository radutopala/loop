package slack

import (
	"context"
	"errors"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/types"
)

// --- NewSocketModeAdapter ---

func (s *BotSuite) TestNewSocketModeAdapter() {
	api := goslack.New("xoxb-fake")
	smClient := socketmode.New(api)
	adapter := NewSocketModeAdapter(smClient)
	require.NotNil(s.T(), adapter)

	// Verify Events() returns a non-nil channel.
	require.NotNil(s.T(), adapter.Events())

	// RunContext with an already-cancelled context returns immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = adapter.RunContext(ctx)

	// Ack with an empty request (exercises the passthrough).
	adapter.Ack(socketmode.Request{})
}

// --- Start / Stop ---

func (s *BotSuite) TestStartSuccess() {
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())

	session.On("AuthTest").Return(&goslack.AuthTestResponse{
		UserID: "U123BOT",
		User:   "loopbot",
	}, nil)
	session.On("SetUserPresence", "auto").Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Start(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "U123BOT", bot.BotUserID())

	cancel()
	time.Sleep(10 * time.Millisecond) // allow goroutines to stop
	session.AssertExpectations(s.T())
}

func (s *BotSuite) TestStartPresenceError() {
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())

	session.On("AuthTest").Return(&goslack.AuthTestResponse{
		UserID: "U123BOT",
		User:   "loopbot",
	}, nil)
	session.On("SetUserPresence", "auto").Return(errors.New("presence_error"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Start(ctx)
	require.NoError(s.T(), err) // presence error is non-fatal
	require.Equal(s.T(), "U123BOT", bot.BotUserID())

	cancel()
	time.Sleep(10 * time.Millisecond)
	session.AssertExpectations(s.T())
}

func (s *BotSuite) TestStartAuthError() {
	session := new(MockSession)
	sc := newMockSocketClient()
	bot := NewBot(session, sc, testLogger())

	session.On("AuthTest").Return(nil, errors.New("invalid_auth"))

	err := bot.Start(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "slack auth test")
}

func (s *BotSuite) TestStop() {
	ctx, cancel := context.WithCancel(context.Background())
	s.bot.cancel = cancel
	err := s.bot.Stop()
	require.NoError(s.T(), err)
	// Context should be cancelled
	select {
	case <-ctx.Done():
	default:
		s.Fail("context should be cancelled")
	}
}

func (s *BotSuite) TestStopNilCancel() {
	err := s.bot.Stop()
	require.NoError(s.T(), err)
}

// --- SendMessage ---

func (s *BotSuite) TestSendMessage() {
	longContent := strings.Repeat("a", maxMessageLen+100)

	tests := []struct {
		name      string
		msg       *bot.OutgoingMessage
		postErr   error
		wantErr   string
		wantCalls int
	}{
		{"plain", &bot.OutgoingMessage{ChannelID: "C123", Content: "hello world"}, nil, "", 1},
		{"thread", &bot.OutgoingMessage{ChannelID: "C123:1111.2222", Content: "reply in thread"}, nil, "", 1},
		{"reply_to", &bot.OutgoingMessage{ChannelID: "C123", Content: "replying", ReplyToMessageID: "9999.0000"}, nil, "", 1},
		{"split", &bot.OutgoingMessage{ChannelID: "C123", Content: longContent}, nil, "", 2},
		{"error", &bot.OutgoingMessage{ChannelID: "C123", Content: "hello"}, errors.New("channel_not_found"), "slack send message", 1},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)
			}

			err := bot.SendMessage(context.Background(), tt.msg)
			if tt.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertNumberOfCalls(s.T(), "PostMessage", tt.wantCalls)
		})
	}
}

// --- SendTyping ---

func (s *BotSuite) TestSendTypingSuccess() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(nil)
	s.session.On("RemoveReaction", reactionEmoji, ref).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.bot.SendTyping(ctx, "C123")
	require.NoError(s.T(), err)
	s.session.AssertCalled(s.T(), "AddReaction", reactionEmoji, ref)

	cancel()
	time.Sleep(20 * time.Millisecond)
	s.session.AssertCalled(s.T(), "RemoveReaction", reactionEmoji, ref)
}

func (s *BotSuite) TestSendTypingNoTrackedMessage() {
	err := s.bot.SendTyping(context.Background(), "C999")
	require.NoError(s.T(), err)
	// Should be a no-op, no calls made.
	s.session.AssertNotCalled(s.T(), "AddReaction")
}

func (s *BotSuite) TestSendTypingCompositeID() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(nil)
	s.session.On("RemoveReaction", reactionEmoji, ref).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.bot.SendTyping(ctx, "C123:9999.0000")
	require.NoError(s.T(), err)
	s.session.AssertCalled(s.T(), "AddReaction", reactionEmoji, ref)
}

func (s *BotSuite) TestSendTypingAddReactionError() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(errors.New("not_in_channel"))

	err := s.bot.SendTyping(context.Background(), "C123")
	require.NoError(s.T(), err) // non-fatal
}

func (s *BotSuite) TestSendTypingAlreadyReacted() {
	ref := goslack.NewRefToMessage("C123", "1234.5678")
	s.bot.lastMessageRef.Store("C123", ref)

	s.session.On("AddReaction", reactionEmoji, ref).Return(errors.New("already_reacted"))
	s.session.On("RemoveReaction", reactionEmoji, ref).Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.bot.SendTyping(ctx, "C123")
	require.NoError(s.T(), err)

	// Cleanup goroutine should still be set up.
	cancel()
	time.Sleep(20 * time.Millisecond)
	s.session.AssertCalled(s.T(), "RemoveReaction", reactionEmoji, ref)
}

// --- RegisterCommands / RemoveCommands ---

func (s *BotSuite) TestRegisterCommandsNoOp() {
	err := s.bot.RegisterCommands(context.Background())
	require.NoError(s.T(), err)
}

func (s *BotSuite) TestRemoveCommandsNoOp() {
	err := s.bot.RemoveCommands(context.Background())
	require.NoError(s.T(), err)
}

// --- Handler Registration ---

func (s *BotSuite) TestOnMessage() {
	s.bot.OnMessage(func(_ context.Context, _ *bot.IncomingMessage) {})
	require.Len(s.T(), s.bot.messageHandlers, 1)
}

func (s *BotSuite) TestOnInteraction() {
	s.bot.OnInteraction(func(_ context.Context, _ *bot.Interaction) {})
	require.Len(s.T(), s.bot.interactionHandlers, 1)
}

func (s *BotSuite) TestOnChannelDelete() {
	s.bot.OnChannelDelete(func(_ context.Context, _ string, _ bool) {})
	require.Len(s.T(), s.bot.channelDeleteHandlers, 1)
}

func (s *BotSuite) TestOnChannelJoin() {
	s.bot.OnChannelJoin(func(_ context.Context, _ string, _ types.Platform) {})
	require.Len(s.T(), s.bot.channelJoinHandlers, 1)
}

// --- BotUserID ---

func (s *BotSuite) TestBotUserID() {
	require.Equal(s.T(), "U123BOT", s.bot.BotUserID())
}

func (s *BotSuite) TestIsBotUser() {
	require.True(s.T(), s.bot.IsBotUser("U123BOT"))
	require.False(s.T(), s.bot.IsBotUser("other"))
}
