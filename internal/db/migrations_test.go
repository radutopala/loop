package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"os"

	_ "modernc.org/sqlite"
)

type MigrationsSuite struct {
	suite.Suite
}

func TestMigrationsSuite(t *testing.T) {
	suite.Run(t, new(MigrationsSuite))
}

// funcMigrationIndices returns the indices of all func migrations in the migrations slice.
func funcMigrationIndices() map[int]bool {
	m := make(map[int]bool)
	for i, mig := range migrations {
		if mig.fn != nil {
			m[i] = true
		}
	}
	return m
}

// funcMigrationIndex returns the index of the first func migration in the migrations slice.
func funcMigrationIndex() int {
	for i, m := range migrations {
		if m.fn != nil {
			return i
		}
	}
	return -1
}

func (s *MigrationsSuite) TestRunMigrationsAllNew() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	// Expect creation of schema_migrations table
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// For each subsequent migration, expect a check + execute + record
	fnIndices := funcMigrationIndices()
	// Find the backfill func migration (second func migration).
	backfillIdx := -1
	for i, m := range migrations {
		if m.fn != nil && i != funcMigrationIndex() {
			backfillIdx = i
		}
	}
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		if i == backfillIdx {
			// migrateBackfillDirPath: two UPDATE statements
			mock.ExpectExec(`UPDATE channels SET dir_path`).
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(`UPDATE channels SET dir_path`).
				WillReturnResult(sqlmock.NewResult(0, 0))
		} else if fnIndices[i] {
			// migrateTimestampsToUTC with empty tables
			mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
			mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))
		} else {
			mock.ExpectExec(`.+`).
				WillReturnResult(sqlmock.NewResult(0, 0))
		}

		mock.ExpectExec(`INSERT INTO schema_migrations`).
			WithArgs(i).
			WillReturnResult(sqlmock.NewResult(int64(i), 1))
	}

	err = RunMigrations(context.Background(), db)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
}

func (s *MigrationsSuite) TestRunMigrationsAlreadyApplied() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// All migrations already applied
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	err = RunMigrations(context.Background(), db)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
}

func (s *MigrationsSuite) TestRunMigrationsSchemaTableError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating schema_migrations table")
}

func (s *MigrationsSuite) TestRunMigrationsCheckVersionError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "checking migration version")
}

func (s *MigrationsSuite) TestRunMigrationsExecError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`.+`).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "executing migration")
}

func (s *MigrationsSuite) TestRunMigrationsRecordError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`.+`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs(1).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "recording migration")
}

func (s *MigrationsSuite) TestMigrationsCount() {
	// Verify we have the expected number of migrations
	require.Greater(s.T(), len(migrations), 1, "should have at least schema_migrations + 1 more migration")
}

func (s *MigrationsSuite) TestRunMigrationsFuncMigrationExecError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	fnIdx := funcMigrationIndex()
	require.Greater(s.T(), fnIdx, 0, "should have a func migration")

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// All migrations before the func migration are already applied
	for i := 1; i < fnIdx; i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	// Func migration not yet applied
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(fnIdx).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// The func queries scheduled_tasks — make it fail
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), fmt.Sprintf("executing migration %d", fnIdx))
}

func (s *MigrationsSuite) TestMigrateBackfillDirPath() {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(s.T(), err)
	defer sqlDB.Close()

	_, err = sqlDB.Exec(`CREATE TABLE channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id TEXT NOT NULL UNIQUE,
		guild_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		dir_path TEXT NOT NULL DEFAULT '',
		parent_id TEXT NOT NULL DEFAULT '',
		platform TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		permissions TEXT NOT NULL DEFAULT '',
		worktree INTEGER NOT NULL DEFAULT 0,
		active INTEGER NOT NULL DEFAULT 1,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(s.T(), err)

	// Insert a top-level channel with empty dir_path.
	_, err = sqlDB.Exec(`INSERT INTO channels (channel_id, name) VALUES ('ch-top', 'top')`)
	require.NoError(s.T(), err)
	// Insert a thread with empty dir_path.
	_, err = sqlDB.Exec(`INSERT INTO channels (channel_id, name, parent_id) VALUES ('ch-thread', 'thread', 'ch-top')`)
	require.NoError(s.T(), err)
	// Insert a channel that already has dir_path (should not be touched).
	_, err = sqlDB.Exec(`INSERT INTO channels (channel_id, name, dir_path) VALUES ('ch-ok', 'ok', '/existing/path')`)
	require.NoError(s.T(), err)

	migrate := makeBackfillDirPath(os.UserHomeDir)
	err = migrate(context.Background(), sqlDB)
	require.NoError(s.T(), err)

	// Top-level channel should have dir_path set.
	var topDir string
	err = sqlDB.QueryRow(`SELECT dir_path FROM channels WHERE channel_id = 'ch-top'`).Scan(&topDir)
	require.NoError(s.T(), err)
	require.Contains(s.T(), topDir, "ch-top/work")
	require.Contains(s.T(), topDir, ".loop")

	// Thread should inherit parent's dir_path.
	var threadDir string
	err = sqlDB.QueryRow(`SELECT dir_path FROM channels WHERE channel_id = 'ch-thread'`).Scan(&threadDir)
	require.NoError(s.T(), err)
	require.Equal(s.T(), topDir, threadDir)

	// Pre-existing dir_path should be unchanged.
	var okDir string
	err = sqlDB.QueryRow(`SELECT dir_path FROM channels WHERE channel_id = 'ch-ok'`).Scan(&okDir)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/existing/path", okDir)
}

func (s *MigrationsSuite) TestMigrateBackfillDirPathChannelUpdateError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	migrate := makeBackfillDirPath(func() (string, error) { return "/home/test", nil })
	mock.ExpectExec(`UPDATE channels SET dir_path`).WillReturnError(sql.ErrConnDone)

	err = migrate(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "backfilling channel dir_path")
}

func (s *MigrationsSuite) TestMigrateBackfillDirPathThreadUpdateError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	migrate := makeBackfillDirPath(func() (string, error) { return "/home/test", nil })
	mock.ExpectExec(`UPDATE channels SET dir_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE channels SET dir_path`).WillReturnError(sql.ErrConnDone)

	err = migrate(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "backfilling thread dir_path")
}

func (s *MigrationsSuite) TestMigrateBackfillDirPathHomeDirError() {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(s.T(), err)
	defer sqlDB.Close()

	migrate := makeBackfillDirPath(func() (string, error) {
		return "", fmt.Errorf("no home")
	})
	err = migrate(context.Background(), sqlDB)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home dir")
}

func (s *MigrationsSuite) TestMigrationHasFuncEntry() {
	fnIdx := funcMigrationIndex()
	require.Greater(s.T(), fnIdx, 0, "should have at least one func migration")
	require.NotNil(s.T(), migrations[fnIdx].fn)
	require.Empty(s.T(), migrations[fnIdx].sql)
}
