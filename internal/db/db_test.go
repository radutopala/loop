package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type StoreSuite struct {
	suite.Suite
	db    *sql.DB
	mock  sqlmock.Sqlmock
	store *SQLiteStore
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (s *StoreSuite) SetupTest() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	s.db = db
	s.mock = mock
	s.store = NewSQLiteStoreFromDB(db)
}

func (s *StoreSuite) TearDownTest() {
	s.db.Close()
}

func newMockChannelRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "dir_path", "parent_id", "platform", "active", "session_id", "permissions", "worktree", "created_at", "updated_at"})
}

func newMockTaskRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name", "auto_delete_sec", "thread_id", "worktree", "origin_branch", "update_before_run", "running", "workflow_name", "workflow_inputs"})
}

func newMockMessageRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "chat_id", "channel_id", "msg_id", "author_id", "author_name", "content", "is_bot", "is_processed", "created_at"})
}

func newMockMemoryRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "file_path", "chunk_index", "content", "content_hash", "embedding", "dimensions", "dir_path", "updated_at"})
}

func (s *StoreSuite) TestClose() {
	s.mock.ExpectClose()
	err := s.store.Close()
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

// --- Channel tests ---

func (s *StoreSuite) TestUpsertChannel() {
	cases := []struct {
		name string
		ch   *Channel
		args []driver.Value
	}{
		{
			name: "basic",
			ch:   &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", Active: true},
			args: []driver.Value{"ch1", "g1", "test-channel", "", "", "", "", "", 1, 0, sqlmock.AnyArg()},
		},
		{
			name: "with dir path",
			ch:   &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", DirPath: "/home/user/project", Active: true},
			args: []driver.Value{"ch1", "g1", "test-channel", "/home/user/project", "", "", "", "", 1, 0, sqlmock.AnyArg()},
		},
		{
			name: "with parent ID",
			ch:   &Channel{ChannelID: "thread1", GuildID: "g1", Name: "", ParentID: "ch1", SessionID: "sess-parent", Active: true},
			args: []driver.Value{"thread1", "g1", "", "", "ch1", "", "sess-parent", "", 1, 0, sqlmock.AnyArg()},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			dbConn, sqlMock, err := sqlmock.New()
			require.NoError(s.T(), err)
			defer dbConn.Close()
			store := NewSQLiteStoreFromDB(dbConn)

			sqlMock.ExpectExec(`INSERT INTO channels`).
				WithArgs(tc.args...).
				WillReturnResult(sqlmock.NewResult(1, 1))

			err = store.UpsertChannel(context.Background(), tc.ch)
			require.NoError(s.T(), err)
			require.NoError(s.T(), sqlMock.ExpectationsWereMet())
		})
	}
}

func (s *StoreSuite) TestGetChannelWithParentID() {
	now := time.Now().UTC()
	rows := newMockChannelRows().
		AddRow(1, "thread1", "g1", "", "/project", "ch1", "", 1, "", "", 0, now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).
		WithArgs("thread1").
		WillReturnRows(rows)

	ch, err := s.store.GetChannel(context.Background(), "thread1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), "ch1", ch.ParentID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertChannelError() {
	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", "", "", "", "", 1, 0, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpsertChannel(context.Background(), ch)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestUpsertChannelWithPermissions() {
	perms := types.Permissions{
		Owners:  types.RoleGrant{Users: []string{"U1"}, Roles: []string{"admin"}},
		Members: types.RoleGrant{Users: []string{"U2"}, Roles: []string{}},
	}
	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", Permissions: perms, Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", "", "", "", `{"owners":{"users":["U1"],"roles":["admin"]},"members":{"users":["U2"],"roles":[]}}`, 1, 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannel() {
	now := time.Now().UTC()
	permJSON := `{"owners":{"users":["U1"],"roles":["admin"]},"members":{"users":[],"roles":[]}}`
	rows := newMockChannelRows().
		AddRow(1, "ch1", "g1", "test", "/home/user/project", "", "discord", 1, "sess-123", permJSON, 0, now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).
		WithArgs("ch1").
		WillReturnRows(rows)

	ch, err := s.store.GetChannel(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), "ch1", ch.ChannelID)
	require.Equal(s.T(), "g1", ch.GuildID)
	require.Equal(s.T(), "/home/user/project", ch.DirPath)
	require.Empty(s.T(), ch.ParentID)
	require.True(s.T(), ch.Active)
	require.Equal(s.T(), "sess-123", ch.SessionID)
	require.Equal(s.T(), []string{"U1"}, ch.Permissions.Owners.Users)
	require.Equal(s.T(), []string{"admin"}, ch.Permissions.Owners.Roles)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannelNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrNoRows)
	ch, err := s.store.GetChannel(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), ch)

	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	ch, err = s.store.GetChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), ch)
}

func (s *StoreSuite) TestGetChannelByDirPath() {
	now := time.Now().UTC()
	permJSON := `{"owners":{"users":["U1"],"roles":[]},"members":{"users":["U2"],"roles":[]}}`
	rows := newMockChannelRows().
		AddRow(1, "ch1", "g1", "loop", "/home/user/dev/loop", "", "discord", 1, "", permJSON, 0, now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/home/user/dev/loop", types.PlatformDiscord).
		WillReturnRows(rows)

	ch, err := s.store.GetChannelByDirPath(context.Background(), "/home/user/dev/loop", types.PlatformDiscord)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), "ch1", ch.ChannelID)
	require.Equal(s.T(), "/home/user/dev/loop", ch.DirPath)
	require.Equal(s.T(), []string{"U1"}, ch.Permissions.Owners.Users)
	require.Equal(s.T(), []string{"U2"}, ch.Permissions.Members.Users)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannelByDirPathNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).WithArgs("/path", types.PlatformDiscord).WillReturnError(sql.ErrNoRows)
	ch, err := s.store.GetChannelByDirPath(context.Background(), "/path", types.PlatformDiscord)
	require.NoError(s.T(), err)
	require.Nil(s.T(), ch)

	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).WithArgs("/path", types.PlatformDiscord).WillReturnError(sql.ErrConnDone)
	ch, err = s.store.GetChannelByDirPath(context.Background(), "/path", types.PlatformDiscord)
	require.Error(s.T(), err)
	require.Nil(s.T(), ch)
}

