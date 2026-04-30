package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// migration is a single schema migration step: either a SQL statement or a Go function.
type migration struct {
	sql string
	fn  func(ctx context.Context, db *sql.DB) error
}

func sqlMigration(s string) migration { return migration{sql: s} }
func funcMigration(fn func(ctx context.Context, db *sql.DB) error) migration {
	return migration{fn: fn}
}

// migrations holds all schema migrations in order.
// Migration 0 bootstraps the schema_migrations table; versions start at 1.
var migrations = []migration{
	sqlMigration(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS chats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id INTEGER NOT NULL,
		channel_id TEXT NOT NULL,
		discord_msg_id TEXT NOT NULL UNIQUE,
		author_id TEXT NOT NULL,
		author_name TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL DEFAULT '',
		is_bot INTEGER NOT NULL DEFAULT 0,
		is_processed INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (chat_id) REFERENCES chats(id)
	)`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_messages_channel_id ON messages(channel_id)`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_messages_is_processed ON messages(is_processed)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS scheduled_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL,
		guild_id TEXT NOT NULL DEFAULT '',
		schedule TEXT NOT NULL,
		type TEXT NOT NULL CHECK(type IN ('cron', 'interval', 'once')),
		prompt TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		next_run_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS task_run_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		response_text TEXT NOT NULL DEFAULT '',
		error_text TEXT NOT NULL DEFAULT '',
		started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		finished_at TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES scheduled_tasks(id)
	)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		session_id TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS registered_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		session_id TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`),
	// Recreate messages table with FK pointing to channels instead of chats.
	sqlMigration(`PRAGMA foreign_keys=OFF`),
	sqlMigration(`CREATE TABLE IF NOT EXISTS messages_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id INTEGER NOT NULL,
		channel_id TEXT NOT NULL,
		discord_msg_id TEXT NOT NULL UNIQUE,
		author_id TEXT NOT NULL DEFAULT '',
		author_name TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL DEFAULT '',
		is_bot INTEGER NOT NULL DEFAULT 0,
		is_processed INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (chat_id) REFERENCES channels(id)
	)`),
	sqlMigration(`INSERT OR IGNORE INTO messages_new SELECT * FROM messages`),
	sqlMigration(`DROP TABLE IF EXISTS messages`),
	sqlMigration(`ALTER TABLE messages_new RENAME TO messages`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_messages_channel_id ON messages(channel_id)`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_messages_is_processed ON messages(is_processed)`),
	sqlMigration(`PRAGMA foreign_keys=ON`),
	sqlMigration(`DROP TABLE IF EXISTS chats`),
	sqlMigration(`DROP TABLE IF EXISTS sessions`),
	sqlMigration(`DROP TABLE IF EXISTS registered_channels`),
	sqlMigration(`ALTER TABLE channels ADD COLUMN dir_path TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_dir_path ON channels(dir_path) WHERE dir_path != ''`),
	funcMigration(migrateTimestampsToUTC),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN template_name TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE channels ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`DROP INDEX IF EXISTS idx_channels_dir_path`),
	sqlMigration(`CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_dir_path ON channels(dir_path) WHERE dir_path != '' AND parent_id = ''`),
	sqlMigration(`ALTER TABLE channels ADD COLUMN platform TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`DROP INDEX IF EXISTS idx_channels_dir_path`),
	sqlMigration(`CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_dir_path ON channels(dir_path, platform) WHERE dir_path != '' AND parent_id = ''`),
	sqlMigration(`ALTER TABLE messages RENAME COLUMN discord_msg_id TO msg_id`),
	// Memory file storage with chunking and dir_path scoping.
	sqlMigration(`CREATE TABLE IF NOT EXISTS memory_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		chunk_index INTEGER NOT NULL DEFAULT 0,
		content TEXT NOT NULL,
		content_hash TEXT NOT NULL DEFAULT '',
		embedding BLOB,
		dimensions INTEGER NOT NULL DEFAULT 0,
		dir_path TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(file_path, chunk_index, dir_path)
	)`),
	// Per-channel RBAC permissions stored as JSON.
	sqlMigration(`ALTER TABLE channels ADD COLUMN permissions TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN auto_delete_sec INTEGER NOT NULL DEFAULT 0`),
	sqlMigration(`UPDATE messages SET author_name = 'agent' WHERE author_name = 'assistant' AND is_bot = 1`),
	sqlMigration(`ALTER TABLE channels ADD COLUMN worktree INTEGER NOT NULL DEFAULT 0`),
	funcMigration(makeBackfillDirPath(os.UserHomeDir)),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN thread_id TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`UPDATE channels SET name = REPLACE(name, '🧵 ', '') WHERE name LIKE '🧵 %' AND platform = 'local'`),
	sqlMigration(`UPDATE channels SET name = REPLACE(name, '🧵 ', '⏱ ') WHERE name LIKE '🧵 %'`),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN worktree INTEGER NOT NULL DEFAULT 0`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_type_next_run ON scheduled_tasks(type, next_run_at)`),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN origin_branch TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN update_before_run INTEGER NOT NULL DEFAULT 0`),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN running INTEGER NOT NULL DEFAULT 0`),
	// Workflow runs table.
	sqlMigration(`CREATE TABLE IF NOT EXISTS workflow_runs (
		id             TEXT PRIMARY KEY,
		workflow_name  TEXT NOT NULL,
		channel_id     TEXT NOT NULL DEFAULT '',
		dir_path       TEXT NOT NULL DEFAULT '',
		worktree_path  TEXT NOT NULL DEFAULT '',
		status         TEXT NOT NULL DEFAULT 'running',
		inputs         TEXT NOT NULL DEFAULT '{}',
		paused_node_id TEXT NOT NULL DEFAULT '',
		error_text     TEXT NOT NULL DEFAULT '',
		started_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		finished_at    TIMESTAMP
	)`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_workflow_runs_channel_id ON workflow_runs(channel_id)`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status)`),
	// Workflow node runs table.
	sqlMigration(`CREATE TABLE IF NOT EXISTS workflow_node_runs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id      TEXT NOT NULL,
		node_id     TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending',
		output      TEXT NOT NULL DEFAULT '',
		error_text  TEXT NOT NULL DEFAULT '',
		attempt     INTEGER NOT NULL DEFAULT 1,
		started_at  TIMESTAMP,
		finished_at TIMESTAMP,
		FOREIGN KEY (run_id) REFERENCES workflow_runs(id),
		UNIQUE(run_id, node_id)
	)`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_workflow_node_runs_run_id ON workflow_node_runs(run_id)`),
	// Heartbeat tracking for running workflow nodes.
	sqlMigration(`ALTER TABLE workflow_node_runs ADD COLUMN last_heartbeat_at TIMESTAMP`),
	// Version-pin the workflow definition at run start time.
	sqlMigration(`ALTER TABLE workflow_runs ADD COLUMN workflow_def TEXT NOT NULL DEFAULT ''`),
	// Scheduled workflow runs: a task can start a workflow instead of an agent prompt.
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN workflow_name TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE scheduled_tasks ADD COLUMN workflow_inputs TEXT NOT NULL DEFAULT '{}'`),
	// Timeline support: agent events (thinking, tool_use, tool_result) live in the
	// messages table next to chat rows, distinguished by `kind`. Content is stored
	// inline; tool_name labels tool_use rows; is_error flags failed tool_result rows.
	sqlMigration(`ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'message'`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN event_uuid TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN parent_uuid TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN chain_position INTEGER NOT NULL DEFAULT 0`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN tool_use_id TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN tool_name TEXT NOT NULL DEFAULT ''`),
	sqlMigration(`ALTER TABLE messages ADD COLUMN is_error INTEGER NOT NULL DEFAULT 0`),
	sqlMigration(`CREATE INDEX IF NOT EXISTS idx_messages_chain ON messages(channel_id, chain_position)`),
	// UNIQUE event_uuid only for agent-event rows; chat rows can share a uuid
	// (queued user messages bundled into a single JSONL entry).
	sqlMigration(`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_event_uuid ON messages(event_uuid) WHERE event_uuid != '' AND kind != 'message'`),
}

