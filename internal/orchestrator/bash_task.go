// bash_task.go holds the bash flavor of scheduled tasks: instead of an agent
// prompt or a workflow, the task runs a shell script inside the channel's
// agent container (same image, mounts, and gates as agent runs). Output goes
// to the task's sub-thread — created on first run exactly like prompt tasks
// (a worktree thread when the task has worktree enabled) and reused on
// subsequent runs via task.ThreadID.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// BashRunner is the optional runner capability behind bash scheduled tasks.
// DockerRunner implements it; the type assertion in executeBashTask keeps the
// core Runner interface (Run/Cleanup) unchanged for mocks that don't care.
type BashRunner interface {
	RunBash(ctx context.Context, script, channelID, dirPath string) (string, error)
}

// bashOutputMaxLen caps the portion of the script output posted to the chat;
// the full output is still returned to the scheduler for the task run log.
const bashOutputMaxLen = 3500

// executeBashTask runs task.BashScript in the channel's agent container and
// posts the output to the task's thread (falling back to the channel when
// thread creation fails). Returns the raw output so the scheduler's run log
// captures it in full. dirPath already points at the task's worktree when the
// task has worktree enabled — the shared worktree block in ExecuteTask ran
// before this dispatch.
func (e *TaskExecutor) executeBashTask(ctx context.Context, task *db.ScheduledTask, dirPath string, channel *db.Channel, worktreeCreated bool) (string, error) {
	br, ok := e.runner.(BashRunner)
	if !ok {
		return "", fmt.Errorf("bash runner not available")
	}

	// Reuse the task's thread; treat a deleted thread as a first run. (For
	// worktree tasks the shared worktree block already did this check and
	// reset task.ThreadID; this covers non-worktree bash tasks.)
	threadID := task.ThreadID
	if threadID != "" {
		if th, err := e.store.GetChannel(ctx, threadID); err != nil || th == nil {
			threadID = ""
			task.ThreadID = ""
		}
	}
	if threadID == "" {
		threadID = e.createBashTaskThread(ctx, task, dirPath, channel, worktreeCreated)
	}
	target := task.ChannelID
	if threadID != "" {
		target = threadID
		// Surface the script as the thread's "user prompt" before the output,
		// mirroring how prompt tasks seed their thread each run.
		storeUserTaskPrompt(ctx, e.store, e.events, threadID, task.BashScript)
	}

	runCtx := ctx
	if timeout := time.Duration(e.containerTimeout.Load()); timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, err := br.RunBash(runCtx, task.BashScript, task.ChannelID, dirPath)
	if err != nil {
		// Surface the failure (with any partial output) so a broken script is
		// visible without opening the task run logs.
		msg := fmt.Sprintf("⏱ task #%d `bash` failed: %v", task.ID, err)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			msg += "\n```\n" + truncateBashOutput(trimmed) + "\n```"
		}
		e.sendBashTaskMessage(ctx, target, msg)
		return output, fmt.Errorf("bash task %d: %w", task.ID, err)
	}

	msg := fmt.Sprintf("⏱ task #%d `bash` output:", task.ID)
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		msg += "\n```\n" + truncateBashOutput(trimmed) + "\n```"
	} else {
		msg += " (no output)"
	}
	e.sendBashTaskMessage(ctx, target, msg)
	return output, nil
}

// createBashTaskThread creates the task's sub-thread, mirroring the prompt
// tasks' first-turn thread creation: same naming, channel upsert (with the
// worktree flag and dirPath so a worktree thread renders as one), task
// linking for recurring tasks, permission invites, and sidebar broadcast.
// Returns "" on failure — the caller falls back to posting in the channel.
func (e *TaskExecutor) createBashTaskThread(ctx context.Context, task *db.ScheduledTask, dirPath string, channel *db.Channel, worktreeCreated bool) string {
	isLocal := channel != nil && channel.Platform == types.PlatformLocal
	taskPrefix := ""
	if !isLocal {
		taskPrefix = "⏱ "
	}
	scheduleLabel := task.Schedule
	if task.Type == db.TaskTypeManual {
		scheduleLabel = "manual"
	}
	prefix := fmt.Sprintf("%stask #%d (`%s`) ", taskPrefix, task.ID, scheduleLabel)
	threadName := types.TruncateString(prefix+task.BashScript, 100)

	threadID, err := e.bot.CreateSimpleThread(ctx, task.ChannelID, threadName, "")
	if err != nil {
		e.logger.Error("creating bash task thread", "error", err, "task_id", task.ID, "channel_id", task.ChannelID)
		return ""
	}

	if channel != nil {
		threadChannel := &db.Channel{
			ChannelID:   threadID,
			GuildID:     channel.GuildID,
			Name:        threadName,
			DirPath:     dirPath,
			ParentID:    task.ChannelID,
			Platform:    channel.Platform,
			Permissions: channel.Permissions,
			Active:      true,
			Worktree:    worktreeCreated,
		}
		if task.Type != db.TaskTypeOnce {
			_ = e.store.LinkTaskThread(ctx, threadChannel, task.ID, threadID)
		} else {
			_ = e.store.UpsertChannel(ctx, threadChannel)
		}
		e.invitePermissionUsers(ctx, threadID, channel.Permissions)
	} else if task.Type != db.TaskTypeOnce {
		_ = e.store.UpdateScheduledTaskThreadID(ctx, task.ID, threadID)
	}
	if task.Type != db.TaskTypeOnce {
		task.ThreadID = threadID
	}
	if e.events != nil {
		e.events.BroadcastChannelCreated(task.ChannelID, threadID)
	}
	return threadID
}

// sendBashTaskMessage posts to the platform and persists the bot message so
// the chat timeline shows the run even after a reload.
func (e *TaskExecutor) sendBashTaskMessage(ctx context.Context, channelID, content string) {
	if err := e.bot.SendMessage(ctx, &bot.OutgoingMessage{ChannelID: channelID, Content: content}); err != nil {
		e.logger.Error("sending bash task output", "error", err, "channel_id", channelID)
	}
	storeBotMessage(ctx, e.store, e.events, channelID, content, "")
}

func truncateBashOutput(s string) string {
	if len(s) > bashOutputMaxLen {
		return s[:bashOutputMaxLen] + "\n… (truncated)"
	}
	return s
}
