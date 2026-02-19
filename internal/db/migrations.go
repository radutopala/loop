package db

import (
	"context"
	"database/sql"
	"fmt"
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
