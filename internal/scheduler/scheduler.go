package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// ErrInvalidSchedule wraps schedule/type validation failures (bad cron, bad
// duration, non-RFC3339 once time, unknown type). Callers can detect it with
// errors.Is to return a 400 rather than a 500 — these are user-input errors,
// not server faults.
var ErrInvalidSchedule = errors.New("invalid task schedule")

// ErrNothingToRun is AddTask's error for a task with no prompt, script,
// workflow or plugin action.
var ErrNothingToRun = errors.New("a task needs a prompt, bash_script or workflow_name")

// TaskExecutor executes a scheduled task and returns a response or error.
type TaskExecutor interface {
	ExecuteTask(ctx context.Context, task *db.ScheduledTask) (string, error)
}

// Scheduler manages scheduled tasks.
type Scheduler interface {
	Start(ctx context.Context) error
	Stop() error
	AddTask(ctx context.Context, task *db.ScheduledTask) (int64, error)
	RemoveTask(ctx context.Context, taskID int64) error
	ListTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error)
	GetTask(ctx context.Context, taskID int64) (*db.ScheduledTask, error)
	SetTaskEnabled(ctx context.Context, taskID int64, enabled bool) error
	ToggleTask(ctx context.Context, taskID int64) (bool, error)
	EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string, autoDeleteSec *int, worktree *bool, originBranch *string, updateBeforeRun *bool, workflowName *string, workflowInputs *string, bashScript *string) error
	RunNow(ctx context.Context, taskID int64) error
}

// TaskEventBroadcaster broadcasts task lifecycle events.
type TaskEventBroadcaster interface {
	BroadcastTaskRunCompleted(data events.TaskRunEventData)
}

// TaskScheduler implements Scheduler using a polling loop.
type TaskScheduler struct {
	store        db.Store
	executor     TaskExecutor
	pollInterval time.Duration
	logger       *slog.Logger
	events       TaskEventBroadcaster

	// inflight holds the IDs of tasks the poll loop is running, from start
	// until next_run_at is advanced: the running flag clears before that, so
	// a later tick would otherwise start the task again.
	mu       sync.Mutex
	inflight map[int64]struct{}

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTaskScheduler creates a new TaskScheduler.
func NewTaskScheduler(store db.Store, executor TaskExecutor, pollInterval time.Duration, logger *slog.Logger) *TaskScheduler {
	return &TaskScheduler{
		store:        store,
		executor:     executor,
		pollInterval: pollInterval,
		logger:       logger,
		inflight:     map[int64]struct{}{},
	}
}

// SetEventBroadcaster sets the event broadcaster for task run lifecycle events.
func (s *TaskScheduler) SetEventBroadcaster(eb TaskEventBroadcaster) {
	s.events = eb
}

// Start launches the polling loop in a background goroutine. Before polling
// begins, any task left flagged `running=1` from a prior daemon process is
// reset — the defer-based release in executeAndLog does not fire if the
// process was killed (OOM, SIGKILL, crash), so without this sweep the row
// would be permanently invisible to GetDueTasks.
func (s *TaskScheduler) Start(ctx context.Context) error {
	if reset, err := s.store.ResetStaleRunningTasks(ctx); err != nil {
		s.logger.Error("failed to reset stale running tasks", "error", err)
	} else if reset > 0 {
		s.logger.Warn("reset stale running tasks from prior daemon run", "count", reset)
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.pollLoop(ctx)
	return nil
}

// Stop gracefully stops the polling loop and waits for it to finish.
func (s *TaskScheduler) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

// AddTask calculates next_run_at, enables the task, and stores it. A task
// with nothing to run is ErrNothingToRun.
func (s *TaskScheduler) AddTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	if strings.TrimSpace(task.Prompt) == "" && strings.TrimSpace(task.BashScript) == "" && task.WorkflowName == "" && task.PluginAction == "" {
		return 0, ErrNothingToRun
	}
	nextRun, err := calculateNextRun(task.Type, task.Schedule, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("calculating next run: %w", err)
	}
	task.NextRunAt = nextRun
	task.Enabled = true
	return s.store.CreateScheduledTask(ctx, task)
}

// RemoveTask deletes a task from the store.
func (s *TaskScheduler) RemoveTask(ctx context.Context, taskID int64) error {
	return s.store.DeleteScheduledTask(ctx, taskID)
}

// ListTasks returns all tasks for a channel.
func (s *TaskScheduler) ListTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	return s.store.ListScheduledTasks(ctx, channelID)
}

// GetTask returns a single scheduled task by ID.
func (s *TaskScheduler) GetTask(ctx context.Context, taskID int64) (*db.ScheduledTask, error) {
	return s.store.GetScheduledTask(ctx, taskID)
}

