package osutil

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type WithinSuite struct {
	suite.Suite
	outside string // a host dir outside the project
	secret  string
	root    string // the project
	loopDir string
}

func TestWithinSuite(t *testing.T) {
	suite.Run(t, new(WithinSuite))
}

func (s *WithinSuite) SetupTest() {
	s.outside = s.T().TempDir()
	s.secret = filepath.Join(s.outside, "secret")
	s.Require().NoError(os.WriteFile(s.secret, []byte("secret"), 0o600))
	s.root = s.T().TempDir()
	s.loopDir = filepath.Join(s.root, ".loop")
	s.Require().NoError(os.Mkdir(s.loopDir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "README.md"), []byte("readme"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(s.loopDir, "plain.md"), []byte("plain"), 0o644))
	for link, target := range map[string]string{
		"inside.md":  "../README.md",
		"abs.md":     s.secret,
		"rel.md":     "../../" + filepath.Base(s.outside) + "/secret",
		"dangling":   filepath.Join(s.outside, "new.json"),
		"linked-dir": s.outside,
	} {
		s.Require().NoError(os.Symlink(target, filepath.Join(s.loopDir, link)))
	}
}

func (s *WithinSuite) in(name string) string { return filepath.Join(s.loopDir, name) }

// escapes are the paths under the project that lead outside it.
func (s *WithinSuite) escapes() map[string]string {
	return map[string]string{
		"absolute symlink":   s.in("abs.md"),
		"relative symlink":   s.in("rel.md"),
		"dangling symlink":   s.in("dangling"),
		"symlinked dir":      s.in("linked-dir/secret"),
		"dot-dot":            filepath.Join(s.root, "..", filepath.Base(s.outside), "secret"),
		"path not under one": "secret",
	}
}

func (s *WithinSuite) TestReadFileIn() {
	for name, path := range map[string]string{"plain": s.in("plain.md"), "symlink inside the root": s.in("inside.md")} {
		s.Run(name, func() {
			got, err := ReadFileIn(s.root, path)
			s.Require().NoError(err)
			s.Require().NotEmpty(got)
		})
	}
	for name, path := range s.escapes() {
		s.Run(name, func() {
			got, err := ReadFileIn(s.root, path)
			s.Require().Error(err)
			s.Require().Empty(got)
		})
	}
	_, err := ReadFileIn(filepath.Join(s.root, "missing"), filepath.Join(s.root, "missing", "x"))
	s.Require().ErrorIs(err, os.ErrNotExist)
}

func (s *WithinSuite) TestOpenFileIn() {
	f, err := OpenFileIn(s.root, s.in("inside.md"))
	s.Require().NoError(err)
	got, err := io.ReadAll(f)
	s.Require().NoError(f.Close())
	s.Require().NoError(err)
	s.Require().NotEmpty(got)
	for name, path := range s.escapes() {
		s.Run(name, func() {
			f, err := OpenFileIn(s.root, path)
			s.Require().Error(err)
			s.Require().Nil(f)
		})
	}
}

func (s *WithinSuite) TestWriteFileIn() {
	s.Run("new file", func() {
		s.Require().NoError(RealSystem{}.WriteFileIn(s.root, s.in("config.json"), []byte("{}"), 0o644))
		got, err := RealSystem{}.ReadFileIn(s.root, s.in("config.json"))
		s.Require().NoError(err)
		s.Require().Equal("{}", string(got))
	})
	s.Run("through a symlink inside the root", func() {
		s.Require().NoError(WriteFileIn(s.root, s.in("inside.md"), []byte("new"), 0o644))
		got, err := os.ReadFile(filepath.Join(s.root, "README.md"))
		s.Require().NoError(err)
		s.Require().Equal("new", string(got))
	})
	for name, path := range s.escapes() {
		s.Run(name, func() {
			s.Require().Error(WriteFileIn(s.root, path, []byte("pwned"), 0o644))
		})
	}
	got, err := os.ReadFile(s.secret)
	s.Require().NoError(err)
	s.Require().Equal("secret", string(got))
	_, err = os.Stat(filepath.Join(s.outside, "new.json"))
	s.Require().ErrorIs(err, os.ErrNotExist, "the dangling link's target wasn't created")
}

