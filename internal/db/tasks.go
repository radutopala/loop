// tasks.go holds SQLiteStore methods for scheduled_tasks and task_run_logs.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *SQLiteStore) CreateScheduledTask(ctx context.Context, task *ScheduledTask) (int64, error) {
	now := s.nowFunc()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec, worktree, origin_branch, update_before_run, workflow_name, workflow_inputs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ChannelID, task.GuildID, task.Schedule, string(task.Type), task.Prompt, boolToInt(task.Enabled), task.NextRunAt, now, now, task.TemplateName, task.AutoDeleteSec, boolToInt(task.Worktree), task.OriginBranch, boolToInt(task.UpdateBeforeRun), task.WorkflowName, task.WorkflowInputs,
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	task.ID = id
	return id, nil
}

func (s *SQLiteStore) GetDueTasks(ctx context.Context, now time.Time) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE enabled = 1 AND running = 0 AND next_run_at <= ?`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *SQLiteStore) UpdateScheduledTask(ctx context.Context, task *ScheduledTask) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET schedule = ?, type = ?, prompt = ?, enabled = ?, next_run_at = ?, updated_at = ?, auto_delete_sec = ?, thread_id = ?, worktree = ?, origin_branch = ?, update_before_run = ?, running = ?, workflow_name = ?, workflow_inputs = ? WHERE id = ?`,
		task.Schedule, string(task.Type), task.Prompt, boolToInt(task.Enabled), task.NextRunAt, s.nowFunc(), task.AutoDeleteSec, task.ThreadID, boolToInt(task.Worktree), task.OriginBranch, boolToInt(task.UpdateBeforeRun), boolToInt(task.Running), task.WorkflowName, task.WorkflowInputs, task.ID,
	)
	return err
}

func (s *SQLiteStore) UpdateScheduledTaskThreadID(ctx context.Context, id int64, threadID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET thread_id = ?, updated_at = ? WHERE id = ?`,
		threadID, s.nowFunc(), id,
	)
	return err
}

// LinkTaskThread atomically registers a thread channel and stamps its ID on
// a recurring scheduled task. Used the first time a task creates a thread so
// a crash mid-update can never leave the scheduled task referencing a thread
// channel that doesn't exist (or vice versa: a thread row no task points to,
// causing the next run to spawn another thread and leak the old row).
func (s *SQLiteStore) LinkTaskThread(ctx context.Context, ch *Channel, taskID int64, threadID string) error {
	var permStr string
	if !ch.Permissions.IsEmpty() {
		data, _ := json.Marshal(ch.Permissions)
		permStr = string(data)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, session_id, permissions, active, worktree, locked, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(channel_id) DO UPDATE SET
			   guild_id = excluded.guild_id,
			   name = excluded.name,
			   dir_path = CASE WHEN excluded.dir_path != '' THEN excluded.dir_path ELSE channels.dir_path END,
			   parent_id = excluded.parent_id,
			   platform = CASE WHEN excluded.platform != '' THEN excluded.platform ELSE channels.platform END,
			   session_id = CASE WHEN excluded.session_id != '' THEN excluded.session_id ELSE channels.session_id END,
			   permissions = CASE WHEN excluded.permissions != '' THEN excluded.permissions ELSE channels.permissions END,
			   active = excluded.active,
			   worktree = excluded.worktree,
			   updated_at = excluded.updated_at`,
			ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.SessionID, permStr, boolToInt(ch.Active), boolToInt(ch.Worktree), boolToInt(ch.Locked), s.nowFunc(),
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE scheduled_tasks SET thread_id = ?, updated_at = ? WHERE id = ?`,
			threadID, s.nowFunc(), taskID,
		)
		return err
	})
}

func (s *SQLiteStore) UpdateScheduledTaskOriginBranch(ctx context.Context, id int64, branch string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET origin_branch = ?, updated_at = ? WHERE id = ?`,
		branch, s.nowFunc(), id,
	)
	return err
}

func (s *SQLiteStore) ClaimScheduledTaskRunning(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET running = 1, updated_at = ? WHERE id = ? AND running = 0`,
		s.nowFunc(), id,
	)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *SQLiteStore) ReleaseScheduledTaskRunning(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET running = 0, updated_at = ? WHERE id = ?`,
		s.nowFunc(), id,
	)
	return err
}

