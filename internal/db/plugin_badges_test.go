package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestPluginBadges sets, replaces, lists and deletes badges against a real
// SQLite; removing a plugin drops its badges.
func (s *IntegrationSuite) TestPluginBadges() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	none, err := store.ListPluginBadges(ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), none)

	require.NoError(s.T(), store.SetPluginBadge(ctx, &PluginBadge{PluginID: "quiz", ChannelID: "ch2", Text: "3"}))
	require.NoError(s.T(), store.SetPluginBadge(ctx, &PluginBadge{PluginID: "quiz", ChannelID: "ch1", Text: "1"}))
	require.NoError(s.T(), store.SetPluginBadge(ctx, &PluginBadge{PluginID: "echo", ChannelID: "ch1", Text: "on"}))
	now = now.Add(time.Hour)
	require.NoError(s.T(), store.SetPluginBadge(ctx, &PluginBadge{PluginID: "quiz", ChannelID: "ch1", Text: "2", Title: "2 quizzes due"}))

	got, err := store.ListPluginBadges(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*PluginBadge{
		{PluginID: "echo", ChannelID: "ch1", Text: "on", UpdatedAt: now.Add(-time.Hour)},
		{PluginID: "quiz", ChannelID: "ch1", Text: "2", Title: "2 quizzes due", UpdatedAt: now},
		{PluginID: "quiz", ChannelID: "ch2", Text: "3", UpdatedAt: now.Add(-time.Hour)},
	}, got)

	require.NoError(s.T(), store.DeletePluginBadge(ctx, "quiz", "ch2"))
	require.NoError(s.T(), store.DeletePluginBadge(ctx, "quiz", "never"))
	require.NoError(s.T(), store.DeletePluginState(ctx, "echo"))
	got, err = store.ListPluginBadges(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*PluginBadge{{PluginID: "quiz", ChannelID: "ch1", Text: "2", Title: "2 quizzes due", UpdatedAt: now}}, got)
}

func (s *StoreSuite) TestPluginBadgesErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"plugin_id", "channel_id", "text", "title", "updated_at"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"set", false, func() error {
			s.mock.ExpectExec(`INSERT INTO plugin_badges`).WillReturnError(boom)
			return s.store.SetPluginBadge(ctx, &PluginBadge{})
		}},
		{"delete", false, func() error {
			s.mock.ExpectExec(`DELETE FROM plugin_badges`).WillReturnError(boom)
			return s.store.DeletePluginBadge(ctx, "x", "ch")
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_badges`).WillReturnError(boom)
			_, err := s.store.ListPluginBadges(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM plugin_badges`).WillReturnRows(sqlmock.NewRows(cols).AddRow("x", "ch", "1", "", "not a time"))
			_, err := s.store.ListPluginBadges(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_badges`).WillReturnRows(sqlmock.NewRows(cols).AddRow("x", "ch", "1", "", time.Now()).RowError(0, boom))
			_, err := s.store.ListPluginBadges(ctx)
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
