package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/worktree"
)

// TaskExecutor implements scheduler.TaskExecutor by running an agent and
// delivering the response to the chat platform.
type TaskExecutor struct {
	runner           Runner
	bot              Bot
	store            db.Store
	logger           *slog.Logger
	containerTimeout atomic.Int64 // nanoseconds
	streamingEnabled atomic.Bool
	configLoad       func() (*config.Config, error)
	events           events.Broadcaster
	timeAfterFunc    func(time.Duration, func()) *time.Timer
	worktreeCreator  *worktree.Creator
}

// NewTaskExecutor creates a new TaskExecutor.
func NewTaskExecutor(runner Runner, bot Bot, store db.Store, logger *slog.Logger, containerTimeout time.Duration, streamingEnabled bool, configLoad func() (*config.Config, error)) *TaskExecutor {
	e := &TaskExecutor{runner: runner, bot: bot, store: store, logger: logger, configLoad: configLoad, timeAfterFunc: time.AfterFunc}
	e.containerTimeout.Store(int64(containerTimeout))
	e.streamingEnabled.Store(streamingEnabled)
	return e
}

// refreshConfig reloads configuration and returns the current container timeout
// and streaming flag. On reload error (or nil configLoad), the last-known-good
// values are returned.
func (e *TaskExecutor) refreshConfig() (time.Duration, bool) {
	if e.configLoad != nil {
		if fresh, err := e.configLoad(); err == nil {
			e.containerTimeout.Store(int64(fresh.ContainerTimeout))
			e.streamingEnabled.Store(fresh.StreamingEnabled)
		}
	}
	return time.Duration(e.containerTimeout.Load()), e.streamingEnabled.Load()
}

// SetEventBroadcaster sets the event broadcaster for real-time updates.
func (e *TaskExecutor) SetEventBroadcaster(eb events.Broadcaster) {
	e.events = eb
}

// SetWorktreeCreator sets the worktree creator for tasks with worktree=true.
func (e *TaskExecutor) SetWorktreeCreator(wc *worktree.Creator) {
	e.worktreeCreator = wc
}