// RunMigrations executes all pending schema migrations.
func RunMigrations(ctx context.Context, sqlDB *sql.DB) error {
	// Ensure schema_migrations table exists (migration 0)
	if _, err := sqlDB.ExecContext(ctx, migrations[0].sql); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	for i := 1; i < len(migrations); i++ {
		version := i
		var count int
		err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&count)
		if err != nil {
			return fmt.Errorf("checking migration version %d: %w", version, err)
		}
		if count > 0 {
			continue
		}

		m := migrations[i]
		if m.fn != nil {
			if err := m.fn(ctx, sqlDB); err != nil {
				return fmt.Errorf("executing migration %d: %w", version, err)
			}
		} else {
			if _, err := sqlDB.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("executing migration %d: %w", version, err)
			}
		}

		if _, err := sqlDB.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
			return fmt.Errorf("recording migration %d: %w", version, err)
		}
	}

	return nil
}

// migrateTimestampsToUTC rewrites all scheduled_tasks and task_run_logs timestamps to UTC.
func migrateTimestampsToUTC(ctx context.Context, sqlDB *sql.DB) error {
	// Migrate scheduled_tasks
	rows, err := sqlDB.QueryContext(ctx, `SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`)
	if err != nil {
		return fmt.Errorf("querying scheduled_tasks: %w", err)
	}
	defer rows.Close()

	type taskRow struct {
		id        int64
		nextRunAt time.Time
		createdAt time.Time
		updatedAt time.Time
	}
	var taskRows []taskRow
	for rows.Next() {
		var r taskRow
		if err := rows.Scan(&r.id, &r.nextRunAt, &r.createdAt, &r.updatedAt); err != nil {
			return fmt.Errorf("scanning scheduled_task: %w", err)
		}
		taskRows = append(taskRows, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating scheduled_tasks: %w", err)
	}

	for _, r := range taskRows {
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE scheduled_tasks SET next_run_at = ?, created_at = ?, updated_at = ? WHERE id = ?`,
			r.nextRunAt.UTC(), r.createdAt.UTC(), r.updatedAt.UTC(), r.id,
		); err != nil {
			return fmt.Errorf("updating scheduled_task %d: %w", r.id, err)
		}
	}

	// Migrate task_run_logs
	logRows, err := sqlDB.QueryContext(ctx, `SELECT id, started_at, finished_at FROM task_run_logs`)
	if err != nil {
		return fmt.Errorf("querying task_run_logs: %w", err)
	}
	defer logRows.Close()

	type logRow struct {
		id         int64
		startedAt  time.Time
		finishedAt time.Time
	}
	var logRowList []logRow
	for logRows.Next() {
		var r logRow
		if err := logRows.Scan(&r.id, &r.startedAt, &r.finishedAt); err != nil {
			return fmt.Errorf("scanning task_run_log: %w", err)
		}
		logRowList = append(logRowList, r)
	}
	if err := logRows.Err(); err != nil {
		return fmt.Errorf("iterating task_run_logs: %w", err)
	}

	for _, r := range logRowList {
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE task_run_logs SET started_at = ?, finished_at = ? WHERE id = ?`,
			r.startedAt.UTC(), r.finishedAt.UTC(), r.id,
		); err != nil {
			return fmt.Errorf("updating task_run_log %d: %w", r.id, err)
		}
	}

	return nil
}

// makeBackfillDirPath returns a migration function that sets dir_path on
// channels/threads created before dir_path was set at creation time.
// Top-level channels get ~/.loop/{channelID}/work; threads inherit parent's dir_path.
func makeBackfillDirPath(userHomeDir func() (string, error)) func(context.Context, *sql.DB) error {
	return func(ctx context.Context, sqlDB *sql.DB) error {
		home, err := userHomeDir()
		if err != nil {
			return fmt.Errorf("getting home dir: %w", err)
		}
		loopDir := filepath.Join(home, ".loop")

		// Backfill top-level channels.
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE channels SET dir_path = ? || '/' || channel_id || '/work' WHERE dir_path = '' AND parent_id = ''`,
			loopDir,
		); err != nil {
			return fmt.Errorf("backfilling channel dir_path: %w", err)
		}

		// Backfill threads from their parent's dir_path.
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE channels SET dir_path = (SELECT p.dir_path FROM channels p WHERE p.channel_id = channels.parent_id) WHERE dir_path = '' AND parent_id != ''`,
		); err != nil {
			return fmt.Errorf("backfilling thread dir_path: %w", err)
		}

		return nil
	}
}
