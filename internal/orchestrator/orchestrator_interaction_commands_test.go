package orchestrator

import (
	"errors"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
)

// --- HandleInteraction tests ---

func (s *OrchestratorSuite) TestHandleInteractionUnknownCommand() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "unknown",
	})
	// Should not panic, just log warning
}

func (s *OrchestratorSuite) TestHandleInteractionSchedule() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.Prompt == "do stuff" && task.Schedule == "0 * * * *" && task.Type == db.TaskTypeCron
	})).Return(int64(42), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task scheduled (ID: 42)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "schedule",
		Options: map[string]string{
			"schedule": "0 * * * *",
			"prompt":   "do stuff",
			"type":     "cron",
		},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionScheduleInterval() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.Type == db.TaskTypeInterval && task.Schedule == "5m"
	})).Return(int64(43), nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task scheduled (ID: 43)."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		GuildID:     "g1",
		CommandName: "schedule",
		Options: map[string]string{
			"schedule": "5m",
			"prompt":   "ping",
			"type":     "interval",
		},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionScheduleError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", s.ctx, mock.Anything).Return(int64(0), errors.New("sched err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to schedule task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "schedule",
		Options: map[string]string{
			"schedule": "0 * * * *",
			"prompt":   "do stuff",
			"type":     "cron",
		},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasks() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	nextRun := time.Now().Add(30 * time.Minute)
	tasks := []*db.ScheduledTask{
		{ID: 1, Prompt: "task1", Schedule: "0 * * * *", Type: db.TaskTypeCron, Enabled: true, NextRunAt: nextRun},
		{ID: 2, Prompt: "task2", Schedule: "5m", Type: db.TaskTypeInterval, Enabled: false, NextRunAt: nextRun.Add(5 * time.Minute)},
		{ID: 3, Prompt: "task3", Schedule: "10m", Type: db.TaskTypeOnce, Enabled: true, NextRunAt: nextRun.Add(10 * time.Minute)},
		{ID: 4, Prompt: "task4", Schedule: "0 12 * * *", Type: db.TaskTypeCron, Enabled: true, NextRunAt: nextRun.Add(15 * time.Minute), AutoDeleteSec: 120},
		{ID: 5, Prompt: "task5", Schedule: "", Type: db.TaskTypeManual, Enabled: true},
	}
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return(tasks, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "Scheduled tasks:") &&
			strings.Contains(out.Content, "ID 1") &&
			strings.Contains(out.Content, "[cron]") &&
			strings.Contains(out.Content, "[enabled]") &&
			strings.Contains(out.Content, "`0 * * * *`") &&
			strings.Contains(out.Content, "task1") &&
			strings.Contains(out.Content, "[disabled]") &&
			strings.Contains(out.Content, "`5m`") &&
			strings.Contains(out.Content, "[once]") &&
			strings.Contains(out.Content, nextRun.Add(10*time.Minute).Local().Format("2006-01-02 15:04 MST")) &&
			!strings.Contains(out.Content, "`10m`") &&
			strings.Contains(out.Content, "next: in ") &&
			strings.Contains(out.Content, "(auto_delete: 120s)") &&
			strings.Contains(out.Content, "[manual]") &&
			strings.Contains(out.Content, "(manual)") &&
			strings.Contains(out.Content, "next: on demand")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasksAutoDeleteNotShownWhenZero() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	nextRun := time.Now().Add(30 * time.Minute)
	tasks := []*db.ScheduledTask{
		{ID: 1, Prompt: "task1", Schedule: "0 * * * *", Type: db.TaskTypeCron, Enabled: true, NextRunAt: nextRun, AutoDeleteSec: 0},
	}
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return(tasks, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "ID 1") &&
			!strings.Contains(out.Content, "auto_delete")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasksFromDirectThread() {
	// direct thread of a top-level channel — should NOT resolve up
	s.store.On("GetChannel", s.ctx, "thread-direct").
		Return(&db.Channel{ChannelID: "thread-direct", ParentID: "ch-top"}, nil)
	s.store.On("GetChannel", s.ctx, "ch-top").
		Return(&db.Channel{ChannelID: "ch-top"}, nil) // no parent_id
	s.scheduler.On("ListTasks", s.ctx, "thread-direct").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "thread-direct",
		CommandName: "tasks",
	})

	s.scheduler.AssertCalled(s.T(), "ListTasks", s.ctx, "thread-direct")
}

func (s *OrchestratorSuite) TestHandleInteractionTasksFromSubThread() {
	// sub-thread → thread → top-level channel. Tasks are on thread.
	s.store.On("GetChannel", s.ctx, "sub-thread").
		Return(&db.Channel{ChannelID: "sub-thread", ParentID: "thread-1"}, nil)
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-root"}, nil)
	tasks := []*db.ScheduledTask{
		{ID: 91, Prompt: "check docs", Schedule: "5m", Type: db.TaskTypeInterval, Enabled: true, NextRunAt: time.Now().Add(5 * time.Minute)},
	}
	s.scheduler.On("ListTasks", s.ctx, "thread-1").Return(tasks, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "sub-thread" && strings.Contains(out.Content, "ID 91")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "sub-thread",
		CommandName: "tasks",
	})

	s.scheduler.AssertCalled(s.T(), "ListTasks", s.ctx, "thread-1")
}

