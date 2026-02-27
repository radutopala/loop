package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// timeAfterFunc is a package-level variable for testability of time.AfterFunc.
var timeAfterFunc = time.AfterFunc

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

	systemPrompt := "IMPORTANT: Do NOT use the send_message, create_thread, or create_channel MCP tools. Your text responses are automatically delivered to the chat. Just respond with text directly."
	if task.AutoDeleteSec > 0 {
		systemPrompt += "\nIf you have nothing meaningful to report, start your response with [EPHEMERAL]. Otherwise respond normally."
	}

	req := &agent.AgentRequest{
		SessionID: sessionID,
		Messages: []agent.AgentMessage{
			{Role: "user", Content: task.Prompt},
		},
		SystemPrompt: systemPrompt,
		ChannelID:    task.ChannelID,
		DirPath:      dirPath,
	}

	var tracker *streamTracker
	var threadID string
	var threadName string
	var threadFailed bool
	if e.streamingEnabled {
		tracker = newStreamTracker(func(text string) {
			if threadID == "" && !threadFailed {
				// First turn — create a thread for the task output
				prefix := fmt.Sprintf("🧵 task #%d (`%s`) ", task.ID, task.Schedule)
				threadName = types.TruncateString(prefix+task.Prompt, 100)
				id, err := e.bot.CreateSimpleThread(ctx, task.ChannelID, threadName, prefix+text)
				if err != nil {
					e.logger.Error("creating task thread", "error", err, "task_id", task.ID, "channel_id", task.ChannelID)
					threadFailed = true
					// Fallback: send to channel directly
					_ = e.bot.SendMessage(ctx, &bot.OutgoingMessage{
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
				_ = e.bot.SendMessage(ctx, &bot.OutgoingMessage{
					ChannelID: targetID,
					Content:   text,
				})
			}
		})
		req.OnTurn = func(text string) {
			// Strip [EPHEMERAL] before the tracker records it, so IsDuplicate
			// correctly matches the final (also stripped) response.
			text = strings.TrimSpace(strings.ReplaceAll(text, "[EPHEMERAL]", ""))
			tracker.OnTurn(text)
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

	// Detect and strip [EPHEMERAL] tag (may appear at start or end of response)
	ephemeral := false
	if task.AutoDeleteSec > 0 && strings.Contains(resp.Response, "[EPHEMERAL]") {
		ephemeral = true
		resp.Response = strings.TrimSpace(strings.ReplaceAll(resp.Response, "[EPHEMERAL]", ""))
	}

	// Send final response to thread (if created) or channel
	targetChannelID := task.ChannelID
	if threadID != "" {
		targetChannelID = threadID
	}

	// Skip final send if it duplicates the last streamed turn
	if tracker == nil || !tracker.IsDuplicate(resp.Response) {
		if err := e.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID: targetChannelID,
			Content:   resp.Response,
		}); err != nil {
			e.logger.Error("sending task response", "error", err, "channel_id", task.ChannelID)
		}
	}

	// Schedule auto-deletion of the thread if ephemeral
	if ephemeral && threadID != "" {
		// Rename thread with ephemeral emoji
		newName := strings.Replace(threadName, "🧵", "💨", 1)
		if err := e.bot.RenameThread(ctx, threadID, newName); err != nil {
			e.logger.Error("renaming ephemeral thread", "error", err, "thread_id", threadID, "task_id", task.ID)
		}
		delay := time.Duration(task.AutoDeleteSec) * time.Second
		timeAfterFunc(delay, func() {
			if err := e.bot.DeleteThread(context.Background(), threadID); err != nil {
				e.logger.Error("auto-deleting task thread", "error", err, "thread_id", threadID, "task_id", task.ID)
			}
		})
	}

	return resp.Response, nil
}
