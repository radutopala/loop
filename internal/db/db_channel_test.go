package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

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
			args: []driver.Value{"ch1", "g1", "test-channel", "", "", "", "", "", 1, 0, 0, sqlmock.AnyArg()},
		},
		{
			name: "with dir path",
			ch:   &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", DirPath: "/home/user/project", Active: true},
			args: []driver.Value{"ch1", "g1", "test-channel", "/home/user/project", "", "", "", "", 1, 0, 0, sqlmock.AnyArg()},
		},
		{
			name: "with parent ID",
			ch:   &Channel{ChannelID: "thread1", GuildID: "g1", Name: "", ParentID: "ch1", SessionID: "sess-parent", Active: true},
			args: []driver.Value{"thread1", "g1", "", "", "ch1", "", "sess-parent", "", 1, 0, 0, sqlmock.AnyArg()},
		},
		{
			name: "with locked",
			ch:   &Channel{ChannelID: "ch-lock", GuildID: "g1", Name: "locked", Active: true, Locked: true},
			args: []driver.Value{"ch-lock", "g1", "locked", "", "", "", "", "", 1, 0, 1, sqlmock.AnyArg()},
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
		AddRow(1, "thread1", "g1", "", "/project", "ch1", "", 1, "", "", 0, 0, now, now)
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
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", "", "", "", "", 1, 0, 0, sqlmock.AnyArg()).
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
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", "", "", "", `{"owners":{"users":["U1"],"roles":["admin"]},"members":{"users":["U2"],"roles":[]}}`, 1, 0, 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateChannelLocked() {
	s.mock.ExpectExec(`UPDATE channels SET locked`).
		WithArgs(1, sqlmock.AnyArg(), "ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateChannelLocked(context.Background(), "ch1", true))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())

	s.mock.ExpectExec(`UPDATE channels SET locked`).
		WithArgs(0, sqlmock.AnyArg(), "ch1").
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateChannelLocked(context.Background(), "ch1", false))
}

func (s *StoreSuite) TestGetChannel() {
	now := time.Now().UTC()
	permJSON := `{"owners":{"users":["U1"],"roles":["admin"]},"members":{"users":[],"roles":[]}}`
	rows := newMockChannelRows().
		AddRow(1, "ch1", "g1", "test", "/home/user/project", "", "discord", 1, "sess-123", permJSON, 0, 0, now, now)
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
		AddRow(1, "ch1", "g1", "loop", "/home/user/dev/loop", "", "discord", 1, "", permJSON, 0, 0, now, now)
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
		AddRow(1, "ch1", "", "loop-local", "/home/user/dev/loop", "", "local", 1, "", "", 0, 0, now, now).
		AddRow(2, "ch2", "g1", "loop-discord", "/home/user/dev/loop", "", "discord", 1, "", "", 0, 0, now, now)
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
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 5))
	s.mock.ExpectExec(`DELETE FROM quality_snapshots WHERE channel_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectExec(`DELETE FROM channels WHERE channel_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectCommit()

	err := s.store.DeleteChannel(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteChannelErrors() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()
	err := s.store.DeleteChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting messages for channel")

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM quality_snapshots WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	err = s.store.DeleteChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting quality snapshots for channel")

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM quality_snapshots WHERE channel_id`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM channels WHERE channel_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()
	err = s.store.DeleteChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestDeleteChannelsByParentID() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 10))
	s.mock.ExpectExec(`DELETE FROM quality_snapshots WHERE channel_id IN`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 4))
	s.mock.ExpectExec(`DELETE FROM channels WHERE parent_id`).
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	s.mock.ExpectCommit()

	err := s.store.DeleteChannelsByParentID(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteChannelsByParentIDErrors() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()
	err := s.store.DeleteChannelsByParentID(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting messages for child channels")

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM quality_snapshots WHERE channel_id IN`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	err = s.store.DeleteChannelsByParentID(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting quality snapshots for child channels")

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id IN`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM quality_snapshots WHERE channel_id IN`).WithArgs("ch1").WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM channels WHERE parent_id`).WithArgs("ch1").WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()
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
		AddRow(1, "ch1", "g1", "alpha", "/home/user/alpha", "", "discord", 1, "sess-1", permJSON, 0, 0, now, now).
		AddRow(2, "ch2", "g1", "beta", "/home/user/beta", "ch1", "discord", 0, "sess-2", "", 0, 1, now, now)
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
	require.False(s.T(), channels[0].Locked)
	require.True(s.T(), channels[1].Locked)
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
		newMockChannelRows().AddRow("not-an-int", "ch1", "g1", "test", "/home/user/project", "", "", 1, "sess-1", "", 0, 0, time.Now().UTC(), time.Now().UTC()))
	channels, err = s.store.ListChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)
}
