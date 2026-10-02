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
			args: []driver.Value{"ch1", "g1", "test-channel", "", "", "", "", "", 1, 0, "", 0, 0, sqlmock.AnyArg()},
		},
		{
			name: "with dir path",
			ch:   &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", DirPath: "/home/user/project", Active: true},
			args: []driver.Value{"ch1", "g1", "test-channel", "/home/user/project", "", "", "", "", 1, 0, "", 0, 0, sqlmock.AnyArg()},
		},
		{
			name: "with parent ID",
			ch:   &Channel{ChannelID: "thread1", GuildID: "g1", Name: "", ParentID: "ch1", SessionID: "sess-parent", Active: true},
			args: []driver.Value{"thread1", "g1", "", "", "ch1", "", "sess-parent", "", 1, 0, "", 0, 0, sqlmock.AnyArg()},
		},
		{
			name: "with locked",
			ch:   &Channel{ChannelID: "ch-lock", GuildID: "g1", Name: "locked", Active: true, Locked: true},
			args: []driver.Value{"ch-lock", "g1", "locked", "", "", "", "", "", 1, 0, "", 1, 0, sqlmock.AnyArg()},
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
		AddRow(1, "thread1", "g1", "", "/project", "ch1", "", 1, "", "", 0, "", 0, "", "", 0, 0, "", "", "", "", "", now, now)
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
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", "", "", "", "", 1, 0, "", 0, 0, sqlmock.AnyArg()).
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
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", "", "", "", `{"owners":{"users":["U1"],"roles":["admin"]},"members":{"users":["U2"],"roles":[]}}`, 1, 0, "", 0, 0, sqlmock.AnyArg()).
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

func (s *StoreSuite) TestUpdateChannelName() {
	s.mock.ExpectExec(`UPDATE channels SET name`).
		WithArgs("new-name", sqlmock.AnyArg(), "ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateChannelName(context.Background(), "ch1", "new-name"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())

	s.mock.ExpectExec(`UPDATE channels SET name`).
		WithArgs("another-name", sqlmock.AnyArg(), "ch1").
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateChannelName(context.Background(), "ch1", "another-name"))
}

func (s *StoreSuite) TestUpdateChannelDescription() {
	tests := []struct {
		name        string
		description string
		err         error
	}{
		{name: "set", description: "fixes the login flow"},
		{name: "clear", description: ""},
		{name: "db error", description: "x", err: sql.ErrConnDone},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			exp := s.mock.ExpectExec(`UPDATE channels SET description = \?, updated_at = \? WHERE channel_id = \?`).
				WithArgs(tt.description, sqlmock.AnyArg(), "ch1")
			if tt.err != nil {
				exp.WillReturnError(tt.err)
			} else {
				exp.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			err := s.store.UpdateChannelDescription(context.Background(), "ch1", tt.description)
			if tt.err != nil {
				require.ErrorIs(s.T(), err, tt.err)
			} else {
				require.NoError(s.T(), err)
			}
			require.NoError(s.T(), s.mock.ExpectationsWereMet())
		})
	}
}

func (s *StoreSuite) TestUpdateChannelTicketURL() {
	tests := []struct {
		name string
		url  string
		err  error
	}{
		{name: "set", url: "https://example.atlassian.net/browse/PROJ-1"},
		{name: "clear", url: ""},
		{name: "db error", url: "https://x", err: sql.ErrConnDone},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			exp := s.mock.ExpectExec(`UPDATE channels SET ticket_url = \?, updated_at = \? WHERE channel_id = \?`).
				WithArgs(tt.url, sqlmock.AnyArg(), "ch1")
			if tt.err != nil {
				exp.WillReturnError(tt.err)
			} else {
				exp.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			err := s.store.UpdateChannelTicketURL(context.Background(), "ch1", tt.url)
			if tt.err != nil {
				require.ErrorIs(s.T(), err, tt.err)
			} else {
				require.NoError(s.T(), err)
			}
			require.NoError(s.T(), s.mock.ExpectationsWereMet())
		})
	}
}

