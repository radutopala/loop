package db

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"strings"
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

// funcMigrationIndex returns the index of the first func migration in the migrations slice.
func funcMigrationIndex() int {
	for i, m := range migrations {
		if m.fn != nil {
			return i
		}
	}
	return -1
}

// funcMigrationName returns the runtime name of the func at migrations[i].
func funcMigrationName(i int) string {
	if migrations[i].fn == nil {
		return ""
	}
	return runtime.FuncForPC(reflect.ValueOf(migrations[i].fn).Pointer()).Name()
}

func (s *MigrationsSuite) TestRunMigrationsAllNew() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	// Expect creation of schema_migrations table
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// For each subsequent migration, expect a check + execute + record.
	// SQL migrations run inside a transaction; func migrations run outside one.
	// Func migrations are identified by their runtime name so the test stays
	// stable across migration list changes.
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		if migrations[i].fn != nil {
			name := funcMigrationName(i)
			switch {
			case strings.Contains(name, "migrateTimestampsToUTC"):
				mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
					WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
				mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
					WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))
			case strings.Contains(name, "makeBackfillDirPath"):
				mock.ExpectExec(`UPDATE channels SET dir_path`).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`UPDATE channels SET dir_path`).
					WillReturnResult(sqlmock.NewResult(0, 0))
			case strings.Contains(name, "migrateScheduledTasksAddManualType"):
				mock.ExpectExec(`PRAGMA foreign_keys=OFF`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`CREATE TABLE scheduled_tasks_new`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`INSERT INTO scheduled_tasks_new`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`DROP TABLE scheduled_tasks`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`ALTER TABLE scheduled_tasks_new RENAME`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`idx_scheduled_tasks_channel_thread`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`idx_scheduled_tasks_due`).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(sqlmock.NewResult(0, 0))
			default:
				s.T().Fatalf("unhandled func migration %d (%s) in TestRunMigrationsAllNew", i, name)
			}
			mock.ExpectExec(`INSERT INTO schema_migrations`).
				WithArgs(i).
				WillReturnResult(sqlmock.NewResult(int64(i), 1))
		} else {
			mock.ExpectBegin()
			mock.ExpectExec(`.+`).
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(`INSERT INTO schema_migrations`).
				WithArgs(i).
				WillReturnResult(sqlmock.NewResult(int64(i), 1))
			mock.ExpectCommit()
		}
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
	mock.ExpectBegin()
	mock.ExpectExec(`.+`).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

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
	mock.ExpectBegin()
	mock.ExpectExec(`.+`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs(1).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "recording migration")
}

func (s *MigrationsSuite) TestRunMigrationsBeginTxError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin().WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "beginning tx for migration")
}

func (s *MigrationsSuite) TestRunMigrationsCommitError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectExec(`.+`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "committing migration")
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

func (s *MigrationsSuite) TestRunMigrationsFuncMigrationRecordError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	fnIdx := funcMigrationIndex()
	require.Greater(s.T(), fnIdx, 0, "should have a func migration")

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Skip earlier migrations as already applied.
	for i := 1; i < fnIdx; i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(fnIdx).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	// Func migration succeeds (assume migrateTimestampsToUTC is the first one).
	name := funcMigrationName(fnIdx)
	require.Contains(s.T(), name, "migrateTimestampsToUTC", "first func migration assumption changed; update test")
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))

	// INSERT fails — covers the post-func record branch.
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs(fnIdx).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), fmt.Sprintf("recording migration %d", fnIdx))
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

func (s *MigrationsSuite) TestMigrateScheduledTasksAddManualTypePragmaError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA foreign_keys=OFF`).WillReturnError(sql.ErrConnDone)

	err = migrateScheduledTasksAddManualType(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "disabling foreign keys")
}

func (s *MigrationsSuite) TestMigrateScheduledTasksAddManualTypeRebuildError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA foreign_keys=OFF`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE TABLE scheduled_tasks_new`).WillReturnError(sql.ErrConnDone)
	// The deferred PRAGMA foreign_keys=ON still runs as the function unwinds.
	mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(sqlmock.NewResult(0, 0))

	err = migrateScheduledTasksAddManualType(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "rebuilding scheduled_tasks")
}

func (s *MigrationsSuite) TestMigrateScheduledTasksAddManualTypeRebuildsOnRealDB() {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(s.T(), err)
	defer sqlDB.Close()

	// Run all migrations up to (and including) the rebuild, then verify the
	// widened CHECK actually accepts a 'manual' row and rejects a bogus type.
	require.NoError(s.T(), RunMigrations(context.Background(), sqlDB))

	_, err = sqlDB.Exec(`INSERT INTO scheduled_tasks (channel_id, schedule, type, prompt) VALUES ('c', '', 'manual', 'p')`)
	require.NoError(s.T(), err, "manual type must satisfy the CHECK constraint")

	_, err = sqlDB.Exec(`INSERT INTO scheduled_tasks (channel_id, schedule, type, prompt) VALUES ('c', '', 'bogus', 'p')`)
	require.Error(s.T(), err, "unknown type must still violate the CHECK constraint")
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
