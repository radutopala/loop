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
)

// mockBashRunner is a Runner that also implements BashRunner, so bash
// scheduled-task tests can exercise executeBashTask through ExecuteTask.
type mockBashRunner struct {
	MockRunner
}

func (m *mockBashRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	args := m.Called(ctx, script, channelID, dirPath)
	return args.String(0), args.Error(1)
}

// newBashExecutor builds a TaskExecutor backed by a BashRunner-capable mock.
func (s *TaskExecutorSuite) newBashExecutor() (*TaskExecutor, *mockBashRunner) {
	br := new(mockBashRunner)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewTaskExecutor(br, s.bot, s.store, logger, 5*time.Minute, nil), br
}

func (s *TaskExecutorSuite) TestExecuteBashTask() {
	task := &db.ScheduledTask{ID: 7, ChannelID: "ch1", BashScript: "echo hello"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work/project"}, nil)
	br.On("RunBash", mock.Anything, "echo hello", "ch1", "/work/project").Return("hello\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && strings.Contains(out.Content, "task #7") && strings.Contains(out.Content, "hello")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(m *db.Message) bool { return m.IsBot })).Return(nil)

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello\n", resp)
	br.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteBashTaskEmptyOutput() {
	task := &db.ScheduledTask{ID: 8, ChannelID: "ch1", BashScript: "true"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work"}, nil)
	br.On("RunBash", mock.Anything, "true", "ch1", "/work").Return("   \n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "(no output)")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskTruncatesLongOutput() {
	task := &db.ScheduledTask{ID: 9, ChannelID: "ch1", BashScript: "yes | head"}
	exec, br := s.newBashExecutor()
	long := strings.Repeat("x", bashOutputMaxLen+500)

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work"}, nil)
	br.On("RunBash", mock.Anything, mock.Anything, "ch1", "/work").Return(long, nil)
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
	task := &db.ScheduledTask{ID: 10, ChannelID: "ch1", BashScript: "exit 1"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work"}, nil)
	br.On("RunBash", mock.Anything, "exit 1", "ch1", "/work").Return("partial out", errors.New("exit status 1"))
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "failed") && strings.Contains(out.Content, "partial out")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "bash task 10")
}

func (s *TaskExecutorSuite) TestExecuteBashTaskErrorNoOutput() {
	task := &db.ScheduledTask{ID: 11, ChannelID: "ch1", BashScript: "exit 1"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work"}, nil)
	br.On("RunBash", mock.Anything, "exit 1", "ch1", "/work").Return("", errors.New("boom"))
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "failed") && !strings.Contains(out.Content, "```")
	})).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	_, err := exec.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
}

func (s *TaskExecutorSuite) TestExecuteBashTaskRunnerNotCapable() {
	// The default MockRunner lacks RunBash — the executor must fail cleanly.
	task := &db.ScheduledTask{ID: 12, ChannelID: "ch1", BashScript: "echo hi"}
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "bash runner not available")
}

func (s *TaskExecutorSuite) TestExecuteBashTaskSendErrorLogged() {
	// A platform send failure must not fail the task — the output still
	// reaches the run log via the return value.
	task := &db.ScheduledTask{ID: 13, ChannelID: "ch1", BashScript: "echo hi"}
	exec, br := s.newBashExecutor()

	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", DirPath: "/work"}, nil)
	br.On("RunBash", mock.Anything, "echo hi", "ch1", "/work").Return("hi\n", nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(errors.New("send failed"))
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	resp, err := exec.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi\n", resp)
}
