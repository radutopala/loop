package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/worktree"
)

// mockBashRunner is a Runner that also implements BashRunner, so bash
// scheduled-task tests can exercise executeBashTask through ExecuteTask.
type mockBashRunner struct {
	MockRunner
}

func (m *mockBashRunner) RunBash(ctx context.Context, script, channelID, dirPath, parentDirPath string) (string, error) {
	args := m.Called(ctx, script, channelID, dirPath, parentDirPath)
	return args.String(0), args.Error(1)
}

// newBashExecutor builds a TaskExecutor backed by a BashRunner-capable mock.
func (s *TaskExecutorSuite) newBashExecutor() (*TaskExecutor, *mockBashRunner) {
	br := new(mockBashRunner)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewTaskExecutor(br, s.bot, s.store, logger, 5*time.Minute, nil), br
}

// expectBashThreadCreation wires the mocks for a first-run thread creation on
// a local-platform channel: CreateSimpleThread + LinkTaskThread (recurring).
func (s *TaskExecutorSuite) expectBashThreadCreation(taskID int64, threadID string) {
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.MatchedBy(func(name string) bool {
		return strings.Contains(name, "task #")
	}), "").Return(threadID, nil)
	s.store.On("LinkTaskThread", mock.Anything, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == threadID && ch.ParentID == "ch1"
	}), taskID, threadID).Return(nil)
	// storeUserTaskPrompt resolves the thread channel for the chat row.
	s.store.On("GetChannel", mock.Anything, threadID).Return(&db.Channel{ID: 9, ChannelID: threadID, ParentID: "ch1"}, nil).Maybe()
}

func localChannel(dir string) *db.Channel {
	return &db.Channel{ID: 1, ChannelID: "ch1", DirPath: dir, Platform: types.PlatformLocal}
}

