package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LocalBotSuite struct {
	suite.Suite
	bot *LocalBot
}

func TestLocalBotSuite(t *testing.T) {
	suite.Run(t, new(LocalBotSuite))
}

func (s *LocalBotSuite) SetupTest() {
	s.bot = NewLocalBot()
}

// --- Lifecycle ---

func (s *LocalBotSuite) TestStartStop() {
	ctx := context.Background()
	require.NoError(s.T(), s.bot.Start(ctx))
	require.NoError(s.T(), s.bot.Stop())
}

func (s *LocalBotSuite) TestRegisterAndRemoveCommands() {
	ctx := context.Background()
	require.NoError(s.T(), s.bot.RegisterCommands(ctx))
	require.NoError(s.T(), s.bot.RemoveCommands(ctx))
}

func (s *LocalBotSuite) TestBotUserID() {
	require.Equal(s.T(), "loop-bot", s.bot.BotUserID())
}

// --- Messaging ---

func (s *LocalBotSuite) TestSendMessage() {
	require.NoError(s.T(), s.bot.SendMessage(context.Background(), &OutgoingMessage{}))
}

func (s *LocalBotSuite) TestSendTyping() {
	require.NoError(s.T(), s.bot.SendTyping(context.Background(), "ch"))
}

func (s *LocalBotSuite) TestPostMessage() {
	require.NoError(s.T(), s.bot.PostMessage(context.Background(), "ch", "text"))
}

// --- Stop button ---

func (s *LocalBotSuite) TestSendStopButton() {
	id, err := s.bot.SendStopButton(context.Background(), "ch", "msg")
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *LocalBotSuite) TestRemoveStopButton() {
	require.NoError(s.T(), s.bot.RemoveStopButton(context.Background(), "ch", "msg"))
}

// --- Channel / thread management ---

func (s *LocalBotSuite) TestCreateChannel() {
	id, err := s.bot.CreateChannel(context.Background(), "name", "topic")
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *LocalBotSuite) TestCreateThread() {
	id, err := s.bot.CreateThread(context.Background(), "ch", "msg", "name", "body")
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *LocalBotSuite) TestCreateSimpleThread() {
	id, err := s.bot.CreateSimpleThread(context.Background(), "ch", "name", "body")
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *LocalBotSuite) TestInviteUserToChannel() {
	require.NoError(s.T(), s.bot.InviteUserToChannel(context.Background(), "ch", "user"))
}

func (s *LocalBotSuite) TestSetChannelTopic() {
	require.NoError(s.T(), s.bot.SetChannelTopic(context.Background(), "ch", "topic"))
}

func (s *LocalBotSuite) TestDeleteThread() {
	require.NoError(s.T(), s.bot.DeleteThread(context.Background(), "thread"))
}

func (s *LocalBotSuite) TestRenameThread() {
	require.NoError(s.T(), s.bot.RenameThread(context.Background(), "thread", "new-name"))
}

// --- Getters ---

func (s *LocalBotSuite) TestGetOwnerUserID() {
	id, err := s.bot.GetOwnerUserID(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *LocalBotSuite) TestGetChannelParentID() {
	id, err := s.bot.GetChannelParentID(context.Background(), "ch")
	require.NoError(s.T(), err)
	require.Empty(s.T(), id)
}

func (s *LocalBotSuite) TestGetChannelName() {
	name, err := s.bot.GetChannelName(context.Background(), "ch")
	require.NoError(s.T(), err)
	require.Empty(s.T(), name)
}

func (s *LocalBotSuite) TestGetMemberRoles() {
	roles, err := s.bot.GetMemberRoles(context.Background(), "guild", "user")
	require.NoError(s.T(), err)
	require.Nil(s.T(), roles)
}

// --- Event handler registration ---

func (s *LocalBotSuite) TestOnMessage() {
	var called bool
	s.bot.OnMessage(func(_ context.Context, _ *IncomingMessage) { called = true })

	require.NotNil(s.T(), s.bot.messageHandler)
	s.bot.messageHandler(context.Background(), &IncomingMessage{})
	require.True(s.T(), called)
}

func (s *LocalBotSuite) TestOnInteraction() {
	var called bool
	s.bot.OnInteraction(func(_ context.Context, _ *Interaction) { called = true })

	require.NotNil(s.T(), s.bot.interactionHandler)
	s.bot.interactionHandler(context.Background(), &Interaction{})
	require.True(s.T(), called)
}

func (s *LocalBotSuite) TestOnChannelDelete() {
	var called bool
	s.bot.OnChannelDelete(func(_ context.Context, _ string, _ bool) { called = true })

	require.NotNil(s.T(), s.bot.channelDeleteHandler)
	s.bot.channelDeleteHandler(context.Background(), "ch", false)
	require.True(s.T(), called)
}

func (s *LocalBotSuite) TestOnChannelJoin() {
	var called bool
	s.bot.OnChannelJoin(func(_ context.Context, _ string) { called = true })

	require.NotNil(s.T(), s.bot.channelJoinHandler)
	s.bot.channelJoinHandler(context.Background(), "ch")
	require.True(s.T(), called)
}
