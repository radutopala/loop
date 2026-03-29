package osutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RealSystemSuite struct {
	suite.Suite
	sys RealSystem
	dir string
}

func TestRealSystemSuite(t *testing.T) {
	suite.Run(t, new(RealSystemSuite))
}

func (s *RealSystemSuite) SetupTest() {
	s.sys = RealSystem{}
	s.dir = s.T().TempDir()
}

func (s *RealSystemSuite) TestStat() {
	info, err := s.sys.Stat(s.dir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *RealSystemSuite) TestReadFileWriteFile() {
	p := filepath.Join(s.dir, "test.txt")
	require.NoError(s.T(), s.sys.WriteFile(p, []byte("hello"), 0o644))
	data, err := s.sys.ReadFile(p)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []byte("hello"), data)
}

func (s *RealSystemSuite) TestReadDir() {
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.dir, "a.txt"), nil, 0o644))
	entries, err := s.sys.ReadDir(s.dir)
	require.NoError(s.T(), err)
	require.Len(s.T(), entries, 1)
}

func (s *RealSystemSuite) TestRemove() {
	p := filepath.Join(s.dir, "rm.txt")
	require.NoError(s.T(), os.WriteFile(p, nil, 0o644))
	require.NoError(s.T(), s.sys.Remove(p))
	_, err := os.Stat(p)
	require.True(s.T(), os.IsNotExist(err))
}

func (s *RealSystemSuite) TestRemoveTraversal() {
	err := s.sys.Remove("../passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "disallowed traversal")
}

func (s *RealSystemSuite) TestOpenSuccess() {
	p := filepath.Join(s.dir, "open.txt")
	require.NoError(s.T(), os.WriteFile(p, []byte("data"), 0o644))
	f, err := s.sys.Open(p)
	require.NoError(s.T(), err)
	require.NoError(s.T(), f.Close())
}

func (s *RealSystemSuite) TestOpenTraversal() {
	_, err := s.sys.Open("../passwd")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "disallowed traversal")
}

func (s *RealSystemSuite) TestMkdirAll() {
	p := filepath.Join(s.dir, "a", "b")
	require.NoError(s.T(), s.sys.MkdirAll(p, 0o755))
	info, err := os.Stat(p)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *RealSystemSuite) TestReadlink() {
	target := filepath.Join(s.dir, "target.txt")
	require.NoError(s.T(), os.WriteFile(target, nil, 0o644))
	link := filepath.Join(s.dir, "link")
	require.NoError(s.T(), os.Symlink(target, link))
	got, err := s.sys.Readlink(link)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target, got)
}

func (s *RealSystemSuite) TestUserHomeDir() {
	home, err := s.sys.UserHomeDir()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), home)
}

func (s *RealSystemSuite) TestGetenv() {
	s.T().Setenv("OSUTIL_TEST_KEY", "val")
	require.Equal(s.T(), "val", s.sys.Getenv("OSUTIL_TEST_KEY"))
	require.Empty(s.T(), s.sys.Getenv("OSUTIL_NONEXISTENT"))
}

func (s *RealSystemSuite) TestEvalSymlinks() {
	p, err := s.sys.EvalSymlinks(s.dir)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), p)
}

func (s *RealSystemSuite) TestWalkDir() {
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.dir, "w.txt"), nil, 0o644))
	var paths []string
	err := s.sys.WalkDir(s.dir, func(path string, _ os.DirEntry, _ error) error {
		paths = append(paths, path)
		return nil
	})
	require.NoError(s.T(), err)
	require.GreaterOrEqual(s.T(), len(paths), 2) // dir + file
}

func (s *RealSystemSuite) TestExecCommandOutput() {
	out, err := s.sys.ExecCommandOutput("echo", "hello")
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(out), "hello")
}

func (s *RealSystemSuite) TestGetwd() {
	wd, err := s.sys.Getwd()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), wd)
}

func (s *RealSystemSuite) TestExecutable() {
	exe, err := s.sys.Executable()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), exe)
}

func (s *RealSystemSuite) TestChmod() {
	p := filepath.Join(s.dir, "chmod.txt")
	require.NoError(s.T(), os.WriteFile(p, nil, 0o644))
	require.NoError(s.T(), s.sys.Chmod(p, 0o755))
}

func (s *RealSystemSuite) TestRename() {
	src := filepath.Join(s.dir, "src.txt")
	dst := filepath.Join(s.dir, "dst.txt")
	require.NoError(s.T(), os.WriteFile(src, []byte("data"), 0o644))
	require.NoError(s.T(), s.sys.Rename(src, dst))
	_, err := os.Stat(src)
	require.True(s.T(), os.IsNotExist(err))
	_, err = os.Stat(dst)
	require.NoError(s.T(), err)
}

func (s *RealSystemSuite) TestCreateTemp() {
	f, err := s.sys.CreateTemp(s.dir, "test-*")
	require.NoError(s.T(), err)
	require.NoError(s.T(), f.Close())
	require.NoError(s.T(), os.Remove(f.Name()))
}

func TestEncodeClaudeProjectPath(t *testing.T) {
	require.Equal(t, "-Users-foo-dev-loop", EncodeClaudeProjectPath("/Users/foo/dev/loop"))
	require.Equal(t, "-home-user--hidden", EncodeClaudeProjectPath("/home/user/.hidden"))
	require.Equal(t, "", EncodeClaudeProjectPath(""))
}
