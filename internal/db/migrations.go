package db

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations holds all schema migration SQL statements in order.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS chats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS messages (
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
	)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_channel_id ON messages(channel_id)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_is_processed ON messages(is_processed)`,
	`CREATE TABLE IF NOT EXISTS scheduled_tasks (
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
	)`,
	`CREATE TABLE IF NOT EXISTS task_run_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		response_text TEXT NOT NULL DEFAULT '',
		error_text TEXT NOT NULL DEFAULT '',
		started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		finished_at TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES scheduled_tasks(id)
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		session_id TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS registered_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		session_id TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`,
	// Recreate messages table with FK pointing to channels instead of chats.
	`PRAGMA foreign_keys=OFF`,
	`CREATE TABLE IF NOT EXISTS messages_new (
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
	)`,
	`INSERT OR IGNORE INTO messages_new SELECT * FROM messages`,
	`DROP TABLE IF EXISTS messages`,
	`ALTER TABLE messages_new RENAME TO messages`,
	`CREATE INDEX IF NOT EXISTS idx_messages_channel_id ON messages(channel_id)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_is_processed ON messages(is_processed)`,
	`PRAGMA foreign_keys=ON`,
	`DROP TABLE IF EXISTS chats`,
	`DROP TABLE IF EXISTS sessions`,
	`DROP TABLE IF EXISTS registered_channels`,
	`ALTER TABLE channels ADD COLUMN dir_path TEXT NOT NULL DEFAULT ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_dir_path ON channels(dir_path) WHERE dir_path != ''`,
}

// RunMigrations executes all pending schema migrations.
func RunMigrations(ctx context.Context, sqlDB *sql.DB) error {
	// Ensure schema_migrations table exists (migration 0)
	if _, err := sqlDB.ExecContext(ctx, migrations[0]); err != nil {
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

		if _, err := sqlDB.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("executing migration %d: %w", version, err)
		}

		if _, err := sqlDB.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
			return fmt.Errorf("recording migration %d: %w", version, err)
		}
	}

	return nil
}