// SetTaskEnabled enables or disables a scheduled task.
func (s *TaskScheduler) SetTaskEnabled(ctx context.Context, taskID int64, enabled bool) error {
	return s.store.UpdateScheduledTaskEnabled(ctx, taskID, enabled)
}

// ToggleTask flips a scheduled task's enabled state and returns the new state.
func (s *TaskScheduler) ToggleTask(ctx context.Context, taskID int64) (bool, error) {
	task, err := s.store.GetScheduledTask(ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("getting task: %w", err)
	}
	if task == nil {
		return false, fmt.Errorf("task %d not found", taskID)
	}

	newEnabled := !task.Enabled
	if err := s.store.UpdateScheduledTaskEnabled(ctx, taskID, newEnabled); err != nil {
		return false, err
	}
	return newEnabled, nil
}

// EditTask updates a scheduled task's schedule, type, prompt, auto_delete_sec, worktree flag, and/or workflow fields.
func (s *TaskScheduler) EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string, autoDeleteSec *int, worktree *bool, originBranch *string, updateBeforeRun *bool, workflowName *string, workflowInputs *string, bashScript *string) error {
	task, err := s.store.GetScheduledTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("getting task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}

	if schedule != nil {
		task.Schedule = *schedule
	}
	if taskType != nil {
		task.Type = db.TaskType(*taskType)
	}
	if prompt != nil {
		task.Prompt = *prompt
	}
	if autoDeleteSec != nil {
		task.AutoDeleteSec = *autoDeleteSec
	}
	if worktree != nil {
		task.Worktree = *worktree
	}
	if originBranch != nil {
		task.OriginBranch = *originBranch
	}
	if updateBeforeRun != nil {
		task.UpdateBeforeRun = *updateBeforeRun
	}
	if workflowName != nil {
		task.WorkflowName = *workflowName
	}
	if workflowInputs != nil {
		task.WorkflowInputs = *workflowInputs
	}
	if bashScript != nil {
		task.BashScript = *bashScript
	}

	if schedule != nil || taskType != nil {
		nextRun, err := calculateNextRun(task.Type, task.Schedule, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("calculating next run: %w", err)
		}
		task.NextRunAt = nextRun
	}

	return s.store.UpdateScheduledTask(ctx, task)
}

func (s *TaskScheduler) pollLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processDueTasks(ctx)
		}
	}
}

func (s *TaskScheduler) processDueTasks(ctx context.Context) {
	tasks, err := s.store.GetDueTasks(ctx, time.Now().UTC())
	if err != nil {
		s.logger.Error("failed to get due tasks", "error", err)
		return
	}

	// Each due task runs on its own goroutine, so a long run doesn't hold
	// back the tasks due after it.
	for _, task := range tasks {
		if !s.beginRun(task.ID) {
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.endRun(task.ID)
			s.executeAndUpdate(ctx, task)
		}()
	}
}

// beginRun marks a task in flight; false if it already is.
func (s *TaskScheduler) beginRun(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inflight[id]; ok {
		return false
	}
	s.inflight[id] = struct{}{}
	return true
}

func (s *TaskScheduler) endRun(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, id)
}

// RunNow immediately executes a task without updating its schedule.
func (s *TaskScheduler) RunNow(ctx context.Context, taskID int64) error {
	task, err := s.store.GetScheduledTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("getting task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	if task.Running {
		return fmt.Errorf("task %d is already running", taskID)
	}
	return s.executeAndLog(ctx, task)
}

func (s *TaskScheduler) executeAndUpdate(ctx context.Context, task *db.ScheduledTask) {
	if err := s.executeAndLog(ctx, task); err != nil {
		return
	}

	now := time.Now().UTC()
	switch task.Type {
	case db.TaskTypeCron:
		nextRun, err := calculateNextRun(db.TaskTypeCron, task.Schedule, now)
		if err != nil {
			s.logger.Error("failed to calculate next cron run", "task_id", task.ID, "error", err)
			return
		}
		task.NextRunAt = nextRun
	case db.TaskTypeInterval:
		nextRun, err := calculateNextRun(db.TaskTypeInterval, task.Schedule, now)
		if err != nil {
			s.logger.Error("failed to calculate next interval run", "task_id", task.ID, "error", err)
			return
		}
		task.NextRunAt = nextRun
	case db.TaskTypeOnce:
		task.Enabled = false
	}

	if err := s.store.UpdateScheduledTask(ctx, task); err != nil {
		s.logger.Error("failed to update scheduled task", "task_id", task.ID, "error", err)
	}
}

