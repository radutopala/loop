package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/osutil"
)

type mockSystem struct {
	mkdirAllFn  func(string, os.FileMode) error
	writeFileFn func(string, []byte, os.FileMode) error
	readFileFn  func(string) ([]byte, error)
	homeDirFn   func() (string, error)

	mkdirAllCalls  []string
	writeFileCalls []writeFileCall
}

type writeFileCall struct {
	name string
	data []byte
}

func (m *mockSystem) MkdirAll(path string, perm os.FileMode) error {
	m.mkdirAllCalls = append(m.mkdirAllCalls, path)
	if m.mkdirAllFn != nil {
		return m.mkdirAllFn(path, perm)
	}
	return nil
}

func (m *mockSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	m.writeFileCalls = append(m.writeFileCalls, writeFileCall{name, data})
	if m.writeFileFn != nil {
		return m.writeFileFn(name, data, perm)
	}
	return nil
}

func (m *mockSystem) MkdirAllIn(_, path string, perm os.FileMode) error {
	return m.MkdirAll(path, perm)
}

func (m *mockSystem) WriteFileIn(_, name string, data []byte, perm os.FileMode) error {
	return m.WriteFile(name, data, perm)
}

func (m *mockSystem) ReadFile(name string) ([]byte, error) {
	if m.readFileFn != nil {
		return m.readFileFn(name)
	}
	return nil, nil
}

func (m *mockSystem) UserHomeDir() (string, error) {
	if m.homeDirFn != nil {
		return m.homeDirFn()
	}
	return "/home/testuser", nil
}

func TestExecCommandRunner(t *testing.T) {
	out, err := ExecCommandRunner(context.Background(), ".", "echo", "hello")
	require.NoError(t, err)
	require.Contains(t, string(out), "hello")
}

func TestExecCommandRunnerHardensGit(t *testing.T) {
	out, err := ExecCommandRunner(context.Background(), t.TempDir(), "git", "config", "--get", "core.hooksPath")
	require.NoError(t, err)
	require.Equal(t, "/dev/null\n", string(out))
}

func TestExecCommandRunnerWithEnv(t *testing.T) {
	run := ExecCommandRunnerWithEnv(func() []string { return []string{"HTTPS_PROXY=http://proxy:3128"} })
	tests := []struct {
		name string
		cmd  string
		args []string
		want string
	}{
		{name: "other commands keep the daemon's environment", cmd: "sh", args: []string{"-c", `echo "$HTTPS_PROXY ${PATH:+path}"`}, want: "http://proxy:3128 path\n"},
		{name: "git stays hardened", cmd: "git", args: []string{"config", "--get", "core.hooksPath"}, want: "/dev/null\n"},
		{name: "git gets the variables", cmd: "git", args: []string{"-c", `alias.proxy=!echo "$HTTPS_PROXY"`, "proxy"}, want: "http://proxy:3128\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(context.Background(), t.TempDir(), tt.cmd, tt.args...)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(out))
		})
	}
}

type CreatorSuite struct {
	suite.Suite
	sys     *mockSystem
	creator *Creator
	runErr  error
	runOut  []byte
	runArgs [][]string
}

func TestCreatorSuite(t *testing.T) {
	suite.Run(t, new(CreatorSuite))
}

func (s *CreatorSuite) SetupTest() {
	s.sys = &mockSystem{}
	s.runErr = nil
	s.runOut = nil
	s.runArgs = nil
	s.creator = &Creator{
		Sys: s.sys,
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			s.runArgs = append(s.runArgs, append([]string{dir, name}, args...))
			return s.runOut, s.runErr
		},
	}
}

func (s *CreatorSuite) TestCreateSuccess() {
	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "")

	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join("/proj", ".worktrees", "wt-abc"), result.WorktreePath)
	require.Equal(s.T(), "worktree/wt-abc", result.BranchName)
	require.False(s.T(), result.SessionStaged, "no session was asked for")

	require.Len(s.T(), s.runArgs, 1)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "add", "-b", "worktree/wt-abc", filepath.Join("/proj", ".worktrees", "wt-abc"), "main"}, s.runArgs[0])

	require.Len(s.T(), s.sys.mkdirAllCalls, 1)
	require.Contains(s.T(), s.sys.mkdirAllCalls[0], ".loop")
	require.Len(s.T(), s.sys.writeFileCalls, 1)
	require.Contains(s.T(), string(s.sys.writeFileCalls[0].data), "extra_dirs")
	require.Contains(s.T(), string(s.sys.writeFileCalls[0].data), "/proj")
}

// A branch can carry a .loop/config.json that's a symlink to a host file:
// seeding the worktree's config leaves that file as it was.
func (s *CreatorSuite) TestCreateSeedStaysInTheWorktree() {
	host := filepath.Join(s.T().TempDir(), "file")
	require.NoError(s.T(), os.WriteFile(host, []byte("host"), 0o644))
	proj := s.T().TempDir()
	s.creator.Sys = osutil.RealSystem{}
	s.creator.Run = func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		loopDir := filepath.Join(args[4], ".loop")
		require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
		return nil, os.Symlink(host, filepath.Join(loopDir, "config.json"))
	}

	_, err := s.creator.Create(context.Background(), proj, "main", "wt-abc", "")

	require.NoError(s.T(), err)
	data, err := os.ReadFile(host)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "host", string(data))
}

