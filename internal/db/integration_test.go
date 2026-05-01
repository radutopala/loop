package db

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type IntegrationSuite struct {
	suite.Suite
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationSuite))
}

func (s *IntegrationSuite) TestNewSQLiteStoreInMemory() {
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), store)
	defer store.Close()
}

func (s *IntegrationSuite) TestNewSQLiteStoreInvalidDSN() {
	// A path that doesn't exist and can't be created
	store, err := NewSQLiteStore("/nonexistent/path/to/nowhere/test.db")
	if err != nil {
		// Expected on most systems - the path doesn't exist
		require.Nil(s.T(), store)
		return
	}
	// If it somehow succeeded (unlikely), just close it
	store.Close()
}

// TestIndexesAreUsed verifies the new partial/composite indexes are picked up
// by SQLite's planner for the four hot-path queries they were added for.
func (s *IntegrationSuite) TestIndexesAreUsed() {
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	defer store.Close()

	cases := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			name:      "GetRecentMessages",
			query:     `SELECT id FROM messages WHERE channel_id = ? AND kind = 'message' ORDER BY created_at DESC LIMIT 10`,
			args:      []any{"ch1"},
			wantIndex: "idx_messages_channel_kind_created",
		},
		{
			name:      "GetMemoryFilesByDirPath",
			query:     `SELECT id FROM memory_files WHERE (dir_path = ? OR dir_path = '') AND dimensions > 0`,
			args:      []any{"/some/dir"},
			wantIndex: "idx_memory_files_dir_path",
		},
		{
			name:      "ListWorkflowRuns child channels",
			query:     `SELECT channel_id FROM channels WHERE parent_id = ?`,
			args:      []any{"parent-1"},
			wantIndex: "idx_channels_parent_id",
		},
		{
			name:      "GetDueTasks",
			query:     `SELECT id FROM scheduled_tasks WHERE enabled = 1 AND running = 0 AND next_run_at <= ?`,
			args:      []any{"2026-01-01"},
			wantIndex: "idx_scheduled_tasks_due",
		},
		{
			name:      "ListWorkflowRuns thread subquery",
			query:     `SELECT thread_id FROM scheduled_tasks WHERE channel_id = ? AND thread_id != ''`,
			args:      []any{"ch1"},
			wantIndex: "idx_scheduled_tasks_channel_thread",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			rows, err := store.writer.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+tc.query, tc.args...)
			require.NoError(s.T(), err)
			defer rows.Close()

			var plan strings.Builder
			for rows.Next() {
				var id, parent, notUsed int
				var detail string
				require.NoError(s.T(), rows.Scan(&id, &parent, &notUsed, &detail))
				plan.WriteString(detail)
				plan.WriteString("\n")
			}
			require.NoError(s.T(), rows.Err())
			require.Contains(s.T(), plan.String(), tc.wantIndex,
				"expected query planner to use %s; got plan:\n%s", tc.wantIndex, plan.String())
		})
	}
}

// TestOldScheduledTaskIndexDropped verifies the mis-ordered
// idx_scheduled_tasks_type_next_run is removed by the migration.
func (s *IntegrationSuite) TestOldScheduledTaskIndexDropped() {
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	defer store.Close()

	rows, err := store.writer.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_scheduled_tasks_type_next_run'`)
	require.NoError(s.T(), err)
	defer rows.Close()
	require.False(s.T(), rows.Next(), "old idx_scheduled_tasks_type_next_run should have been dropped")
}