func (s *StoreSuite) TestGetChannelsByDirPath() {
	now := time.Now().UTC()
	rows := newMockChannelRows().
		AddRow(1, "ch1", "", "loop-local", "/home/user/dev/loop", "", "local", 1, "", "", 0, now, now).
		AddRow(2, "ch2", "g1", "loop-discord", "/home/user/dev/loop", "", "discord", 1, "", "", 0, now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/home/user/dev/loop").
		WillReturnRows(rows)

	channels, err := s.store.GetChannelsByDirPath(context.Background(), "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Len(s.T(), channels, 2)
	require.Equal(s.T(), "ch1", channels[0].ChannelID)
	require.Equal(s.T(), "ch2", channels[1].ChannelID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannelsByDirPathEmpty() {
	rows := newMockChannelRows()
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/nonexistent").
		WillReturnRows(rows)

	channels, err := s.store.GetChannelsByDirPath(context.Background(), "/nonexistent")
	require.NoError(s.T(), err)
	require.Empty(s.T(), channels)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannelsByDirPathError() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/path").
		WillReturnError(sql.ErrConnDone)

	channels, err := s.store.GetChannelsByDirPath(context.Background(), "/path")
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)
}

func (s *StoreSuite) TestIsChannelActive() {
	s.mock.ExpectQuery(`SELECT COUNT`).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	active, err := s.store.IsChannelActive(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.True(s.T(), active)

	s.mock.ExpectQuery(`SELECT COUNT`).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	active, err = s.store.IsChannelActive(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.False(s.T(), active)

	s.mock.ExpectQuery(`SELECT COUNT`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	active, err = s.store.IsChannelActive(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.False(s.T(), active)
}

func (s *StoreSuite) TestUpdateSessionID() {
	s.mock.ExpectExec(`UPDATE channels SET session_id`).WithArgs("new-sess", sqlmock.AnyArg(), "ch1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateSessionID(context.Background(), "ch1", "new-sess"))

	s.mock.ExpectExec(`UPDATE channels SET session_id`).WithArgs("new-sess", sqlmock.AnyArg(), "ch1").WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateSessionID(context.Background(), "ch1", "new-sess"))
}

func (s *StoreSuite) TestUpdateChannelPermissions() {
	perms := types.Permissions{
		Owners:  types.RoleGrant{Users: []string{"U1"}, Roles: []string{"admin"}},
		Members: types.RoleGrant{Users: []string{"U2"}},
	}
	s.mock.ExpectExec(`UPDATE channels SET permissions`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "ch1", "ch1").WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(s.T(), s.store.UpdateChannelPermissions(context.Background(), "ch1", perms))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())

	s.mock.ExpectExec(`UPDATE channels SET permissions`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "ch1", "ch1").WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateChannelPermissions(context.Background(), "ch1", types.Permissions{}))
}

// --- DeleteChannel tests ---

func (s *StoreSuite) TestDeleteChannel() {
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 5))
	s.mock.ExpectExec(`DELETE FROM channels WHERE channel_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.DeleteChannel(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteChannelErrors() {
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	err := s.store.DeleteChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting messages for channel")

	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM channels WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	err = s.store.DeleteChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestDeleteChannelsByParentID() {
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 10))
	s.mock.ExpectExec(`DELETE FROM channels WHERE parent_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 3))

	err := s.store.DeleteChannelsByParentID(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteChannelsByParentIDErrors() {
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	err := s.store.DeleteChannelsByParentID(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting messages for child channels")

	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM channels WHERE parent_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	err = s.store.DeleteChannelsByParentID(context.Background(), "ch1")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListChannelIDsByParentID() {
	rows := sqlmock.NewRows([]string{"channel_id"}).AddRow("t1").AddRow("t2")
	s.mock.ExpectQuery(`SELECT channel_id FROM channels WHERE parent_id`).
		WithArgs("ch1").
		WillReturnRows(rows)

	ids, err := s.store.ListChannelIDsByParentID(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"t1", "t2"}, ids)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListChannelIDsByParentIDEmpty() {
	rows := sqlmock.NewRows([]string{"channel_id"})
	s.mock.ExpectQuery(`SELECT channel_id FROM channels WHERE parent_id`).
		WithArgs("ch1").
		WillReturnRows(rows)

	ids, err := s.store.ListChannelIDsByParentID(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), ids)
}

func (s *StoreSuite) TestListChannelIDsByParentIDError() {
	s.mock.ExpectQuery(`SELECT channel_id FROM channels WHERE parent_id`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)

	ids, err := s.store.ListChannelIDsByParentID(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), ids)
}

func (s *StoreSuite) TestListChannelIDsByParentIDScanError() {
	rows := sqlmock.NewRows([]string{"channel_id"}).AddRow(nil)
	s.mock.ExpectQuery(`SELECT channel_id FROM channels WHERE parent_id`).
		WithArgs("ch1").
		WillReturnRows(rows)

	ids, err := s.store.ListChannelIDsByParentID(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), ids)
}

func (s *StoreSuite) TestListChannels() {
	now := time.Now().UTC()
	permJSON := `{"owners":{"users":["U1"],"roles":[]},"members":{"users":[],"roles":[]}}`
	rows := newMockChannelRows().
		AddRow(1, "ch1", "g1", "alpha", "/home/user/alpha", "", "discord", 1, "sess-1", permJSON, 0, now, now).
		AddRow(2, "ch2", "g1", "beta", "/home/user/beta", "ch1", "discord", 0, "sess-2", "", 0, now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels ORDER BY name ASC`).
		WillReturnRows(rows)

	channels, err := s.store.ListChannels(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), channels, 2)
	require.Equal(s.T(), "ch1", channels[0].ChannelID)
	require.Equal(s.T(), "alpha", channels[0].Name)
	require.Equal(s.T(), "/home/user/alpha", channels[0].DirPath)
	require.Empty(s.T(), channels[0].ParentID)
	require.True(s.T(), channels[0].Active)
	require.Equal(s.T(), "sess-1", channels[0].SessionID)
	require.Equal(s.T(), []string{"U1"}, channels[0].Permissions.Owners.Users)
	require.Equal(s.T(), "ch2", channels[1].ChannelID)
	require.Equal(s.T(), "beta", channels[1].Name)
	require.Equal(s.T(), "ch1", channels[1].ParentID)
	require.False(s.T(), channels[1].Active)
	require.True(s.T(), channels[1].Permissions.IsEmpty())
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListChannelsEmpty() {
	rows := newMockChannelRows()
	s.mock.ExpectQuery(`SELECT .+ FROM channels ORDER BY name ASC`).
		WillReturnRows(rows)

	channels, err := s.store.ListChannels(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), channels)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListChannelsErrors() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels ORDER BY name ASC`).WillReturnError(sql.ErrConnDone)
	channels, err := s.store.ListChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)

	s.mock.ExpectQuery(`SELECT .+ FROM channels ORDER BY name ASC`).WillReturnRows(
		newMockChannelRows().AddRow("not-an-int", "ch1", "g1", "test", "/home/user/project", "", "", 1, "sess-1", "", 0, time.Now().UTC(), time.Now().UTC()))
	channels, err = s.store.ListChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)
}

// --- Message tests ---

func (s *StoreSuite) TestInsertMessage() {
	msg := &Message{
		ChatID: 1, ChannelID: "ch1", MsgID: "msg1",
		AuthorID: "u1", AuthorName: "user1", Content: "hello",
		IsBot: false, IsProcessed: false, CreatedAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(msg.ChatID, msg.ChannelID, msg.MsgID, msg.AuthorID, msg.AuthorName, msg.Content, 0, 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(42, 1))

	err := s.store.InsertMessage(context.Background(), msg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(42), msg.ID)
}

func (s *StoreSuite) TestInsertMessageErrors() {
	anyArgs := []driver.Value{sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()}
	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)
	err := s.store.InsertMessage(context.Background(), &Message{ChatID: 1, ChannelID: "ch1", MsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()})
	require.Error(s.T(), err)

	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	err = s.store.InsertMessage(context.Background(), &Message{ChatID: 1, ChannelID: "ch1", MsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()})
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestMarkMessagesProcessed() {
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1`).
		WithArgs(int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.MarkMessagesProcessed(context.Background(), []int64{1, 2})
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestMarkMessagesProcessedError() {
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.MarkMessagesProcessed(context.Background(), []int64{1, 2}))
}

func (s *StoreSuite) TestMarkMessagesProcessedEmpty() {
	err := s.store.MarkMessagesProcessed(context.Background(), []int64{})
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestDeleteQueuedMessage() {
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id = \? AND msg_id = \? AND is_bot = 0 AND is_processed = 0`).
		WithArgs("ch1", "msg-queued").
		WillReturnResult(sqlmock.NewResult(0, 1))

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "msg-queued")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
}

