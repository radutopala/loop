package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestPluginSwitches sets, replaces, lists and deletes switch states against
// a real SQLite; removing a plugin drops its switches.
func (s *IntegrationSuite) TestPluginSwitches() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	none, err := store.ListPluginSwitches(ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), none)

	require.NoError(s.T(), store.SetPluginSwitch(ctx, &PluginSwitch{PluginID: "mode", ChannelID: "ch2", SwitchID: "a", On: true}))
	require.NoError(s.T(), store.SetPluginSwitch(ctx, &PluginSwitch{PluginID: "mode", ChannelID: "ch1", SwitchID: "b"}))
	require.NoError(s.T(), store.SetPluginSwitch(ctx, &PluginSwitch{PluginID: "echo", ChannelID: "ch1", SwitchID: "a", On: true}))
	now = now.Add(time.Hour)
	require.NoError(s.T(), store.SetPluginSwitch(ctx, &PluginSwitch{PluginID: "mode", ChannelID: "ch1", SwitchID: "b", On: true, Status: "ok", Title: "Connected"}))

	got, err := store.ListPluginSwitches(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*PluginSwitch{
		{PluginID: "echo", ChannelID: "ch1", SwitchID: "a", On: true, UpdatedAt: now.Add(-time.Hour)},
		{PluginID: "mode", ChannelID: "ch1", SwitchID: "b", On: true, Status: "ok", Title: "Connected", UpdatedAt: now},
		{PluginID: "mode", ChannelID: "ch2", SwitchID: "a", On: true, UpdatedAt: now.Add(-time.Hour)},
	}, got)

	require.NoError(s.T(), store.DeletePluginSwitch(ctx, "mode", "ch2", "a"))
	require.NoError(s.T(), store.DeletePluginSwitch(ctx, "mode", "never", "a"))
	require.NoError(s.T(), store.DeletePluginState(ctx, "echo"))
	got, err = store.ListPluginSwitches(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*PluginSwitch{{PluginID: "mode", ChannelID: "ch1", SwitchID: "b", On: true, Status: "ok", Title: "Connected", UpdatedAt: now}}, got)
}

func (s *StoreSuite) TestPluginSwitchesErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"plugin_id", "channel_id", "switch_id", "is_on", "status", "title", "updated_at"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"set", false, func() error {
			s.mock.ExpectExec(`INSERT INTO plugin_composer_switches`).WillReturnError(boom)
			return s.store.SetPluginSwitch(ctx, &PluginSwitch{})
		}},
		{"delete", false, func() error {
			s.mock.ExpectExec(`DELETE FROM plugin_composer_switches`).WillReturnError(boom)
			return s.store.DeletePluginSwitch(ctx, "x", "ch", "a")
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_composer_switches`).WillReturnError(boom)
			_, err := s.store.ListPluginSwitches(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM plugin_composer_switches`).WillReturnRows(sqlmock.NewRows(cols).AddRow("x", "ch", "a", true, "", "", "not a time"))
			_, err := s.store.ListPluginSwitches(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_composer_switches`).WillReturnRows(sqlmock.NewRows(cols).AddRow("x", "ch", "a", true, "", "", time.Now()).RowError(0, boom))
			_, err := s.store.ListPluginSwitches(ctx)
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
