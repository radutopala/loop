// Package fsmigrate provides a versioned, idempotent migration runner for
// state under the user's Loop home directory (~/.loop/). It mirrors the
// schema-migration pattern in internal/db: a positional slice of migrations,
// state tracked in a SQLite table, append-only with no down migrations.
package fsmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// System is the subset of filesystem operations a migration may use.
// It is satisfied by osutil.RealSystem and by the appSystem interface in
// cmd/loop, so callers do not need to wrap their existing abstractions.
type System interface {
	Stat(name string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	Remove(name string) error
	// Rename is used by atomicWriteConfig to swap a temp file into place
	// once its contents have been written. Required to make config.json
	// updates crash-safe — a SIGKILL between WriteFile's truncate and the
	// writev would otherwise leave the user with an empty config.json.
	Rename(oldpath, newpath string) error
}

// Ctx is passed to every migration's Apply function.
type Ctx struct {
	Sys     System
	LoopDir string
	Version string
}

// Migration is a single filesystem migration step.
type Migration struct {
	Description string
	Apply       func(ctx context.Context, c *Ctx) error
}

// Run executes all pending filesystem migrations against the supplied
// SQLite writer connection. State is tracked in the fs_migrations table.
// The runner is safe to call on every daemon startup: applied versions are
// skipped via a COUNT check, mirroring internal/db.RunMigrations.
func Run(ctx context.Context, sqlDB *sql.DB, c *Ctx) error {
	if _, err := sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS fs_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("creating fs_migrations table: %w", err)
	}

	for i := 1; i < len(migrations); i++ {
		version := i
		var count int
		err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM fs_migrations WHERE version = ?", version).Scan(&count)
		if err != nil {
			return fmt.Errorf("checking fs migration version %d: %w", version, err)
		}
		if count > 0 {
			continue
		}

		m := migrations[i]
		if err := m.Apply(ctx, c); err != nil {
			return fmt.Errorf("applying fs migration %d (%s): %w", version, m.Description, err)
		}

		if _, err := sqlDB.ExecContext(ctx, "INSERT INTO fs_migrations (version) VALUES (?)", version); err != nil {
			return fmt.Errorf("recording fs migration %d: %w", version, err)
		}
	}

	return nil
}
