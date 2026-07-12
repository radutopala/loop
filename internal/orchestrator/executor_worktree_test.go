package orchestrator

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/worktree"
)

// --- Worktree tests ---

func (s *TaskExecutorSuite) TestWorktreeFirstRun() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	// Mock worktree creator
	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				if args[1] == "--abbrev-ref" {
					return []byte("main\n"), nil
				}
			}
			return nil, nil // git worktree add succeeds
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeFirstRunOriginBranchPersistError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				if args[1] == "--abbrev-ref" {
					return []byte("main\n"), nil
				}
			}
			return nil, nil
		},
	})

	// Error persisting origin branch — should log but continue.
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(errors.New("db error"))

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeSubsequentRun() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, ThreadID: "wt-thread",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", Platform: types.PlatformLocal,
	}, nil)
	// Worktree subsequent run: get thread channel to get its DirPath
	s.store.On("GetChannel", s.ctx, "wt-thread").Return(&db.Channel{
		ChannelID: "wt-thread", DirPath: "/proj/.worktrees/task-10-abc", SessionID: "sess-wt",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{
		ID: 10, Type: db.TaskTypeCron, ThreadID: "wt-thread",
	}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.DirPath == "/proj/.worktrees/task-10-abc" && req.SessionID == "sess-wt"
	})).Return(&agent.AgentResponse{Response: "done2", SessionID: "s3"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "wt-thread", "s3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done2", resp)
}

func (s *TaskExecutorSuite) TestWorktreeDanglingThreadCreatesNewWorktree() {
	// Task has ThreadID pointing at a channel that no longer exists (e.g. the
	// thread was deleted from the UI without clearing the task's thread_id).
	// The executor must fall back to first-run behavior: create a new worktree
	// instead of reusing the parent channel's dirPath (which would cause a
	// duplicate Docker mount because dirPath == parentDirPath).
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, ThreadID: "stale-thread",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	// Dangling ThreadID — channel lookup returns nil.
	s.store.On("GetChannel", s.ctx, "stale-thread").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" && len(args) > 1 && args[1] == "--abbrev-ref" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		// DirPath must be the NEW worktree, not the parent channel's path.
		return strings.Contains(req.DirPath, ".worktrees/task-10-") && req.DirPath != "/proj"
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeCreationError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return []byte("fatal: error"), errors.New("exit 1")
		},
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating worktree for task 10")
}

func (s *TaskExecutorSuite) TestWorktreeBranchDetectionError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			return nil, errors.New("not a git repo")
		},
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting current branch")
}

func (s *TaskExecutorSuite) TestWorktreeDetachedHead() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
				return []byte("HEAD\n"), nil
			}
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("abc123\n"), nil
			}
			return nil, nil
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "abc123").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s4"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s4").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestWorktreeDetachedHeadFallbackError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
				return []byte("HEAD\n"), nil
			}
			// Second rev-parse HEAD fails
			return nil, errors.New("git error")
		},
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting current branch")
}