func (s *WithinSuite) TestCheckIn() {
	for name, path := range map[string]string{
		"missing file":            s.in("config.json"),
		"missing dir":             filepath.Join(s.root, "nope", "config.json"),
		"symlink inside the root": s.in("inside.md"),
	} {
		s.Run(name, func() { s.Require().NoError(CheckIn(s.root, path)) })
	}
	s.Require().NoError(CheckIn(filepath.Join(s.root, "missing"), filepath.Join(s.root, "missing", "x")), "missing root")
	for name, path := range s.escapes() {
		s.Run(name, func() { s.Require().Error(CheckIn(s.root, path)) })
	}
	s.Require().Error(CheckIn(s.secret, filepath.Join(s.secret, "x")), "root is a file")
}

// A link swapped in after a check doesn't redirect the write: the write
// resolves the path itself.
func (s *WithinSuite) TestWriteFileInAfterSwap() {
	path := s.in("config.json")
	s.Require().NoError(CheckIn(s.root, path))
	s.Require().NoError(os.Symlink(s.secret, path))
	s.Require().Error(WriteFileIn(s.root, path, []byte("pwned"), 0o644))
	got, err := os.ReadFile(s.secret)
	require.NoError(s.T(), err)
	s.Require().Equal("secret", string(got))
}

func (s *WithinSuite) TestMkdirAllIn() {
	for name, path := range map[string]string{
		"new dirs":                 filepath.Join(s.loopDir, "a", "b"),
		"an existing dir":          s.loopDir,
		"under a link in the root": filepath.Join(s.root, "self", "c"),
	} {
		s.Run(name, func() {
			s.Require().NoError(os.Symlink(".", filepath.Join(s.root, "self")))
			s.Require().NoError(RealSystem{}.MkdirAllIn(s.root, path, 0o755))
			info, err := os.Stat(path)
			s.Require().NoError(err)
			s.Require().True(info.IsDir())
			s.Require().NoError(os.Remove(filepath.Join(s.root, "self")))
		})
	}
	for name, path := range s.escapes() {
		s.Run(name, func() {
			err := MkdirAllIn(s.root, filepath.Join(path, "x"), 0o755)
			s.Require().Error(err)
		})
	}
	entries, err := os.ReadDir(s.outside)
	s.Require().NoError(err)
	s.Require().Len(entries, 1, "nothing was made outside the root")
}

func (s *WithinSuite) TestOpenRootIn() {
	s.Run("a dir", func() {
		r, err := OpenRootIn(s.root, s.loopDir)
		s.Require().NoError(err)
		defer r.Close()
		got, err := r.ReadFile("plain.md")
		s.Require().NoError(err)
		s.Require().Equal("plain", string(got))
	})
	s.Run("a dir under a link in the root", func() {
		s.Require().NoError(os.Symlink(".", filepath.Join(s.root, "self")))
		r, err := OpenRootIn(s.root, filepath.Join(s.root, "self", ".loop"))
		s.Require().NoError(err)
		s.Require().NoError(r.Close())
	})
	s.Require().NoError(os.Symlink("..", s.in("up")))
	for name, dir := range map[string]string{
		"a link to a dir in the root": s.in("up"),
		"a link to a dir outside":     s.in("linked-dir"),
		"dot-dot":                     filepath.Join(s.root, "..", filepath.Base(s.outside)),
		"a dangling link":             s.in("dangling"),
		"a file":                      s.in("plain.md"),
	} {
		s.Run(name, func() {
			r, err := OpenRootIn(s.root, dir)
			s.Require().Error(err)
			s.Require().Nil(r)
		})
	}
	_, err := OpenRootIn(s.root, s.in("up"))
	s.Require().True(IsEscape(err), "a linked dir reads as an escape")
	_, err = OpenRootIn(s.root, s.in("missing"))
	s.Require().ErrorIs(err, os.ErrNotExist)
}

func (s *WithinSuite) TestIsEscape() {
	_, err := ReadFileIn(s.root, s.in("abs.md"))
	s.Require().True(IsEscape(err), "os.Root's own refusal: %v", err)
	err = MkdirAllIn(s.root, filepath.Join(s.in("abs.md"), "dir"), 0o755)
	s.Require().True(IsEscape(err), "MkdirAll's refusal, wrapped twice: %v", err)
	_, err = ReadFileIn(s.root, s.in("missing"))
	s.Require().False(IsEscape(err))
	s.Require().False(IsEscape(nil))
}