func (s *StoreSuite) TestGetChannel() {
	now := time.Now().UTC()
	permJSON := `{"owners":{"users":["U1"],"roles":["admin"]},"members":{"users":[],"roles":[]}}`
	rows := newMockChannelRows().
		AddRow(1, "ch1", "g1", "test", "/home/user/project", "", "discord", 1, "sess-123", permJSON, 0, "", 0, "", "", 0, 0, "reviews PRs", "https://example.atlassian.net/browse/PROJ-1", "", "", "", now, now)
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
	require.Equal(s.T(), "reviews PRs", ch.Description)
	require.Equal(s.T(), "https://example.atlassian.net/browse/PROJ-1", ch.TicketURL)
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
		AddRow(1, "ch1", "g1", "loop", "/home/user/dev/loop", "", "discord", 1, "", permJSON, 0, "", 0, "", "", 0, 0, "", "", "", "", "", now, now)
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
		AddRow(1, "ch1", "", "loop-local", "/home/user/dev/loop", "", "local", 1, "", "", 0, "", 0, "", "", 0, 0, "", "", "", "", "", now, now).
		AddRow(2, "ch2", "g1", "loop-discord", "/home/user/dev/loop", "", "discord", 1, "", "", 0, "", 0, "", "", 0, 0, "", "", "", "", "", now, now)
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

func (s *StoreSuite) TestSessionInUse() {
	tests := []struct {
		name    string
		count   int
		err     error
		want    bool
		wantErr bool
	}{
		{name: "in use", count: 1, want: true},
		{name: "not in use", count: 0},
		{name: "error", err: sql.ErrConnDone, wantErr: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			q := s.mock.ExpectQuery(`SELECT COUNT\(\*\) FROM channels WHERE session_id = \? AND channel_id != \?`).WithArgs("sess-1", "l1")
			if tc.err != nil {
				q.WillReturnError(tc.err)
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(tc.count))
			}
			got, err := s.store.SessionInUse(context.Background(), "sess-1", "l1")
			if tc.wantErr {
				require.Error(s.T(), err)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMarkSessionForkPending() {
	s.mock.ExpectExec(`UPDATE channels SET session_id = \?, fork_pending = 1, fork_resume_at = \?`).
		WithArgs("sess-1", "", sqlmock.AnyArg(), "t2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	ok, err := s.store.MarkSessionForkPending(context.Background(), "t2", "sess-1")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	// A thread deleted meanwhile updates nothing.
	s.mock.ExpectExec(`UPDATE channels SET session_id = \?, fork_pending = 1, fork_resume_at = \?`).
		WithArgs("sess-1", "", sqlmock.AnyArg(), "t2").
		WillReturnResult(sqlmock.NewResult(0, 0))
	ok, err = s.store.MarkSessionForkPending(context.Background(), "t2", "sess-1")
	require.NoError(s.T(), err)
	require.False(s.T(), ok)

	s.mock.ExpectExec(`UPDATE channels SET session_id = \?, fork_pending = 1, fork_resume_at = \?`).
		WithArgs("sess-1", "", sqlmock.AnyArg(), "t2").
		WillReturnError(sql.ErrConnDone)
	_, err = s.store.MarkSessionForkPending(context.Background(), "t2", "sess-1")
	require.Error(s.T(), err)

	s.mock.ExpectExec(`UPDATE channels SET session_id = \?, fork_pending = 1, fork_resume_at = \?`).
		WithArgs("sess-1", "", sqlmock.AnyArg(), "t2").
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	_, err = s.store.MarkSessionForkPending(context.Background(), "t2", "sess-1")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestUpdateChannelAgentOverrides() {
	s.mock.ExpectExec(`UPDATE channels SET model_override = \?, effort_override = \?`).
		WithArgs("claude-opus-4-8", "high", sqlmock.AnyArg(), "ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateChannelAgentOverrides(context.Background(), "ch1", "claude-opus-4-8", "high"))

	s.mock.ExpectExec(`UPDATE channels SET model_override = \?, effort_override = \?`).
		WithArgs("", "", sqlmock.AnyArg(), "ch1").
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateChannelAgentOverrides(context.Background(), "ch1", "", ""))
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

// deleteStep is one statement of a channel delete, for walking the
// statements up to the one that fails.
type deleteStep struct {
	query  string
	errMsg string
}

var deleteChannelSteps = []deleteStep{
	{`DELETE FROM messages WHERE channel_id = \?`, "deleting messages for channel"},
	{`DELETE FROM quality_snapshots WHERE channel_id = \?`, "deleting quality snapshots for channel"},
	{`DELETE FROM messages WHERE channel_id IN \(SELECT channel_id FROM channels WHERE kind IN \(` + hiddenKindsRe + `\) AND parent_id IN \(\?\)\)`, "deleting hidden thread messages"},
	{`DELETE FROM quality_snapshots WHERE channel_id IN \(SELECT channel_id FROM channels WHERE kind IN \(` + hiddenKindsRe + `\) AND parent_id IN \(\?\)\)`, "deleting hidden thread quality snapshots"},
	{`DELETE FROM learn_proposals WHERE channel_id IN \(\?\)`, "deleting learn proposals"},
	{`DELETE FROM explanations WHERE channel_id IN \(\?\)`, "deleting explanations"},
	{`DELETE FROM learn_passes WHERE channel_id IN \(\?\)`, "deleting learn passes"},
	{`DELETE FROM channels WHERE kind IN \(` + hiddenKindsRe + `\) AND parent_id IN \(\?\)`, "deleting hidden threads"},
	{`DELETE FROM channels WHERE channel_id = \?`, ""},
}

const (
	childIDs      = `SELECT channel_id FROM channels WHERE parent_id = \?`
	hiddenKindsRe = `'learn', 'explain'`
)

var deleteChildrenSteps = []deleteStep{
	{`DELETE FROM messages WHERE channel_id IN \(SELECT channel_id FROM channels WHERE kind IN \(` + hiddenKindsRe + `\) AND parent_id IN \(` + childIDs + `\)\)`, "deleting hidden thread messages"},
	{`DELETE FROM quality_snapshots WHERE channel_id IN \(SELECT channel_id FROM channels WHERE kind IN \(` + hiddenKindsRe + `\) AND parent_id IN \(` + childIDs + `\)\)`, "deleting hidden thread quality snapshots"},
	{`DELETE FROM learn_proposals WHERE channel_id IN \(` + childIDs + `\)`, "deleting learn proposals"},
	{`DELETE FROM explanations WHERE channel_id IN \(` + childIDs + `\)`, "deleting explanations"},
	{`DELETE FROM learn_passes WHERE channel_id IN \(` + childIDs + `\)`, "deleting learn passes"},
	{`DELETE FROM channels WHERE kind IN \(` + hiddenKindsRe + `\) AND parent_id IN \(` + childIDs + `\)`, "deleting hidden threads"},
	{`DELETE FROM messages WHERE channel_id IN \(` + childIDs + `\)`, "deleting messages for child channels"},
	{`DELETE FROM quality_snapshots WHERE channel_id IN \(` + childIDs + `\)`, "deleting quality snapshots for child channels"},
	{`DELETE FROM channels WHERE parent_id = \?`, ""},
}

// expectDeleteSteps expects steps in order; the one at failAt fails and the
// transaction rolls back, or all succeed and it commits when failAt is -1.
func (s *StoreSuite) expectDeleteSteps(steps []deleteStep, failAt int) {
	s.mock.ExpectBegin()
	for i, st := range steps {
		exp := s.mock.ExpectExec(st.query).WithArgs("ch1")
		if i == failAt {
			exp.WillReturnError(sql.ErrConnDone)
			s.mock.ExpectRollback()
			return
		}
		exp.WillReturnResult(sqlmock.NewResult(0, 1))
	}
	s.mock.ExpectCommit()
}

func (s *StoreSuite) TestDeleteChannel() {
	s.expectDeleteSteps(deleteChannelSteps, -1)
	require.NoError(s.T(), s.store.DeleteChannel(context.Background(), "ch1"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteChannelErrors() {
	for i, st := range deleteChannelSteps {
		s.expectDeleteSteps(deleteChannelSteps, i)
		err := s.store.DeleteChannel(context.Background(), "ch1")
		require.Error(s.T(), err, st.query)
		require.Contains(s.T(), err.Error(), st.errMsg)
		require.NoError(s.T(), s.mock.ExpectationsWereMet())
	}
}

func (s *StoreSuite) TestDeleteChannelsByParentID() {
	s.expectDeleteSteps(deleteChildrenSteps, -1)
	require.NoError(s.T(), s.store.DeleteChannelsByParentID(context.Background(), "ch1"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteChannelsByParentIDErrors() {
	for i, st := range deleteChildrenSteps {
		s.expectDeleteSteps(deleteChildrenSteps, i)
		err := s.store.DeleteChannelsByParentID(context.Background(), "ch1")
		require.Error(s.T(), err, st.query)
		require.Contains(s.T(), err.Error(), st.errMsg)
		require.NoError(s.T(), s.mock.ExpectationsWereMet())
	}
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
		AddRow(1, "ch1", "g1", "alpha", "/home/user/alpha", "", "discord", 1, "sess-1", permJSON, 0, "", 0, "", "", 0, 0, "", "", "", "", "", now, now).
		AddRow(2, "ch2", "g1", "beta", "/home/user/beta", "ch1", "discord", 0, "sess-2", "", 0, "", 1, "", "", 0, 0, "", "", "", "", "", now, now)
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

func (s *StoreSuite) TestChannelActivity() {
	at := time.Date(2026, 9, 25, 11, 3, 6, 0, time.UTC)
	s.mock.ExpectQuery(`SELECT m.channel_id, m.created_at FROM channels c\s+JOIN messages m ON m.id = \(SELECT MAX\(id\) FROM messages WHERE channel_id = c.channel_id\)`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id", "created_at"}).AddRow("ch1", at).AddRow("ch2", at.Add(time.Hour)))

	activity, err := s.store.ChannelActivity(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[string]time.Time{"ch1": at, "ch2": at.Add(time.Hour)}, activity)
}

func (s *StoreSuite) TestChannelActivityErrors() {
	tests := []struct {
		name  string
		setup func()
	}{
		{name: "query", setup: func() {
			s.mock.ExpectQuery(`SELECT m.channel_id`).WillReturnError(sql.ErrConnDone)
		}},
		{name: "scan", setup: func() {
			s.mock.ExpectQuery(`SELECT m.channel_id`).WillReturnRows(sqlmock.NewRows([]string{"channel_id", "created_at"}).AddRow("ch1", "not-a-time"))
		}},
		{name: "rows", setup: func() {
			s.mock.ExpectQuery(`SELECT m.channel_id`).WillReturnRows(sqlmock.NewRows([]string{"channel_id", "created_at"}).AddRow("ch1", time.Now()).RowError(0, sql.ErrConnDone))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			tc.setup()
			activity, err := s.store.ChannelActivity(context.Background())
			require.Error(s.T(), err)
			require.Nil(s.T(), activity)
		})
	}
}

func (s *StoreSuite) TestListChannelsErrors() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels ORDER BY name ASC`).WillReturnError(sql.ErrConnDone)
	channels, err := s.store.ListChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)

	s.mock.ExpectQuery(`SELECT .+ FROM channels ORDER BY name ASC`).WillReturnRows(
		newMockChannelRows().AddRow("not-an-int", "ch1", "g1", "test", "/home/user/project", "", "", 1, "sess-1", "", 0, "", 0, "", "", 0, 0, "", "", "", "", "", time.Now().UTC(), time.Now().UTC()))
	channels, err = s.store.ListChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)
}
