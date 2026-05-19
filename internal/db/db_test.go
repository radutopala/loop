package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type StoreSuite struct {
	suite.Suite
	db    *sql.DB
	mock  sqlmock.Sqlmock
	store *SQLiteStore
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (s *StoreSuite) SetupTest() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	s.db = db
	s.mock = mock
	s.store = NewSQLiteStoreFromDB(db)
}

func (s *StoreSuite) TearDownTest() {
	s.db.Close()
}

func newMockChannelRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "dir_path", "parent_id", "platform", "active", "session_id", "permissions", "worktree", "locked", "created_at", "updated_at"})
}

func newMockTaskRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name", "auto_delete_sec", "thread_id", "worktree", "origin_branch", "update_before_run", "running", "workflow_name", "workflow_inputs"})
}

func newMockMessageRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "chat_id", "channel_id", "msg_id", "author_id", "author_name", "content", "is_bot", "is_processed", "is_triggered", "is_running", "priority", "mode", "created_at", "kind", "chain_position", "tool_use_id", "tool_name", "is_error", "trigger_msg_id"})
}

// addMessageRow appends a chat-row with default empty values for the
// timeline + processor columns, matching the pre-feature shape used
// across most existing tests.
func addMessageRow(rows *sqlmock.Rows, id, chatID int64, channelID, msgID, authorID, authorName, content string, isBot, isProcessed int, createdAt time.Time) *sqlmock.Rows {
	return rows.AddRow(id, chatID, channelID, msgID, authorID, authorName, content, isBot, isProcessed, 0, 0, 0, "", createdAt, "message", int64(0), "", "", 0, "")
}

func newMockMemoryRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "file_path", "chunk_index", "content", "content_hash", "embedding", "dimensions", "dir_path", "updated_at"})
}

func (s *StoreSuite) TestClose() {
	s.mock.ExpectClose()
	err := s.store.Close()
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestWithTxBeginError() {
	s.mock.ExpectBegin().WillReturnError(sql.ErrConnDone)
	err := s.store.withTx(context.Background(), func(_ *sql.Tx) error { return nil })
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "beginning tx")
}

func (s *StoreSuite) TestWithTxCommitError() {
	s.mock.ExpectBegin()
	s.mock.ExpectCommit().WillReturnError(sql.ErrConnDone)
	err := s.store.withTx(context.Background(), func(_ *sql.Tx) error { return nil })
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestWriterDB() {
	require.Same(s.T(), s.db, s.store.WriterDB())
}
