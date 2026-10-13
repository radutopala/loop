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

	none, err := store.GetPluginSchedule(ctx, "cogtest", "ch1", "daily")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none)
	daily := func(schedule string) *ScheduledTask {
		return &ScheduledTask{ChannelID: "ch1", Schedule: schedule, Type: TaskTypeCron, Enabled: true, NextRunAt: now, PluginID: "cogtest", PluginAction: "daily"}
	}
	require.NoError(s.T(), store.ApplyPluginSchedule(ctx, daily("0 9 * * *"), "a"))
	tasks, err := store.ListScheduledTasks(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 1)
	require.Equal(s.T(), "cogtest", tasks[0].PluginID)
	require.Equal(s.T(), "daily", tasks[0].PluginAction)
	taskID := tasks[0].ID
	have, err := store.GetPluginSchedule(ctx, "cogtest", "ch1", "daily")
	require.NoError(s.T(), err)
	require.Equal(s.T(), &PluginSchedule{Applied: "a", TaskID: taskID}, have)

	require.NoError(s.T(), store.UpdateScheduledTaskEnabled(ctx, taskID, false))
	require.NoError(s.T(), store.ApplyPluginSchedule(ctx, daily("0 10 * * *"), "b"))
	tasks, err = store.ListScheduledTasks(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 1, "an action gets one task per channel")
	require.Equal(s.T(), taskID, tasks[0].ID)
	require.Equal(s.T(), "0 10 * * *", tasks[0].Schedule)
	require.True(s.T(), tasks[0].Enabled, "applying a schedule sets the task to what it asks")
	_, err = store.InsertTaskRunLog(ctx, &TaskRunLog{TaskID: taskID, Status: RunStatusSuccess, StartedAt: now})
	require.NoError(s.T(), err)

	enabled := func() bool {
		t, err := store.GetScheduledTask(ctx, taskID)
		require.NoError(s.T(), err)
		return t.Enabled
	}
	require.NoError(s.T(), store.SetPluginTasksPaused(ctx, "cogtest", "ch1", true))
	require.False(s.T(), enabled())
	require.NoError(s.T(), store.SetPluginTasksPaused(ctx, "cogtest", "ch1", false))
	require.True(s.T(), enabled(), "a paused task comes back on")
	require.NoError(s.T(), store.UpdateScheduledTaskEnabled(ctx, taskID, false))
	require.NoError(s.T(), store.SetPluginTasksPaused(ctx, "cogtest", "ch1", true))
	require.NoError(s.T(), store.SetPluginTasksPaused(ctx, "cogtest", "ch1", false))
	require.False(s.T(), enabled(), "one the owner turned off stays off")

	require.NoError(s.T(), store.DeleteScheduledTask(ctx, taskID))
	have, err = store.GetPluginSchedule(ctx, "cogtest", "ch1", "daily")
	require.NoError(s.T(), err)
	require.Equal(s.T(), &PluginSchedule{Applied: "b"}, have, "a deleted task keeps its record")
	require.NoError(s.T(), store.ApplyPluginSchedule(ctx, daily("0 9 * * *"), "c"))
	tasks, err = store.ListScheduledTasks(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 1, "and applying makes it again")
	taskID = tasks[0].ID
	_, err = store.InsertTaskRunLog(ctx, &TaskRunLog{TaskID: taskID, Status: RunStatusSuccess, StartedAt: now})
	require.NoError(s.T(), err)
	require.NoError(s.T(), store.DeletePluginSchedule(ctx, "cogtest", "ch1", "daily"))
	tasks, err = store.ListScheduledTasks(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), tasks)
	none, err = store.GetPluginSchedule(ctx, "cogtest", "ch1", "daily")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none, "deleting drops the record too")

	require.NoError(s.T(), store.ApplyPluginSchedule(ctx, &ScheduledTask{ChannelID: "ch2", Schedule: "1h", Type: TaskTypeInterval, NextRunAt: now, PluginID: "cogtest", PluginAction: "daily"}, "d"))
	require.NoError(s.T(), store.DeletePluginState(ctx, "cogtest"))
	tasks, err = store.ListScheduledTasks(ctx, "ch2")
	require.NoError(s.T(), err)
	require.Empty(s.T(), tasks, "removing a plugin drops its tasks")
	none, err = store.GetPluginSchedule(ctx, "cogtest", "ch2", "daily")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none, "and their records, so a new install starts afresh")
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
		{"delete state", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`DELETE FROM task_run_logs`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.DeletePluginState(ctx, "x")
		}},
		{"get schedule", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_schedules p`).WillReturnError(boom)
			_, err := s.store.GetPluginSchedule(ctx, "x", "ch", "a")
			return err
		}},
		{"apply schedule's record", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO plugin_schedules`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.ApplyPluginSchedule(ctx, &ScheduledTask{}, "a")
		}},
		{"apply schedule's update", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO plugin_schedules`).WillReturnResult(sqlmock.NewResult(0, 1))
			s.mock.ExpectExec(`UPDATE scheduled_tasks`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.ApplyPluginSchedule(ctx, &ScheduledTask{}, "a")
		}},
		{"apply schedule's update rows", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO plugin_schedules`).WillReturnResult(sqlmock.NewResult(0, 1))
			s.mock.ExpectExec(`UPDATE scheduled_tasks`).WillReturnResult(sqlmock.NewErrorResult(boom))
			s.mock.ExpectRollback()
			return s.store.ApplyPluginSchedule(ctx, &ScheduledTask{}, "a")
		}},
		{"apply schedule's insert", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`INSERT INTO plugin_schedules`).WillReturnResult(sqlmock.NewResult(0, 1))
			s.mock.ExpectExec(`UPDATE scheduled_tasks`).WillReturnResult(sqlmock.NewResult(0, 0))
			s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.ApplyPluginSchedule(ctx, &ScheduledTask{}, "a")
		}},
		{"delete schedule", false, func() error {
			s.mock.ExpectBegin()
			s.mock.ExpectExec(`DELETE FROM task_run_logs`).WillReturnError(boom)
			s.mock.ExpectRollback()
			return s.store.DeletePluginSchedule(ctx, "x", "ch", "a")
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
