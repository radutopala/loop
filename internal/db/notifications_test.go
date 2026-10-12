package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestNotifications inserts, lists, marks read and deletes notifications
// against a real SQLite; each source keeps its newest NotificationsKept,
// and removing a plugin drops its notifications.
func (s *IntegrationSuite) TestNotifications() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	none, err := store.ListNotifications(ctx, 10)
	require.NoError(s.T(), err)
	require.Empty(s.T(), none)

	run := &Notification{Kind: NotificationRun, ChannelID: "ch1", Title: "Task done", Body: "in 3s"}
	require.NoError(s.T(), store.InsertNotification(ctx, run))
	require.NotZero(s.T(), run.ID)
	require.Equal(s.T(), now, run.CreatedAt)
	card := &Notification{PluginID: "quiz", Kind: NotificationCard, ChannelID: "ch2", Card: "score", Data: `{"score":3}`}
	require.NoError(s.T(), store.InsertNotification(ctx, card))

	got, err := store.ListNotifications(ctx, 10)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*Notification{
		{ID: card.ID, PluginID: "quiz", Kind: NotificationCard, ChannelID: "ch2", Card: "score", Data: `{"score":3}`, CreatedAt: now},
		{ID: run.ID, Kind: NotificationRun, ChannelID: "ch1", Title: "Task done", Body: "in 3s", Data: "null", CreatedAt: now},
	}, got)
	got, err = store.ListNotifications(ctx, 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1, "up to the limit")

	require.NoError(s.T(), store.MarkNotificationsRead(ctx, run.ID))
	got, err = store.ListNotifications(ctx, 10)
	require.NoError(s.T(), err)
	require.False(s.T(), got[0].Read)
	require.True(s.T(), got[1].Read)

	found, err := store.DeleteNotification(ctx, run.ID)
	require.NoError(s.T(), err)
	require.True(s.T(), found)
	found, err = store.DeleteNotification(ctx, run.ID)
	require.NoError(s.T(), err)
	require.False(s.T(), found)

	found, err = store.DeleteChannelNotifications(ctx, "ch2")
	require.NoError(s.T(), err)
	require.True(s.T(), found)
	found, err = store.DeleteChannelNotifications(ctx, "ch2")
	require.NoError(s.T(), err)
	require.False(s.T(), found)

	// Each source keeps its newest NotificationsKept.
	for range NotificationsKept + 2 {
		require.NoError(s.T(), store.InsertNotification(ctx, &Notification{PluginID: "quiz", Kind: NotificationNotify}))
	}
	loop := &Notification{Kind: NotificationWorkflow}
	require.NoError(s.T(), store.InsertNotification(ctx, loop))
	got, err = store.ListNotifications(ctx, 1000)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, NotificationsKept+1)
	require.Equal(s.T(), loop.ID, got[0].ID)

	require.NoError(s.T(), store.DeletePluginState(ctx, "quiz"))
	got, err = store.ListNotifications(ctx, 1000)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1, "removing a plugin drops its notifications")

	require.NoError(s.T(), store.DeleteNotifications(ctx))
	got, err = store.ListNotifications(ctx, 10)
	require.NoError(s.T(), err)
	require.Empty(s.T(), got)
}

func (s *StoreSuite) TestNotificationsErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"id", "plugin_id", "kind", "channel_id", "title", "body", "card", "data", "read", "created_at"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"insert", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO notifications`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.InsertNotification(ctx, &Notification{})
		}},
		{"insert id", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO notifications`).WillReturnResult(sqlmock.NewErrorResult(boom))
			s.mock.ExpectRollback()
			return s.store.InsertNotification(ctx, &Notification{})
		}},
		{"insert prune", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO notifications`).WillReturnResult(sqlmock.NewResult(1, 1))
			s.mock.ExpectExec(`DELETE FROM notifications`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.InsertNotification(ctx, &Notification{})
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM notifications`).WillReturnError(boom)
			_, err := s.store.ListNotifications(ctx, 10)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM notifications`).WillReturnRows(sqlmock.NewRows(cols).AddRow(1, "", "run", "", "", "", "", "null", false, "not a time"))
			_, err := s.store.ListNotifications(ctx, 10)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM notifications`).WillReturnRows(sqlmock.NewRows(cols).AddRow(1, "", "run", "", "", "", "", "null", false, time.Now()).RowError(0, boom))
			_, err := s.store.ListNotifications(ctx, 10)
			return err
		}},
		{"mark read", false, func() error {
			s.mock.ExpectExec(`UPDATE notifications SET read`).WillReturnError(boom)
			return s.store.MarkNotificationsRead(ctx, 1)
		}},
		{"delete", false, func() error {
			s.mock.ExpectExec(`DELETE FROM notifications WHERE id`).WillReturnError(boom)
			_, err := s.store.DeleteNotification(ctx, 1)
			return err
		}},
		{"delete count", false, func() error {
			s.mock.ExpectExec(`DELETE FROM notifications WHERE id`).WillReturnResult(sqlmock.NewErrorResult(boom))
			_, err := s.store.DeleteNotification(ctx, 1)
			return err
		}},
		{"delete all", false, func() error {
			s.mock.ExpectExec(`DELETE FROM notifications`).WillReturnError(boom)
			return s.store.DeleteNotifications(ctx)
		}},
		{"delete channel", false, func() error {
			s.mock.ExpectExec(`DELETE FROM notifications WHERE channel_id`).WillReturnError(boom)
			_, err := s.store.DeleteChannelNotifications(ctx, "ch")
			return err
		}},
		{"delete channel count", false, func() error {
			s.mock.ExpectExec(`DELETE FROM notifications WHERE channel_id`).WillReturnResult(sqlmock.NewErrorResult(boom))
			_, err := s.store.DeleteChannelNotifications(ctx, "ch")
			return err
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			err := tc.run()
			if tc.scan {
				require.Error(s.T(), err)
				return
			}
			require.ErrorIs(s.T(), err, boom)
		})
	}
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
