// bash_task.go holds the bash flavor of scheduled tasks: instead of an agent
// prompt or a workflow, the task runs a shell script inside the channel's
// agent container (same image, mounts, and gates as agent runs) and posts the
// output to the channel.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
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
// posts the output to the channel. Returns the raw output so the scheduler's
// run log captures it in full.
func (e *TaskExecutor) executeBashTask(ctx context.Context, task *db.ScheduledTask, dirPath string) (string, error) {
	br, ok := e.runner.(BashRunner)
	if !ok {
		return "", fmt.Errorf("bash runner not available")
	}

	runCtx := ctx
	if timeout := time.Duration(e.containerTimeout.Load()); timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, err := br.RunBash(runCtx, task.BashScript, task.ChannelID, dirPath)
	if err != nil {
		// Surface the failure (with any partial output) to the channel so a
		// broken script is visible without opening the task run logs.
		msg := fmt.Sprintf("⏱ task #%d `bash` failed: %v", task.ID, err)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			msg += "\n```\n" + truncateBashOutput(trimmed) + "\n```"
		}
		e.sendBashTaskMessage(ctx, task.ChannelID, msg)
		return output, fmt.Errorf("bash task %d: %w", task.ID, err)
	}

	msg := fmt.Sprintf("⏱ task #%d `bash` output:", task.ID)
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		msg += "\n```\n" + truncateBashOutput(trimmed) + "\n```"
	} else {
		msg += " (no output)"
	}
	e.sendBashTaskMessage(ctx, task.ChannelID, msg)
	return output, nil
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
