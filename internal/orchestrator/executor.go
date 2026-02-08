package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
)

// TaskExecutor implements scheduler.TaskExecutor by running an agent and
// delivering the response to Discord.
type TaskExecutor struct {
	runner Runner
	bot    Bot
	store  db.Store
	logger *slog.Logger
}

// NewTaskExecutor creates a new TaskExecutor.
func NewTaskExecutor(runner Runner, bot Bot, store db.Store, logger *slog.Logger) *TaskExecutor {
	return &TaskExecutor{runner: runner, bot: bot, store: store, logger: logger}
}

// ExecuteTask runs an agent for the given scheduled task and sends the result to Discord.
func (e *TaskExecutor) ExecuteTask(ctx context.Context, task *db.ScheduledTask) (string, error) {
	// Send notification message before executing the task
	notificationMsg := fmt.Sprintf("🕒 Running scheduled task (ID: %d)\nType: %s\nSchedule: %s\nPrompt: %s",
		task.ID, task.Type, task.Schedule, task.Prompt)
	if err := e.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: task.ChannelID,
		Content:   notificationMsg,
	}); err != nil {
		e.logger.Error("sending task notification", "error", err, "task_id", task.ID, "channel_id", task.ChannelID)
		// Continue with task execution even if notification fails
	}

	channel, err := e.store.GetChannel(ctx, task.ChannelID)
	if err != nil {
		e.logger.Error("getting channel for task", "error", err, "channel_id", task.ChannelID)
	}

	sessionID := ""
	dirPath := ""
	if channel != nil {
		sessionID = channel.SessionID
		dirPath = channel.DirPath
	}

	req := &agent.AgentRequest{
		SessionID: sessionID,
		Messages: []agent.AgentMessage{
			{Role: "user", Content: task.Prompt},
		},
		ChannelID: task.ChannelID,
		DirPath:   dirPath,
	}

	resp, err := e.runner.Run(ctx, req)
	if err != nil {
		return "", fmt.Errorf("running agent: %w", err)
	}

	if resp.Error != "" {
		return "", fmt.Errorf("agent error: %s", resp.Error)
	}

	if err := e.store.UpdateSessionID(ctx, task.ChannelID, resp.SessionID); err != nil {
		e.logger.Error("updating session data after task", "error", err, "channel_id", task.ChannelID)
	}

	if err := e.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: task.ChannelID,
		Content:   resp.Response,
	}); err != nil {
		e.logger.Error("sending task response", "error", err, "channel_id", task.ChannelID)
	}

	return resp.Response, nil
}