func (s *CreatorSuite) TestCreateWithSessionCopy() {
	s.sys.readFileFn = func(name string) ([]byte, error) {
		return []byte("session data"), nil
	}

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "sess-123")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)

	require.True(s.T(), result.SessionStaged)

	// Config write + session file write
	require.Len(s.T(), s.sys.writeFileCalls, 2)
	require.Contains(s.T(), s.sys.writeFileCalls[1].name, "sess-123.jsonl")
}

func (s *CreatorSuite) TestCreateGitError() {
	s.runErr = fmt.Errorf("exit status 1")
	s.runOut = []byte("fatal: branch already exists")

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "")

	require.Error(s.T(), err)
	require.Nil(s.T(), result)
	require.Contains(s.T(), err.Error(), "fatal: branch already exists")
}

func (s *CreatorSuite) TestCreateMkdirError() {
	s.sys.mkdirAllFn = func(string, os.FileMode) error {
		return fmt.Errorf("permission denied")
	}

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)
	require.Empty(s.T(), s.sys.writeFileCalls)
}

func (s *CreatorSuite) TestCreateWriteConfigError() {
	s.sys.writeFileFn = func(string, []byte, os.FileMode) error {
		return fmt.Errorf("disk full")
	}

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)
}

func (s *CreatorSuite) TestCreateSessionCopyReadError() {
	s.sys.readFileFn = func(string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "sess-1")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)
	// The worktree is usable, but it has no conversation to fork — callers
	// must not pin the session id on it.
	require.False(s.T(), result.SessionStaged)
}

func (s *CreatorSuite) TestCreateSessionCopyHomeDirError() {
	s.sys.homeDirFn = func() (string, error) {
		return "", fmt.Errorf("no home")
	}

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "sess-1")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)
}

func (s *CreatorSuite) TestCreateSessionCopyInvalidSessionID() {
	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "..")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)
}

func (s *CreatorSuite) TestCreateSessionCopyMkdirDstError() {
	s.sys.readFileFn = func(string) ([]byte, error) {
		return []byte("data"), nil
	}
	callCount := 0
	s.sys.mkdirAllFn = func(string, os.FileMode) error {
		callCount++
		if callCount > 1 {
			return fmt.Errorf("cannot create dst dir")
		}
		return nil
	}

	result, err := s.creator.Create(context.Background(), "/proj", "main", "wt-abc", "sess-1")

	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)
}

func (s *CreatorSuite) TestCreateWith() {
	wtPath := filepath.Join("/proj", ".worktrees", "pr-12")
	tests := []struct {
		name string
		// fails maps a git subcommand to the error it returns.
		fails       map[string]error
		checkoutErr error
		wantErr     string
		wantGit     []string
	}{
		{name: "checked out", wantGit: []string{"worktree", "symbolic-ref"}},
		{name: "worktree add fails", fails: map[string]error{"worktree add": errors.New("exit 128")}, wantErr: "git worktree add failed", wantGit: []string{"worktree"}},
		{name: "checkout fails", checkoutErr: errors.New("already checked out"), wantErr: "already checked out", wantGit: []string{"worktree", "worktree", "worktree"}},
		{
			name: "checkout and cleanup fail", checkoutErr: errors.New("already checked out"),
			fails:   map[string]error{"worktree remove": errors.New("exit 1")},
			wantErr: "git worktree remove failed", wantGit: []string{"worktree", "worktree"},
		},
		{
			name:    "head unreadable",
			fails:   map[string]error{"symbolic-ref": errors.New("exit 1"), "rev-parse": errors.New("exit 128")},
			wantErr: "resolving HEAD", wantGit: []string{"worktree", "symbolic-ref", "rev-parse"},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			var git []string
			s.creator.Run = func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
				git = append(git, args[0])
				if err := tt.fails[args[0]+" "+args[1]]; err != nil {
					return nil, err
				}
				if err := tt.fails[args[0]]; err != nil {
					return nil, err
				}
				return []byte("feat/pr-head\n"), nil
			}
			var checkedOut string
			result, err := s.creator.CreateWith(context.Background(), "/proj", "pr-12", "", func(_ context.Context, dir string) error {
				checkedOut = dir
				return tt.checkoutErr
			})
			require.Equal(s.T(), tt.wantGit, git)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				require.Nil(s.T(), result)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), wtPath, checkedOut)
			require.Equal(s.T(), &CreateResult{WorktreePath: wtPath, BranchName: "feat/pr-head"}, result)
			require.Len(s.T(), s.sys.writeFileCalls, 1)
		})
	}
}