// executeAndLog runs the task executor, records the result in a run log, and
// broadcasts a completion event. It does not update the task's schedule.
func (s *TaskScheduler) executeAndLog(ctx context.Context, task *db.ScheduledTask) error {
	platform := ""
	if ch, err := s.store.GetChannel(ctx, task.ChannelID); err == nil && ch != nil {
		platform = string(ch.Platform)
	}
	s.logger.Info("executing task", "task_id", task.ID, "channel_id", task.ChannelID, "platform", platform, "type", task.Type, "schedule", task.Schedule)

	// Atomically claim the task — prevents concurrent executions even under races.
	claimed, err := s.store.ClaimScheduledTaskRunning(ctx, task.ID)
	if err != nil {
		s.logger.Error("failed to claim task", "task_id", task.ID, "error", err)
		return err
	}
	if !claimed {
		s.logger.Info("task already claimed by another execution", "task_id", task.ID)
		return fmt.Errorf("task %d is already running", task.ID)
	}
	defer func() {
		if err := s.store.ReleaseScheduledTaskRunning(ctx, task.ID); err != nil {
			s.logger.Error("failed to release task running state", "task_id", task.ID, "error", err)
		}
	}()

	runLog := &db.TaskRunLog{
		TaskID:    task.ID,
		Status:    db.RunStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	logID, err := s.store.InsertTaskRunLog(ctx, runLog)
	if err != nil {
		s.logger.Error("failed to insert task run log", "task_id", task.ID, "error", err)
		return err
	}
	runLog.ID = logID

	response, execErr := s.executor.ExecuteTask(ctx, task)

	runLog.FinishedAt = time.Now().UTC()
	if execErr != nil {
		runLog.Status = db.RunStatusFailed
		runLog.ErrorText = execErr.Error()
		s.logger.Error("task execution failed", "task_id", task.ID, "platform", platform, "error", execErr)
	} else {
		runLog.Status = db.RunStatusSuccess
		runLog.ResponseText = response
		s.logger.Info("task execution completed", "task_id", task.ID, "channel_id", task.ChannelID, "platform", platform)
	}

	if err := s.store.UpdateTaskRunLog(ctx, runLog); err != nil {
		s.logger.Error("failed to update task run log", "task_id", task.ID, "error", err)
	}

	if s.events != nil {
		s.events.BroadcastTaskRunCompleted(events.TaskRunEventData{
			TaskID:    task.ID,
			RunID:     runLog.ID,
			Status:    string(runLog.Status),
			ChannelID: task.ChannelID,
			ThreadID:  task.ThreadID,
			Kind:      taskRunKind(task),
			Error:     runLog.ErrorText,
		})
	}

	return nil
}

// taskRunKind is what task runs, checked in the order the executor does.
func taskRunKind(task *db.ScheduledTask) string {
	switch {
	case task.WorkflowName != "":
		return events.TaskRunWorkflow
	case task.PluginID != "":
		return events.TaskRunPlugin
	case task.BashScript != "":
		return events.TaskRunBash
	}
	return events.TaskRunAgent
}

func parseOnceSchedule(schedule string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: once schedule %q must be RFC3339 (e.g. 2026-02-09T14:30:00Z): %w", ErrInvalidSchedule, schedule, err)
	}
	return t, nil
}

// ValidateSchedule checks schedule parses for taskType the way AddTask reads
// it. The error wraps ErrInvalidSchedule.
func ValidateSchedule(taskType db.TaskType, schedule string) error {
	_, err := calculateNextRun(taskType, schedule, time.Time{})
	return err
}

// NextRun is when a task of taskType on schedule first runs after now: the
// zero time, so at once, for a schedule ValidateSchedule refuses.
func NextRun(taskType db.TaskType, schedule string, now time.Time) time.Time {
	t, _ := calculateNextRun(taskType, schedule, now)
	return t
}

func calculateNextRun(taskType db.TaskType, schedule string, now time.Time) (time.Time, error) {
	switch taskType {
	case db.TaskTypeCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(schedule)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: cron schedule %q: %w", ErrInvalidSchedule, schedule, err)
		}
		return sched.Next(now), nil
	case db.TaskTypeInterval:
		d, err := time.ParseDuration(schedule)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: interval %q: %w", ErrInvalidSchedule, schedule, err)
		}
		return now.Add(d), nil
	case db.TaskTypeOnce:
		t, err := parseOnceSchedule(schedule)
		if err != nil {
			return time.Time{}, err
		}
		// Normalize to UTC before storage. time.Parse keeps the RFC3339
		// offset (e.g. +03:00) and the sqlite driver serializes time.Time
		// WITH its zone, while GetDueTasks compares next_run_at against a
		// UTC now as TEXT — so a local-offset value compares by its
		// wall-clock digits against the UTC clock and fires hours late (by
		// exactly the zone offset). Cron/interval are unaffected: they
		// derive from time.Now().UTC().
		return t.UTC(), nil
	case db.TaskTypeManual:
		// Manual tasks have no schedule; the poller never selects them
		// (GetDueTasks excludes type='manual'), so the zero next-run is inert.
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("%w: unknown task type %q", ErrInvalidSchedule, taskType)
	}
}
