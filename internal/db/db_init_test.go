package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// --- initDB tests ---

func (s *StoreSuite) TestInitDBSuccess() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA synchronous=NORMAL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA cache_size=-32768`).WillReturnResult(sqlmock.NewResult(0, 0))
	// schema_migrations creation
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	// Each migration check (already applied)
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).WithArgs(i).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	err = initDB(db)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestInitDBErrors() {
	ok := sqlmock.NewResult(0, 0)
	cases := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantMsg string
	}{
		{"WAL error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnError(sql.ErrConnDone)
		}, "enabling WAL mode"},
		{"busy timeout error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnError(sql.ErrConnDone)
		}, "setting busy timeout"},
		{"foreign keys error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnError(sql.ErrConnDone)
		}, "enabling foreign keys"},
		{"synchronous error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA synchronous=NORMAL`).WillReturnError(sql.ErrConnDone)
		}, "setting synchronous mode"},
		{"cache size error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA synchronous=NORMAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA cache_size=-32768`).WillReturnError(sql.ErrConnDone)
		}, "setting cache size"},
		{"migrations error", func(m sqlmock.Sqlmock) {
			m.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA busy_timeout=5000`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA synchronous=NORMAL`).WillReturnResult(ok)
			m.ExpectExec(`PRAGMA cache_size=-32768`).WillReturnResult(ok)
			m.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnError(sql.ErrConnDone)
		}, "running migrations"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			db, mock, err := sqlmock.New()
			require.NoError(s.T(), err)
			defer db.Close()
			tc.setup(mock)
			err = initDB(db)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.wantMsg)
		})
	}
}

func (s *StoreSuite) TestMigrateTimestampsToUTC() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	localTime := time.Date(2026, 2, 8, 11, 0, 0, 0, time.FixedZone("EET", 2*60*60))

	// scheduled_tasks query
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
			AddRow(1, localTime, localTime, localTime))
	mock.ExpectExec(`UPDATE scheduled_tasks SET next_run_at`).
		WithArgs(localTime.UTC(), localTime.UTC(), localTime.UTC(), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// task_run_logs query
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
			AddRow(10, localTime, localTime))
	mock.ExpectExec(`UPDATE task_run_logs SET started_at`).
		WithArgs(localTime.UTC(), localTime.UTC(), int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMigrateTimestampsToUTCEmpty() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMigrateTimestampsToUTCErrors() {
	now := time.Now().UTC()
	emptyTaskRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"})
	}
	cases := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantMsg string
	}{
		{"query error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnError(sql.ErrConnDone)
		}, "querying scheduled_tasks"},
		{"update error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
					AddRow(1, now, now, now))
			m.ExpectExec(`UPDATE scheduled_tasks SET next_run_at`).WillReturnError(sql.ErrConnDone)
		}, "updating scheduled_task 1"},
		{"log query error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).WillReturnError(sql.ErrConnDone)
		}, "querying task_run_logs"},
		{"scan error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
					AddRow("not-an-int", "bad", "bad", "bad"))
		}, "scanning scheduled_task"},
		{"rows error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
					CloseError(fmt.Errorf("rows iteration error")))
		}, "iterating scheduled_tasks"},
		{"log scan error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
					AddRow("not-an-int", "bad", "bad"))
		}, "scanning task_run_log"},
		{"log rows error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
					CloseError(fmt.Errorf("log rows iteration error")))
		}, "iterating task_run_logs"},
		{"log update error", func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).WillReturnRows(emptyTaskRows())
			m.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).AddRow(10, now, now))
			m.ExpectExec(`UPDATE task_run_logs SET started_at`).WillReturnError(sql.ErrConnDone)
		}, "updating task_run_log 10"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			db, mock, err := sqlmock.New()
			require.NoError(s.T(), err)
			defer db.Close()
			tc.setup(mock)
			err = migrateTimestampsToUTC(context.Background(), db)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.wantMsg)
		})
	}
}

// --- NewSQLiteStore tests ---

func (s *StoreSuite) TestNewSQLiteStoreOpenError() {
	openFunc := func(driver, dsn string) (*sql.DB, error) {
		return nil, fmt.Errorf("open failed")
	}

	store, err := newSQLiteStoreWith(openFunc, "test.db")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
	require.Contains(s.T(), err.Error(), "opening database")
}

