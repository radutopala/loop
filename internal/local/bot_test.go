package local

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type MockLocalStore struct {
	mock.Mock
}

func (m *MockLocalStore) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockLocalStore) UpsertChannel(ctx context.Context, ch *db.Channel) error {
	return m.Called(ctx, ch).Error(0)
}

func (m *MockLocalStore) DeleteChannel(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *MockLocalStore) InsertMessage(ctx context.Context, msg *db.Message) error {
	return m.Called(ctx, msg).Error(0)
}

type BotSuite struct {
	suite.Suite
	store  *MockLocalStore
	bot    *Bot
	logger *slog.Logger
}

func TestBotSuite(t *testing.T) {
	suite.Run(t, new(BotSuite))
}

func (s *BotSuite) SetupTest() {
	s.store = new(MockLocalStore)
	s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	s.bot = NewBot(s.store, s.logger)
	// Use deterministic ID generation.
	generateThreadID = func() string { return "test-thread-id" }
}

func (s *BotSuite) TearDownTest() {
	// Restore default.
	generateThreadID = defaultGenerateThreadID
}

// --- Lifecycle ---

func (s *BotSuite) TestStartStop() {
	ctx := context.Background()
	require.NoError(s.T(), s.bot.Start(ctx))
	require.NoError(s.T(), s.bot.Stop())
}

func (s *BotSuite) TestRegisterAndRemoveCommands() {
	ctx := context.Background()
	require.NoError(s.T(), s.bot.RegisterCommands(ctx))
	require.NoError(s.T(), s.bot.RemoveCommands(ctx))
}

func (s *BotSuite) TestBotUserID() {
	require.Equal(s.T(), "loop-bot", s.bot.BotUserID())
}

func (s *BotSuite) TestIsBotUser() {
	require.True(s.T(), s.bot.IsBotUser("loop-bot"))
	require.False(s.T(), s.bot.IsBotUser("other-user"))
}

// --- Messaging no-ops ---

func (s *BotSuite) TestSendMessage() {
	require.NoError(s.T(), s.bot.SendMessage(context.Background(), &bot.OutgoingMessage{}))
}

func (s *BotSuite) TestSendTyping() {
	require.NoError(s.T(), s.bot.SendTyping(context.Background(), "ch"))
}

func (s *BotSuite) TestPostMessage() {
	require.NoError(s.T(), s.bot.PostMessage(context.Background(), "ch", "text"))
}

// --- Stop button no-ops ---

func (s *BotSuite) TestSendStopButton() {
	id, err := s.bot.SendStopButton(context.Background(), "ch", "msg")
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *BotSuite) TestRemoveStopButton() {
	require.NoError(s.T(), s.bot.RemoveStopButton(context.Background(), "ch", "msg"))
}

// --- DB-backed: GetChannelParentID ---

func (s *BotSuite) TestGetChannelParentID() {
	ch := &db.Channel{ChannelID: "thread-1", ParentID: "parent-1"}
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(ch, nil)

	parentID, err := s.bot.GetChannelParentID(context.Background(), "thread-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "parent-1", parentID)
	s.store.AssertExpectations(s.T())
}

func (s *BotSuite) TestGetChannelParentIDNotFound() {
	s.store.On("GetChannel", mock.Anything, "unknown").Return(nil, nil)

	parentID, err := s.bot.GetChannelParentID(context.Background(), "unknown")
	require.NoError(s.T(), err)
	require.Empty(s.T(), parentID)
}

func (s *BotSuite) TestGetChannelParentIDError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db error"))

	_, err := s.bot.GetChannelParentID(context.Background(), "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting channel")
}

// --- DB-backed: GetChannelName ---

func (s *BotSuite) TestGetChannelName() {
	ch := &db.Channel{ChannelID: "ch-1", Name: "my-channel"}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)

	name, err := s.bot.GetChannelName(context.Background(), "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "my-channel", name)
}

func (s *BotSuite) TestGetChannelNameNotFound() {
	s.store.On("GetChannel", mock.Anything, "unknown").Return(nil, nil)

	name, err := s.bot.GetChannelName(context.Background(), "unknown")
	require.NoError(s.T(), err)
	require.Empty(s.T(), name)
}

func (s *BotSuite) TestGetChannelNameError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db error"))

	_, err := s.bot.GetChannelName(context.Background(), "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting channel")
}

// --- DB-backed: CreateSimpleThread ---