func (s *TaskExecutorSuite) TestWorktreeFalsePreservesExisting() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.DirPath == "/proj"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s5"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s5").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestWorktreeTaskSetsParentDirPath() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/proj" && strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestNonWorktreeTaskNoParentDirPath() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "" && req.DirPath == "/proj"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s5"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s5").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestNonWorktreeTaskOnWorktreeChannelSetsParentDirPath() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "wt-ch", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	// The task's channel is itself a worktree channel.
	s.store.On("GetChannel", s.ctx, "wt-ch").Return(&db.Channel{
		ChannelID: "wt-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "parent-ch", Worktree: true,
	}, nil)
	// Parent channel lookup returns the original project dir.
	s.store.On("GetChannel", s.ctx, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/proj" && req.DirPath == "/proj/.worktrees/wt-1"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s6"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt-ch", "s6").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

// TestTaskOnThreadUnderWorktreeSetsParentDirPath covers case 3: the task's
// channel is a THREAD under a worktree channel — it shares the worktree's
// dir_path but carries no worktree flag. The merge chain must be inherited
// from the worktree's own parent (the root project channel), otherwise the
// root's .loop/config.json (gates, model, MCP servers) is silently ignored.
func (s *TaskExecutorSuite) TestTaskOnThreadUnderWorktreeSetsParentDirPath() {
	task := &db.ScheduledTask{
		ID: 11, ChannelID: "thread-ch", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	// The task's channel is a plain thread whose parent is the worktree channel.
	s.store.On("GetChannel", s.ctx, "thread-ch").Return(&db.Channel{
		ChannelID: "thread-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "wt-ch", Worktree: false,
	}, nil)
	s.store.On("GetChannel", s.ctx, "wt-ch").Return(&db.Channel{
		ChannelID: "wt-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "root-ch", Worktree: true,
	}, nil)
	s.store.On("GetChannel", s.ctx, "root-ch").Return(&db.Channel{
		ChannelID: "root-ch", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(11)).Return(&db.ScheduledTask{ID: 11, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/proj" && req.DirPath == "/proj/.worktrees/wt-1"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s8"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-ch", "s8").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

// TestWorktreeTaskOnWorktreeChannelAnchorsAtRoot covers case 4: a worktree
// task scheduled ON a worktree channel creates a NESTED worktree. The config
// chain must anchor at the root checkout — not the intermediate worktree,
// whose untracked .loop/config.json doesn't exist — so the root's gates,
// model, and MCP servers still apply.
func (s *TaskExecutorSuite) TestWorktreeTaskOnWorktreeChannelAnchorsAtRoot() {
	task := &db.ScheduledTask{
		ID: 12, ChannelID: "wt-ch", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "wt-ch").Return(&db.Channel{
		ChannelID: "wt-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "root-ch", Worktree: true,
	}, nil)
	s.store.On("GetChannel", s.ctx, "root-ch").Return(&db.Channel{
		ChannelID: "root-ch", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(12)).Return(&db.ScheduledTask{ID: 12, Type: db.TaskTypeCron}, nil)
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(12), "main").Return(nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/proj" && strings.Contains(req.DirPath, ".worktrees/task-12-")
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s9"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt-ch", "s9").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestNonWorktreeTaskOnWorktreeChannelParentLookupError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "wt-ch", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	s.store.On("GetChannel", s.ctx, "wt-ch").Return(&db.Channel{
		ChannelID: "wt-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "parent-ch", Worktree: true,
	}, nil)
	// Parent lookup fails — parentDirPath stays empty (graceful fallback).
	s.store.On("GetChannel", s.ctx, "parent-ch").Return(nil, errors.New("db error"))
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "" && req.DirPath == "/proj/.worktrees/wt-1"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s7"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt-ch", "s7").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestWorktreeFirstRunWithOriginBranch() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "develop",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	var createdBranch string
	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			// Should NOT be called for rev-parse since OriginBranch is set.
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				s.Fail("getCurrentBranch should not be called when OriginBranch is set")
			}
			// Capture the branch used for worktree add.
			if name == "git" && len(args) > 2 && args[0] == "worktree" {
				createdBranch = args[len(args)-1] // last arg is the branch
			}
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
	require.Equal(s.T(), "develop", createdBranch)
}

func (s *TaskExecutorSuite) TestWorktreeFirstRunPersistsDetectedBranch() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "", // empty — auto-detect
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main")
}

func (s *TaskExecutorSuite) TestWorktreeUpdateBeforeRunSystemPrompt() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "main",
		UpdateBeforeRun: true, ThreadID: "thread-1",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetChannel", s.ctx, "thread-1").Return(&db.Channel{
		ChannelID: "thread-1", DirPath: "/proj/.worktrees/task-10-abc", SessionID: "s-thread",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{Sys: &mockWorktreeSys{}})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		msg := req.Messages[0].Content
		return strings.Contains(msg, "git rebase origin/main") &&
			strings.Contains(msg, "git fetch origin main") &&
			strings.Contains(msg, "git stash") &&
			strings.HasSuffix(msg, "build") &&
			!strings.Contains(req.SystemPrompt, "git rebase")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s3"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-1", "s3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeNoUpdatePromptWhenDisabled() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "main",
		UpdateBeforeRun: false, ThreadID: "thread-1",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetChannel", s.ctx, "thread-1").Return(&db.Channel{
		ChannelID: "thread-1", DirPath: "/proj/.worktrees/task-10-abc", SessionID: "s-thread",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{Sys: &mockWorktreeSys{}})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.Messages[0].Content == "build" && !strings.Contains(req.Messages[0].Content, "git rebase")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s3"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-1", "s3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

// mockWorktreeSys is a minimal System implementation for worktree tests.
type mockWorktreeSys struct{}

func (m *mockWorktreeSys) MkdirAll(string, os.FileMode) error          { return nil }
func (m *mockWorktreeSys) WriteFile(string, []byte, os.FileMode) error { return nil }
func (m *mockWorktreeSys) ReadFile(string) ([]byte, error)             { return nil, nil }
func (m *mockWorktreeSys) UserHomeDir() (string, error)                { return "/home/test", nil }

func (s *TaskExecutorSuite) TestRefreshConfigReloads() {
	called := false
	s.executor.configLoad = func() (*config.Config, error) {
		called = true
		return &config.Config{
			ContainerTimeout: 99 * time.Second,
		}, nil
	}

	timeout := s.executor.refreshConfig()
	require.True(s.T(), called)
	require.Equal(s.T(), 99*time.Second, timeout)
}

func (s *TaskExecutorSuite) TestRefreshConfigFallbackOnError() {
	// Set initial values.
	s.executor.containerTimeout.Store(int64(30 * time.Second))

	s.executor.configLoad = func() (*config.Config, error) {
		return nil, errors.New("reload failed")
	}

	timeout := s.executor.refreshConfig()
	require.Equal(s.T(), 30*time.Second, timeout)
}

func (s *TaskExecutorSuite) TestRefreshConfigNilLoader() {
	s.executor.containerTimeout.Store(int64(42 * time.Second))

	// configLoad is already nil from SetupTest (passed nil).
	timeout := s.executor.refreshConfig()
	require.Equal(s.T(), 42*time.Second, timeout)
}

// TestWorktreeFirstRunInjectsPromptIntoWorktreeThread closes the gap raised in
// review: a local task that wants a worktree, on its first streamed turn,
// creates a thread that must point at the WORKTREE (not the parent dir) and the
// injected prompt user message must land in that thread. CreateSimpleThread
// seeds the thread with the parent DirPath, but LinkTaskThread overwrites it
// with the worktree path before any message is stored.
func (s *TaskExecutorSuite) TestWorktreeFirstRunInjectsPromptIntoWorktreeThread() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build the thing", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetChannel", mock.Anything, "wt-thread").Return(&db.Channel{ID: 88, ChannelID: "wt-thread"}, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)
	s.store.On("UpdateSessionID", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(_ context.Context, _, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	// Empty initial message on local: the executor seeds the thread itself.
	s.bot.On("CreateSimpleThread", s.ctx, "ch1", mock.Anything, "").Return("wt-thread", nil).Once()
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil).Maybe()

	// The thread channel must be linked with the WORKTREE DirPath, not "/proj".
	var linkedDir string
	var linkedWorktree bool
	s.store.On("LinkTaskThread", mock.Anything, mock.MatchedBy(func(ch *db.Channel) bool {
		if ch.ChannelID != "wt-thread" {
			return false
		}
		linkedDir = ch.DirPath
		linkedWorktree = ch.Worktree
		return true
	}), int64(10), "wt-thread").Return(nil).Once()

	var inserted []*db.Message
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		inserted = append(inserted, args.Get(1).(*db.Message))
	}).Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil || !strings.Contains(req.DirPath, ".worktrees/task-10-") {
			return false
		}
		req.OnTurn("working on it")
		return true
	})).Return(&agent.AgentResponse{Response: "working on it", SessionID: "s2"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)

	// Thread points at the worktree, flagged as a worktree.
	require.Contains(s.T(), linkedDir, ".worktrees/task-10-")
	require.NotEqual(s.T(), "/proj", linkedDir)
	require.True(s.T(), linkedWorktree)

	// The prompt landed in the worktree thread as a user message, ahead of the reply.
	require.GreaterOrEqual(s.T(), len(inserted), 2)
	require.False(s.T(), inserted[0].IsBot)
	require.Equal(s.T(), "build the thing", inserted[0].Content)
	require.Equal(s.T(), "wt-thread", inserted[0].ChannelID)
	require.True(s.T(), inserted[1].IsBot)
}
