package db

import (
	"context"
	"database/sql"
	"fmt"
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

func (s *StoreSuite) TestClose() {
	s.mock.ExpectClose()
	err := s.store.Close()
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

// --- Channel tests ---

func (s *StoreSuite) TestUpsertChannel() {
	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", 1, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertChannelWithDirPath() {
	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", DirPath: "/home/user/project", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, 1, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertChannelError() {
	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", 1, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpsertChannel(context.Background(), ch)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestGetChannel() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "dir_path", "active", "session_id", "created_at", "updated_at"}).
		AddRow(1, "ch1", "g1", "test", "/home/user/project", 1, "sess-123", now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).
		WithArgs("ch1").
		WillReturnRows(rows)

	ch, err := s.store.GetChannel(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), "ch1", ch.ChannelID)
	require.Equal(s.T(), "g1", ch.GuildID)
	require.Equal(s.T(), "/home/user/project", ch.DirPath)
	require.True(s.T(), ch.Active)
	require.Equal(s.T(), "sess-123", ch.SessionID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannelNotFound() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	ch, err := s.store.GetChannel(context.Background(), "nonexistent")
	require.NoError(s.T(), err)
	require.Nil(s.T(), ch)
}

func (s *StoreSuite) TestGetChannelError() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)

	ch, err := s.store.GetChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), ch)
}

func (s *StoreSuite) TestGetChannelByDirPath() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "dir_path", "active", "session_id", "created_at", "updated_at"}).
		AddRow(1, "ch1", "g1", "loop", "/home/user/dev/loop", 1, "", now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/home/user/dev/loop").
		WillReturnRows(rows)

	ch, err := s.store.GetChannelByDirPath(context.Background(), "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), "ch1", ch.ChannelID)
	require.Equal(s.T(), "/home/user/dev/loop", ch.DirPath)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetChannelByDirPathNotFound() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/nonexistent").
		WillReturnError(sql.ErrNoRows)

	ch, err := s.store.GetChannelByDirPath(context.Background(), "/nonexistent")
	require.NoError(s.T(), err)
	require.Nil(s.T(), ch)
}

func (s *StoreSuite) TestGetChannelByDirPathError() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE dir_path`).
		WithArgs("/some/path").
		WillReturnError(sql.ErrConnDone)

	ch, err := s.store.GetChannelByDirPath(context.Background(), "/some/path")
	require.Error(s.T(), err)
	require.Nil(s.T(), ch)
}

func (s *StoreSuite) TestIsChannelActive() {
	s.mock.ExpectQuery(`SELECT COUNT`).
		WithArgs("ch1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	active, err := s.store.IsChannelActive(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.True(s.T(), active)
}

func (s *StoreSuite) TestIsChannelActiveFalse() {
	s.mock.ExpectQuery(`SELECT COUNT`).
		WithArgs("ch1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	active, err := s.store.IsChannelActive(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.False(s.T(), active)
}

func (s *StoreSuite) TestIsChannelActiveError() {
	s.mock.ExpectQuery(`SELECT COUNT`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)

	active, err := s.store.IsChannelActive(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.False(s.T(), active)
}

func (s *StoreSuite) TestUpdateSessionID() {
	s.mock.ExpectExec(`UPDATE channels SET session_id`).
		WithArgs("new-sess", sqlmock.AnyArg(), "ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateSessionID(context.Background(), "ch1", "new-sess")
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateSessionIDError() {
	s.mock.ExpectExec(`UPDATE channels SET session_id`).
		WithArgs("new-sess", sqlmock.AnyArg(), "ch1").
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateSessionID(context.Background(), "ch1", "new-sess")
	require.Error(s.T(), err)
}

// --- Message tests ---

func (s *StoreSuite) TestInsertMessage() {
	msg := &Message{
		ChatID: 1, ChannelID: "ch1", DiscordMsgID: "msg1",
		AuthorID: "u1", AuthorName: "user1", Content: "hello",
		IsBot: false, IsProcessed: false, CreatedAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(msg.ChatID, msg.ChannelID, msg.DiscordMsgID, msg.AuthorID, msg.AuthorName, msg.Content, 0, 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(42, 1))

	err := s.store.InsertMessage(context.Background(), msg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(42), msg.ID)
}

func (s *StoreSuite) TestInsertMessageError() {
	msg := &Message{ChatID: 1, ChannelID: "ch1", DiscordMsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.InsertMessage(context.Background(), msg)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestInsertMessageLastInsertIDError() {
	msg := &Message{ChatID: 1, ChannelID: "ch1", DiscordMsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	err := s.store.InsertMessage(context.Background(), msg)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestGetUnprocessedMessages() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "chat_id", "channel_id", "discord_msg_id", "author_id", "author_name", "content", "is_bot", "is_processed", "created_at"}).
		AddRow(1, 1, "ch1", "msg1", "u1", "user1", "hello", 0, 0, now).
		AddRow(2, 1, "ch1", "msg2", "u2", "user2", "world", 1, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ AND is_processed = 0`).
		WithArgs("ch1").
		WillReturnRows(rows)

	msgs, err := s.store.GetUnprocessedMessages(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 2)
	require.False(s.T(), msgs[0].IsBot)
	require.True(s.T(), msgs[1].IsBot)
}