// ResetStaleRunningTasks clears `running=1` on any scheduled_tasks row and
// finalizes any orphaned `task_run_logs` row whose run never completed (e.g.
// because the daemon was killed before its defer-based release could fire).
// Returns the number of tasks whose running flag was cleared. Safe to call
// at daemon startup — by that point no task is genuinely in flight, since
// task execution is owned by this same process.
func (s *SQLiteStore) ResetStaleRunningTasks(ctx context.Context) (int64, error) {
	var reset int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		now := s.nowFunc()
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_run_logs
			    SET status = 'failed',
			        finished_at = ?,
			        error_text = CASE WHEN error_text = '' THEN 'daemon restarted while running' ELSE error_text END
			  WHERE status = 'running' AND finished_at IS NULL`,
			now,
		); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE scheduled_tasks SET running = 0, updated_at = ? WHERE running = 1`,
			now,
		)
		if err != nil {
			return err
		}
		reset, err = result.RowsAffected()
		return err
	})
	return reset, err
}

func (s *SQLiteStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_run_logs WHERE task_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = ?`, id); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE channel_id = ? AND (type != 'once' OR next_run_at > ?) ORDER BY next_run_at ASC`,
		channelID, s.nowFunc(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *SQLiteStore) ListAllScheduledTasks(ctx context.Context) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE (type != 'once' OR next_run_at > ?) ORDER BY next_run_at ASC`,
		s.nowFunc(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *SQLiteStore) UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolToInt(enabled), s.nowFunc(), id,
	)
	return err
}

func (s *SQLiteStore) GetScheduledTask(ctx context.Context, id int64) (*ScheduledTask, error) {
	task := &ScheduledTask{}
	var enabled, worktree, updateBeforeRun, running int
	var taskType string
	err := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE id = ?`,
		id,
	).Scan(&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
		&taskType, &task.Prompt, &enabled, &task.NextRunAt,
		&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec, &task.ThreadID, &worktree,
		&task.OriginBranch, &updateBeforeRun, &running,
		&task.WorkflowName, &task.WorkflowInputs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Type = TaskType(taskType)
	task.Enabled = enabled == 1
	task.Worktree = worktree == 1
	task.UpdateBeforeRun = updateBeforeRun == 1
	task.Running = running == 1
	return task, nil
}

func (s *SQLiteStore) GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*ScheduledTask, error) {
	task := &ScheduledTask{}
	var enabled, worktree, updateBeforeRun, running int
	var taskType string
	err := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE channel_id = ? AND template_name = ?`,
		channelID, templateName,
	).Scan(&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
		&taskType, &task.Prompt, &enabled, &task.NextRunAt,
		&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec, &task.ThreadID, &worktree,
		&task.OriginBranch, &updateBeforeRun, &running,
		&task.WorkflowName, &task.WorkflowInputs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Type = TaskType(taskType)
	task.Enabled = enabled == 1
	task.Worktree = worktree == 1
	task.UpdateBeforeRun = updateBeforeRun == 1
	task.Running = running == 1
	return task, nil
}

func (s *SQLiteStore) InsertTaskRunLog(ctx context.Context, trl *TaskRunLog) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO task_run_logs (task_id, status, response_text, error_text, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		trl.TaskID, string(trl.Status), trl.ResponseText, trl.ErrorText, trl.StartedAt,
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	trl.ID = id
	return id, nil
}

func (s *SQLiteStore) UpdateTaskRunLog(ctx context.Context, trl *TaskRunLog) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_run_logs SET status = ?, response_text = ?, error_text = ?, finished_at = ? WHERE id = ?`,
		string(trl.Status), trl.ResponseText, trl.ErrorText, trl.FinishedAt, trl.ID,
	)
	return err
}

func (s *SQLiteStore) ListTaskRunLogs(ctx context.Context, taskID int64, limit int) ([]*TaskRunLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, status, response_text, error_text, started_at, finished_at
		 FROM task_run_logs WHERE task_id = ? ORDER BY started_at DESC LIMIT ?`,
		taskID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []*TaskRunLog
	for rows.Next() {
		trl := &TaskRunLog{}
		if err := rows.Scan(&trl.ID, &trl.TaskID, &trl.Status, &trl.ResponseText, &trl.ErrorText, &trl.StartedAt, &trl.FinishedAt); err != nil {
			return nil, err
		}
		logs = append(logs, trl)
	}
	return logs, rows.Err()
}