func (s *TaskExecutorSuite) TestExecuteBashTaskCreatesThread() {
	task := &db.ScheduledTask{ID: 7, ChannelID: "ch1", BashScript: "echo hello", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work/project"), nil)
	s.expectBashThreadCreation(7, "th-7")
	br.On("RunBash", mock.Anything, "echo hello", "ch1", "/work/project", "").Return("hello\n", nil)
	// Output goes to the thread, not the channel.
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "th-7" && strings.Contains(out.Content, "task #7") && strings.Contains(out.Content, "hello")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello\n", resp)
	require.Equal(s.T(), "th-7", task.ThreadID)
	br.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteBashTaskReusesThread() {
	// Second run: ThreadID already set — no thread creation, output to thread.
	task := &db.ScheduledTask{ID: 8, ChannelID: "ch1", BashScript: "true", ThreadID: "th-8", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.store.On("GetChannel", mock.Anything, "th-8").Return(&db.Channel{ID: 9, ChannelID: "th-8", ParentID: "ch1", DirPath: "/work"}, nil)
	br.On("RunBash", mock.Anything, "true", "ch1", "/work", "").Return("   \n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "th-8" && strings.Contains(out.Content, "(no output)")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.bot.AssertNotCalled(s.T(), "CreateSimpleThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskThreadDeletedRecreates() {
	// The remembered thread is gone — treat as first run and recreate.
	task := &db.ScheduledTask{ID: 9, ChannelID: "ch1", BashScript: "true", ThreadID: "th-gone", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.store.On("GetChannel", mock.Anything, "th-gone").Return(nil, nil)
	s.expectBashThreadCreation(9, "th-new")
	br.On("RunBash", mock.Anything, "true", "ch1", "/work", "").Return("", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "th-new"
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "th-new", task.ThreadID)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskOnceDoesNotLinkThread() {
	// once tasks get a thread but don't persist the link (they auto-disable).
	task := &db.ScheduledTask{ID: 10, ChannelID: "ch1", BashScript: "true", Type: db.TaskTypeOnce, Schedule: "2026-01-01T00:00:00Z"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.Anything, "").Return("th-once", nil)
	s.store.On("UpsertChannel", mock.Anything, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "th-once"
	})).Return(nil)
	s.store.On("GetChannel", mock.Anything, "th-once").Return(&db.Channel{ID: 9, ChannelID: "th-once"}, nil).Maybe()
	br.On("RunBash", mock.Anything, "true", "ch1", "/work", "").Return("", nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Empty(s.T(), task.ThreadID)
	s.store.AssertNotCalled(s.T(), "LinkTaskThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskThreadCreateFailsFallsBackToChannel() {
	task := &db.ScheduledTask{ID: 11, ChannelID: "ch1", BashScript: "echo hi", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.Anything, "").Return("", errors.New("thread create failed"))
	br.On("RunBash", mock.Anything, "echo hi", "ch1", "/work", "").Return("hi\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && strings.Contains(out.Content, "hi")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi\n", resp)
	require.Empty(s.T(), task.ThreadID)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskNoChannelRowPersistsThreadID() {
	// Channel row missing: the thread is still linked via the task row.
	task := &db.ScheduledTask{ID: 12, ChannelID: "ch1", BashScript: "true", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.Anything, "").Return("th-12", nil)
	s.store.On("UpdateScheduledTaskThreadID", mock.Anything, int64(12), "th-12").Return(nil)
	s.store.On("GetChannel", mock.Anything, "th-12").Return(nil, nil).Maybe()
	br.On("RunBash", mock.Anything, "true", "ch1", "", "").Return("", nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "th-12", task.ThreadID)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskTruncatesLongOutput() {
	task := &db.ScheduledTask{ID: 13, ChannelID: "ch1", BashScript: "yes | head", ThreadID: "th-13", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()
	long := strings.Repeat("x", bashOutputMaxLen+500)

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.store.On("GetChannel", mock.Anything, "th-13").Return(&db.Channel{ID: 9, ChannelID: "th-13"}, nil)
	br.On("RunBash", mock.Anything, mock.Anything, "ch1", "/work", "").Return(long, nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "(truncated)") && len(out.Content) < bashOutputMaxLen+200
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	// The run log still receives the full output.
	require.Len(s.T(), resp, len(long))
}

func (s *TaskExecutorSuite) TestExecuteBashTaskError() {
	task := &db.ScheduledTask{ID: 14, ChannelID: "ch1", BashScript: "exit 1", ThreadID: "th-14", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.store.On("GetChannel", mock.Anything, "th-14").Return(&db.Channel{ID: 9, ChannelID: "th-14"}, nil)
	br.On("RunBash", mock.Anything, "exit 1", "ch1", "/work", "").Return("partial out", errors.New("exit status 1"))
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "th-14" && strings.Contains(out.Content, "failed") && strings.Contains(out.Content, "partial out")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "bash task 14")
}

func (s *TaskExecutorSuite) TestExecuteBashTaskErrorNoOutput() {
	task := &db.ScheduledTask{ID: 15, ChannelID: "ch1", BashScript: "exit 1", ThreadID: "th-15", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.store.On("GetChannel", mock.Anything, "th-15").Return(&db.Channel{ID: 9, ChannelID: "th-15"}, nil)
	br.On("RunBash", mock.Anything, "exit 1", "ch1", "/work", "").Return("", errors.New("boom"))
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "failed") && !strings.Contains(out.Content, "```")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskRunnerNotCapable() {
	// The default MockRunner lacks RunBash — the executor must fail cleanly.
	task := &db.ScheduledTask{ID: 16, ChannelID: "ch1", BashScript: "echo hi"}
	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "bash runner not available")
}

func (s *TaskExecutorSuite) TestExecuteBashTaskSendErrorLogged() {
	// A platform send failure must not fail the task — the output still
	// reaches the run log via the return value.
	task := &db.ScheduledTask{ID: 17, ChannelID: "ch1", BashScript: "echo hi", ThreadID: "th-17", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.store.On("GetChannel", mock.Anything, "th-17").Return(&db.Channel{ID: 9, ChannelID: "th-17"}, nil)
	br.On("RunBash", mock.Anything, "echo hi", "ch1", "/work", "").Return("hi\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(errors.New("send failed"))
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi\n", resp)
}

// --- bash + worktree (shared thread-keyed worktree block) ---

func (s *TaskExecutorSuite) TestExecuteBashTaskWorktreeFirstRun() {
	// First run with worktree: the shared block creates .worktrees/task-18-<hex>,
	// the thread is created as a worktree thread (Worktree=true, DirPath=wt).
	task := &db.ScheduledTask{ID: 18, ChannelID: "ch1", BashScript: "make build", Worktree: true, Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()
	exec.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil // git worktree add succeeds
		},
	})

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work/project"), nil)
	s.store.On("UpdateScheduledTaskOriginBranch", mock.Anything, int64(18), "main").Return(nil)
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.Anything, "").Return("th-18", nil)
	s.store.On("LinkTaskThread", mock.Anything, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "th-18" && ch.Worktree && strings.Contains(ch.DirPath, ".worktrees/task-18-")
	}), int64(18), "th-18").Return(nil)
	s.store.On("GetChannel", mock.Anything, "th-18").Return(&db.Channel{ID: 9, ChannelID: "th-18"}, nil).Maybe()
	// The script runs in the new worktree but must still resolve the root
	// project's .loop/config.json (mounts, image, gates) — the worktree's own
	// copy is untracked and holds only extra_dirs.
	br.On("RunBash", mock.Anything, "make build", "ch1", mock.MatchedBy(func(dir string) bool {
		return strings.Contains(dir, "/work/project/.worktrees/task-18-")
	}), "/work/project").Return("ok\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "th-18"
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "th-18", task.ThreadID)
	br.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteBashTaskWorktreeReuseViaThread() {
	// Recurring run: the thread remembers the worktree via its DirPath — no
	// git commands run at all.
	task := &db.ScheduledTask{ID: 19, ChannelID: "ch1", BashScript: "make test", Worktree: true, ThreadID: "th-19", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()
	exec.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			s.T().Fatalf("no git command expected on reuse, got: %s %v", name, args)
			return nil, nil
		},
	})

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work/project"), nil)
	s.store.On("GetChannel", mock.Anything, "th-19").Return(&db.Channel{
		ID: 9, ChannelID: "th-19", ParentID: "ch1", Worktree: true,
		DirPath: "/work/project/.worktrees/task-19-abcd",
	}, nil)
	br.On("RunBash", mock.Anything, "make test", "ch1", "/work/project/.worktrees/task-19-abcd", "/work/project").Return("ok\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "th-19"
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	br.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteBashTaskWorktreeBranchDetectError() {
	task := &db.ScheduledTask{ID: 20, ChannelID: "ch1", BashScript: "true", Worktree: true, Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, _ := s.newBashExecutor()
	exec.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			return []byte("fatal: not a git repository"), errors.New("exit 128")
		},
	})

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work/project"), nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting current branch")
}

func (s *TaskExecutorSuite) TestExecuteBashTaskManualThreadNameAndBroadcast() {
	// Manual tasks label the thread "manual" (no schedule), and a configured
	// event broadcaster is notified about the new thread.
	task := &db.ScheduledTask{ID: 30, ChannelID: "ch1", BashScript: "true", Type: db.TaskTypeManual}
	exec, br := s.newBashExecutor()
	eb := new(MockEventBroadcaster)
	exec.SetEventBroadcaster(eb)
	eb.On("BroadcastChannelCreated", "ch1", "th-30").Return().Once()
	eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return().Maybe()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(localChannel("/work"), nil)
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.MatchedBy(func(name string) bool {
		return strings.Contains(name, "(`manual`)")
	}), "").Return("th-30", nil)
	s.store.On("LinkTaskThread", mock.Anything, mock.Anything, int64(30), "th-30").Return(nil)
	s.store.On("GetChannel", mock.Anything, "th-30").Return(&db.Channel{ID: 9, ChannelID: "th-30"}, nil).Maybe()
	br.On("RunBash", mock.Anything, "true", "ch1", "/work", "").Return("", nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteBashTaskOnWorktreeChannelResolvesRootProject() {
	// A plain (non-worktree) bash task scheduled on a channel that already IS
	// a worktree. Its dir is the worktree checkout, whose .loop/config.json is
	// untracked and normally carries only extra_dirs, so the container layer
	// must be handed the root project dir to merge against — otherwise the
	// project's mounts, image, and gates silently fall back to the globals.
	task := &db.ScheduledTask{ID: 21, ChannelID: "wt1", BashScript: "make sync", ThreadID: "th-21", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "wt1").Return(&db.Channel{
		ID: 2, ChannelID: "wt1", ParentID: "root", Worktree: true,
		DirPath: "/work/project/.worktrees/feature", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "root").Return(localChannel("/work/project"), nil)
	s.store.On("GetChannel", mock.Anything, "th-21").Return(&db.Channel{ID: 9, ChannelID: "th-21", ParentID: "wt1"}, nil)
	br.On("RunBash", mock.Anything, "make sync", "wt1", "/work/project/.worktrees/feature", "/work/project").Return("synced\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "synced\n", resp)
	br.AssertExpectations(s.T())
}