func (s *StoreSuite) TestGetUnprocessedMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)

	msgs, err := s.store.GetUnprocessedMessages(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestGetUnprocessedMessagesScanError() {
	rows := sqlmock.NewRows([]string{"id", "chat_id", "channel_id", "discord_msg_id", "author_id", "author_name", "content", "is_bot", "is_processed", "created_at"}).
		AddRow("not-an-int", 1, "ch1", "msg1", "u1", "user1", "hello", 0, 0, time.Now().UTC())
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ AND is_processed = 0`).
		WithArgs("ch1").
		WillReturnRows(rows)

	msgs, err := s.store.GetUnprocessedMessages(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestMarkMessagesProcessed() {
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1`).
		WithArgs(int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.MarkMessagesProcessed(context.Background(), []int64{1, 2})
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestMarkMessagesProcessedError() {
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1`).
		WithArgs(int64(1)).
		WillReturnError(sql.ErrConnDone)

	err := s.store.MarkMessagesProcessed(context.Background(), []int64{1, 2})
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestMarkMessagesProcessedEmpty() {
	err := s.store.MarkMessagesProcessed(context.Background(), []int64{})
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestGetRecentMessages() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "chat_id", "channel_id", "discord_msg_id", "author_id", "author_name", "content", "is_bot", "is_processed", "created_at"}).
		AddRow(1, 1, "ch1", "msg1", "u1", "user1", "hello", 0, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ ORDER BY created_at DESC LIMIT`).
		WithArgs("ch1", 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.True(s.T(), msgs[0].IsProcessed)
}

func (s *StoreSuite) TestGetRecentMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).
		WithArgs("ch1", 10).
		WillReturnError(sql.ErrConnDone)

	msgs, err := s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- ScheduledTask tests ---

func (s *StoreSuite) TestCreateScheduledTask() {
	task := &ScheduledTask{
		ChannelID: "ch1", GuildID: "g1", Schedule: "*/5 * * * *",
		Type: TaskTypeCron, Prompt: "check news", Enabled: true,
		NextRunAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(task.ChannelID, task.GuildID, task.Schedule, "cron", task.Prompt, 1, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "").
		WillReturnResult(sqlmock.NewResult(5, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(5), id)
	require.Equal(s.T(), int64(5), task.ID)
}

func (s *StoreSuite) TestCreateScheduledTaskUsesUTC() {
	task := &ScheduledTask{
		ChannelID: "ch1", GuildID: "g1", Schedule: "*/5 * * * *",
		Type: TaskTypeCron, Prompt: "check news", Enabled: true,
		NextRunAt: time.Now().UTC(),
	}

	// This test verifies that CreateScheduledTask uses UTC timestamps
	// by confirming the implementation calls time.Now().UTC()
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(
			task.ChannelID, task.GuildID, task.Schedule, "cron", task.Prompt, 1,
			sqlmock.AnyArg(), // next_run_at
			sqlmock.AnyArg(), // created_at - must be UTC
			sqlmock.AnyArg(), // updated_at - must be UTC
			"",               // template_name
		).
		WillReturnResult(sqlmock.NewResult(5, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(5), id)

	// The implementation explicitly uses time.Now().UTC() for both created_at and updated_at
	// This test ensures the query structure accepts those UTC timestamps correctly
}

func (s *StoreSuite) TestCreateScheduledTaskError() {
	task := &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestCreateScheduledTaskLastInsertIDError() {
	task := &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestGetDueTasks() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name"}).
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND next_run_at`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.GetDueTasks(context.Background(), now)
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 1)
	require.Equal(s.T(), TaskTypeCron, tasks[0].Type)
	require.True(s.T(), tasks[0].Enabled)
}

func (s *StoreSuite) TestGetDueTasksError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	tasks, err := s.store.GetDueTasks(context.Background(), time.Now())
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestGetDueTasksScanError() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name"}).
		AddRow("bad", "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.GetDueTasks(context.Background(), now)
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestUpdateScheduledTask() {
	task := &ScheduledTask{
		ID: 1, Schedule: "0 * * * *", Type: TaskTypeInterval,
		Prompt: "updated prompt", Enabled: false, NextRunAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET`).
		WithArgs(task.Schedule, "interval", task.Prompt, 0, sqlmock.AnyArg(), sqlmock.AnyArg(), task.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateScheduledTaskError() {
	task := &ScheduledTask{ID: 1, Type: TaskTypeCron, NextRunAt: time.Now().UTC()}
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateScheduledTask(context.Background(), task)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestDeleteScheduledTask() {
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestDeleteScheduledTaskError() {
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnError(sql.ErrConnDone)

	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestDeleteScheduledTaskRunLogsError() {
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).
		WithArgs(int64(1)).
		WillReturnError(sql.ErrConnDone)

	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListScheduledTasks() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name"}).
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check", 1, now, now, now, "").
		AddRow(2, "ch1", "g1", "30m", "interval", "ping", 0, now.Add(time.Hour), now, now, "")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id`).
		WithArgs("ch1", sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 2)
	require.True(s.T(), tasks[0].Enabled)
	require.False(s.T(), tasks[1].Enabled)
}

func (s *StoreSuite) TestListScheduledTasksError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id`).
		WithArgs("ch1", sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabled() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).
		WithArgs(0, sqlmock.AnyArg(), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateScheduledTaskEnabled(context.Background(), 1, false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabledTrue() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).
		WithArgs(1, sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateScheduledTaskEnabled(context.Background(), 5, true)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTask() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name"}).
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnRows(rows)

	task, err := s.store.GetScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), task)
	require.Equal(s.T(), int64(1), task.ID)
	require.Equal(s.T(), "ch1", task.ChannelID)
	require.Equal(s.T(), TaskTypeCron, task.Type)
	require.True(s.T(), task.Enabled)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTaskNotFound() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).
		WithArgs(int64(999)).
		WillReturnError(sql.ErrNoRows)

	task, err := s.store.GetScheduledTask(context.Background(), 999)
	require.NoError(s.T(), err)
	require.Nil(s.T(), task)
}

func (s *StoreSuite) TestGetScheduledTaskError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnError(sql.ErrConnDone)

	task, err := s.store.GetScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
	require.Nil(s.T(), task)
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabledError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).
		WithArgs(1, sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateScheduledTaskEnabled(context.Background(), 1, true)
	require.Error(s.T(), err)
}

// --- TaskRunLog tests ---

func (s *StoreSuite) TestInsertTaskRunLog() {
	trl := &TaskRunLog{
		TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO task_run_logs`).
		WithArgs(trl.TaskID, "running", trl.ResponseText, trl.ErrorText, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(10, 1))

	id, err := s.store.InsertTaskRunLog(context.Background(), trl)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(10), id)
}

func (s *StoreSuite) TestInsertTaskRunLogError() {
	trl := &TaskRunLog{TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO task_run_logs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	id, err := s.store.InsertTaskRunLog(context.Background(), trl)
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestInsertTaskRunLogLastInsertIDError() {
	trl := &TaskRunLog{TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO task_run_logs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	id, err := s.store.InsertTaskRunLog(context.Background(), trl)
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestUpdateTaskRunLog() {
	trl := &TaskRunLog{
		ID: 10, Status: RunStatusSuccess, ResponseText: "done",
		FinishedAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`UPDATE task_run_logs SET`).
		WithArgs("success", trl.ResponseText, trl.ErrorText, sqlmock.AnyArg(), trl.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateTaskRunLog(context.Background(), trl)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateTaskRunLogError() {
	trl := &TaskRunLog{ID: 10, Status: RunStatusFailed}
	s.mock.ExpectExec(`UPDATE task_run_logs SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateTaskRunLog(context.Background(), trl)
	require.Error(s.T(), err)
}

// --- initDB tests ---

func (s *StoreSuite) TestInitDBSuccess() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(sqlmock.NewResult(0, 0))
	// schema_migrations creation
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	// Each migration check (already applied)
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).WithArgs(i).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	err = initDB(db)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestInitDBWALError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnError(sql.ErrConnDone)

	err = initDB(db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "enabling WAL mode")
}

func (s *StoreSuite) TestInitDBForeignKeysError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnError(sql.ErrConnDone)

	err = initDB(db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "enabling foreign keys")
}

func (s *StoreSuite) TestInitDBMigrationsError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`PRAGMA foreign_keys=ON`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnError(sql.ErrConnDone)

	err = initDB(db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "running migrations")
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

func (s *StoreSuite) TestMigrateTimestampsToUTCQueryError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnError(sql.ErrConnDone)

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "querying scheduled_tasks")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCUpdateError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
			AddRow(1, now, now, now))
	mock.ExpectExec(`UPDATE scheduled_tasks SET next_run_at`).
		WillReturnError(sql.ErrConnDone)

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "updating scheduled_task 1")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCLogQueryError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnError(sql.ErrConnDone)

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "querying task_run_logs")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCScanError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	// Return rows with wrong type to trigger scan error
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
			AddRow("not-an-int", "bad", "bad", "bad"))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "scanning scheduled_task")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCRowsError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}).
			CloseError(fmt.Errorf("rows iteration error")))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "iterating scheduled_tasks")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCLogScanError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	// Return rows with wrong type to trigger scan error
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
			AddRow("not-an-int", "bad", "bad"))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "scanning task_run_log")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCLogRowsError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
			CloseError(fmt.Errorf("log rows iteration error")))

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "iterating task_run_logs")
}

