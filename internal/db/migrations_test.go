package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MigrationsSuite struct {
	suite.Suite
}

func TestMigrationsSuite(t *testing.T) {
	suite.Run(t, new(MigrationsSuite))
}

func (s *MigrationsSuite) TestRunMigrationsAllNew() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	// Expect creation of schema_migrations table
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// For each subsequent migration, expect a check + execute + record
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec(`.+`).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`INSERT INTO schema_migrations`).
			WithArgs(i).
			WillReturnResult(sqlmock.NewResult(int64(i), 1))
	}

	// Data migration: migrateTimestampsToUTC at version len(migrations)
	dataMigrationVersion := len(migrations)
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(dataMigrationVersion).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// Empty scheduled_tasks
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	// Empty task_run_logs
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs(dataMigrationVersion).
		WillReturnResult(sqlmock.NewResult(int64(dataMigrationVersion), 1))

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

	// Data migration also already applied
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(len(migrations)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

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

// expectAllSQLMigrationsApplied sets up mock expectations for all SQL migrations as already applied.
func expectAllSQLMigrationsApplied(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}
}

func (s *MigrationsSuite) TestRunMigrationsDataMigrationCheckError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	expectAllSQLMigrationsApplied(mock)

	// Data migration version check fails
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(len(migrations)).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "checking migration version")
}

func (s *MigrationsSuite) TestRunMigrationsDataMigrationExecError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	expectAllSQLMigrationsApplied(mock)

	// Data migration version not yet applied
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(len(migrations)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// Data migration function queries scheduled_tasks - make it fail
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "executing data migration")
}

func (s *MigrationsSuite) TestRunMigrationsDataMigrationRecordError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	expectAllSQLMigrationsApplied(mock)

	// Data migration version not yet applied
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(len(migrations)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// Empty tables — migration succeeds
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))
	// Recording fails
	mock.ExpectExec(`INSERT INTO schema_migrations`).
		WithArgs(len(migrations)).
		WillReturnError(sql.ErrConnDone)

	err = RunMigrations(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "recording migration")
}
