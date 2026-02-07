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