func (s *StoreSuite) TestDeleteQueuedMessageNotFound() {
	s.mock.ExpectExec(`DELETE FROM messages`).
		WithArgs("ch1", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "missing")
	require.NoError(s.T(), err)
	require.False(s.T(), ok)
}

func (s *StoreSuite) TestDeleteQueuedMessageExecError() {
	s.mock.ExpectExec(`DELETE FROM messages`).
		WithArgs("ch1", "msg1").
		WillReturnError(sql.ErrConnDone)

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "msg1")
	require.Error(s.T(), err)
	require.False(s.T(), ok)
}

func (s *StoreSuite) TestDeleteQueuedMessageRowsAffectedError() {
	s.mock.ExpectExec(`DELETE FROM messages`).
		WithArgs("ch1", "msg1").
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "msg1")
	require.Error(s.T(), err)
	require.False(s.T(), ok)
}

func (s *StoreSuite) TestGetRecentMessages() {
	now := time.Now().UTC()
	rows := newMockMessageRows().
		AddRow(1, 1, "ch1", "msg1", "u1", "user1", "hello", 0, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ ORDER BY created_at DESC LIMIT`).
		WithArgs("ch1", 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.True(s.T(), msgs[0].IsProcessed)
}

func (s *StoreSuite) TestGetRecentMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", 10).WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)

	// Scan error inside scanMessages.
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", 10).WillReturnRows(
		newMockMessageRows().AddRow("not-an-int", 1, "ch1", "msg1", "u1", "user1", "hello", 0, 0, time.Now().UTC()))
	msgs, err = s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestGetMessagesCursor() {
	now := time.Now().UTC()
	rows := newMockMessageRows().
		AddRow(5, 1, "ch1", "msg5", "u1", "user1", "five", 0, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ ORDER BY id DESC LIMIT`).
		WithArgs("ch1", 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 0, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.Equal(s.T(), int64(5), msgs[0].ID)
}

func (s *StoreSuite) TestGetMessagesCursorWithCursor() {
	now := time.Now().UTC()
	rows := newMockMessageRows().
		AddRow(3, 1, "ch1", "msg3", "u1", "user1", "three", 0, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ AND id < .+ ORDER BY id DESC LIMIT`).
		WithArgs("ch1", int64(5), 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 5, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.Equal(s.T(), int64(3), msgs[0].ID)
}

func (s *StoreSuite) TestGetMessagesCursorError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", 10).WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 0, 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestGetMessagesCursorWithCursorError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", int64(5), 10).WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 5, 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- SearchMessages tests ---

func (s *StoreSuite) TestSearchMessages() {
	now := time.Now().UTC()
	rows := newMockMessageRows().
		AddRow(10, 1, "ch1", "msg10", "u1", "alice", "hello world", 0, 0, now).
		AddRow(5, 2, "ch2", "msg5", "bot", "assistant", "hello there", 1, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE content LIKE .+ ORDER BY created_at DESC LIMIT`).
		WithArgs("%hello%", 20).
		WillReturnRows(rows)

	msgs, err := s.store.SearchMessages(context.Background(), "hello", 20)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 2)
	require.Equal(s.T(), "hello world", msgs[0].Content)
	require.Equal(s.T(), "ch1", msgs[0].ChannelID)
	require.False(s.T(), msgs[0].IsBot)
	require.Equal(s.T(), "hello there", msgs[1].Content)
	require.True(s.T(), msgs[1].IsBot)
}

func (s *StoreSuite) TestSearchMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE content LIKE`).
		WithArgs("%fail%", 10).
		WillReturnError(sql.ErrConnDone)

	msgs, err := s.store.SearchMessages(context.Background(), "fail", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- GetMessagesAround tests ---

func (s *StoreSuite) TestGetMessagesAround() {
	now := time.Now().UTC()
	rows := newMockMessageRows().
		AddRow(8, 1, "ch1", "msg8", "u1", "alice", "before", 0, 0, now).
		AddRow(10, 1, "ch1", "msg10", "u1", "alice", "target", 0, 0, now).
		AddRow(11, 1, "ch1", "msg11", "bot", "assistant", "after", 1, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM .+ UNION ALL .+ ORDER BY id ASC`).
		WithArgs("ch1", int64(10), 25, "ch1", int64(10), 25).
		WillReturnRows(rows)

	msgs, err := s.store.GetMessagesAround(context.Background(), "ch1", 10, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 3)
	require.Equal(s.T(), int64(8), msgs[0].ID)
	require.Equal(s.T(), int64(10), msgs[1].ID)
	require.Equal(s.T(), int64(11), msgs[2].ID)
}

