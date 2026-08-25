package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

func (s *CreatorSuite) TestMoveSuccess() {
	err := s.creator.Move(context.Background(), "/proj", "/proj/.worktrees/wt-old", "/proj/.worktrees/wt-new", "worktree/wt-old", "worktree/wt-new")

	require.NoError(s.T(), err)
	require.Len(s.T(), s.runArgs, 2)
	require.Equal(s.T(), []string{"/proj", "git", "worktree", "move", "/proj/.worktrees/wt-old", "/proj/.worktrees/wt-new"}, s.runArgs[0])
	require.Equal(s.T(), []string{"/proj/.worktrees/wt-new", "git", "branch", "-m", "worktree/wt-old", "worktree/wt-new"}, s.runArgs[1])
}

func (s *CreatorSuite) TestMoveWorktreeFail() {
	s.runErr = fmt.Errorf("exit status 1")
	s.runOut = []byte("fatal: not a worktree path")

	err := s.creator.Move(context.Background(), "/proj", "/proj/.worktrees/wt-old", "/proj/.worktrees/wt-new", "worktree/wt-old", "worktree/wt-new")

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "git worktree move failed")
	require.Contains(s.T(), err.Error(), "fatal: not a worktree path")
}

func (s *CreatorSuite) TestMoveBranchRenameFail() {
	callCount := 0
	s.creator.Run = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		s.runArgs = append(s.runArgs, append([]string{dir, name}, args...))
		callCount++
		if callCount == 2 {
			return []byte("fatal: branch rename failed"), fmt.Errorf("exit status 1")
		}
		return nil, nil
	}

	err := s.creator.Move(context.Background(), "/proj", "/proj/.worktrees/wt-old", "/proj/.worktrees/wt-new", "worktree/wt-old", "worktree/wt-new")

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "git branch -m failed")
	require.Contains(s.T(), err.Error(), "fatal: branch rename failed")
}
