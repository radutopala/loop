package db

import (
	"context"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPluginThreadsAreHidden checks against a real SQLite that a plugin's
// hidden thread is listed, searched and deleted like the learn and explain
// ones, and that a bare "plugin:" kind, with no plugin id, isn't hidden.
func (s *IntegrationSuite) TestPluginThreadsAreHidden() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.UpsertChannel(ctx, &Channel{ChannelID: "t1", Name: "t1", ParentID: "c1"}))

	require.ErrorContains(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p0", ParentID: "c1", Kind: ChannelKindPluginPrefix}), "not a hidden thread kind")
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p1", ParentID: "c1", Kind: PluginKind("cogtest")}))
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p2", ParentID: "t1", Kind: PluginKind("echo")}))
	// A bare prefix can only come from outside InsertHiddenThread; it must
	// still not count as hidden.
	_, err = store.db.ExecContext(ctx, `INSERT INTO channels (channel_id, name, parent_id, kind) VALUES ('b1', 'b1', 'c1', ?)`, ChannelKindPluginPrefix)
	require.NoError(s.T(), err)

	hidden, err := store.ListHiddenThreads(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), hidden, 1)
	require.Equal(s.T(), "p1", hidden[0].ChannelID)
	p, err := store.GetHiddenThread(ctx, "c1", PluginKind("cogtest"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "p1", p.ChannelID)
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p3", ParentID: "t1", Kind: PluginKind("cogtest")}))
	threads, err := store.ListPluginThreads(ctx, "cogtest")
	require.NoError(s.T(), err)
	require.Len(s.T(), threads, 2)
	require.Equal(s.T(), "p1", threads[0].ChannelID)
	require.Equal(s.T(), "p3", threads[1].ChannelID)

	for i, id := range []string{"p1", "b1", "c1"} {
		require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: id, MsgID: "m" + id, Content: "quiz", Kind: MessageKindMessage, CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}))
	}
	found, err := store.SearchMessages(ctx, "quiz", 10)
	require.NoError(s.T(), err)
	ids := make([]string, 0, len(found))
	for _, m := range found {
		ids = append(ids, m.ChannelID)
	}
	require.Equal(s.T(), []string{"c1", "b1"}, ids, "plugin threads are left out of search")

	require.NoError(s.T(), store.DeleteChannelsByParentID(ctx, "c1"))
	ch, err := store.GetChannel(ctx, "p2")
	require.NoError(s.T(), err)
	require.Nil(s.T(), ch, "a plugin thread under a deleted thread goes with it")
}