func (s *StoreSuite) TestGetMessagesAroundError() {
	s.mock.ExpectQuery(`SELECT .+ FROM .+ UNION ALL`).
		WithArgs("ch1", int64(5), 25, "ch1", int64(5), 25).
		WillReturnError(sql.ErrConnDone)

	msgs, err := s.store.GetMessagesAround(context.Background(), "ch1", 5, 50)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- ScheduledTask tests ---

func (s *StoreSuite) TestCreateScheduledTask() {
	task := &ScheduledTask{
		ChannelID: "ch1", GuildID: "g1", Schedule: "*/5 * * * *",
		Type: TaskTypeCron, Prompt: "check news", Enabled: true,
		NextRunAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(task.ChannelID, task.GuildID, task.Schedule, "cron", task.Prompt, 1, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "", 0, 0, "", 0, "", "").
		WillReturnResult(sqlmock.NewResult(5, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(5), id)
	require.Equal(s.T(), int64(5), task.ID)
}

func (s *StoreSuite) TestCreateScheduledTaskErrors() {
	anyArgs := []driver.Value{sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)
	id, err := s.store.CreateScheduledTask(context.Background(), &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)

	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	id, err = s.store.CreateScheduledTask(context.Background(), &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestGetDueTasks() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND running = 0 AND next_run_at`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.GetDueTasks(context.Background(), now)
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 1)
	require.Equal(s.T(), TaskTypeCron, tasks[0].Type)
	require.True(s.T(), tasks[0].Enabled)
}

func (s *StoreSuite) TestGetDueTasksErrors() {
	now := time.Now().UTC()
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND running = 0`).WithArgs(sqlmock.AnyArg()).WillReturnError(sql.ErrConnDone)
	tasks, err := s.store.GetDueTasks(context.Background(), now)
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)

	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND running = 0`).WithArgs(sqlmock.AnyArg()).WillReturnRows(
		newMockTaskRows().AddRow("bad", "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}"))
	tasks, err = s.store.GetDueTasks(context.Background(), now)
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestUpdateScheduledTask() {
	task := &ScheduledTask{
		ID: 1, Schedule: "0 * * * *", Type: TaskTypeInterval,
		Prompt: "updated prompt", Enabled: false, NextRunAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET`).
		WithArgs(task.Schedule, "interval", task.Prompt, 0, sqlmock.AnyArg(), sqlmock.AnyArg(), 0, "", 0, "", 0, 0, "", "", task.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateScheduledTaskError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTask(context.Background(), &ScheduledTask{ID: 1, Type: TaskTypeCron, NextRunAt: time.Now().UTC()}))
}

func (s *StoreSuite) TestDeleteScheduledTask() {
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestDeleteScheduledTaskErrors() {
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)

	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	err = s.store.DeleteScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListScheduledTasks() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}").
		AddRow(2, "ch1", "g1", "30m", "interval", "ping", 0, now.Add(time.Hour), now, now, "", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id`).
		WithArgs("ch1", sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 2)
	require.True(s.T(), tasks[0].Enabled)
	require.False(s.T(), tasks[1].Enabled)
}

func (s *StoreSuite) TestListScheduledTasksError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id`).WithArgs("ch1", sqlmock.AnyArg()).WillReturnError(sql.ErrConnDone)
	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestListAllScheduledTasks() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}").
		AddRow(2, "ch2", "g1", "1h", "interval", "deploy", 1, now.Add(time.Hour), now, now, "", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.ListAllScheduledTasks(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 2)
	require.Equal(s.T(), "ch1", tasks[0].ChannelID)
	require.Equal(s.T(), "ch2", tasks[1].ChannelID)
}

func (s *StoreSuite) TestListAllScheduledTasksError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE`).WithArgs(sqlmock.AnyArg()).WillReturnError(sql.ErrConnDone)
	tasks, err := s.store.ListAllScheduledTasks(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabled() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).WithArgs(0, sqlmock.AnyArg(), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateScheduledTaskEnabled(context.Background(), 1, false))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTask() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnRows(rows)

	task, err := s.store.GetScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), task)
	require.Equal(s.T(), int64(1), task.ID)
	require.Equal(s.T(), "ch1", task.ChannelID)
	require.Equal(s.T(), TaskTypeCron, task.Type)
	require.True(s.T(), task.Enabled)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTaskNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).WithArgs(int64(1)).WillReturnError(sql.ErrNoRows)
	task, err := s.store.GetScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
	require.Nil(s.T(), task)

	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	task, err = s.store.GetScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
	require.Nil(s.T(), task)
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabledError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).WithArgs(1, sqlmock.AnyArg(), int64(1)).WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTaskEnabled(context.Background(), 1, true))
}

func (s *StoreSuite) TestUpdateScheduledTaskThreadID() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("thread-1", sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateScheduledTaskThreadID(context.Background(), 5, "thread-1"))
}

func (s *StoreSuite) TestUpdateScheduledTaskThreadIDError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("t", sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTaskThreadID(context.Background(), 1, "t"))
}

func (s *StoreSuite) TestUpdateScheduledTaskOriginBranch() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET origin_branch`).
		WithArgs("main", sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateScheduledTaskOriginBranch(context.Background(), 5, "main"))
}

func (s *StoreSuite) TestUpdateScheduledTaskOriginBranchError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET origin_branch`).
		WithArgs("main", sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTaskOriginBranch(context.Background(), 1, "main"))
}

func (s *StoreSuite) TestClaimScheduledTaskRunning() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 5)
	require.NoError(s.T(), err)
	require.True(s.T(), claimed)
}

func (s *StoreSuite) TestClaimScheduledTaskRunningAlreadyRunning() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 5)
	require.NoError(s.T(), err)
	require.False(s.T(), claimed)
}

func (s *StoreSuite) TestClaimScheduledTaskRunningExecError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 1)
	require.Error(s.T(), err)
	require.False(s.T(), claimed)
}

func (s *StoreSuite) TestClaimScheduledTaskRunningRowsError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(1)).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 1)
	require.Error(s.T(), err)
	require.False(s.T(), claimed)
}

func (s *StoreSuite) TestReleaseScheduledTaskRunning() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.ReleaseScheduledTaskRunning(context.Background(), 5))
}

func (s *StoreSuite) TestReleaseScheduledTaskRunningError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.ReleaseScheduledTaskRunning(context.Background(), 1))
}

// --- TaskRunLog tests ---

func (s *StoreSuite) TestInsertTaskRunLog() {
	trl := &TaskRunLog{
		TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO task_run_logs`).
		WithArgs(trl.TaskID, "running", trl.ResponseText, trl.ErrorText, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(10, 1))

	id, err := s.store.InsertTaskRunLog(context.Background(), trl)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(10), id)
}

func (s *StoreSuite) TestInsertTaskRunLogErrors() {
	anyArgs := []driver.Value{sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()}
	s.mock.ExpectExec(`INSERT INTO task_run_logs`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)
	id, err := s.store.InsertTaskRunLog(context.Background(), &TaskRunLog{TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)

	s.mock.ExpectExec(`INSERT INTO task_run_logs`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	id, err = s.store.InsertTaskRunLog(context.Background(), &TaskRunLog{TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestUpdateTaskRunLog() {
	trl := &TaskRunLog{
		ID: 10, Status: RunStatusSuccess, ResponseText: "done",
		FinishedAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`UPDATE task_run_logs SET`).
		WithArgs("success", trl.ResponseText, trl.ErrorText, sqlmock.AnyArg(), trl.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateTaskRunLog(context.Background(), trl)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateTaskRunLogError() {
	s.mock.ExpectExec(`UPDATE task_run_logs SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateTaskRunLog(context.Background(), &TaskRunLog{ID: 10, Status: RunStatusFailed}))
}

func (s *StoreSuite) TestListTaskRunLogs() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "task_id", "status", "response_text", "error_text", "started_at", "finished_at"}).
		AddRow(1, 42, "success", "ok", "", now, now.Add(time.Second)).
		AddRow(2, 42, "failed", "", "boom", now.Add(time.Minute), now.Add(time.Minute+time.Second))
	s.mock.ExpectQuery(`SELECT .+ FROM task_run_logs WHERE task_id .+ ORDER BY started_at DESC LIMIT`).
		WithArgs(int64(42), 50).
		WillReturnRows(rows)

	logs, err := s.store.ListTaskRunLogs(context.Background(), 42, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), logs, 2)
	require.Equal(s.T(), RunStatusSuccess, logs[0].Status)
	require.Equal(s.T(), "boom", logs[1].ErrorText)
}

func (s *StoreSuite) TestListTaskRunLogsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM task_run_logs WHERE task_id`).
		WithArgs(int64(42), 50).
		WillReturnError(sql.ErrConnDone)

	logs, err := s.store.ListTaskRunLogs(context.Background(), 42, 50)
	require.Error(s.T(), err)
	require.Nil(s.T(), logs)
}

func (s *StoreSuite) TestListTaskRunLogsScanError() {
	rows := sqlmock.NewRows([]string{"id", "task_id", "status", "response_text", "error_text", "started_at", "finished_at"}).
		AddRow("bad", 42, "success", "ok", "", time.Now().UTC(), time.Now().UTC())
	s.mock.ExpectQuery(`SELECT .+ FROM task_run_logs WHERE task_id`).
		WithArgs(int64(42), 50).
		WillReturnRows(rows)

	logs, err := s.store.ListTaskRunLogs(context.Background(), 42, 50)
	require.Error(s.T(), err)
	require.Nil(s.T(), logs)
}

// --- initDB tests ---

func (s *StoreSuite) TestInitDBSuccess() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(sqlmock.NewResult(0, 0))
	// schema_migrations creation
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	// Each migration check (already applied)
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).WithArgs(i).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	err = initDB(db)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestInitDBErrors() {
	ok := sqlmock.NewResult(0, 0)
	cases := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantMsg string
	}{
		{"WAL error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnError(sql.ErrConnDone)
		}, "enabling WAL mode"},
		{"busy timeout error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnError(sql.ErrConnDone)
		}, "setting busy timeout"},
		{"foreign keys error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnError(sql.ErrConnDone)
		}, "enabling foreign keys"},
		{"migrations error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(ok)
			m.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnError(sql.ErrConnDone)
		}, "running migrations"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			db, mock, err := sqlmock.New()
			require.NoError(s.T(), err)
			defer db.Close()
			tc.setup(mock)
			err = initDB(db)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.wantMsg)
		})
	}
}

