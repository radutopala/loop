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
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, 1, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertChannel(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertChannelError() {
	ch := &Channel{ChannelID: "ch1", GuildID: "g1", Name: "test-channel", Active: true}
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, 1, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpsertChannel(context.Background(), ch)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestGetChannel() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "active", "session_id", "created_at", "updated_at"}).
		AddRow(1, "ch1", "g1", "test", 1, "sess-123", now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE channel_id`).
		WithArgs("ch1").
		WillReturnRows(rows)

	ch, err := s.store.GetChannel(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), "ch1", ch.ChannelID)
	require.Equal(s.T(), "g1", ch.GuildID)
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

func (s *StoreSuite) TestSetChannelActive() {
	s.mock.ExpectExec(`UPDATE channels SET active`).
		WithArgs(0, sqlmock.AnyArg(), "ch1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.SetChannelActive(context.Background(), "ch1", false)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestSetChannelActiveError() {
	s.mock.ExpectExec(`UPDATE channels SET active`).
		WithArgs(1, sqlmock.AnyArg(), "ch1").
		WillReturnError(sql.ErrConnDone)

	err := s.store.SetChannelActive(context.Background(), "ch1", true)
	require.Error(s.T(), err)
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

func (s *StoreSuite) TestGetRegisteredChannels() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "active", "session_id", "created_at", "updated_at"}).
		AddRow(1, "ch1", "g1", "test1", 1, "", now, now).
		AddRow(2, "ch2", "g1", "test2", 1, "", now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE active = 1`).
		WillReturnRows(rows)

	channels, err := s.store.GetRegisteredChannels(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), channels, 2)
	require.True(s.T(), channels[0].Active)
}

func (s *StoreSuite) TestGetRegisteredChannelsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE active = 1`).
		WillReturnError(sql.ErrConnDone)

	channels, err := s.store.GetRegisteredChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)
}

func (s *StoreSuite) TestGetRegisteredChannelsScanError() {
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "name", "active", "session_id", "created_at", "updated_at"}).
		AddRow("not-an-int", "ch1", "g1", "test", 1, "", time.Now().UTC(), time.Now().UTC())
	s.mock.ExpectQuery(`SELECT .+ FROM channels WHERE active = 1`).
		WillReturnRows(rows)

	channels, err := s.store.GetRegisteredChannels(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), channels)
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
		WithArgs(task.ChannelID, task.GuildID, task.Schedule, "cron", task.Prompt, 1, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(5, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(5), id)
	require.Equal(s.T(), int64(5), task.ID)
}

func (s *StoreSuite) TestCreateScheduledTaskError() {
	task := &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestCreateScheduledTaskLastInsertIDError() {
	task := &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestGetDueTasks() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at"}).
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now)
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
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at"}).
		AddRow("bad", "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now)
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
	rows := sqlmock.NewRows([]string{"id", "channel_id", "guild_id", "schedule", "type", "prompt", "enabled", "next_run_at", "created_at", "updated_at"}).
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check", 1, now, now, now).
		AddRow(2, "ch1", "g1", "30m", "interval", "ping", 1, now.Add(time.Hour), now, now)
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND enabled = 1`).
		WithArgs("ch1").
		WillReturnRows(rows)

	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 2)
	require.True(s.T(), tasks[0].Enabled)
	require.True(s.T(), tasks[1].Enabled)
}

func (s *StoreSuite) TestListScheduledTasksError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id .+ AND enabled = 1`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)

	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
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
	// Each migration check + apply
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
