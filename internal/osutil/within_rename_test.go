package osutil

import (
	"os"
	"path/filepath"
)

func (s *WithinSuite) TestRenameIn() {
	tmp := s.in("config.json.tmp")
	s.Require().NoError(os.WriteFile(tmp, []byte("new"), 0o644))

	// A symlink at the destination is replaced, not followed.
	s.Require().NoError(RenameIn(s.root, tmp, s.in("abs.md")))
	got, err := os.ReadFile(s.in("abs.md"))
	s.Require().NoError(err)
	s.Require().Equal("new", string(got))
	got, err = os.ReadFile(s.secret)
	s.Require().NoError(err)
	s.Require().Equal("secret", string(got))

	for name, tc := range map[string]struct{ from, to string }{
		"source leads out":      {s.in("linked-dir/secret"), s.in("taken")},
		"destination leads out": {s.in("plain.md"), s.in("linked-dir/taken")},
		"destination not under": {s.in("plain.md"), "taken"},
	} {
		s.Run(name, func() {
			s.Require().Error(RenameIn(s.root, tc.from, tc.to))
			s.Require().FileExists(s.secret)
			s.Require().NoFileExists(filepath.Join(s.outside, "taken"))
		})
	}
	s.Require().NoError(RealSystem{}.RenameIn(s.root, s.in("plain.md"), s.in("moved.md")))
	s.Require().FileExists(s.in("moved.md"))
}

func (s *WithinSuite) TestRemoveIn() {
	s.Require().Error(RemoveIn(s.root, s.in("linked-dir/secret")))
	s.Require().FileExists(s.secret)
	// The link itself goes, not what it points to.
	s.Require().NoError(RemoveIn(s.root, s.in("abs.md")))
	s.Require().NoFileExists(s.in("abs.md"))
	s.Require().FileExists(s.secret)
	s.Require().NoError(RealSystem{}.RemoveIn(s.root, s.in("plain.md")))
	s.Require().NoFileExists(s.in("plain.md"))
}

func (s *WithinSuite) TestRootFS() {
	f := RootFS{Root: s.root}
	tmp := s.in("config.json.tmp")
	s.Require().NoError(f.MkdirAll(s.in("sub"), 0o755))
	s.Require().NoError(f.WriteFile(tmp, []byte("new"), 0o644))
	fi, err := f.Stat(tmp)
	s.Require().NoError(err)
	s.Require().Equal(int64(3), fi.Size())
	got, err := f.ReadFile(tmp)
	s.Require().NoError(err)
	s.Require().Equal("new", string(got))
	s.Require().NoError(f.Rename(tmp, s.in("config.json")))
	s.Require().NoError(f.Remove(s.in("config.json")))

	for name, path := range s.escapes() {
		s.Run(name, func() {
			_, err := f.Stat(path)
			s.Require().Error(err)
			_, err = f.ReadFile(path)
			s.Require().Error(err)
			s.Require().Error(f.WriteFile(path, []byte("x"), 0o644))
			s.Require().Error(f.MkdirAll(filepath.Join(path, "dir"), 0o755))
		})
	}
	got, err = os.ReadFile(s.secret)
	s.Require().NoError(err)
	s.Require().Equal("secret", string(got))
}