func (s *StoreSuite) TestMigrateTimestampsToUTC() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	localTime := time.Date(2026, 2, 8, 11, 0, 0, 0, time.FixedZone("EET", 2*60*60))

	// scheduled_tasks query
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
			AddRow(1, localTime, localTime, localTime))
	mock.ExpectExec(`UPDATE scheduled_tasks SET next_run_at`).
		WithArgs(localTime.UTC(), localTime.UTC(), localTime.UTC(), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// task_run_logs query
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
			AddRow(10, localTime, localTime))
	mock.ExpectExec(`UPDATE task_run_logs SET started_at`).
		WithArgs(localTime.UTC(), localTime.UTC(), int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMigrateTimestampsToUTCEmpty() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMigrateTimestampsToUTCErrors() {
	now := time.Now().UTC()
	emptyTaskRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"})
	}
	cases := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantMsg string
	}{
		{"query error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnError(sql.ErrConnDone)
		}, "querying scheduled_tasks"},
		{"update error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
					AddRow(1, now, now, now))
			m.ExpectExec(`UPDATE scheduled_tasks SET next_run_at`).WillReturnError(sql.ErrConnDone)
		}, "updating scheduled_task 1"},
		{"log query error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).WillReturnError(sql.ErrConnDone)
		}, "querying task_run_logs"},
		{"scan error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
					AddRow("not-an-int", "bad", "bad", "bad"))
		}, "scanning scheduled_task"},
		{"rows error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
					CloseError(fmt.Errorf("rows iteration error")))
		}, "iterating scheduled_tasks"},
		{"log scan error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
					AddRow("not-an-int", "bad", "bad"))
		}, "scanning task_run_log"},
		{"log rows error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
					CloseError(fmt.Errorf("log rows iteration error")))
		}, "iterating task_run_logs"},
		{"log update error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).AddRow(10, now, now))
			m.ExpectExec(`UPDATE task_run_logs SET started_at`).WillReturnError(sql.ErrConnDone)
		}, "updating task_run_log 10"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			db, mock, err := sqlmock.New()
			require.NoError(s.T(), err)
			defer db.Close()
			tc.setup(mock)
			err = migrateTimestampsToUTC(context.Background(), db)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.wantMsg)
		})
	}
}

// --- NewSQLiteStore tests ---

func (s *StoreSuite) TestNewSQLiteStoreOpenError() {
	openFunc := func(driver, dsn string) (*sql.DB, error) {
		return nil, fmt.Errorf("open failed")
	}

	store, err := newSQLiteStoreWith(openFunc, "test.db")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
	require.Contains(s.T(), err.Error(), "opening database")
}

func (s *StoreSuite) TestNewSQLiteStoreInitDBError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)

	openFunc := func(driver, dsn string) (*sql.DB, error) {
		return db, nil
	}

	// initDB will fail on WAL pragma
	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnError(sql.ErrConnDone)
	mock.ExpectClose()

	store, err := newSQLiteStoreWith(openFunc, "test.db")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
}

func (s *StoreSuite) TestNewSQLiteStoreWithNowFunc() {
	// Exercises the nowFunc lambda set in newSQLiteStoreWith's success path.
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	defer store.Close()

	now := store.nowFunc()
	require.False(s.T(), now.IsZero())
}

func (s *StoreSuite) TestNewSQLiteStoreReaderOpenError() {
	callCount := 0
	openFunc := func(driver, dsn string) (*sql.DB, error) {
		callCount++
		if callCount == 1 {
			// Writer opens successfully with a real in-memory DB.
			return sql.Open(driver, dsn)
		}
		// Reader open fails.
		return nil, fmt.Errorf("reader open error")
	}
	store, err := newSQLiteStoreWith(openFunc, ":memory:")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
	require.Contains(s.T(), err.Error(), "opening reader database")
}

func (s *StoreSuite) TestSplitDB() {
	// Use a temp file so writer and reader share the same database.
	tmpFile := filepath.Join(s.T().TempDir(), "split_test.db")
	store, err := NewSQLiteStore(tmpFile)
	require.NoError(s.T(), err)
	defer store.Close()

	ctx := context.Background()

	// ExecContext via writer.
	_, err = store.db.ExecContext(ctx, `CREATE TABLE test_split (id INTEGER PRIMARY KEY, val TEXT)`)
	require.NoError(s.T(), err)
	_, err = store.db.ExecContext(ctx, `INSERT INTO test_split (val) VALUES (?)`, "hello")
	require.NoError(s.T(), err)

	// QueryRowContext via reader.
	var val string
	row := store.db.QueryRowContext(ctx, `SELECT val FROM test_split WHERE id = 1`)
	require.NoError(s.T(), row.Scan(&val))
	require.Equal(s.T(), "hello", val)

	// QueryContext via reader.
	rows, err := store.db.QueryContext(ctx, `SELECT val FROM test_split`)
	require.NoError(s.T(), err)
	defer rows.Close()
	require.True(s.T(), rows.Next())
	require.NoError(s.T(), rows.Scan(&val))
	require.Equal(s.T(), "hello", val)
}

func (s *StoreSuite) TestSplitDBCloseWriterError() {
	writerDB, writerMock, err := sqlmock.New()
	require.NoError(s.T(), err)
	readerDB, readerMock, err := sqlmock.New()
	require.NoError(s.T(), err)

	writerMock.ExpectClose().WillReturnError(sql.ErrConnDone)
	readerMock.ExpectClose()

	sdb := &splitDB{writer: writerDB, reader: readerDB}
	store := &SQLiteStore{db: sdb}
	err = store.Close()
	require.ErrorIs(s.T(), err, sql.ErrConnDone)
}

