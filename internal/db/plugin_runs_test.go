package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestPluginRuns stores, lists and deletes plugin runs against a real
// SQLite, listing each with whether its message still waits to run.
func (s *IntegrationSuite) TestPluginRuns() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }
	chatID := seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p1", ParentID: "c1", Kind: PluginKind("cogtest")}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "p1", MsgID: "queued", IsTriggered: true, Kind: MessageKindMessage, CreatedAt: now}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "p1", MsgID: "ran", IsTriggered: true, IsProcessed: true, Kind: MessageKindMessage, CreatedAt: now}))

	runs, err := store.ListPluginRuns(ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), runs)

	queued := &PluginRun{RunID: "queued", PluginID: "cogtest", Agent: "grader", ReadOnly: true, Prompt: "grade", Model: "opus", Effort: "high", Tools: `[{"name":"save"}]`, ThreadID: "p1", ChannelID: "c1"}
	require.NoError(s.T(), store.InsertPluginRun(ctx, queued))
	now = now.Add(time.Minute)
	require.NoError(s.T(), store.InsertPluginRun(ctx, &PluginRun{RunID: "ran", PluginID: "cogtest", Agent: "grader", Prompt: "grade", Tools: "[]", ThreadID: "p1", ChannelID: "c1"}))
	now = now.Add(time.Minute)
	require.NoError(s.T(), store.InsertPluginRun(ctx, &PluginRun{RunID: "dropped", PluginID: "cogtest", Agent: "grader", Prompt: "grade", Tools: "[]", ThreadID: "p1", ChannelID: "c1"}))
	require.Error(s.T(), store.InsertPluginRun(ctx, queued), "a run id is stored once")

	runs, err = store.ListPluginRuns(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 3)
	queued.CreatedAt, queued.Queued = now.Add(-2*time.Minute), true
	require.Equal(s.T(), queued, runs[0])
	require.Equal(s.T(), "ran", runs[1].RunID)
	require.False(s.T(), runs[1].Queued, "its message ran")
	require.Equal(s.T(), "dropped", runs[2].RunID)
	require.False(s.T(), runs[2].Queued, "its message is gone")

	require.NoError(s.T(), store.DeletePluginRun(ctx, "ran"))
	require.NoError(s.T(), store.DeletePluginRun(ctx, "missing"))
	runs, err = store.ListPluginRuns(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
}

func (s *StoreSuite) TestPluginRunsErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"run_id", "plugin_id", "agent", "read_only", "prompt", "model", "effort", "tools", "thread_id", "channel_id", "created_at", "queued"}

	// A scan error is the driver's, not boom.
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"insert", false, func() error {
			s.mock.ExpectExec(`INSERT INTO plugin_runs`).WillReturnError(boom)
			return s.store.InsertPluginRun(ctx, &PluginRun{})
		}},
		{"delete", false, func() error {
			s.mock.ExpectExec(`DELETE FROM plugin_runs`).WithArgs("r1").WillReturnError(boom)
			return s.store.DeletePluginRun(ctx, "r1")
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_runs`).WillReturnError(boom)
			_, err := s.store.ListPluginRuns(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM plugin_runs`).
				WillReturnRows(sqlmock.NewRows(cols).AddRow("r1", "p", "a", false, "", "", "", "[]", "t", "c", "not a time", true))
			_, err := s.store.ListPluginRuns(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_runs`).
				WillReturnRows(sqlmock.NewRows(cols).AddRow("r1", "p", "a", false, "", "", "", "[]", "t", "c", time.Now(), true).RowError(0, boom))
			_, err := s.store.ListPluginRuns(ctx)
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
