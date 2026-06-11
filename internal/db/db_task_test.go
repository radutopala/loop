package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

// --- ScheduledTask tests ---

func (s *StoreSuite) TestCreateScheduledTask() {
	task := &ScheduledTask{
		ChannelID: "ch1", GuildID: "g1", Schedule: "*/5 * * * *",
		Type: TaskTypeCron, Prompt: "check news", Enabled: true,
		NextRunAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).
		WithArgs(task.ChannelID, task.GuildID, task.Schedule, "cron", task.Prompt, 1, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "", 0, 0, "", 0, "", "").
		WillReturnResult(sqlmock.NewResult(5, 1))

	id, err := s.store.CreateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(5), id)
	require.Equal(s.T(), int64(5), task.ID)
}

func (s *StoreSuite) TestCreateScheduledTaskErrors() {
	anyArgs := []driver.Value{sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()}
	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)
	id, err := s.store.CreateScheduledTask(context.Background(), &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)

	s.mock.ExpectExec(`INSERT INTO scheduled_tasks`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	id, err = s.store.CreateScheduledTask(context.Background(), &ScheduledTask{ChannelID: "ch1", Type: TaskTypeCron, NextRunAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)
}

func (s *StoreSuite) TestGetDueTasks() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND running = 0 AND type != 'manual' AND next_run_at`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.GetDueTasks(context.Background(), now)
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 1)
	require.Equal(s.T(), TaskTypeCron, tasks[0].Type)
	require.True(s.T(), tasks[0].Enabled)
}

func (s *StoreSuite) TestGetDueTasksErrors() {
	now := time.Now().UTC()
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND running = 0`).WithArgs(sqlmock.AnyArg()).WillReturnError(sql.ErrConnDone)
	tasks, err := s.store.GetDueTasks(context.Background(), now)
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)

	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE enabled = 1 AND running = 0`).WithArgs(sqlmock.AnyArg()).WillReturnRows(
		newMockTaskRows().AddRow("bad", "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}"))
	tasks, err = s.store.GetDueTasks(context.Background(), now)
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestUpdateScheduledTask() {
	task := &ScheduledTask{
		ID: 1, Schedule: "0 * * * *", Type: TaskTypeInterval,
		Prompt: "updated prompt", Enabled: false, NextRunAt: time.Now().UTC(),
	}
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET`).
		WithArgs(task.Schedule, "interval", task.Prompt, 0, sqlmock.AnyArg(), sqlmock.AnyArg(), 0, "", 0, "", 0, 0, "", "", task.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateScheduledTask(context.Background(), task)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateScheduledTaskError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTask(context.Background(), &ScheduledTask{ID: 1, Type: TaskTypeCron, NextRunAt: time.Now().UTC()}))
}

func (s *StoreSuite) TestDeleteScheduledTask() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectCommit()

	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestDeleteScheduledTaskErrors() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()
	err := s.store.DeleteScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM task_run_logs WHERE task_id`).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()
	err = s.store.DeleteScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListScheduledTasks() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}").
		AddRow(2, "ch1", "g1", "30m", "interval", "ping", 0, now.Add(time.Hour), now, now, "", 0, "", 0, "", 0, 0, "", "{}")
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
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE channel_id`).WithArgs("ch1", sqlmock.AnyArg()).WillReturnError(sql.ErrConnDone)
	tasks, err := s.store.ListScheduledTasks(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestListAllScheduledTasks() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}").
		AddRow(2, "ch2", "g1", "1h", "interval", "deploy", 1, now.Add(time.Hour), now, now, "", 0, "", 0, "", 0, 0, "", "{}")
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(rows)

	tasks, err := s.store.ListAllScheduledTasks(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), tasks, 2)
	require.Equal(s.T(), "ch1", tasks[0].ChannelID)
	require.Equal(s.T(), "ch2", tasks[1].ChannelID)
}