func (s *CreatorSuite) TestHeadRef() {
	tests := []struct {
		name    string
		results map[string]struct { // keyed by git subcommand
			out string
			err error
		}
		want    string
		wantErr string
	}{
		{
			name: "branch",
			results: map[string]struct {
				out string
				err error
			}{"symbolic-ref": {out: "feat/renamed\n"}},
			want: "feat/renamed",
		},
		{
			name: "detached",
			results: map[string]struct {
				out string
				err error
			}{
				"symbolic-ref": {err: fmt.Errorf("exit status 1")},
				"rev-parse":    {out: "abc1234\n"},
			},
			want: "abc1234",
		},
		{
			name: "not a repo",
			results: map[string]struct {
				out string
				err error
			}{
				"symbolic-ref": {err: fmt.Errorf("exit status 128")},
				"rev-parse":    {out: "fatal: not a git repository\n", err: fmt.Errorf("exit status 128")},
			},
			wantErr: "resolving HEAD of /proj/.worktrees/wt-abc failed: fatal: not a git repository",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.creator.Run = func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
				require.Equal(s.T(), "/proj/.worktrees/wt-abc", dir)
				r := tc.results[args[0]]
				return []byte(r.out), r.err
			}
			got, err := s.creator.HeadRef(context.Background(), "/proj/.worktrees/wt-abc")
			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *CreatorSuite) TestRemoveSuccess() {
	err := s.creator.Remove(context.Background(), "/proj", "/proj/.worktrees/wt-abc")

	require.NoError(s.T(), err)
	require.Len(s.T(), s.runArgs, 2)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "remove", "--force", "--force", "/proj/.worktrees/wt-abc"}, s.runArgs[0])
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "prune"}, s.runArgs[1])
}

func (s *CreatorSuite) TestRemoveGitError() {
	s.runErr = fmt.Errorf("exit status 1")
	s.runOut = []byte("fatal: not a worktree")

	err := s.creator.Remove(context.Background(), "/proj", "/proj/.worktrees/wt-abc")

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "fatal: not a worktree")
}

func (s *CreatorSuite) TestLockSuccessWithReason() {
	err := s.creator.Lock(context.Background(), "/proj", "/proj/.worktrees/wt-abc", "locked from Loop UI")

	require.NoError(s.T(), err)
	require.Len(s.T(), s.runArgs, 1)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "lock", "/proj/.worktrees/wt-abc", "--reason", "locked from Loop UI"}, s.runArgs[0])
}

func (s *CreatorSuite) TestLockSuccessWithoutReason() {
	err := s.creator.Lock(context.Background(), "/proj", "/proj/.worktrees/wt-abc", "")

	require.NoError(s.T(), err)
	require.Len(s.T(), s.runArgs, 1)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "lock", "/proj/.worktrees/wt-abc"}, s.runArgs[0])
}

func (s *CreatorSuite) TestLockAlreadyLockedNoOp() {
	s.runErr = fmt.Errorf("exit status 1")
	s.runOut = []byte("fatal: '/proj/.worktrees/wt-abc' is already locked")

	err := s.creator.Lock(context.Background(), "/proj", "/proj/.worktrees/wt-abc", "")

	require.NoError(s.T(), err)
}

func (s *CreatorSuite) TestLockGitError() {
	s.runErr = fmt.Errorf("exit status 128")
	s.runOut = []byte("fatal: not a worktree")

	err := s.creator.Lock(context.Background(), "/proj", "/proj/.worktrees/wt-abc", "")

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "fatal: not a worktree")
}

func (s *CreatorSuite) TestUnlockSuccess() {
	err := s.creator.Unlock(context.Background(), "/proj", "/proj/.worktrees/wt-abc")

	require.NoError(s.T(), err)
	require.Len(s.T(), s.runArgs, 1)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "unlock", "/proj/.worktrees/wt-abc"}, s.runArgs[0])
}

func (s *CreatorSuite) TestUnlockNotLockedNoOp() {
	s.runErr = fmt.Errorf("exit status 1")
	s.runOut = []byte("fatal: '/proj/.worktrees/wt-abc' is not locked")

	err := s.creator.Unlock(context.Background(), "/proj", "/proj/.worktrees/wt-abc")

	require.NoError(s.T(), err)
}

func (s *CreatorSuite) TestUnlockGitError() {
	s.runErr = fmt.Errorf("exit status 128")
	s.runOut = []byte("fatal: not a worktree")

	err := s.creator.Unlock(context.Background(), "/proj", "/proj/.worktrees/wt-abc")

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "fatal: not a worktree")
}

func (s *CreatorSuite) TestRemovePruneError() {
	callCount := 0
	s.creator.Run = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		s.runArgs = append(s.runArgs, append([]string{dir, name}, args...))
		callCount++
		if callCount == 2 {
			return []byte("prune failed"), fmt.Errorf("exit status 1")
		}
		return nil, nil
	}

	err := s.creator.Remove(context.Background(), "/proj", "/proj/.worktrees/wt-abc")

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "prune failed")
}
