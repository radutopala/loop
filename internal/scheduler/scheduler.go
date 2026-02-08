package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/robfig/cron/v3"
)

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
	SetTaskEnabled(ctx context.Context, taskID int64, enabled bool) error
	ToggleTask(ctx context.Context, taskID int64) (bool, error)
	EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string) error
}

// TaskScheduler implements Scheduler using a polling loop.
type TaskScheduler struct {
	store        db.Store
	executor     TaskExecutor
	pollInterval time.Duration
	logger       *slog.Logger

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
	}
}

// Start launches the polling loop in a background goroutine.
func (s *TaskScheduler) Start(ctx context.Context) error {
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

// AddTask calculates next_run_at, enables the task, and stores it.
func (s *TaskScheduler) AddTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	nextRun, err := calculateNextRun(task.Type, task.Schedule, time.Now())
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

// EditTask updates a scheduled task's schedule, type, and/or prompt.
func (s *TaskScheduler) EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string) error {
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

	if schedule != nil || taskType != nil {
		nextRun, err := calculateNextRun(task.Type, task.Schedule, time.Now())
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
	tasks, err := s.store.GetDueTasks(ctx, time.Now())
	if err != nil {
		s.logger.Error("failed to get due tasks", "error", err)
		return
	}

	for _, task := range tasks {
		s.executeAndUpdate(ctx, task)
	}
}

func (s *TaskScheduler) executeAndUpdate(ctx context.Context, task *db.ScheduledTask) {
	runLog := &db.TaskRunLog{
		TaskID:    task.ID,
		Status:    db.RunStatusRunning,
		StartedAt: time.Now(),
	}
	logID, err := s.store.InsertTaskRunLog(ctx, runLog)
	if err != nil {
		s.logger.Error("failed to insert task run log", "task_id", task.ID, "error", err)
		return
	}
	runLog.ID = logID

	response, execErr := s.executor.ExecuteTask(ctx, task)

	runLog.FinishedAt = time.Now()
	if execErr != nil {
		runLog.Status = db.RunStatusFailed
		runLog.ErrorText = execErr.Error()
	} else {
		runLog.Status = db.RunStatusSuccess
		runLog.ResponseText = response
	}

	if err := s.store.UpdateTaskRunLog(ctx, runLog); err != nil {
		s.logger.Error("failed to update task run log", "task_id", task.ID, "error", err)
	}

	now := time.Now()
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

func calculateNextRun(taskType db.TaskType, schedule string, now time.Time) (time.Time, error) {
	switch taskType {
	case db.TaskTypeCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(schedule)
		if err != nil {
			return time.Time{}, fmt.Errorf("parsing cron schedule %q: %w", schedule, err)
		}
		return sched.Next(now), nil
	case db.TaskTypeInterval:
		d, err := time.ParseDuration(schedule)
		if err != nil {
			return time.Time{}, fmt.Errorf("parsing interval %q: %w", schedule, err)
		}
		return now.Add(d), nil
	case db.TaskTypeOnce:
		d, err := time.ParseDuration(schedule)
		if err != nil {
			return time.Time{}, fmt.Errorf("parsing once schedule %q: %w", schedule, err)
		}
		return now.Add(d), nil
	default:
		return time.Time{}, fmt.Errorf("unknown task type %q", taskType)
	}
}