// --- Helper tests ---

func (s *StoreSuite) TestBoolToInt() {
	require.Equal(s.T(), 1, boolToInt(true))
	require.Equal(s.T(), 0, boolToInt(false))
}

func (s *StoreSuite) TestGetScheduledTaskByTemplateName() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "my-template", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).
		WithArgs("ch1", "my-template").
		WillReturnRows(rows)

	task, err := s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "my-template")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), task)
	require.Equal(s.T(), int64(1), task.ID)
	require.Equal(s.T(), "ch1", task.ChannelID)
	require.Equal(s.T(), "my-template", task.TemplateName)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTaskByTemplateNameNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).WithArgs("ch1", "tmpl").WillReturnError(sql.ErrNoRows)
	task, err := s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "tmpl")
	require.NoError(s.T(), err)
	require.Nil(s.T(), task)

	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).WithArgs("ch1", "tmpl").WillReturnError(sql.ErrConnDone)
	task, err = s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "tmpl")
	require.Error(s.T(), err)
	require.Nil(s.T(), task)
}

// --- Memory file tests ---

func (s *StoreSuite) TestUpsertMemoryFile() {
	file := &MemoryFile{
		FilePath: "/memory/test.md",
		Content:  "container cleanup", ContentHash: "abc123",
		Embedding: []byte{1, 2, 3, 4}, Dimensions: 768,
	}
	s.mock.ExpectExec(`INSERT INTO memory_files`).
		WithArgs(file.FilePath, file.ChunkIndex, file.Content, file.ContentHash, file.Embedding, file.Dimensions, file.DirPath, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertMemoryFile(context.Background(), file)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertMemoryFileError() {
	s.mock.ExpectExec(`INSERT INTO memory_files`).WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpsertMemoryFile(context.Background(), &MemoryFile{FilePath: "f", Content: "c", ContentHash: "h", Embedding: []byte{}, Dimensions: 1}))
}

func (s *StoreSuite) TestGetMemoryFilesByDirPath() {
	now := time.Now().UTC()
	rows := newMockMemoryRows().
		AddRow(1, "/m/a.md", 0, "content a", "hash-a", []byte{1, 2}, 768, "", now).
		AddRow(2, "/m/b.md", 0, "content b", "hash-b", []byte{3, 4}, 768, "", now)
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).
		WithArgs("/project").
		WillReturnRows(rows)

	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
	require.Equal(s.T(), "/m/a.md", files[0].FilePath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFilesByDirPathIncludesGlobal() {
	now := time.Now().UTC()
	rows := newMockMemoryRows().
		AddRow(1, "/a/x.md", 0, "content a", "hash-a", []byte{1, 2}, 768, "/project", now).
		AddRow(2, "/b/y.md", 0, "content b", "hash-b", []byte{3, 4}, 768, "", now)
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).
		WithArgs("/project").
		WillReturnRows(rows)

	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
	require.Equal(s.T(), "/project", files[0].DirPath)
	require.Equal(s.T(), "", files[1].DirPath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFilesByDirPathEmpty() {
	rows := newMockMemoryRows()
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).
		WithArgs("").
		WillReturnRows(rows)

	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "")
	require.NoError(s.T(), err)
	require.Empty(s.T(), files)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFilesByDirPathErrors() {
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).WithArgs("/project").WillReturnError(sql.ErrConnDone)
	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)

	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).WithArgs("/project").WillReturnRows(
		sqlmock.NewRows([]string{"id", "file_path"}).AddRow(1, "path")) // wrong column count
	files, err = s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)
}

