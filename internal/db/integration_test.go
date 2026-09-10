package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestSteerQueuedMessageOrdersAndUndelays runs the steer UPDATE against a real
// SQLite so the two things its SQL claims are checked end to end: the steered
// row outranks everything else queued, and a row that was delayed into the
// future becomes claimable now while staying visible to the delay poller
// (which ignores not_before = 0).
func (s *IntegrationSuite) TestSteerQueuedMessageOrdersAndUndelays() {
	// A file DB, not ":memory:": the store splits reads and writes across two
	// connections, and two in-memory connections are two different databases.
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()

	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "ch1")
	queue := func(msgID string, priority int, notBefore int64) {
		require.NoError(s.T(), store.InsertMessage(ctx, &Message{
			ChatID:      chatID,
			ChannelID:   "ch1",
			MsgID:       msgID,
			Content:     msgID,
			IsTriggered: true,
			Priority:    priority,
			NotBefore:   notBefore,
			Kind:        MessageKindMessage,
			CreatedAt:   time.Now(),
		}))
	}
	queue("first", 5, 0)
	queue("second", 0, 0)
	queue("delayed", 0, time.Now().Add(time.Hour).Unix())

	steered, err := store.SteerQueuedMessage(ctx, "ch1", "delayed")
	require.NoError(s.T(), err)
	require.True(s.T(), steered)

	// Due now, and still flagged as a delayed row so the poller wakes the
	// channel even when nothing is draining it.
	due, err := store.ChannelsWithDueDelayedMessages(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"ch1"}, due)

	claimed, err := store.ClaimNextPending(ctx, "ch1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), claimed)
	require.Equal(s.T(), "delayed", claimed.MsgID)
	require.Greater(s.T(), claimed.Priority, 5)
}

// TestSteerQueuedMessageSkipsClaimedRow guards the one row steering must not
// touch: the message the agent is already running. Re-prioritising it would
// hand the next claim a row that is mid-flight.
func (s *IntegrationSuite) TestSteerQueuedMessageSkipsClaimedRow() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{
		ChatID:      seedChannel(s.T(), store, "ch1"),
		ChannelID:   "ch1",
		MsgID:       "running",
		Content:     "running",
		IsTriggered: true,
		Kind:        MessageKindMessage,
		CreatedAt:   time.Now(),
	}))
	claimed, err := store.ClaimNextPending(ctx, "ch1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), claimed)

	steered, err := store.SteerQueuedMessage(ctx, "ch1", "running")
	require.NoError(s.T(), err)
	require.False(s.T(), steered)
}

// seedChannel creates the channel row a message's chat_id foreign key points
// at, returning the row id to use as ChatID.
func seedChannel(t *testing.T, store *SQLiteStore, channelID string) int64 {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.UpsertChannel(ctx, &Channel{ChannelID: channelID, Name: channelID, DirPath: "/tmp/" + channelID}))
	ch, err := store.GetChannel(ctx, channelID)
	require.NoError(t, err)
	return ch.ID
}
