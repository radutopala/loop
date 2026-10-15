package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestSideChatLifecycle creates side chats against a real SQLite, checks
// they stay out of search, and that deleting their parent takes them along.
func (s *IntegrationSuite) TestSideChatLifecycle() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.UpsertChannel(ctx, &Channel{ChannelID: "t1", Name: "t1", ParentID: "c1"}))

	require.NoError(s.T(), store.InsertSideChat(ctx, &Channel{ChannelID: "s1", Name: "c1 (fork)", DirPath: "/tmp/c1", ParentID: "c1", Platform: "local", SessionID: "sess-1", ForkPending: true}))
	require.NoError(s.T(), store.InsertSideChat(ctx, &Channel{ChannelID: "s2", Name: "c1 (fresh)", DirPath: "/tmp/c1", ParentID: "c1", Platform: "local"}))
	require.NoError(s.T(), store.InsertSideChat(ctx, &Channel{ChannelID: "s3", Name: "t1 (fresh)", ParentID: "t1", Platform: "local"}))
	require.ErrorIs(s.T(), store.InsertSideChat(ctx, &Channel{ChannelID: "s4", ParentID: "gone"}), ErrParentGone)

	sides, err := store.ListSideChats(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), sides, 2)
	require.Equal(s.T(), "s1", sides[0].ChannelID)
	require.Equal(s.T(), ChannelKindSide, sides[0].Kind)
	require.Equal(s.T(), "sess-1", sides[0].SessionID)
	require.True(s.T(), sides[0].ForkPending)
	require.True(s.T(), sides[0].Active)
	require.Empty(s.T(), sides[1].SessionID)
	require.False(s.T(), sides[1].ForkPending)

	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "s1", MsgID: "m1", Content: "side", Kind: MessageKindMessage, CreatedAt: time.Now()}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "c1", MsgID: "m2", Content: "side in chat", Kind: MessageKindMessage, CreatedAt: time.Now()}))
	found, err := store.SearchMessages(ctx, "side", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), found, 1, "side chats are left out of search")
	require.Equal(s.T(), "c1", found[0].ChannelID)

	require.NoError(s.T(), store.DeleteChannelsByParentID(ctx, "c1"))
	require.NoError(s.T(), store.DeleteChannel(ctx, "c1"))
	for _, id := range []string{"t1", "s1", "s2", "s3"} {
		ch, err := store.GetChannel(ctx, id)
		require.NoError(s.T(), err)
		require.Nil(s.T(), ch, id)
	}
	msgs, err := store.GetMessagesCursor(ctx, "s1", 0, 10)
	require.NoError(s.T(), err)
	require.Empty(s.T(), msgs)
}

func (s *StoreSuite) TestSideChatErrors() {
	ctx := context.Background()
	boom := errors.New("boom")

	s.Run("insert", func() {
		s.mock.ExpectExec(`INSERT INTO channels`).WillReturnError(boom)
		require.ErrorIs(s.T(), s.store.InsertSideChat(ctx, &Channel{}), boom)
	})
	s.Run("insert rows affected", func() {
		s.mock.ExpectExec(`INSERT INTO channels`).WillReturnResult(sqlmock.NewErrorResult(boom))
		require.ErrorIs(s.T(), s.store.InsertSideChat(ctx, &Channel{}), boom)
	})
	s.Run("list", func() {
		s.mock.ExpectQuery(`FROM channels WHERE parent_id = \? AND kind = \?`).WithArgs("c1", ChannelKindSide).WillReturnError(boom)
		_, err := s.store.ListSideChats(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
}