func (s *BotSuite) TestCreateSimpleThread() {
	parent := &db.Channel{
		ID:          1,
		ChannelID:   "ch-1",
		GuildID:     "guild-1",
		DirPath:     "/work",
		Platform:    types.PlatformLocal,
		SessionID:   "sess-1",
		Permissions: types.Permissions{Owners: types.RoleGrant{Roles: []string{"r1"}}},
	}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil).Once()

	s.store.On("UpsertChannel", mock.Anything, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "test-thread-id" &&
			ch.GuildID == "guild-1" &&
			ch.ParentID == "ch-1" &&
			ch.DirPath == "/work" &&
			ch.Platform == types.PlatformLocal &&
			ch.SessionID == "sess-1" &&
			ch.Active
	})).Return(nil)

	// After upsert, GetChannel for the new thread to store the initial message.
	threadCh := &db.Channel{ID: 2, ChannelID: "test-thread-id"}
	s.store.On("GetChannel", mock.Anything, "test-thread-id").Return(threadCh, nil)

	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 2 &&
			m.ChannelID == "test-thread-id" &&
			m.Content == "hello" &&
			m.IsBot &&
			m.AuthorName == "assistant"
	})).Return(nil)

	threadID, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "thread-name", "hello")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test-thread-id", threadID)
	s.store.AssertExpectations(s.T())
}

func (s *BotSuite) TestCreateSimpleThreadNoInitialMessage() {
	parent := &db.Channel{ID: 1, ChannelID: "ch-1", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	threadID, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "name", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test-thread-id", threadID)
	// InsertMessage should NOT be called when initialMessage is empty.
	s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
}

func (s *BotSuite) TestCreateSimpleThreadParentNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)

	_, err := s.bot.CreateSimpleThread(context.Background(), "missing", "name", "msg")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *BotSuite) TestCreateSimpleThreadParentLookupError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db error"))

	_, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "name", "msg")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up parent channel")
}

func (s *BotSuite) TestCreateSimpleThreadUpsertError() {
	parent := &db.Channel{ID: 1, ChannelID: "ch-1", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(errors.New("upsert error"))

	_, err := s.bot.CreateSimpleThread(context.Background(), "ch-1", "name", "msg")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing thread")
}

// --- DB-backed: CreateThread ---

func (s *BotSuite) TestCreateThread() {
	parent := &db.Channel{ID: 1, ChannelID: "ch-1", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	// mentionUserID is ignored for local platform.
	threadID, err := s.bot.CreateThread(context.Background(), "ch-1", "name", "user-42", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test-thread-id", threadID)
}

// --- DB-backed: DeleteThread ---

func (s *BotSuite) TestDeleteThread() {
	s.store.On("DeleteChannel", mock.Anything, "thread-1").Return(nil)

	require.NoError(s.T(), s.bot.DeleteThread(context.Background(), "thread-1"))
	s.store.AssertExpectations(s.T())
}

func (s *BotSuite) TestDeleteThreadError() {
	s.store.On("DeleteChannel", mock.Anything, "thread-1").Return(errors.New("db error"))

	require.Error(s.T(), s.bot.DeleteThread(context.Background(), "thread-1"))
}

// --- DB-backed: RenameThread ---

func (s *BotSuite) TestRenameThread() {
	ch := &db.Channel{ID: 1, ChannelID: "thread-1", Name: "old", ParentID: "ch-1", DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(ch, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.MatchedBy(func(c *db.Channel) bool {
		return c.Name == "new-name" && c.ChannelID == "thread-1" && c.DirPath == "/work"
	})).Return(nil)

	require.NoError(s.T(), s.bot.RenameThread(context.Background(), "thread-1", "new-name"))
	s.store.AssertExpectations(s.T())
}

func (s *BotSuite) TestRenameThreadNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil)

	require.NoError(s.T(), s.bot.RenameThread(context.Background(), "missing", "name"))
}

func (s *BotSuite) TestRenameThreadGetError() {
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(nil, errors.New("db error"))

	err := s.bot.RenameThread(context.Background(), "thread-1", "name")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting thread")
}

func (s *BotSuite) TestRenameThreadUpsertError() {
	ch := &db.Channel{ID: 1, ChannelID: "thread-1", Name: "old"}
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(ch, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(errors.New("upsert error"))

	require.Error(s.T(), s.bot.RenameThread(context.Background(), "thread-1", "new"))
}

// --- No-op channel methods ---

func (s *BotSuite) TestInviteUserToChannel() {
	require.NoError(s.T(), s.bot.InviteUserToChannel(context.Background(), "ch", "user"))
}

func (s *BotSuite) TestSetChannelTopic() {
	require.NoError(s.T(), s.bot.SetChannelTopic(context.Background(), "ch", "topic"))
}

// --- ChannelCreator methods ---

func (s *BotSuite) TestCreateChannel() {
	id, err := s.bot.CreateChannel(context.Background(), "my-channel")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), id)
}

func (s *BotSuite) TestGetOwnerUserID() {
	id, err := s.bot.GetOwnerUserID(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

// --- Event handler registration ---

func (s *BotSuite) TestOnMessage() {
	var called bool
	s.bot.OnMessage(func(_ context.Context, _ *bot.IncomingMessage) { called = true })

	require.NotNil(s.T(), s.bot.messageHandler)
	s.bot.messageHandler(context.Background(), &bot.IncomingMessage{})
	require.True(s.T(), called)
}

func (s *BotSuite) TestOnInteraction() {
	var called bool
	s.bot.OnInteraction(func(_ context.Context, _ *bot.Interaction) { called = true })

	require.NotNil(s.T(), s.bot.interactionHandler)
	s.bot.interactionHandler(context.Background(), &bot.Interaction{})
	require.True(s.T(), called)
}

func (s *BotSuite) TestOnChannelDelete() {
	var called bool
	s.bot.OnChannelDelete(func(_ context.Context, _ string, _ bool) { called = true })

	require.NotNil(s.T(), s.bot.channelDeleteHandler)
	s.bot.channelDeleteHandler(context.Background(), "ch", false)
	require.True(s.T(), called)
}

func (s *BotSuite) TestOnChannelJoin() {
	var called bool
	s.bot.OnChannelJoin(func(_ context.Context, _ string, _ types.Platform) { called = true })

	require.NotNil(s.T(), s.bot.channelJoinHandler)
	s.bot.channelJoinHandler(context.Background(), "ch", types.PlatformLocal)
	require.True(s.T(), called)
}

// --- HandleIncomingMessage ---

func (s *BotSuite) TestHandleIncomingMessagePlain() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "hello")

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "ch-1", received.ChannelID)
	require.Equal(s.T(), "user-1", received.AuthorID)
	require.Equal(s.T(), "user-1", received.AuthorName)
	require.Equal(s.T(), "hello", received.Content)
	require.False(s.T(), received.IsBotMention)
	require.False(s.T(), received.HasPrefix)
	require.True(s.T(), received.IsDM)
}