// ExecuteTask runs an agent for the given scheduled task and sends the result to the chat platform.
func (e *TaskExecutor) ExecuteTask(ctx context.Context, task *db.ScheduledTask) (string, error) {
	containerTimeout, streamingEnabled := e.refreshConfig()

	channel, err := e.store.GetChannel(ctx, task.ChannelID)
	if err != nil {
		e.logger.Error("getting channel for task", "error", err, "channel_id", task.ChannelID)
	}

	dirPath := ""
	if channel != nil {
		dirPath = channel.DirPath
	}

	// Worktree: on first run, create a git worktree; on subsequent runs, reuse
	// the thread's DirPath which already points to the worktree.
	worktreeCreated := false
	if task.Worktree && e.worktreeCreator != nil && dirPath != "" {
		if task.ThreadID == "" {
			// First run — create worktree using explicit or auto-detected branch.
			branch := task.OriginBranch
			if branch == "" {
				detectedBranch, branchErr := e.getCurrentBranch(ctx, dirPath)
				if branchErr != nil {
					return "", fmt.Errorf("getting current branch for worktree: %w", branchErr)
				}
				branch = detectedBranch
				// Persist the detected branch so subsequent updates know the target.
				if err := e.store.UpdateScheduledTaskOriginBranch(ctx, task.ID, branch); err != nil {
					e.logger.Error("persisting origin branch", "error", err, "task_id", task.ID)
				}
			}
			name := fmt.Sprintf("task-%d-%s", task.ID, randutil.HexID(4))
			sessionForCopy := ""
			if channel != nil {
				sessionForCopy = channel.SessionID
			}
			result, wtErr := e.worktreeCreator.Create(ctx, dirPath, branch, name, sessionForCopy)
			if wtErr != nil {
				return "", fmt.Errorf("creating worktree for task %d: %w", task.ID, wtErr)
			}
			dirPath = result.WorktreePath
			worktreeCreated = true
			e.logger.Info("created worktree for task", "task_id", task.ID, "worktree_path", dirPath)
		} else {
			// Subsequent runs — reuse thread's DirPath
			if threadCh, err := e.store.GetChannel(ctx, task.ThreadID); err == nil && threadCh != nil && threadCh.DirPath != "" {
				dirPath = threadCh.DirPath
			}
		}
	}

	// Determine which session to resume:
	// - Recurring task with existing thread → resume the thread's own session
	// - First run (no thread yet) → fork the parent channel's session for initial context
	sessionID := ""
	forkSession := false
	if task.ThreadID != "" {
		if threadCh, err := e.store.GetChannel(ctx, task.ThreadID); err == nil && threadCh != nil {
			sessionID = threadCh.SessionID
		}
	} else if channel != nil && channel.SessionID != "" {
		sessionID = channel.SessionID
		forkSession = true
	}

	systemPrompt := "IMPORTANT: Do NOT use the send_message, create_thread, or create_channel MCP tools. Your text responses are automatically delivered to the chat. Just respond with text directly."
	if task.AutoDeleteSec > 0 {
		systemPrompt += "\nIf you have nothing meaningful to report, start your response with [EPHEMERAL]. Otherwise respond normally."
	}
	// Track parent dir for worktree config inheritance (model, etc.).
	parentDirPath := ""
	if task.Worktree && channel != nil {
		parentDirPath = channel.DirPath
	}

	// Prepend git update instructions to the user prompt when enabled.
	prompt := task.Prompt
	if task.UpdateBeforeRun && task.OriginBranch != "" {
		prompt = fmt.Sprintf("Before starting work, update your worktree to the latest origin/%s:\n1. git stash (if there are uncommitted changes)\n2. git fetch origin %s\n3. git rebase origin/%s (this keeps your previous commits on top of latest origin)\n4. git stash pop (if you stashed changes in step 1)\nHandle any merge conflicts if they arise.\n\n%s", task.OriginBranch, task.OriginBranch, task.OriginBranch, task.Prompt)
	}

	req := &agent.AgentRequest{
		SessionID:   sessionID,
		ForkSession: forkSession,
		Messages: []agent.AgentMessage{
			{Role: "user", Content: prompt},
		},
		SystemPrompt:  systemPrompt,
		ChannelID:     task.ChannelID,
		DirPath:       dirPath,
		ParentDirPath: parentDirPath,
		AgentID:       "chat",
	}

	var tracker *streamTracker
	var threadID string
	var threadName string
	var threadFailed bool
	// Reuse existing thread for recurring tasks (all platforms).
	// Re-fetch from DB in case a concurrent execution persisted it since this task was loaded.
	isLocal := channel != nil && channel.Platform == types.PlatformLocal
	if task.Type != db.TaskTypeOnce {
		if fresh, err := e.store.GetScheduledTask(ctx, task.ID); err == nil && fresh != nil && fresh.ThreadID != "" {
			task.ThreadID = fresh.ThreadID
		}
	}
	if task.ThreadID != "" && task.Type != db.TaskTypeOnce {
		threadID = task.ThreadID
	}
	// hasExistingThread is true when the thread was created by a previous run
	// on the local platform. For subsequent local runs, register the agent
	// under the thread so the stop button in the thread view targets the
	// correct container. Discord/Slack are left unchanged — their threads
	// use platform-native delivery and don't have a local stop button.
	hasExistingThread := threadID != "" && isLocal
	if hasExistingThread {
		req.ChannelID = threadID
	}
	if streamingEnabled {
		tracker = newStreamTracker(func(text string) {
			if threadID == "" && !threadFailed {
				// First turn — create a thread for the task output
				taskPrefix := ""
				if !isLocal {
					taskPrefix = "⏱ "
				}
				prefix := fmt.Sprintf("%stask #%d (`%s`) ", taskPrefix, task.ID, task.Schedule)
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
					storeBotMessage(ctx, e.store, e.events, task.ChannelID, text)
					return
				}
				threadID = id
				// Upsert thread channel inheriting from parent so botForChannel
				// can resolve it for subsequent operations (rename, delete, etc.).
				if channel != nil {
					_ = e.store.UpsertChannel(ctx, &db.Channel{
						ChannelID:   threadID,
						GuildID:     channel.GuildID,
						Name:        threadName,
						DirPath:     dirPath,
						ParentID:    task.ChannelID,
						Platform:    channel.Platform,
						SessionID:   channel.SessionID,
						Permissions: channel.Permissions,
						Active:      true,
						Worktree:    worktreeCreated,
					})
					e.invitePermissionUsers(ctx, threadID, channel.Permissions)
				}
				// Persist thread ID so recurring tasks reuse the same thread.
				if task.Type != db.TaskTypeOnce {
					task.ThreadID = threadID
					_ = e.store.UpdateScheduledTaskThreadID(ctx, task.ID, threadID)
				}
				// Notify the UI that a new thread was created so the
				// sidebar refreshes immediately.
				if e.events != nil {
					e.events.BroadcastChannelCreated(task.ChannelID, threadID)
				}
				// Broadcast to the thread (not the parent channel) so the
				// Electron app shows the initial message in the thread view.
				// Don't use storeBotMessage here — CreateSimpleThread
				// already stored the message in the DB for the thread.
				if e.events != nil {
					e.events.BroadcastMessageCreated(threadID, events.MessageEventData{
						MsgID:       generateMessageID(),
						AuthorName:  "agent",
						Content:     prefix + text,
						IsBot:       true,
						IsProcessed: true,
					})
				}
			} else {
				targetID := threadID
				if targetID == "" {
					targetID = task.ChannelID
				}
				if err := e.bot.SendMessage(ctx, &bot.OutgoingMessage{
					ChannelID: targetID,
					Content:   text,
				}); err != nil {
					e.logger.Error("streaming send failed", "error", err, "channel_id", targetID)
				}
				storeBotMessage(ctx, e.store, e.events, targetID, text)
			}
		})
		req.OnTurn = func(text string) {
			// Strip [EPHEMERAL] before the tracker records it, so IsDuplicate
			// correctly matches the final (also stripped) response.
			text = strings.TrimSpace(strings.ReplaceAll(text, "[EPHEMERAL]", ""))
			tracker.OnTurn(text)
		}
		if e.events != nil {
			req.OnToolUse = func(name, input string) {
				targetID := threadID
				if targetID == "" {
					targetID = task.ChannelID
				}
				e.events.BroadcastToolUse(targetID, events.ToolUseEventData{
					ToolName: name,
					Input:    input,
				})
				if name == "AskUserQuestion" {
					var data events.AskUserQuestionEventData
					if err := json.Unmarshal([]byte(input), &data); err == nil && len(data.Questions) > 0 {
						e.events.BroadcastAskUser(targetID, data)
					}
				}
				if name == "ExitPlanMode" {
					var data events.ExitPlanModeEventData
					if err := json.Unmarshal([]byte(input), &data); err == nil && data.Plan != "" {
						e.events.BroadcastExitPlan(targetID, data)
					}
				}
				if name == "TodoWrite" {
					var data events.TodoWriteEventData
					if err := json.Unmarshal([]byte(input), &data); err == nil && len(data.Todos) > 0 {
						e.events.BroadcastTodoWrite(targetID, data)
					}
				}
			}
			req.OnActivity = func(activity, detail string) {
				targetID := threadID
				if targetID == "" {
					targetID = task.ChannelID
				}
				data := events.AgentActivityEventData{Activity: activity}
				if activity == "model" {
					data.Model = detail
				} else {
					data.Description = detail
				}
				e.events.BroadcastAgentActivity(targetID, data)
			}
		}
	}

	// Generate a unique run ID so the frontend can distinguish concurrent runs
	// on the same channel (e.g. a scheduled task vs. a chat agent).
	runID := randutil.HexID(8)

	// Broadcast running status. For subsequent runs, broadcast to both the
	// thread (for direct subscribers) and the parent (with thread_id set, for
	// subscription bootstrap). The frontend routes the parent event to the
	// thread's store via thread_id so the parent doesn't show running state.
	if e.events != nil {
		status := events.AgentStatusEventData{Status: "running", RunID: runID, ThreadID: threadID}
		if hasExistingThread {
			e.events.BroadcastAgentStatus(threadID, status)
		}
		e.events.BroadcastAgentStatus(task.ChannelID, status)
	}

	runCtx, runCancel := context.WithTimeout(ctx, containerTimeout)
	defer runCancel()

	resp, err := e.runner.Run(runCtx, req)
	if err != nil {
		if e.events != nil {
			errStatus := events.AgentStatusEventData{Status: "error", RunID: runID, Error: err.Error(), ThreadID: threadID}
			if hasExistingThread {
				// Broadcast to thread directly so the thread view updates immediately.
				e.events.BroadcastAgentStatus(threadID, errStatus)
			}
			e.events.BroadcastAgentStatus(task.ChannelID, errStatus)
		}
		return "", fmt.Errorf("running agent: %w", err)
	}

	if resp.Error != "" {
		if e.events != nil {
			errStatus := events.AgentStatusEventData{Status: "error", RunID: runID, Error: resp.Error, ThreadID: threadID}
			if hasExistingThread {
				e.events.BroadcastAgentStatus(threadID, errStatus)
			}
			e.events.BroadcastAgentStatus(task.ChannelID, errStatus)
		}
		return "", fmt.Errorf("agent error: %s", resp.Error)
	}

	// Update session on the thread (not the parent channel) so subsequent
	// recurring runs resume the thread's own conversation.
	sessionTarget := threadID
	if sessionTarget == "" {
		sessionTarget = task.ChannelID
	}
	if err := e.store.UpdateSessionID(ctx, sessionTarget, resp.SessionID); err != nil {
		e.logger.Error("updating session data after task", "error", err, "channel_id", sessionTarget)
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
		storeBotMessage(ctx, e.store, e.events, targetChannelID, resp.Response)
	}

	// Broadcast completed status. For subsequent runs, broadcast to both
	// thread and parent (with thread_id set) so both views update immediately.
	// For first runs, targetChannelID may be a newly-created thread or the channel.
	if e.events != nil {
		done := events.AgentStatusEventData{
			Status:     "completed",
			RunID:      runID,
			DurationMs: resp.DurationMs,
			NumTurns:   resp.NumTurns,
			Model:      resp.Model,
			ThreadID:   threadID,
		}
		if targetChannelID != task.ChannelID {
			e.events.BroadcastAgentStatus(targetChannelID, done)
		}
		e.events.BroadcastAgentStatus(task.ChannelID, done)
	}

	// Schedule auto-deletion of the thread when auto_delete_sec is configured
	if task.AutoDeleteSec > 0 && threadID != "" {
		if ephemeral {
			var newName string
			if isLocal {
				newName = "[ephemeral] " + threadName
			} else {
				newName = strings.Replace(threadName, "⏱ ", "💨 ", 1)
			}
			if err := e.bot.RenameThread(ctx, threadID, newName); err != nil {
				e.logger.Error("renaming ephemeral thread", "error", err, "thread_id", threadID, "task_id", task.ID)
			}
		}
		delay := time.Duration(task.AutoDeleteSec) * time.Second
		e.timeAfterFunc(delay, func() {
			if err := e.bot.DeleteThread(context.Background(), threadID); err != nil {
				e.logger.Error("auto-deleting task thread", "error", err, "thread_id", threadID, "task_id", task.ID)
			}
			if e.events != nil {
				e.events.BroadcastChannelDeleted(threadID)
			}
		})
	}

	return resp.Response, nil
}