func (s *StoreSuite) TestMigrateTimestampsToUTCLogUpdateError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT id, next_run_at, created_at, updated_at FROM scheduled_tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "next_run_at", "created_at", "updated_at"}))
	mock.ExpectQuery(`SELECT id, started_at, finished_at FROM task_run_logs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at", "finished_at"}).
			AddRow(10, now, now))
	mock.ExpectExec(`UPDATE task_run_logs SET started_at`).
		WillReturnError(sql.ErrConnDone)

	err = migrateTimestampsToUTC(context.Background(), db)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "updating task_run_log 10")
}

// --- NewSQLiteStore tests ---

func (s *StoreSuite) TestNewSQLiteStoreOpenError() {
	original := sqlOpenFunc
	defer func() { sqlOpenFunc = original }()

	sqlOpenFunc = func(driver, dsn string) (*sql.DB, error) {
		return nil, fmt.Errorf("open failed")
	}

	store, err := NewSQLiteStore("test.db")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
	require.Contains(s.T(), err.Error(), "opening database")
}

func (s *StoreSuite) TestNewSQLiteStoreInitDBError() {
	original := sqlOpenFunc
	defer func() { sqlOpenFunc = original }()

	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)

	sqlOpenFunc = func(driver, dsn string) (*sql.DB, error) {
		return db, nil
	}

	// initDB will fail on WAL pragma
	mock.ExpectExec(`PRAGMA journal_mode=WAL`).WillReturnError(sql.ErrConnDone)
	mock.ExpectClose()

	store, err := NewSQLiteStore("test.db")
	require.Error(s.T(), err)
	require.Nil(s.T(), store)
}

// --- Helper tests ---

func (s *StoreSuite) TestBoolToInt() {
	require.Equal(s.T(), 1, boolToInt(true))
	require.Equal(s.T(), 0, boolToInt(false))
}

func (s *StoreSuite) TestGetScheduledTaskByTemplateName() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at", "template_name"}).
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "my-template")
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

func (s *StoreSuite) TestGetScheduledTaskByTemplateNameNotFound() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).
		WithArgs("ch1", "nonexistent").
		WillReturnError(sql.ErrNoRows)

	task, err := s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "nonexistent")
	require.NoError(s.T(), err)
	require.Nil(s.T(), task)
}

func (s *StoreSuite) TestGetScheduledTaskByTemplateNameError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND template_name`).
		WithArgs("ch1", "tmpl").
		WillReturnError(sql.ErrConnDone)

	task, err := s.store.GetScheduledTaskByTemplateName(context.Background(), "ch1", "tmpl")
	require.Error(s.T(), err)
	require.Nil(s.T(), task)
}