func (s *OrchestratorSuite) TestHandleInteractionTasksEmpty() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTasksError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return(nil, errors.New("list err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to list tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTask() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	nextRun := time.Now().Add(30 * time.Minute)
	task := &db.ScheduledTask{
		ID: 74, Prompt: "full prompt text that is very long and would be truncated in list view",
		Schedule: "0 * * * *", Type: db.TaskTypeCron, Enabled: false,
		NextRunAt: nextRun, TemplateName: "tk-auto-worker", AutoDeleteSec: 60,
	}
	s.store.On("GetScheduledTask", s.ctx, int64(74)).Return(task, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "**Task 74**") &&
			strings.Contains(out.Content, "Type: cron") &&
			strings.Contains(out.Content, "Schedule: `0 * * * *`") &&
			strings.Contains(out.Content, "Status: disabled") &&
			strings.Contains(out.Content, "Next run: in ") &&
			strings.Contains(out.Content, "Template: tk-auto-worker") &&
			strings.Contains(out.Content, "Auto-delete: 60s") &&
			strings.Contains(out.Content, "**Prompt:**") &&
			strings.Contains(out.Content, "full prompt text that is very long")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "74"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskManual() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	task := &db.ScheduledTask{
		ID: 75, Prompt: "summarise open notes",
		Schedule: "", Type: db.TaskTypeManual, Enabled: true,
	}
	s.store.On("GetScheduledTask", s.ctx, int64(75)).Return(task, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "**Task 75**") &&
			strings.Contains(out.Content, "Type: manual") &&
			strings.Contains(out.Content, "Schedule: (manual)") &&
			strings.Contains(out.Content, "Next run: on demand")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "75"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskNotFound() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(99)).Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task 99 not found."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "99"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskInvalidID() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(42)).Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to get task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "42"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskNoTemplateNoAutoDelete() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	nextRun := time.Now().Add(30 * time.Minute)
	task := &db.ScheduledTask{
		ID: 1, Prompt: "simple task", Schedule: "5m", Type: db.TaskTypeInterval,
		Enabled: true, NextRunAt: nextRun,
	}
	s.store.On("GetScheduledTask", s.ctx, int64(1)).Return(task, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "**Task 1**") &&
			strings.Contains(out.Content, "Status: enabled") &&
			!strings.Contains(out.Content, "Template:") &&
			!strings.Contains(out.Content, "Auto-delete:")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "1"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskOnceType() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	nextRun := time.Now().Add(30 * time.Minute)
	task := &db.ScheduledTask{
		ID: 5, Prompt: "one-time task", Schedule: "2026-03-01T09:00:00Z", Type: db.TaskTypeOnce,
		Enabled: true, NextRunAt: nextRun,
	}
	s.store.On("GetScheduledTask", s.ctx, int64(5)).Return(task, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "**Task 5**") &&
			strings.Contains(out.Content, "Type: once") &&
			strings.Contains(out.Content, "Schedule: "+nextRun.Local().Format("2006-01-02 15:04 MST")) &&
			!strings.Contains(out.Content, "`2026-03-01T09:00:00Z`")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "5"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionTaskWithWorktreeFields() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	nextRun := time.Now().Add(30 * time.Minute)
	task := &db.ScheduledTask{
		ID: 74, Prompt: "do stuff", Schedule: "0 * * * *", Type: db.TaskTypeCron,
		Enabled: true, NextRunAt: nextRun, Worktree: true, OriginBranch: "main", UpdateBeforeRun: true,
	}
	s.store.On("GetScheduledTask", s.ctx, int64(74)).Return(task, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" &&
			strings.Contains(out.Content, "**Task 74**") &&
			strings.Contains(out.Content, "Worktree: true") &&
			strings.Contains(out.Content, "Origin branch: main") &&
			strings.Contains(out.Content, "Update before run: true")
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "task",
		Options:     map[string]string{"task_id": "74"},
	})

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionCancel() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("RemoveTask", s.ctx, int64(42)).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task 42 cancelled."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "cancel",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionCancelInvalidID() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "cancel",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionCancelError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("RemoveTask", s.ctx, int64(42)).Return(errors.New("remove err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to cancel task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "cancel",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleEnable() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("ToggleTask", s.ctx, int64(42)).Return(true, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task 42 enabled."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleDisable() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("ToggleTask", s.ctx, int64(42)).Return(false, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task 42 disabled."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleInvalidID() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionToggleError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("ToggleTask", s.ctx, int64(42)).Return(false, errors.New("toggle err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to toggle task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "toggle",
		Options:     map[string]string{"task_id": "42"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("EditTask", s.ctx, int64(42), new("0 9 * * *"), (*string)(nil), (*string)(nil), (*int)(nil), (*bool)(nil), (*string)(nil), (*bool)(nil), (*string)(nil), (*string)(nil), (*string)(nil)).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task 42 updated."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "42", "schedule": "0 9 * * *"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditWithTypeAndPrompt() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("EditTask", s.ctx, int64(10), (*string)(nil), new("interval"), new("new prompt"), (*int)(nil), (*bool)(nil), (*string)(nil), (*bool)(nil), (*string)(nil), (*string)(nil), (*string)(nil)).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Task 10 updated."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "10", "type": "interval", "prompt": "new prompt"},
	})

	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditInvalidID() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Invalid task ID."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "abc"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditNoFields() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "At least one of schedule, type, or prompt is required."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "42"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionEditError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.scheduler.On("EditTask", s.ctx, int64(42), (*string)(nil), (*string)(nil), new("new"), (*int)(nil), (*bool)(nil), (*string)(nil), (*bool)(nil), (*string)(nil), (*string)(nil), (*string)(nil)).Return(errors.New("edit err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to edit task."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "edit",
		Options:     map[string]string{"task_id": "42", "prompt": "new"},
	})

	s.scheduler.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionStatus() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Loop bot is running."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "status",
	})

	s.bot.AssertExpectations(s.T())
}
