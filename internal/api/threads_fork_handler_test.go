package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
)

func (s *ServerSuite) TestForkThread_Plain() {
	s.srv.SetEventsHub(NewEventsHub(s.srv.logger))
	s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{
		ChannelID: "t1", ParentID: "ch1", Name: "research", SessionID: "sess-1",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", "research (fork)", "", "").Return("t2", nil)
	s.store.On("MarkSessionForkPending", mock.Anything, "t2", "sess-1").Return(nil)
	// importSessionMessages walks parent/thread lookups; missing session file
	// on disk short-circuits it harmlessly.
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/proj"}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "t2").Return(&db.Channel{ChannelID: "t2", ParentID: "ch1"}, nil).Maybe()
	s.sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()

	rec := s.testRequest("POST", "/api/threads/t1/fork", "")
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	var resp forkThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "t2", resp.ThreadID)
	require.Empty(s.T(), resp.WorktreePath)
	s.threads.AssertExpectations(s.T())
	s.store.AssertCalled(s.T(), "MarkSessionForkPending", mock.Anything, "t2", "sess-1")
}

func (s *ServerSuite) TestForkThread_PlainWithoutSessionSkipsCopy() {
	s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{
		ChannelID: "t1", ParentID: "ch1", Name: "research",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", "research (fork)", "", "").Return("t2", nil)

	rec := s.testRequest("POST", "/api/threads/t1/fork", "")
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, "t2", mock.Anything)
}

func (s *ServerSuite) TestForkThread_Errors() {
	// Not a thread (no parent).
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil).Once()
	rec := s.testRequest("POST", "/api/threads/ch1/fork", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	// Unknown thread.
	s.store.On("GetChannel", mock.Anything, "nope").Return(nil, nil).Once()
	rec = s.testRequest("POST", "/api/threads/nope/fork", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	// Store error.
	s.store.On("GetChannel", mock.Anything, "boom").Return(nil, errors.New("db down")).Once()
	rec = s.testRequest("POST", "/api/threads/boom/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	// CreateThread error.
	s.store.On("GetChannel", mock.Anything, "t9").Return(&db.Channel{ChannelID: "t9", ParentID: "ch1", Name: "x"}, nil).Once()
	s.threads.On("CreateThread", mock.Anything, "ch1", "x (fork)", "", "").Return("", errors.New("nope")).Once()
	rec = s.testRequest("POST", "/api/threads/t9/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestForkThread_Worktree() {
	dir := initGitRepo(s.T())
	// Create the SOURCE worktree for real so its branch exists to fork from.
	cmd := exec.Command("git", "worktree", "add", "-b", "worktree/src-wt", dir+"/.worktrees/src-wt")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))
	s.srv.sys = s.sys
	s.srv.SetEventsHub(NewEventsHub(s.srv.logger))
	s.sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()

	s.store.On("GetChannel", mock.Anything, "wt1").Return(&db.Channel{
		ChannelID: "wt1", ParentID: "ch1", Name: "src", Worktree: true,
		DirPath: dir + "/.worktrees/src-wt", SessionID: "sess-1",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.MatchedBy(func(name string) bool {
		return strings.Contains(name, "fork of worktree/src-wt")
	}), "", "").Return("wt2", nil)
	s.store.On("GetChannel", mock.Anything, "wt2").Return(&db.Channel{ChannelID: "wt2", ParentID: "ch1", Active: true}, nil)
	s.store.On("MarkSessionForkPending", mock.Anything, "wt2", "sess-1").Return(nil)
	upserted := make(chan *db.Channel, 1)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		select {
		case upserted <- args.Get(1).(*db.Channel):
		default:
		}
	}).Return(nil)

	rec := s.testRequest("POST", "/api/threads/wt1/fork", "")
	require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
	var resp forkThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "wt2", resp.ThreadID)
	require.Contains(s.T(), resp.WorktreePath, ".worktrees/")

	ch := <-upserted
	require.True(s.T(), ch.Worktree)
	require.Equal(s.T(), "worktree/src-wt", ch.BaseBranch, "fork diffs against the source branch")
	require.Equal(s.T(), resp.WorktreePath, ch.DirPath)
	s.store.AssertCalled(s.T(), "MarkSessionForkPending", mock.Anything, "wt2", "sess-1")
	s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, "wt2", mock.Anything)
	// The source's transcript sits under its own worktree's project dir, not
	// the parent channel's.
	srcTranscript := filepath.Join("/home/testuser", ".claude", "projects", osutil.EncodeClaudeProjectPath(dir+"/.worktrees/src-wt"), "sess-1.jsonl")
	s.sys.AssertCalled(s.T(), "ReadFile", srcTranscript)
	s.sys.AssertCalled(s.T(), "Open", srcTranscript)
}