// getCurrentBranch returns the current branch name in the given directory.
func (e *TaskExecutor) getCurrentBranch(ctx context.Context, dirPath string) (string, error) {
	out, err := e.worktreeCreator.Run(ctx, dirPath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse in %s: %s", dirPath, strings.TrimSpace(string(out)))
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		// Detached HEAD — fall back to the commit hash.
		out, err = e.worktreeCreator.Run(ctx, dirPath, "git", "rev-parse", "HEAD")
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD in %s: %s", dirPath, strings.TrimSpace(string(out)))
		}
		branch = strings.TrimSpace(string(out))
	}
	return branch, nil
}

// invitePermissionUsers invites all RBAC owner and member users to a thread.
func (e *TaskExecutor) invitePermissionUsers(ctx context.Context, threadID string, perms types.Permissions) {
	for _, userID := range perms.Owners.Users {
		if err := e.bot.InviteUserToChannel(ctx, threadID, userID); err != nil {
			e.logger.Error("inviting owner to task thread", "error", err, "thread_id", threadID, "user_id", userID)
		}
	}
	for _, userID := range perms.Members.Users {
		if err := e.bot.InviteUserToChannel(ctx, threadID, userID); err != nil {
			e.logger.Error("inviting member to task thread", "error", err, "thread_id", threadID, "user_id", userID)
		}
	}
}
