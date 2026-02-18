package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
)

// TaskExecutor implements scheduler.TaskExecutor by running an agent and
// delivering the response to the chat platform.
type TaskExecutor struct {
	runner           Runner
	bot              Bot
	store            db.Store
	logger           *slog.Logger
	containerTimeout time.Duration
	streamingEnabled bool
}

// NewTaskExecutor creates a new TaskExecutor.
func NewTaskExecutor(runner Runner, bot Bot, store db.Store, logger *slog.Logger, containerTimeout time.Duration, streamingEnabled bool) *TaskExecutor {
	return &TaskExecutor{runner: runner, bot: bot, store: store, logger: logger, containerTimeout: containerTimeout, streamingEnabled: streamingEnabled}
}

// ExecuteTask runs an agent for the given scheduled task and sends the result to the chat platform.
func (e *TaskExecutor) ExecuteTask(ctx context.Context, task *db.ScheduledTask) (string, error) {
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
		SystemPrompt: "IMPORTANT: Do NOT use the send_message, create_thread, or create_channel MCP tools. Your text responses are automatically delivered to the chat. Just respond with text directly.",
		ChannelID:    task.ChannelID,
		DirPath:      dirPath,
	}

	var lastStreamedText string
	var threadID string
	var threadFailed bool
	if e.streamingEnabled {
		req.OnTurn = func(text string) {
			if text == "" {
				return
			}
			lastStreamedText = text
			if threadID == "" && !threadFailed {
				// First turn — create a thread for the task output
				prefix := fmt.Sprintf("🧵 task #%d (`%s`) ", task.ID, task.Schedule)
				name := prefix + truncateString(task.Prompt, 80)
				id, err := e.bot.CreateSimpleThread(ctx, task.ChannelID, name, prefix+text)
				if err != nil {
					e.logger.Error("creating task thread", "error", err, "task_id", task.ID, "channel_id", task.ChannelID)
					threadFailed = true
					// Fallback: send to channel directly
					_ = e.bot.SendMessage(ctx, &OutgoingMessage{
						ChannelID: task.ChannelID,
						Content:   text,
					})
					return
				}
				threadID = id
			} else {
				targetID := threadID
				if targetID == "" {
					targetID = task.ChannelID
				}
				_ = e.bot.SendMessage(ctx, &OutgoingMessage{
					ChannelID: targetID,
					Content:   text,
				})
			}
		}
	}

	runCtx, runCancel := context.WithTimeout(ctx, e.containerTimeout)
	defer runCancel()

	resp, err := e.runner.Run(runCtx, req)
	if err != nil {
		return "", fmt.Errorf("running agent: %w", err)
	}

	if resp.Error != "" {
		return "", fmt.Errorf("agent error: %s", resp.Error)
	}

	if err := e.store.UpdateSessionID(ctx, task.ChannelID, resp.SessionID); err != nil {
		e.logger.Error("updating session data after task", "error", err, "channel_id", task.ChannelID)
	}

	// Send final response to thread (if created) or channel
	targetChannelID := task.ChannelID
	if threadID != "" {
		targetChannelID = threadID
	}

	// Skip final send if it duplicates the last streamed turn
	if resp.Response != lastStreamedText {
		if err := e.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: targetChannelID,
			Content:   resp.Response,
		}); err != nil {
			e.logger.Error("sending task response", "error", err, "channel_id", task.ChannelID)
		}
	}

	return resp.Response, nil
}
