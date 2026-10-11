package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestPluginRuns stores, marks, lists and deletes plugin runs against a
// real SQLite, listing each with whether its message still waits to run.
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
	require.NoError(s.T(), store.MarkPluginRunDone(ctx, "ran"))
	require.NoError(s.T(), store.MarkPluginRunDone(ctx, "missing"))

	runs, err = store.ListPluginRuns(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 3)
	queued.CreatedAt, queued.Queued = now.Add(-2*time.Minute), true
	require.Equal(s.T(), queued, runs[0])
	require.Equal(s.T(), "ran", runs[1].RunID)
	require.False(s.T(), runs[1].Queued, "its message ran")
	require.True(s.T(), runs[1].Done)
	require.Equal(s.T(), "dropped", runs[2].RunID)
	require.False(s.T(), runs[2].Queued, "its message is gone")
	require.False(s.T(), runs[2].Done)

	require.NoError(s.T(), store.DeletePluginRun(ctx, "ran"))
	require.NoError(s.T(), store.DeletePluginRun(ctx, "missing"))
	runs, err = store.ListPluginRuns(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
}

// TestCountPluginRunsSince counts a plugin's run messages in its hidden
// threads from a time on, leaving out its agent's replies, other plugins'
// runs and other channels' messages.
func (s *IntegrationSuite) TestCountPluginRunsSince() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	chatID := seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p1", ParentID: "c1", Kind: PluginKind("cogtest")}))
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "p2", ParentID: "c1", Kind: PluginKind("other")}))
	for i, m := range []*Message{
		{ChannelID: "p1", CreatedAt: t0.Add(-time.Minute)},
		{ChannelID: "p1", CreatedAt: t0},
		{ChannelID: "p1", CreatedAt: t0.Add(time.Hour)},
		{ChannelID: "p1", CreatedAt: t0.Add(time.Hour), IsBot: true},
		{ChannelID: "p2", CreatedAt: t0.Add(time.Hour)},
		{ChannelID: "c1", CreatedAt: t0.Add(time.Hour)},
	} {
		m.ChatID, m.MsgID, m.Kind = chatID, fmt.Sprintf("m%d", i), MessageKindMessage
		require.NoError(s.T(), store.InsertMessage(ctx, m))
	}

	n, err := store.CountPluginRunsSince(ctx, "cogtest", t0)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 2, n)
	n, err = store.CountPluginRunsSince(ctx, "cogtest", t0.Add(2*time.Hour))
	require.NoError(s.T(), err)
	require.Zero(s.T(), n)
}

func (s *StoreSuite) TestPluginRunsErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"run_id", "plugin_id", "agent", "read_only", "prompt", "model", "effort", "tools", "thread_id", "channel_id", "created_at", "done", "queued"}

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
		{"mark done", false, func() error {
			s.mock.ExpectExec(`UPDATE plugin_runs SET done`).WithArgs("r1").WillReturnError(boom)
			return s.store.MarkPluginRunDone(ctx, "r1")
		}},
		{"count", false, func() error {
			s.mock.ExpectQuery(`SELECT COUNT\(\*\) FROM messages`).WillReturnError(boom)
			_, err := s.store.CountPluginRunsSince(ctx, "p", time.Now())
			return err
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_runs`).WillReturnError(boom)
			_, err := s.store.ListPluginRuns(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM plugin_runs`).
				WillReturnRows(sqlmock.NewRows(cols).AddRow("r1", "p", "a", false, "", "", "", "[]", "t", "c", "not a time", false, true))
			_, err := s.store.ListPluginRuns(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM plugin_runs`).
				WillReturnRows(sqlmock.NewRows(cols).AddRow("r1", "p", "a", false, "", "", "", "[]", "t", "c", time.Now(), false, true).RowError(0, boom))
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
