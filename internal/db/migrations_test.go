package db

import (
	"context"
	"database/sql"
	"fmt"
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
	fnIdx := funcMigrationIndex()
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		if i == fnIdx {
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

func (s *MigrationsSuite) TestMigrationHasFuncEntry() {
	fnIdx := funcMigrationIndex()
	require.Greater(s.T(), fnIdx, 0, "should have at least one func migration")
	require.NotNil(s.T(), migrations[fnIdx].fn)
	require.Empty(s.T(), migrations[fnIdx].sql)
}