func (s *StoreSuite) TestNewSQLiteStoreInitDBError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)

	openFunc := func(driver, dsn string) (*sql.DB, error) {
		return db, nil
	}

	// initDB will fail on WAL pragma
	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnError(sql.ErrConnDone)
	mock.ExpectClose()

	store, err := newSQLiteStoreWith(openFunc, "test.db")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
}

func (s *StoreSuite) TestNewSQLiteStoreWithNowFunc() {
	// Exercises the nowFunc lambda set in newSQLiteStoreWith's success path.
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	defer store.Close()

	now := store.nowFunc()
	require.False(s.T(), now.IsZero())
}

func (s *StoreSuite) TestWriterDBReturnsHandle() {
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	defer store.Close()

	require.NotNil(s.T(), store.WriterDB())
}

func (s *StoreSuite) TestNewSQLiteStoreReaderOpenError() {
	callCount := 0
	openFunc := func(driver, dsn string) (*sql.DB, error) {
		callCount++
		if callCount == 1 {
			// Writer opens successfully with a real in-memory DB.
			return sql.Open(driver, dsn)
		}
		// Reader open fails.
		return nil, fmt.Errorf("reader open error")
	}
	store, err := newSQLiteStoreWith(openFunc, ":memory:")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
	require.Contains(s.T(), err.Error(), "opening reader database")
}

func (s *StoreSuite) TestSplitDB() {
	// Use a temp file so writer and reader share the same database.
	tmpFile := filepath.Join(s.T().TempDir(), "split_test.db")
	store, err := NewSQLiteStore(tmpFile)
	require.NoError(s.T(), err)
	defer store.Close()

	ctx := context.Background()

	// ExecContext via writer.
	_, err = store.db.ExecContext(ctx, `CREATE TABLE test_split (id INTEGER PRIMARY KEY, val TEXT)`)
	require.NoError(s.T(), err)
	_, err = store.db.ExecContext(ctx, `INSERT INTO test_split (val) VALUES (?)`, "hello")
	require.NoError(s.T(), err)

	// QueryRowContext via reader.
	var val string
	row := store.db.QueryRowContext(ctx, `SELECT val FROM test_split WHERE id = 1`)
	require.NoError(s.T(), row.Scan(&val))
	require.Equal(s.T(), "hello", val)

	// QueryContext via reader.
	rows, err := store.db.QueryContext(ctx, `SELECT val FROM test_split`)
	require.NoError(s.T(), err)
	defer rows.Close()
	require.True(s.T(), rows.Next())
	require.NoError(s.T(), rows.Scan(&val))
	require.Equal(s.T(), "hello", val)
}

func (s *StoreSuite) TestSplitDBCloseWriterError() {
	writerDB, writerMock, err := sqlmock.New()
	require.NoError(s.T(), err)
	readerDB, readerMock, err := sqlmock.New()
	require.NoError(s.T(), err)

	writerMock.ExpectClose().WillReturnError(sql.ErrConnDone)
	readerMock.ExpectClose()

	sdb := &splitDB{writer: writerDB, reader: readerDB}
	store := &SQLiteStore{db: sdb}
	err = store.Close()
	require.ErrorIs(s.T(), err, sql.ErrConnDone)
}

// --- Helper tests ---

func (s *StoreSuite) TestBoolToInt() {
	require.Equal(s.T(), 1, boolToInt(true))
	require.Equal(s.T(), 0, boolToInt(false))
}

func (s *StoreSuite) TestGetScheduledTaskByTemplateName() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "my-template", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).
		WithArgs("ch1", "my-template").
		WillReturnRows(rows)

	task, err := s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "my-template")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), task)
	require.Equal(s.T(), int64(1), task.ID)
	require.Equal(s.T(), "ch1", task.ChannelID)
	require.Equal(s.T(), "my-template", task.TemplateName)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTaskByTemplateNameNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).WithArgs("ch1", "tmpl").WillReturnError(sql.ErrNoRows)
	task, err := s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "tmpl")
	require.NoError(s.T(), err)
	require.Nil(s.T(), task)

	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).WithArgs("ch1", "tmpl").WillReturnError(sql.ErrConnDone)
	task, err = s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "tmpl")
	require.Error(s.T(), err)
	require.Nil(s.T(), task)
}