func (s *StoreSuite) TestListAllScheduledTasksError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE`).WithArgs(sqlmock.AnyArg()).WillReturnError(sql.ErrConnDone)
	tasks, err := s.store.ListAllScheduledTasks(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabled() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).WithArgs(0, sqlmock.AnyArg(), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateScheduledTaskEnabled(context.Background(), 1, false))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetScheduledTask() {
	now := time.Now().UTC()
	rows := newMockTaskRows().
		AddRow(1, "ch1", "g1", "*/5 * * * *", "cron", "check news", 1, now, now, now, "", 0, "", 0, "", 0, 0, "", "{}")
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

func (s *StoreSuite) TestGetScheduledTaskNotFoundAndError() {
	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).WithArgs(int64(1)).WillReturnError(sql.ErrNoRows)
	task, err := s.store.GetScheduledTask(context.Background(), 1)
	require.NoError(s.T(), err)
	require.Nil(s.T(), task)

	s.mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id`).WithArgs(int64(1)).WillReturnError(sql.ErrConnDone)
	task, err = s.store.GetScheduledTask(context.Background(), 1)
	require.Error(s.T(), err)
	require.Nil(s.T(), task)
}

func (s *StoreSuite) TestUpdateScheduledTaskEnabledError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET enabled`).WithArgs(1, sqlmock.AnyArg(), int64(1)).WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTaskEnabled(context.Background(), 1, true))
}

func (s *StoreSuite) TestUpdateScheduledTaskThreadID() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("thread-1", sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateScheduledTaskThreadID(context.Background(), 5, "thread-1"))
}

func (s *StoreSuite) TestUpdateScheduledTaskThreadIDError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("t", sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTaskThreadID(context.Background(), 1, "t"))
}

func (s *StoreSuite) TestLinkTaskThread() {
	ch := &Channel{ChannelID: "thread-1", GuildID: "g", Name: "n", ParentID: "ch-parent", Active: true}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(ch.ChannelID, ch.GuildID, ch.Name, "", ch.ParentID, "", "", "", 1, 0, "", 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("thread-1", sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectCommit()

	require.NoError(s.T(), s.store.LinkTaskThread(context.Background(), ch, 7, "thread-1"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestLinkTaskThreadWithPermissions() {
	perms := types.Permissions{
		Owners: types.RoleGrant{Users: []string{"U1"}},
	}
	ch := &Channel{ChannelID: "thread-2", Permissions: perms, Active: true}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs("thread-2", "", "", "", "", "", "", `{"owners":{"users":["U1"],"roles":null},"members":{"users":null,"roles":null}}`, 1, 0, "", 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("thread-2", sqlmock.AnyArg(), int64(8)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectCommit()

	require.NoError(s.T(), s.store.LinkTaskThread(context.Background(), ch, 8, "thread-2"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestLinkTaskThreadChannelInsertError() {
	ch := &Channel{ChannelID: "thread-1", Active: true}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	require.Error(s.T(), s.store.LinkTaskThread(context.Background(), ch, 7, "thread-1"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestLinkTaskThreadTaskUpdateError() {
	ch := &Channel{ChannelID: "thread-1", Active: true}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO channels`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET thread_id`).
		WithArgs("thread-1", sqlmock.AnyArg(), int64(7)).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	require.Error(s.T(), s.store.LinkTaskThread(context.Background(), ch, 7, "thread-1"))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateScheduledTaskOriginBranch() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET origin_branch`).
		WithArgs("main", sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.UpdateScheduledTaskOriginBranch(context.Background(), 5, "main"))
}

func (s *StoreSuite) TestUpdateScheduledTaskOriginBranchError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET origin_branch`).
		WithArgs("main", sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateScheduledTaskOriginBranch(context.Background(), 1, "main"))
}

func (s *StoreSuite) TestClaimScheduledTaskRunning() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 5)
	require.NoError(s.T(), err)
	require.True(s.T(), claimed)
}

func (s *StoreSuite) TestClaimScheduledTaskRunningAlreadyRunning() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 5)
	require.NoError(s.T(), err)
	require.False(s.T(), claimed)
}

func (s *StoreSuite) TestClaimScheduledTaskRunningExecError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 1)
	require.Error(s.T(), err)
	require.False(s.T(), claimed)
}

func (s *StoreSuite) TestClaimScheduledTaskRunningRowsError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 1`).
		WithArgs(sqlmock.AnyArg(), int64(1)).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	claimed, err := s.store.ClaimScheduledTaskRunning(context.Background(), 1)
	require.Error(s.T(), err)
	require.False(s.T(), claimed)
}

