package api

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

func (s *ServerSuite) TestMoveTask() {
	local := types.PlatformLocal
	root := &db.Channel{ChannelID: "root", GuildID: "g1", DirPath: "/repo", Platform: local}
	wt := &db.Channel{ChannelID: "wt", ParentID: "root", GuildID: "g1", DirPath: "/repo-wt", Worktree: true, Platform: local}
	// A thread three levels down: tasks belong to its depth-1 parent.
	deep := &db.Channel{ChannelID: "deep", ParentID: "wt", GuildID: "g1", DirPath: "/repo-wt", Platform: local}
	plain := &db.Channel{ChannelID: "plain", ParentID: "root", GuildID: "g1", DirPath: "/repo", Platform: local}
	other := &db.Channel{ChannelID: "other", GuildID: "g1", DirPath: "/other", Platform: local}
	otherWt := &db.Channel{ChannelID: "other-wt", ParentID: "other", GuildID: "g1", DirPath: "/other-wt", Worktree: true, Platform: local}
	sharedThread := &db.Channel{ChannelID: "tt", ParentID: "wt", DirPath: "/repo-wt", SessionID: "sess", Platform: local}
	ownWorktree := &db.Channel{ChannelID: "tt", ParentID: "root", DirPath: "/repo-task", Worktree: true, SessionID: "sess", Platform: local}

	copied := func() *testutil.MockSystem {
		sys := new(testutil.MockSystem)
		sys.On("UserHomeDir").Return("/home/u", nil)
		src := filepath.Join("/home/u/.claude/projects", osutil.EncodeClaudeProjectPath("/repo-wt"), "sess.jsonl")
		dstDir := filepath.Join("/home/u/.claude/projects", osutil.EncodeClaudeProjectPath("/repo"))
		sys.On("ReadFile", src).Return([]byte("transcript"), nil).Once()
		sys.On("MkdirAll", dstDir, mock.Anything).Return(nil).Once()
		sys.On("WriteFile", filepath.Join(dstDir, "sess.jsonl"), []byte("transcript"), mock.Anything).Return(nil).Once()
		return sys
	}

	tests := []struct {
		name     string
		path     string
		body     string
		task     *db.ScheduledTask
		taskErr  error
		channels []*db.Channel
		missing  []string
		chanErr  map[string]error
		sys      func() *testutil.MockSystem
		move     *db.TaskMove
		moveErr  error
		hub      bool
		wantCode int
		wantMsg  string
	}{
		{
			name:     "shared thread follows from a worktree thread to the root",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{root, wt, sharedThread},
			sys:      copied,
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1", ThreadID: "tt", ThreadDirPath: "/repo"},
			hub:      true,
			wantCode: http.StatusOK,
		},
		{
			name:     "a task's own worktree keeps its dir when moving to a worktree thread",
			body:     `{"channel_id":"wt"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "root", ThreadID: "tt", Worktree: true},
			channels: []*db.Channel{root, wt, ownWorktree},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "wt", GuildID: "g1", ThreadID: "tt", ThreadDirPath: "/repo-task"},
			hub:      true,
			wantCode: http.StatusOK,
		},
		{
			name:     "a deeper thread resolves to its depth-1 parent",
			body:     `{"channel_id":"deep"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "root"},
			channels: []*db.Channel{root, wt, deep},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "wt", GuildID: "g1"},
			hub:      true,
			wantCode: http.StatusOK,
		},
		{
			name:     "a deleted thread isn't moved",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{root, wt},
			missing:  []string{"tt"},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1"},
			wantCode: http.StatusOK,
		},
		{
			name: "other platforms detach the thread",
			body: `{"channel_id":"root"}`,
			task: &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{
				{ChannelID: "root", GuildID: "g1", Platform: types.PlatformDiscord},
				{ChannelID: "wt", ParentID: "root", GuildID: "g1", Platform: types.PlatformDiscord},
			},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1"},
			wantCode: http.StatusOK,
		},
		{
			name:     "a thread already in the target's dir copies nothing",
			body:     `{"channel_id":"plain"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "root", ThreadID: "tt"},
			channels: []*db.Channel{root, plain, {ChannelID: "tt", ParentID: "root", DirPath: "/repo", SessionID: "sess", Platform: local}},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "plain", GuildID: "g1", ThreadID: "tt", ThreadDirPath: "/repo"},
			wantCode: http.StatusOK,
		},
		{
			name:     "a thread without a session copies nothing",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{root, wt, {ChannelID: "tt", ParentID: "wt", DirPath: "/repo-wt", Platform: local}},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1", ThreadID: "tt", ThreadDirPath: "/repo"},
			wantCode: http.StatusOK,
		},
		{
			name:     "a failed session copy still moves the task",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{root, wt, sharedThread},
			sys: func() *testutil.MockSystem {
				sys := new(testutil.MockSystem)
				sys.On("UserHomeDir").Return("", errors.New("no home"))
				return sys
			},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1", ThreadID: "tt", ThreadDirPath: "/repo"},
			wantCode: http.StatusOK,
		},
		{
			name:     "a sibling thread in the same project",
			body:     `{"channel_id":"plain"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt"},
			channels: []*db.Channel{root, wt, plain},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "plain", GuildID: "g1"},
			wantCode: http.StatusOK,
		},
		{
			name:     "source channel gone",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "gone"},
			channels: []*db.Channel{root},
			missing:  []string{"gone"},
			wantCode: http.StatusNotFound,
			wantMsg:  "task channel not found",
		},
		{
			name:     "already there",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "root", ThreadID: "tt"},
			channels: []*db.Channel{root},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid id",
			path:     "/api/tasks/abc/move",
			body:     `{"channel_id":"root"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "bad json",
			body:     `{`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing channel_id",
			body:     `{}`,
			wantCode: http.StatusBadRequest,
			wantMsg:  "channel_id is required",
		},
		{
			name:     "get task error",
			body:     `{"channel_id":"root"}`,
			taskErr:  errors.New("db down"),
			wantCode: http.StatusInternalServerError,
			wantMsg:  "db down",
		},
		{
			name:     "task not found",
			body:     `{"channel_id":"root"}`,
			wantCode: http.StatusNotFound,
			wantMsg:  "task not found",
		},
		{
			name:     "into its own thread",
			body:     `{"channel_id":"tt"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "root", ThreadID: "tt"},
			channels: []*db.Channel{root, ownWorktree},
			wantCode: http.StatusBadRequest,
			wantMsg:  "own thread",
		},
		{
			name:     "target lookup error",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt"},
			chanErr:  map[string]error{"root": errors.New("db down")},
			wantCode: http.StatusInternalServerError,
			wantMsg:  "db down",
		},
		{
			name:     "target not found",
			body:     `{"channel_id":"nope"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt"},
			missing:  []string{"nope"},
			wantCode: http.StatusNotFound,
			wantMsg:  "channel not found",
		},
		{
			name:     "source lookup error",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt"},
			channels: []*db.Channel{root},
			chanErr:  map[string]error{"wt": errors.New("db down")},
			wantCode: http.StatusInternalServerError,
			wantMsg:  "db down",
		},
		{
			name:     "from another project's root",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "other"},
			channels: []*db.Channel{root, other},
			wantCode: http.StatusBadRequest,
			wantMsg:  "another project",
		},
		{
			name:     "from a thread to another project's thread",
			body:     `{"channel_id":"other-wt"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{wt, other, otherWt},
			wantCode: http.StatusBadRequest,
			wantMsg:  "another project",
		},
		{
			name:     "another platform",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "slack"},
			channels: []*db.Channel{root, {ChannelID: "slack", Platform: types.PlatformSlack}},
			wantCode: http.StatusBadRequest,
			wantMsg:  "another project",
		},
		{
			name:     "thread lookup error",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt", ThreadID: "tt"},
			channels: []*db.Channel{root, wt},
			chanErr:  map[string]error{"tt": errors.New("db down")},
			wantCode: http.StatusInternalServerError,
			wantMsg:  "db down",
		},
		{
			name:     "running",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt"},
			channels: []*db.Channel{root, wt},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1"},
			moveErr:  db.ErrTaskRunning,
			wantCode: http.StatusConflict,
			wantMsg:  "task is running",
		},
		{
			name:     "move error",
			body:     `{"channel_id":"root"}`,
			task:     &db.ScheduledTask{ID: 42, ChannelID: "wt"},
			channels: []*db.Channel{root, wt},
			move:     &db.TaskMove{TaskID: 42, ChannelID: "root", GuildID: "g1"},
			moveErr:  errors.New("db down"),
			wantCode: http.StatusInternalServerError,
			wantMsg:  "db down",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			sys := new(testutil.MockSystem)
			if tc.sys != nil {
				sys = tc.sys()
			}
			s.srv.sys = sys
			if tc.hub {
				s.srv.SetEventsHub(NewEventsHub(slog.Default()))
			}
			s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(tc.task, tc.taskErr)
			for _, ch := range tc.channels {
				s.store.On("GetChannel", mock.Anything, ch.ChannelID).Return(ch, nil)
			}
			for _, id := range tc.missing {
				s.store.On("GetChannel", mock.Anything, id).Return(nil, nil)
			}
			for id, err := range tc.chanErr {
				s.store.On("GetChannel", mock.Anything, id).Return(nil, err)
			}
			if tc.move != nil {
				s.store.On("MoveScheduledTask", mock.Anything, *tc.move).Return(tc.moveErr).Once()
			}

			path := tc.path
			if path == "" {
				path = "/api/tasks/42/move"
			}
			rec := s.testRequest("POST", path, tc.body)

			require.Equal(s.T(), tc.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tc.wantMsg)
			s.store.AssertExpectations(s.T())
			sys.AssertExpectations(s.T())
		})
	}
}