func (s *ServerSuite) TestForkThread_NotConfigured() {
	s.srv.store = nil
	rec := s.testRequest("POST", "/api/threads/t1/fork", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)

	s.srv.store = s.store
	s.srv.threads = nil
	rec = s.testRequest("POST", "/api/threads/t1/fork", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestForkThread_UpdateSessionError() {
	s.srv.SetEventsHub(NewEventsHub(s.srv.logger))
	s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{
		ChannelID: "t1", ParentID: "ch1", Name: "n", SessionID: "sess-1",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", "n (fork)", "", "").Return("t2", nil)
	s.store.On("MarkSessionForkPending", mock.Anything, "t2", "sess-1").Return(errors.New("db")).Once()
	rec := s.testRequest("POST", "/api/threads/t1/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestForkThread_WorktreeErrors() {
	s.srv.SetEventsHub(NewEventsHub(s.srv.logger))
	// Parent lookup error.
	s.store.On("GetChannel", mock.Anything, "wtE").Return(&db.Channel{
		ChannelID: "wtE", ParentID: "pE", Worktree: true, DirPath: "/x/.worktrees/a",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "pE").Return(nil, errors.New("db")).Once()
	rec := s.testRequest("POST", "/api/threads/wtE/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	// Parent without dir_path.
	s.store.On("GetChannel", mock.Anything, "pE").Return(&db.Channel{ChannelID: "pE"}, nil).Once()
	rec = s.testRequest("POST", "/api/threads/wtE/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	// The source's HEAD can't be resolved (its dir is gone).
	s.store.On("GetChannel", mock.Anything, "pE").Return(&db.Channel{ChannelID: "pE", DirPath: s.T().TempDir()}, nil)
	rec = s.testRequest("POST", "/api/threads/wtE/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "resolving HEAD of /x/.worktrees/a failed")
}

func (s *ServerSuite) TestForkThread_WorktreeCreateError() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "worktree", "add", "-b", "worktree/cerr", dir+"/.worktrees/cerr")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))

	s.store.On("GetChannel", mock.Anything, "wtC").Return(&db.Channel{
		ChannelID: "wtC", ParentID: "pC", Worktree: true, DirPath: dir + "/.worktrees/cerr",
	}, nil)
	// The parent's dir isn't a git repo, so `git worktree add` fails.
	s.store.On("GetChannel", mock.Anything, "pC").Return(&db.Channel{ChannelID: "pC", DirPath: s.T().TempDir()}, nil)
	rec := s.testRequest("POST", "/api/threads/wtC/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "git worktree add failed")
}

// TestForkThread_WorktreeCheckedOutRef: the source worktree's branch isn't
// worktree/<dir name> once an agent renames it, and a worktree can sit on a
// detached HEAD. The fork branches from what the source has checked out.
func (s *ServerSuite) TestForkThread_WorktreeCheckedOutRef() {
	tests := []struct {
		name  string
		setup [][]string // git commands run in the source worktree
		want  func(dir string) string
	}{
		{
			name:  "renamed branch",
			setup: [][]string{{"branch", "-m", "feat/renamed"}},
			want:  func(string) string { return "feat/renamed" },
		},
		{
			name:  "detached HEAD",
			setup: [][]string{{"checkout", "--detach"}},
			want: func(dir string) string {
				out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
				require.NoError(s.T(), err)
				return strings.TrimSpace(string(out))
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			dir := initGitRepo(s.T())
			src := dir + "/.worktrees/wt-src"
			cmd := exec.Command("git", "worktree", "add", "-b", "worktree/wt-src", src)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			require.NoError(s.T(), err, string(out))
			for _, args := range tc.setup {
				out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput()
				require.NoError(s.T(), err, string(out))
			}
			want := tc.want(src)

			s.store.On("GetChannel", mock.Anything, "wt1").Return(&db.Channel{
				ChannelID: "wt1", ParentID: "ch1", Name: "src", Worktree: true, DirPath: src,
			}, nil)
			s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
			s.threads.On("CreateThread", mock.Anything, "ch1", mock.MatchedBy(func(name string) bool {
				return strings.HasSuffix(name, "(fork of "+want+")")
			}), "", "").Return("wt2", nil)
			s.store.On("GetChannel", mock.Anything, "wt2").Return(&db.Channel{ChannelID: "wt2", ParentID: "ch1"}, nil)
			upserted := make(chan *db.Channel, 1)
			s.store.On("UpsertChannel", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				upserted <- args.Get(1).(*db.Channel)
			}).Return(nil)
			s.store.On("UpdateSessionID", mock.Anything, "wt2", "").Return(nil)

			rec := s.testRequest("POST", "/api/threads/wt1/fork", "")
			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			ch := <-upserted
			require.Equal(s.T(), want, ch.BaseBranch)
			head, err := exec.Command("git", "-C", ch.DirPath, "rev-parse", "HEAD").Output()
			require.NoError(s.T(), err)
			srcHead, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
			require.NoError(s.T(), err)
			require.Equal(s.T(), string(srcHead), string(head), "the fork starts at the source's commit")
		})
	}
}

func (s *ServerSuite) TestForkThread_WorktreeThreadRowErrors() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "worktree", "add", "-b", "worktree/errsrc", dir+"/.worktrees/errsrc")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))

	src := &db.Channel{ChannelID: "wtR", ParentID: "chR", Name: "s", Worktree: true, DirPath: dir + "/.worktrees/errsrc"}
	s.store.On("GetChannel", mock.Anything, "wtR").Return(src, nil)
	s.store.On("GetChannel", mock.Anything, "chR").Return(&db.Channel{ChannelID: "chR", DirPath: dir}, nil)

	// CreateThread error.
	s.threads.On("CreateThread", mock.Anything, "chR", mock.Anything, "", "").Return("", errors.New("nope")).Once()
	rec := s.testRequest("POST", "/api/threads/wtR/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	// New thread row lookup fails.
	s.threads.On("CreateThread", mock.Anything, "chR", mock.Anything, "", "").Return("wtR2", nil)
	s.store.On("GetChannel", mock.Anything, "wtR2").Return(nil, nil).Once()
	rec = s.testRequest("POST", "/api/threads/wtR/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	// Upsert fails.
	s.store.On("GetChannel", mock.Anything, "wtR2").Return(&db.Channel{ChannelID: "wtR2", ParentID: "chR"}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(errors.New("db")).Once()
	rec = s.testRequest("POST", "/api/threads/wtR/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)

	// Clearing the inherited session fails.
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateSessionID", mock.Anything, "wtR2", "").Return(errors.New("db")).Once()
	rec = s.testRequest("POST", "/api/threads/wtR/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestForkThread_WorktreeMarkForkPendingError() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "worktree", "add", "-b", "worktree/mark-wt", dir+"/.worktrees/mark-wt")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))
	s.srv.sys = s.sys

	s.store.On("GetChannel", mock.Anything, "wt1").Return(&db.Channel{
		ChannelID: "wt1", ParentID: "ch1", Name: "src", Worktree: true,
		DirPath: dir + "/.worktrees/mark-wt", SessionID: "sess-1",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("wt2", nil)
	s.store.On("GetChannel", mock.Anything, "wt2").Return(&db.Channel{ChannelID: "wt2", ParentID: "ch1"}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)
	s.store.On("MarkSessionForkPending", mock.Anything, "wt2", "sess-1").Return(errors.New("db"))

	rec := s.testRequest("POST", "/api/threads/wt1/fork", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "db")
}

// TestForkThread_WorktreeSessionNotStaged: Claude Code prunes transcripts
// after 30 days while the channel keeps pinning the id, so the copy into the
// new worktree's project dir can fail. The fork must then start clean —
// pinning the id anyway makes every turn die on `--resume`.
func (s *ServerSuite) TestForkThread_WorktreeSessionNotStaged() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "worktree", "add", "-b", "worktree/stale-wt", dir+"/.worktrees/stale-wt")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))

	// The transcript is gone from ~/.claude/projects, so the copy fails.
	s.srv.sys = s.sys
	s.sys.Override("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	s.store.On("GetChannel", mock.Anything, "wt1").Return(&db.Channel{
		ChannelID: "wt1", ParentID: "ch1", Name: "src", Worktree: true,
		DirPath: dir + "/.worktrees/stale-wt", SessionID: "sess-pruned",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: dir}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("wt2", nil)
	// The new thread row carries the session it inherited from the channel.
	s.store.On("GetChannel", mock.Anything, "wt2").Return(&db.Channel{ChannelID: "wt2", ParentID: "ch1", Active: true, SessionID: "sess-channel"}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateSessionID", mock.Anything, "wt2", "").Return(nil)

	rec := s.testRequest("POST", "/api/threads/wt1/fork", "")
	require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())

	// Upserting an empty id keeps the stored one, so it's cleared explicitly.
	s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, "wt2", "")
	s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, "wt2", mock.Anything)
}
