package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// --- Memory file tests ---

func (s *StoreSuite) TestUpsertMemoryFile() {
	file := &MemoryFile{
		FilePath: "/memory/test.md",
		Content:  "container cleanup", ContentHash: "abc123",
		Embedding: []byte{1, 2, 3, 4}, Dimensions: 768,
	}
	s.mock.ExpectExec(`INSERT INTO memory_files`).
		WithArgs(file.FilePath, file.ChunkIndex, file.Content, file.ContentHash, file.Embedding, file.Dimensions, file.DirPath, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertMemoryFile(context.Background(), file)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertMemoryFileError() {
	s.mock.ExpectExec(`INSERT INTO memory_files`).WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpsertMemoryFile(context.Background(), &MemoryFile{FilePath: "f", Content: "c", ContentHash: "h", Embedding: []byte{}, Dimensions: 1}))
}

func (s *StoreSuite) TestGetMemoryFilesByDirPath() {
	now := time.Now().UTC()
	rows := newMockMemoryRows().
		AddRow(1, "/m/a.md", 0, "content a", "hash-a", []byte{1, 2}, 768, "", now).
		AddRow(2, "/m/b.md", 0, "content b", "hash-b", []byte{3, 4}, 768, "", now)
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).
		WithArgs("/project").
		WillReturnRows(rows)

	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
	require.Equal(s.T(), "/m/a.md", files[0].FilePath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFilesByDirPathIncludesGlobal() {
	now := time.Now().UTC()
	rows := newMockMemoryRows().
		AddRow(1, "/a/x.md", 0, "content a", "hash-a", []byte{1, 2}, 768, "/project", now).
		AddRow(2, "/b/y.md", 0, "content b", "hash-b", []byte{3, 4}, 768, "", now)
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).
		WithArgs("/project").
		WillReturnRows(rows)

	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
	require.Equal(s.T(), "/project", files[0].DirPath)
	require.Equal(s.T(), "", files[1].DirPath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFilesByDirPathEmpty() {
	rows := newMockMemoryRows()
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).
		WithArgs("").
		WillReturnRows(rows)

	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "")
	require.NoError(s.T(), err)
	require.Empty(s.T(), files)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFilesByDirPathErrors() {
	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).WithArgs("/project").WillReturnError(sql.ErrConnDone)
	files, err := s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)

	s.mock.ExpectQuery(`SELECT .+ FROM memory_files WHERE`).WithArgs("/project").WillReturnRows(
		sqlmock.NewRows([]string{"id", "file_path"}).AddRow(1, "path")) // wrong column count
	files, err = s.store.GetMemoryFilesByDirPath(context.Background(), "/project")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)
}

func (s *StoreSuite) TestGetMemoryFileHash() {
	s.mock.ExpectQuery(`SELECT content_hash FROM memory_files WHERE file_path`).
		WithArgs("/m/a.md", "").
		WillReturnRows(sqlmock.NewRows([]string{"content_hash"}).AddRow("abc123"))

	hash, err := s.store.GetMemoryFileHash(context.Background(), "/m/a.md", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "abc123", hash)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetMemoryFileHashNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT content_hash FROM memory_files`).WithArgs("/m/a.md", "").WillReturnError(sql.ErrNoRows)
	hash, err := s.store.GetMemoryFileHash(context.Background(), "/m/a.md", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "", hash)

	s.mock.ExpectQuery(`SELECT content_hash FROM memory_files`).WithArgs("/m/a.md", "").WillReturnError(sql.ErrConnDone)
	hash, err = s.store.GetMemoryFileHash(context.Background(), "/m/a.md", "")
	require.Error(s.T(), err)
	require.Equal(s.T(), "", hash)
}

func (s *StoreSuite) TestDeleteMemoryFile() {
	s.mock.ExpectExec(`DELETE FROM memory_files WHERE file_path`).
		WithArgs("/m/a.md", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.DeleteMemoryFile(context.Background(), "/m/a.md", "")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteMemoryFileError() {
	s.mock.ExpectExec(`DELETE FROM memory_files`).WithArgs("/m/a.md", "").WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.DeleteMemoryFile(context.Background(), "/m/a.md", ""))
}

func (s *StoreSuite) TestListDistinctMemoryFilePaths() {
	rows := sqlmock.NewRows([]string{"file_path", "dir_path"}).
		AddRow("/projects/foo/memory/a.md", "/projects/foo").
		AddRow("/projects/foo/memory/b.md", "/projects/foo")
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnRows(rows)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
	require.Equal(s.T(), "/projects/foo/memory/a.md", files[0].FilePath)
	require.Equal(s.T(), "/projects/foo/memory/b.md", files[1].FilePath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListDistinctMemoryFilePathsEmpty() {
	rows := sqlmock.NewRows([]string{"file_path", "dir_path"})
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnRows(rows)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.NoError(s.T(), err)
	require.Nil(s.T(), files)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListDistinctMemoryFilePathsError() {
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnError(sql.ErrConnDone)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)
}

func (s *StoreSuite) TestListDistinctMemoryFilePathsScanError() {
	rows := sqlmock.NewRows([]string{"file_path", "dir_path"}).
		AddRow(nil, "/projects/foo") // nil file_path causes scan error
	s.mock.ExpectQuery(`SELECT DISTINCT file_path, dir_path FROM memory_files`).
		WithArgs("/projects/foo").
		WillReturnRows(rows)

	files, err := s.store.ListDistinctMemoryFilePaths(context.Background(), "/projects/foo")
	require.Error(s.T(), err)
	require.Nil(s.T(), files)
}

func (s *StoreSuite) TestNowFuncUsedInUpsertChannel() {
	fixedTime := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	s.store.nowFunc = func() time.Time { return fixedTime }

	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs("ch1", "g1", "test", "", "", "", "", "", 1, 0, fixedTime).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestNowFuncUsedInCreateScheduledTask() {
	fixedTime := time.Date(2099, 6, 15, 12, 0, 0, 0, time.UTC)
	s.store.nowFunc = func() time.Time { return fixedTime }

	task := &ScheduledTask{
		ChannelID: "ch1", Schedule: "0 9 * * *", Type: TaskTypeCron,
		Prompt: "test", Enabled: true, NextRunAt: fixedTime,
	}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs("ch1", "", "0 9 * * *", "cron", "test", 1, fixedTime, fixedTime, fixedTime, "", 0, 0, "", 0, "", "").
		WillReturnResult(sqlmock.NewResult(1, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(1), id)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