func (s *StoreSuite) TestReleaseScheduledTaskRunning() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(s.T(), s.store.ReleaseScheduledTaskRunning(context.Background(), 5))
}

func (s *StoreSuite) TestReleaseScheduledTaskRunningError() {
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg(), int64(1)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.ReleaseScheduledTaskRunning(context.Background(), 1))
}

func (s *StoreSuite) TestResetStaleRunningTasks() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE task_run_logs`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 3))
	s.mock.ExpectCommit()

	reset, err := s.store.ResetStaleRunningTasks(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(3), reset)
}

func (s *StoreSuite) TestResetStaleRunningTasksLogUpdateError() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE task_run_logs`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningTasks(context.Background())
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestResetStaleRunningTasksTaskUpdateError() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE task_run_logs`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningTasks(context.Background())
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestResetStaleRunningTasksRowsAffectedError() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE task_run_logs`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`UPDATE scheduled_tasks SET running = 0`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningTasks(context.Background())
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

func (s *StoreSuite) TestInsertTaskRunLogErrors() {
	anyArgs := []driver.Value{sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()}
	s.mock.ExpectExec(`INSERT INTO task_run_logs`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)
	id, err := s.store.InsertTaskRunLog(context.Background(), &TaskRunLog{TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC()})
	require.Error(s.T(), err)
	require.Equal(s.T(), int64(0), id)

	s.mock.ExpectExec(`INSERT INTO task_run_logs`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	id, err = s.store.InsertTaskRunLog(context.Background(), &TaskRunLog{TaskID: 1, Status: RunStatusRunning, StartedAt: time.Now().UTC()})
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
	s.mock.ExpectExec(`UPDATE task_run_logs SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.UpdateTaskRunLog(context.Background(), &TaskRunLog{ID: 10, Status: RunStatusFailed}))
}

func (s *StoreSuite) TestListTaskRunLogs() {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "task_id", "status", "response_text", "error_text", "started_at", "finished_at"}).
		AddRow(1, 42, "success", "ok", "", now, now.Add(time.Second)).
		AddRow(2, 42, "failed", "", "boom", now.Add(time.Minute), now.Add(time.Minute+time.Second))
	s.mock.ExpectQuery(`SELECT .+ FROM task_run_logs WHERE task_id .+ ORDER BY started_at DESC LIMIT`).
		WithArgs(int64(42), 50).
		WillReturnRows(rows)

	logs, err := s.store.ListTaskRunLogs(context.Background(), 42, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), logs, 2)
	require.Equal(s.T(), RunStatusSuccess, logs[0].Status)
	require.Equal(s.T(), "boom", logs[1].ErrorText)
}

func (s *StoreSuite) TestListTaskRunLogsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM task_run_logs WHERE task_id`).
		WithArgs(int64(42), 50).
		WillReturnError(sql.ErrConnDone)

	logs, err := s.store.ListTaskRunLogs(context.Background(), 42, 50)
	require.Error(s.T(), err)
	require.Nil(s.T(), logs)
}

func (s *StoreSuite) TestListTaskRunLogsScanError() {
	rows := sqlmock.NewRows([]string{"id", "task_id", "status", "response_text", "error_text", "started_at", "finished_at"}).
		AddRow("bad", 42, "success", "ok", "", time.Now().UTC(), time.Now().UTC())
	s.mock.ExpectQuery(`SELECT .+ FROM task_run_logs WHERE task_id`).
		WithArgs(int64(42), 50).
		WillReturnRows(rows)

	logs, err := s.store.ListTaskRunLogs(context.Background(), 42, 50)
	require.Error(s.T(), err)
	require.Nil(s.T(), logs)
}
