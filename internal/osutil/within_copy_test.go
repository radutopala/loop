package osutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func (s *WithinSuite) TestCopyInFile() {
	s.Require().NoError(os.Chmod(s.in("plain.md"), 0o640))
	s.Require().NoError(CopyIn(s.root, s.in("plain.md"), s.root, s.in("copy.md")))
	got, err := os.ReadFile(s.in("copy.md"))
	s.Require().NoError(err)
	s.Require().Equal("plain", string(got))
	fi, err := os.Stat(s.in("copy.md"))
	s.Require().NoError(err)
	s.Require().Equal(fs.FileMode(0o640), fi.Mode().Perm())
}

func (s *WithinSuite) TestCopyInDir() {
	src := filepath.Join(s.root, "src")
	s.Require().NoError(os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("a"), 0o644))
	s.Require().NoError(os.Symlink(s.secret, filepath.Join(src, "out")))
	s.Require().NoError(syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644))
	s.Require().NoError(os.Chmod(filepath.Join(src, "sub"), 0o555))
	s.T().Cleanup(func() { _ = os.Chmod(filepath.Join(src, "sub"), 0o755) })

	dst := filepath.Join(s.root, "dst")
	s.Require().NoError(RealSystem{}.CopyIn(s.root, src, s.root, dst))
	s.T().Cleanup(func() { _ = os.Chmod(filepath.Join(dst, "sub"), 0o755) })

	got, err := os.ReadFile(filepath.Join(dst, "sub", "a.txt"))
	s.Require().NoError(err)
	s.Require().Equal("a", string(got))
	fi, err := os.Stat(filepath.Join(dst, "sub"))
	s.Require().NoError(err)
	s.Require().Equal(fs.FileMode(0o555), fi.Mode().Perm())
	// A symlink is copied as a link to the same target, not followed.
	target, err := os.Readlink(filepath.Join(dst, "out"))
	s.Require().NoError(err)
	s.Require().Equal(s.secret, target)
	// Special files are left out.
	s.Require().NoFileExists(filepath.Join(dst, "pipe"))
}

func (s *WithinSuite) TestCopyInAcrossRoots() {
	other := s.T().TempDir()
	s.Require().NoError(CopyIn(s.root, filepath.Join(s.root, "README.md"), other, filepath.Join(other, "README.md")))
	s.Require().FileExists(filepath.Join(other, "README.md"))
}

func (s *WithinSuite) TestCopyInRefusals() {
	missing := filepath.Join(s.outside, "missing")
	for name, tc := range map[string]struct {
		srcRoot, src, dstRoot, dst string
		is                         error
	}{
		"source not under its root":      {s.root, "plain.md", s.root, s.in("x"), nil},
		"destination not under its root": {s.root, s.in("plain.md"), s.root, "x", nil},
		"into itself":                    {s.root, s.loopDir, s.root, s.loopDir, ErrCopyIntoItself},
		"into a dir under itself":        {s.root, s.loopDir, s.root, s.in("nested"), ErrCopyIntoItself},
		"missing source root":            {missing, filepath.Join(missing, "a"), s.root, s.in("x"), fs.ErrNotExist},
		"missing destination root":       {s.root, s.in("plain.md"), missing, filepath.Join(missing, "a"), fs.ErrNotExist},
		"missing source":                 {s.root, s.in("nope"), s.root, s.in("x"), fs.ErrNotExist},
		"source leads out":               {s.root, s.in("linked-dir/secret"), s.root, s.in("x"), nil},
		"destination exists":             {s.root, s.in("plain.md"), s.root, filepath.Join(s.root, "README.md"), fs.ErrExist},
		"destination parent missing":     {s.root, s.in("plain.md"), s.root, s.in("no/x"), fs.ErrNotExist},
		"dir, destination parent gone":   {s.root, s.loopDir, s.root, filepath.Join(s.root, "no", "x"), fs.ErrNotExist},
		"link, destination parent gone":  {s.root, s.in("abs.md"), s.root, s.in("no/x"), fs.ErrNotExist},
	} {
		s.Run(name, func() {
			err := CopyIn(tc.srcRoot, tc.src, tc.dstRoot, tc.dst)
			s.Require().Error(err)
			if tc.is != nil {
				s.Require().True(errors.Is(err, tc.is), err)
			}
			s.Require().NoFileExists(s.in("x"))
		})
	}
	// README.md was not overwritten.
	got, err := os.ReadFile(filepath.Join(s.root, "README.md"))
	s.Require().NoError(err)
	s.Require().Equal("readme", string(got))
}