func (s *BotSuite) TestHandleIncomingMessageMention() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "@LoopBot do this")

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "do this", received.Content)
	require.True(s.T(), received.IsBotMention)
}

func (s *BotSuite) TestHandleIncomingMessagePrefix() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "!loop check status")

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "check status", received.Content)
	require.True(s.T(), received.HasPrefix)
}

func (s *BotSuite) TestHandleIncomingMessageDefaultAuthor() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "", "hello")

	require.NotNil(s.T(), received)
	require.Equal(s.T(), DefaultAuthorID, received.AuthorID)
	require.Equal(s.T(), DefaultAuthorID, received.AuthorName)
}

func (s *BotSuite) TestHandleIncomingMessageNoHandler() {
	// Should not panic when no handler is set.
	require.NotPanics(s.T(), func() {
		s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "hello")
	})
}

func (s *BotSuite) TestHandleIncomingMessageTimestamp() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	before := time.Now().UTC()
	s.bot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "hello")
	after := time.Now().UTC()

	require.NotNil(s.T(), received)
	require.False(s.T(), received.Timestamp.Before(before))
	require.False(s.T(), received.Timestamp.After(after))
}

// --- HandleThreadCreated ---

func (s *BotSuite) TestHandleThreadCreated() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "Do the task")

	require.NotNil(s.T(), received)
	require.Equal(s.T(), "thread-1", received.ChannelID)
	require.Equal(s.T(), "user-1", received.AuthorID)
	require.Equal(s.T(), "Do the task", received.Content)
	require.True(s.T(), received.IsBotMention)
}

func (s *BotSuite) TestHandleThreadCreatedEmptyMessage() {
	var called bool
	s.bot.OnMessage(func(_ context.Context, _ *bot.IncomingMessage) {
		called = true
	})

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "user-1", "")

	require.False(s.T(), called)
}

func (s *BotSuite) TestHandleThreadCreatedDefaultAuthor() {
	var received *bot.IncomingMessage
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received = msg
	})

	s.bot.HandleThreadCreated(context.Background(), "thread-1", "", "hello")

	require.NotNil(s.T(), received)
	require.Equal(s.T(), DefaultAuthorID, received.AuthorID)
}

// keep a reference to the real generateThreadID for TearDownTest
var defaultGenerateThreadID = generateThreadID

func TestGenerateThreadIDDefault(t *testing.T) {
	got := defaultGenerateThreadID()
	require.Len(t, got, 12) // 6 bytes = 12 hex chars
}