func (s *StoreSuite) TestGetMemoryFileHash() {
	s.mock.ExpectQuery(`SELECT content_hash FROM memory_files WHERE file_path`).
		WithArgs("/m/a.md", "").
		WillReturnRows(sqlmock.NewRows([]string{"content_hash"}).AddRow("abc123"))

	hash, err := s.store.GetMemoryFileHash(context.Background(), "/m/a.md", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "abc123", hash)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFileHashNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT content_hash FROM memory_files`).WithArgs("/m/a.md", "").WillReturnError(sql.ErrNoRows)
	hash, err := s.store.GetMemoryFileHash(context.Background(), "/m/a.md", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "", hash)

	s.mock.ExpectQuery(`SELECT content_hash FROM memory_files`).WithArgs("/m/a.md", "").WillReturnError(sql.ErrConnDone)
	hash, err = s.store.GetMemoryFileHash(context.Background(), "/m/a.md", "")
	require.Error(s.T(), err)
	require.Equal(s.T(), "", hash)
}

func (s *StoreSuite) TestDeleteMemoryFile() {
	s.mock.ExpectExec(`DELETE FROM memory_files WHERE file_path`).
		WithArgs("/m/a.md", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.DeleteMemoryFile(context.Background(), "/m/a.md", "")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteMemoryFileError() {
	s.mock.ExpectExec(`DELETE FROM memory_files`).WithArgs("/m/a.md", "").WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.DeleteMemoryFile(context.Background(), "/m/a.md", ""))
}

func (s *StoreSuite) TestListDistinctMemoryFilePaths() {
	rows := sqlmock.NewRows([]string{"file_path", "dir_path"}).
		AddRow("/projects/foo/memory/a.md", "/projects/foo").
		AddRow("/projects/foo/memory/b.md", "/projects/foo")
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnRows(rows)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
	require.Equal(s.T(), "/projects/foo/memory/a.md", files[0].FilePath)
	require.Equal(s.T(), "/projects/foo/memory/b.md", files[1].FilePath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListDistinctMemoryFilePathsEmpty() {
	rows := sqlmock.NewRows([]string{"file_path", "dir_path"})
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnRows(rows)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.NoError(s.T(), err)
	require.Nil(s.T(), files)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListDistinctMemoryFilePathsError() {
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnError(sql.ErrConnDone)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)
}

func (s *StoreSuite) TestListDistinctMemoryFilePathsScanError() {
	rows := sqlmock.NewRows([]string{"file_path", "dir_path"}).
		AddRow(nil, "/projects/foo") // nil file_path causes scan error
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnRows(rows)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)
}

func (s *StoreSuite) TestNowFuncUsedInUpsertChannel() {
	fixedTime := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	s.store.nowFunc = func() time.Time { return fixedTime }

	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs("ch1", "g1", "test", "", "", "", "", "", 1, 0, fixedTime).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestNowFuncUsedInCreateScheduledTask() {
	fixedTime := time.Date(2099, 6, 15, 12, 0, 0, 0, time.UTC)
	s.store.nowFunc = func() time.Time { return fixedTime }

	task := &ScheduledTask{
		ChannelID: "ch1", Schedule: "0 9 * * *", Type: TaskTypeCron,
		Prompt: "test", Enabled: true, NextRunAt: fixedTime,
	}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs("ch1", "", "0 9 * * *", "cron", "test", 1, fixedTime, fixedTime, fixedTime, "", 0, 0, "", 0, "", "").
		WillReturnResult(sqlmock.NewResult(1, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(1), id)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

// --- WorkflowRun tests ---

func newMockWorkflowRunRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "workflow_name", "channel_id", "dir_path", "worktree_path",
		"status", "inputs", "paused_node_id", "error_text", "workflow_def", "started_at", "finished_at",
	})
}

func newMockNodeRunRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "run_id", "node_id", "status", "output", "error_text", "attempt", "started_at", "finished_at", "last_heartbeat_at",
	})
}

func (s *StoreSuite) TestCreateWorkflowRun() {
	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:           "run-1",
		WorkflowName: "deploy",
		ChannelID:    "ch1",
		DirPath:      "/project",
		WorktreePath: "/worktree",
		Status:       WorkflowRunStatusRunning,
		Inputs:       `{"env":"prod"}`,
		PausedNodeID: "",
		ErrorText:    "",
		StartedAt:    now,
	}

	s.mock.ExpectExec(`INSERT INTO workflow_runs`).
		WithArgs(run.ID, run.WorkflowName, run.ChannelID, run.DirPath, run.WorktreePath,
			string(run.Status), run.Inputs, run.PausedNodeID, run.ErrorText, run.WorkflowDef, run.StartedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.CreateWorkflowRun(context.Background(), run)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestCreateWorkflowRunError() {
	now := time.Now().UTC()
	run := &WorkflowRun{ID: "run-1", WorkflowName: "deploy", Status: WorkflowRunStatusRunning, StartedAt: now}

	s.mock.ExpectExec(`INSERT INTO workflow_runs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.CreateWorkflowRun(context.Background(), run)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestGetWorkflowRun() {
	now := time.Now().UTC()
	finishedAt := now.Add(time.Minute)
	rows := newMockWorkflowRunRows().
		AddRow("run-1", "deploy", "ch1", "/project", "/worktree", "completed", `{"env":"prod"}`, "", "", `{"name":"deploy"}`, now, &finishedAt)

	s.mock.ExpectQuery(`FROM workflow_runs WHERE id`).
		WithArgs("run-1").
		WillReturnRows(rows)

	run, err := s.store.GetWorkflowRun(context.Background(), "run-1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), run)
	require.Equal(s.T(), "run-1", run.ID)
	require.Equal(s.T(), "deploy", run.WorkflowName)
	require.Equal(s.T(), "ch1", run.ChannelID)
	require.Equal(s.T(), WorkflowRunStatusCompleted, run.Status)
	require.Equal(s.T(), `{"name":"deploy"}`, run.WorkflowDef)
	require.NotNil(s.T(), run.FinishedAt)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetWorkflowRunNotFoundAndError() {
	// Not found: empty rows causes sql.ErrNoRows on Scan.
	s.mock.ExpectQuery(`FROM workflow_runs WHERE id`).
		WithArgs("run-missing").
		WillReturnRows(newMockWorkflowRunRows())

	run, err := s.store.GetWorkflowRun(context.Background(), "run-missing")
	require.NoError(s.T(), err)
	require.Nil(s.T(), run)

	// Query-level error: function returns (zero-value-run, err).
	s.mock.ExpectQuery(`FROM workflow_runs WHERE id`).
		WithArgs("run-missing").
		WillReturnError(sql.ErrConnDone)

	run, err = s.store.GetWorkflowRun(context.Background(), "run-missing")
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
	_ = run
}

func (s *StoreSuite) TestUpdateWorkflowRun() {
	finishedAt := time.Now().UTC()
	run := &WorkflowRun{
		ID:           "run-1",
		Status:       WorkflowRunStatusCompleted,
		PausedNodeID: "",
		ErrorText:    "",
		FinishedAt:   &finishedAt,
	}

	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(string(run.Status), run.PausedNodeID, run.ErrorText, run.FinishedAt, run.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateWorkflowRun(context.Background(), run)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateWorkflowRunFailed() {
	run := &WorkflowRun{
		ID:        "run-1",
		Status:    WorkflowRunStatusFailed,
		ErrorText: "something went wrong",
	}

	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(string(run.Status), run.PausedNodeID, run.ErrorText, run.FinishedAt, run.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateWorkflowRun(context.Background(), run)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateWorkflowRunError() {
	run := &WorkflowRun{ID: "run-1", Status: WorkflowRunStatusFailed}

	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateWorkflowRun(context.Background(), run)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListWorkflowRuns() {
	now := time.Now().UTC()
	finishedAt := now.Add(time.Minute)
	rows := newMockWorkflowRunRows().
		AddRow("run-2", "build", "ch1", "/proj2", "", "completed", "", "", "", "", now, &finishedAt).
		AddRow("run-1", "deploy", "ch1", "/proj1", "/wt", "running", `{"k":"v"}`, "", "", "", now.Add(-time.Hour), nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT`).
		WithArgs("ch1", "ch1", "ch1", 50).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
	require.Equal(s.T(), "run-2", runs[0].ID)
	require.Equal(s.T(), WorkflowRunStatusCompleted, runs[0].Status)
	require.NotNil(s.T(), runs[0].FinishedAt)
	require.Equal(s.T(), "run-1", runs[1].ID)
	require.Equal(s.T(), WorkflowRunStatusRunning, runs[1].Status)
	require.Nil(s.T(), runs[1].FinishedAt)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsIncludesChildChannelRuns() {
	now := time.Now().UTC()
	// Three sources of runs that should all appear when listing for "dm":
	//   - run-direct: stored under "dm" itself (manual or direct task run)
	//   - run-child:  stored under "thread-real" whose parent is "dm"
	//   - run-ghost:  stored under "thread-ghost" — no channel row, but a
	//                 scheduled task on "dm" has thread_id="thread-ghost"
	rows := newMockWorkflowRunRows().
		AddRow("run-ghost", "build", "thread-ghost", "/proj", "", "completed", "", "", "", "", now, nil).
		AddRow("run-child", "test", "thread-real", "/proj", "", "completed", "", "", "", "", now.Add(-time.Minute), nil).
		AddRow("run-direct", "deploy", "dm", "/proj", "", "running", "", "", "", "", now.Add(-time.Hour), nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \?\s+OR channel_id IN \(SELECT channel_id FROM channels WHERE parent_id = \?\)\s+OR channel_id IN \(SELECT thread_id FROM scheduled_tasks WHERE channel_id = \? AND thread_id != ''\)`).
		WithArgs("dm", "dm", "dm", 50).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "dm", 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 3)
	require.Equal(s.T(), "run-ghost", runs[0].ID)
	require.Equal(s.T(), "thread-ghost", runs[0].ChannelID)
	require.Equal(s.T(), "run-child", runs[1].ID)
	require.Equal(s.T(), "thread-real", runs[1].ChannelID)
	require.Equal(s.T(), "run-direct", runs[2].ID)
	require.Equal(s.T(), "dm", runs[2].ChannelID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsWithoutChannelFilter() {
	now := time.Now().UTC()
	rows := newMockWorkflowRunRows().
		AddRow("run-3", "test", "ch2", "/proj3", "", "failed", "", "", "timeout", "", now, nil).
		AddRow("run-1", "deploy", "ch1", "/proj1", "", "running", "", "", "", "", now.Add(-time.Hour), nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs ORDER BY started_at DESC LIMIT`).
		WithArgs(10).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
	require.Equal(s.T(), "run-3", runs[0].ID)
	require.Equal(s.T(), "ch2", runs[0].ChannelID)
	require.Equal(s.T(), WorkflowRunStatusFailed, runs[0].Status)
	require.Equal(s.T(), "run-1", runs[1].ID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsEmpty() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT`).
		WithArgs("ch-empty", "ch-empty", "ch-empty", 10).
		WillReturnRows(newMockWorkflowRunRows())

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch-empty", 10)
	require.NoError(s.T(), err)
	require.Nil(s.T(), runs)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT`).
		WithArgs("ch1", "ch1", "ch1", 10).
		WillReturnError(sql.ErrConnDone)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs ORDER BY started_at DESC LIMIT`).
		WithArgs(10).
		WillReturnError(sql.ErrConnDone)

	runs, err = s.store.ListWorkflowRuns(context.Background(), "", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsScanError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT`).
		WithArgs("ch1", "ch1", "ch1", 10).
		WillReturnRows(newMockWorkflowRunRows().AddRow("bad-id", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsByStatus() {
	now := time.Now().UTC()
	rows := newMockWorkflowRunRows().
		AddRow("run-1", "wf1", "ch1", "/p", "", "running", "{}", "", "", "", now, nil).
		AddRow("run-2", "wf2", "ch2", "/p", "", "paused", "{}", "approve", "", "", now, nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs WHERE status IN`).
		WithArgs("running", "paused").
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), []WorkflowRunStatus{
		WorkflowRunStatusRunning, WorkflowRunStatusPaused,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
	require.Equal(s.T(), WorkflowRunStatusRunning, runs[0].Status)
	require.Equal(s.T(), WorkflowRunStatusPaused, runs[1].Status)
	require.Equal(s.T(), "approve", runs[1].PausedNodeID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsByStatusEmpty() {
	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), nil)
	require.NoError(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsByStatusError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs WHERE status IN`).
		WithArgs("running").
		WillReturnError(sql.ErrConnDone)

	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), []WorkflowRunStatus{WorkflowRunStatusRunning})
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsByStatusScanError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs WHERE status IN`).
		WithArgs("paused").
		WillReturnRows(newMockWorkflowRunRows().AddRow("bad", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), []WorkflowRunStatus{WorkflowRunStatusPaused})
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestUpsertNodeRunInsert() {
	now := time.Now().UTC()
	nr := &NodeRun{
		RunID:     "run-1",
		NodeID:    "node-a",
		Status:    NodeRunStatusRunning,
		Output:    "",
		ErrorText: "",
		Attempt:   1,
		StartedAt: &now,
	}

	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(nr.RunID, nr.NodeID, string(nr.Status), nr.Output, nr.ErrorText, nr.Attempt, nr.StartedAt, nr.FinishedAt, nr.LastHeartbeatAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertNodeRun(context.Background(), nr)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertNodeRunUpdate() {
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(time.Second * 30)
	nr := &NodeRun{
		RunID:      "run-1",
		NodeID:     "node-a",
		Status:     NodeRunStatusSuccess,
		Output:     "build passed",
		ErrorText:  "",
		Attempt:    1,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
	}

	// Second upsert simulates ON CONFLICT update path (same exec signature).
	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(nr.RunID, nr.NodeID, string(nr.Status), nr.Output, nr.ErrorText, nr.Attempt, nr.StartedAt, nr.FinishedAt, nr.LastHeartbeatAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpsertNodeRun(context.Background(), nr)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertNodeRunError() {
	nr := &NodeRun{RunID: "run-1", NodeID: "node-a", Status: NodeRunStatusPending, Attempt: 1}

	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpsertNodeRun(context.Background(), nr)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListNodeRuns() {
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(time.Second)
	rows := newMockNodeRunRows().
		AddRow(1, "run-1", "node-a", "success", "output-a", "", 1, &startedAt, &finishedAt, nil).
		AddRow(2, "run-1", "node-b", "running", "", "", 1, &startedAt, nil, &startedAt)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-1").
		WillReturnRows(rows)

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-1")
	require.NoError(s.T(), err)
	require.Len(s.T(), nodeRuns, 2)
	require.Equal(s.T(), int64(1), nodeRuns[0].ID)
	require.Equal(s.T(), "run-1", nodeRuns[0].RunID)
	require.Equal(s.T(), "node-a", nodeRuns[0].NodeID)
	require.Equal(s.T(), NodeRunStatusSuccess, nodeRuns[0].Status)
	require.Equal(s.T(), "output-a", nodeRuns[0].Output)
	require.NotNil(s.T(), nodeRuns[0].FinishedAt)
	require.Equal(s.T(), "node-b", nodeRuns[1].NodeID)
	require.Equal(s.T(), NodeRunStatusRunning, nodeRuns[1].Status)
	require.Nil(s.T(), nodeRuns[1].FinishedAt)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListNodeRunsEmpty() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-empty").
		WillReturnRows(newMockNodeRunRows())

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-empty")
	require.NoError(s.T(), err)
	require.Nil(s.T(), nodeRuns)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListNodeRunsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-1").
		WillReturnError(sql.ErrConnDone)

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), nodeRuns)
}

func (s *StoreSuite) TestListNodeRunsScanError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-1").
		WillReturnRows(newMockNodeRunRows().AddRow("bad-id", nil, nil, nil, nil, nil, nil, nil, nil, nil))

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), nodeRuns)
}

func (s *StoreSuite) TestUpdateNodeHeartbeat() {
	s.mock.ExpectExec(`UPDATE workflow_node_runs SET last_heartbeat_at`).
		WithArgs(sqlmock.AnyArg(), "run-1", "node-a").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateNodeHeartbeat(context.Background(), "run-1", "node-a")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateNodeHeartbeatError() {
	s.mock.ExpectExec(`UPDATE workflow_node_runs SET last_heartbeat_at`).
		WithArgs(sqlmock.AnyArg(), "run-1", "node-a").
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateNodeHeartbeat(context.Background(), "run-1", "node-a")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestDeleteWorkflowRun() {
	s.mock.ExpectExec(`DELETE FROM workflow_node_runs WHERE run_id`).
		WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	s.mock.ExpectExec(`DELETE FROM workflow_runs WHERE id`).
		WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.DeleteWorkflowRun(context.Background(), "run-1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteWorkflowRunErrors() {
	// First exec (delete node runs) fails.
	s.mock.ExpectExec(`DELETE FROM workflow_node_runs WHERE run_id`).
		WithArgs("run-1").
		WillReturnError(sql.ErrConnDone)

	err := s.store.DeleteWorkflowRun(context.Background(), "run-1")
	require.Error(s.T(), err)

	// First exec succeeds, second (delete workflow run) fails.
	s.mock.ExpectExec(`DELETE FROM workflow_node_runs WHERE run_id`).
		WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM workflow_runs WHERE id`).
		WithArgs("run-1").
		WillReturnError(sql.ErrConnDone)

	err = s.store.DeleteWorkflowRun(context.Background(), "run-1")
	require.Error(s.T(), err)
}
