package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestNotifications inserts, lists, counts, marks read and deletes a
// channel's notifications against a real SQLite; each source keeps its newest NotificationsKept,
// and removing a plugin drops its notifications.
func (s *IntegrationSuite) TestNotifications() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	none, err := store.ListNotifications(ctx, "ch1", 10)
	require.NoError(s.T(), err)
	require.Empty(s.T(), none)
	counts, err := store.UnreadNotificationCounts(ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), counts)

	run := &Notification{Kind: NotificationRun, ChannelID: "ch1", Title: "Task done", Body: "in 3s"}
	require.NoError(s.T(), store.InsertNotification(ctx, run))
	require.NotZero(s.T(), run.ID)
	require.Equal(s.T(), now, run.CreatedAt)
	card := &Notification{PluginID: "quiz", Kind: NotificationCard, ChannelID: "ch1", Card: "score", Data: `{"score":3}`}
	require.NoError(s.T(), store.InsertNotification(ctx, card))
	other := &Notification{Kind: NotificationWorkflow, ChannelID: "ch2", Title: "Workflow w completed"}
	require.NoError(s.T(), store.InsertNotification(ctx, other))

	got, err := store.ListNotifications(ctx, "ch1", 10)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*Notification{
		{ID: card.ID, PluginID: "quiz", Kind: NotificationCard, ChannelID: "ch1", Card: "score", Data: `{"score":3}`, CreatedAt: now},
		{ID: run.ID, Kind: NotificationRun, ChannelID: "ch1", Title: "Task done", Body: "in 3s", Data: "null", CreatedAt: now},
	}, got, "only the channel's, newest first")
	got, err = store.ListNotifications(ctx, "ch1", 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1, "up to the limit")
	counts, err = store.UnreadNotificationCounts(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[string]int{"ch1": 2, "ch2": 1}, counts)

	// Marking read reaches only the channel's, up to the id.
	require.NoError(s.T(), store.MarkNotificationsRead(ctx, "ch1", run.ID))
	got, err = store.ListNotifications(ctx, "ch1", 10)
	require.NoError(s.T(), err)
	require.False(s.T(), got[0].Read)
	require.True(s.T(), got[1].Read)
	require.NoError(s.T(), store.MarkNotificationsRead(ctx, "ch1", other.ID))
	counts, err = store.UnreadNotificationCounts(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[string]int{"ch2": 1}, counts)

	found, err := store.DeleteNotification(ctx, run.ID)
	require.NoError(s.T(), err)
	require.True(s.T(), found)
	found, err = store.DeleteNotification(ctx, run.ID)
	require.NoError(s.T(), err)
	require.False(s.T(), found)

	found, err = store.DeleteChannelNotifications(ctx, "ch1")
	require.NoError(s.T(), err)
	require.True(s.T(), found)
	found, err = store.DeleteChannelNotifications(ctx, "ch1")
	require.NoError(s.T(), err)
	require.False(s.T(), found)
	got, err = store.ListNotifications(ctx, "ch2", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1, "other channels keep theirs")

	// Each source keeps its newest NotificationsKept.
	for range NotificationsKept + 2 {
		require.NoError(s.T(), store.InsertNotification(ctx, &Notification{PluginID: "quiz", Kind: NotificationNotify, ChannelID: "ch1"}))
	}
	loop := &Notification{Kind: NotificationWorkflow, ChannelID: "ch1"}
	require.NoError(s.T(), store.InsertNotification(ctx, loop))
	got, err = store.ListNotifications(ctx, "ch1", 1000)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, NotificationsKept+1)
	require.Equal(s.T(), loop.ID, got[0].ID)

	require.NoError(s.T(), store.DeletePluginState(ctx, "quiz"))
	got, err = store.ListNotifications(ctx, "ch1", 1000)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1, "removing a plugin drops its notifications")
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
			_, err := s.store.ListNotifications(ctx, "ch", 10)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM notifications`).WillReturnRows(sqlmock.NewRows(cols).AddRow(1, "", "run", "", "", "", "", "null", false, "not a time"))
			_, err := s.store.ListNotifications(ctx, "ch", 10)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM notifications`).WillReturnRows(sqlmock.NewRows(cols).AddRow(1, "", "run", "", "", "", "", "null", false, time.Now()).RowError(0, boom))
			_, err := s.store.ListNotifications(ctx, "ch", 10)
			return err
		}},
		{"mark read", false, func() error {
			s.mock.ExpectExec(`UPDATE notifications SET read`).WillReturnError(boom)
			return s.store.MarkNotificationsRead(ctx, "ch", 1)
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
		{"unread query", false, func() error {
			s.mock.ExpectQuery(`SELECT channel_id, COUNT`).WillReturnError(boom)
			_, err := s.store.UnreadNotificationCounts(ctx)
			return err
		}},
		{"unread scan", true, func() error {
			s.mock.ExpectQuery(`SELECT channel_id, COUNT`).WillReturnRows(sqlmock.NewRows([]string{"channel_id", "n"}).AddRow("ch", "many"))
			_, err := s.store.UnreadNotificationCounts(ctx)
			return err
		}},
		{"unread rows", false, func() error {
			s.mock.ExpectQuery(`SELECT channel_id, COUNT`).WillReturnRows(sqlmock.NewRows([]string{"channel_id", "n"}).AddRow("ch", 1).RowError(0, boom))
			_, err := s.store.UnreadNotificationCounts(ctx)
			return err
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
