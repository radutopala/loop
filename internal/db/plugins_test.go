package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestPlugins stores, updates, lists and deletes plugin state and plugin
// containers against a real SQLite.
func (s *IntegrationSuite) TestPlugins() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	missing, err := store.GetPluginState(ctx, "cogtest")
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)

	cog := &PluginState{PluginID: "cogtest", Image: "example.com/cogtest:1.0.0", Digest: "sha256:aa", Version: "1.0.0", API: "1", ApprovedCaps: []string{"files.read", "ui.panel"}, Status: PluginStatusInstalled}
	require.NoError(s.T(), store.UpsertPluginState(ctx, cog))
	require.NoError(s.T(), store.UpsertPluginState(ctx, &PluginState{PluginID: "echo", Image: "echo:dev", Version: "0.1.0", API: "1", Dev: true, Status: PluginStatusInstalled}))

	now = now.Add(time.Hour)
	cog.Version, cog.Digest, cog.Status, cog.VerifiedKeyID = "1.1.0", "sha256:bb", PluginStatusDisabled, "key1"
	require.NoError(s.T(), store.UpsertPluginState(ctx, cog))

	got, err := store.GetPluginState(ctx, "cogtest")
	require.NoError(s.T(), err)
	require.Equal(s.T(), &PluginState{
		PluginID: "cogtest", Image: "example.com/cogtest:1.0.0", Digest: "sha256:bb", Version: "1.1.0", API: "1",
		ApprovedCaps: []string{"files.read", "ui.panel"}, Status: PluginStatusDisabled, VerifiedKeyID: "key1",
		InstalledAt: now.Add(-time.Hour), UpdatedAt: now,
	}, got, "an upgrade keeps the install time")

	all, err := store.ListPluginStates(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), all, 2)
	require.Equal(s.T(), "echo", all[1].PluginID)
	require.True(s.T(), all[1].Dev)
	require.Equal(s.T(), []string(nil), all[1].ApprovedCaps)

	require.NoError(s.T(), store.UpsertPluginContainer(ctx, &PluginContainer{PluginID: "cogtest", ChannelID: "ch2", Status: PluginContainerStopped}))
	require.NoError(s.T(), store.UpsertPluginContainer(ctx, &PluginContainer{PluginID: "cogtest", ChannelID: "ch1", ContainerID: "c1", HostPort: 40001, Status: PluginContainerRunning}))
	require.NoError(s.T(), store.UpsertPluginContainer(ctx, &PluginContainer{PluginID: "echo", ChannelID: "ch1", ContainerID: "e1", Status: PluginContainerRunning}))
	require.NoError(s.T(), store.UpsertPluginContainer(ctx, &PluginContainer{PluginID: "cogtest", ChannelID: "ch2", ContainerID: "c2", Status: PluginContainerFailed, LastError: "unhealthy"}))

	cs, err := store.ListPluginContainers(ctx, "cogtest")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*PluginContainer{
		{PluginID: "cogtest", ChannelID: "ch1", ContainerID: "c1", HostPort: 40001, Status: PluginContainerRunning, UpdatedAt: now},
		{PluginID: "cogtest", ChannelID: "ch2", ContainerID: "c2", Status: PluginContainerFailed, LastError: "unhealthy", UpdatedAt: now},
	}, cs)
	cs, err = store.ListPluginContainers(ctx, "")
	require.NoError(s.T(), err)
	require.Len(s.T(), cs, 3)

	require.NoError(s.T(), store.DeletePluginContainer(ctx, "cogtest", "ch1"))
	cs, err = store.ListPluginContainers(ctx, "cogtest")
	require.NoError(s.T(), err)
	require.Len(s.T(), cs, 1)
	require.Equal(s.T(), "ch2", cs[0].ChannelID)

	require.NoError(s.T(), store.DeletePluginState(ctx, "cogtest"))
	got, err = store.GetPluginState(ctx, "cogtest")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
	cs, err = store.ListPluginContainers(ctx, "")
	require.NoError(s.T(), err)
	require.Len(s.T(), cs, 1, "removing a plugin drops its containers' rows")
	require.Equal(s.T(), "echo", cs[0].PluginID)
}

func (s *StoreSuite) TestPluginsErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	stateCols := []string{"plugin_id", "image", "digest", "version", "api", "approved_caps", "dev", "status", "verified_key_id", "installed_at", "updated_at"}
	containerCols := []string{"plugin_id", "channel_id", "container_id", "host_port", "status", "last_error", "updated_at"}

	// A scan error is the driver's, not boom.
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"get state", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_state WHERE`).WillReturnError(boom)
			_, err := s.store.GetPluginState(ctx, "x")
			return err
		}},
		{"list states query", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_state ORDER`).WillReturnError(boom)
			_, err := s.store.ListPluginStates(ctx)
			return err
		}},
		{"list states scan", true, func() error {
			s.mock.ExpectQuery(`FROM plugin_state ORDER`).
				WillReturnRows(sqlmock.NewRows(stateCols).AddRow("x", "i", "", "1.0.0", "1", "[]", false, "installed", "", "not a time", time.Now()))
			_, err := s.store.ListPluginStates(ctx)
			return err
		}},
		{"list states rows", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_state ORDER`).
				WillReturnRows(sqlmock.NewRows(stateCols).AddRow("x", "i", "", "1.0.0", "1", "[]", false, "installed", "", time.Now(), time.Now()).RowError(0, boom))
			_, err := s.store.ListPluginStates(ctx)
			return err
		}},
		{"delete state's containers", false, func() error {
			s.mock.ExpectExec(`DELETE FROM plugin_containers`).WillReturnError(boom)
			return s.store.DeletePluginState(ctx, "x")
		}},
		{"list containers query", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_containers`).WillReturnError(boom)
			_, err := s.store.ListPluginContainers(ctx, "x")
			return err
		}},
		{"list containers scan", true, func() error {
			s.mock.ExpectQuery(`FROM plugin_containers`).
				WillReturnRows(sqlmock.NewRows(containerCols).AddRow("x", "ch", "c", 0, "running", "", "not a time"))
			_, err := s.store.ListPluginContainers(ctx, "x")
			return err
		}},
		{"list containers rows", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_containers`).
				WillReturnRows(sqlmock.NewRows(containerCols).AddRow("x", "ch", "c", 0, "running", "", time.Now()).RowError(0, boom))
			_, err := s.store.ListPluginContainers(ctx, "x")
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
