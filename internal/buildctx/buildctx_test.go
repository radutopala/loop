package buildctx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type BuildCtxSuite struct {
	suite.Suite
	dir string
}

func TestBuildCtxSuite(t *testing.T) {
	suite.Run(t, new(BuildCtxSuite))
}

func (s *BuildCtxSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

func (s *BuildCtxSuite) write(name, content string, mode os.FileMode) {
	p := filepath.Join(s.dir, filepath.FromSlash(name))
	require.NoError(s.T(), os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(s.T(), os.WriteFile(p, []byte(content), mode))
	require.NoError(s.T(), os.Chmod(p, mode))
}

func (s *BuildCtxSuite) root() *os.Root {
	r, err := os.OpenRoot(s.dir)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { r.Close() })
	return r
}

// context fills s.dir with a context of every kind of entry, returning its
// fingerprint.
func (s *BuildCtxSuite) context() Files {
	s.write("Dockerfile", "FROM loop-agent\n", 0o644)
	s.write("bin/setup.sh", "echo hi\n", 0o755)
	require.NoError(s.T(), os.Mkdir(filepath.Join(s.dir, "empty"), 0o755))
	require.NoError(s.T(), os.Symlink("../outside", filepath.Join(s.dir, "bin", "link")))
	return Files{
		"Dockerfile":   FileDigest([]byte("FROM loop-agent\n"), 0o644),
		"bin/setup.sh": FileDigest([]byte("echo hi\n"), 0o755),
		"bin/link":     "symlink:../outside",
	}
}

func (s *BuildCtxSuite) TestFileDigest() {
	plain := FileDigest([]byte("x"), 0o644)
	require.Equal(s.T(), "sha256:2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881", plain)
	require.Equal(s.T(), plain+" +x", FileDigest([]byte("x"), 0o744))
	require.NotEqual(s.T(), plain, FileDigest([]byte("y"), 0o644))
}

func (s *BuildCtxSuite) TestDigest() {
	want := s.context()
	got, err := new(Cache).Digest(s.root())
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)
}

func (s *BuildCtxSuite) TestDigestEmpty() {
	got, err := new(Cache).Digest(s.root())
	require.NoError(s.T(), err)
	require.Equal(s.T(), Files{}, got)
}

func (s *BuildCtxSuite) TestSnapshot() {
	want := s.context()
	dst := s.T().TempDir()
	got, err := Snapshot(s.root(), dst)
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)

	copied, err := os.OpenRoot(dst)
	require.NoError(s.T(), err)
	defer copied.Close()
	again, err := new(Cache).Digest(copied)
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, again, "the copy fingerprints as the original")
	fi, err := os.Stat(filepath.Join(dst, "empty"))
	require.NoError(s.T(), err)
	require.True(s.T(), fi.IsDir())
}

func (s *BuildCtxSuite) TestSnapshotWriteErrors() {
	tests := []struct {
		name  string
		err   error
		setup func(src, dst string)
	}{
		{name: "file", err: syscall.EISDIR, setup: func(src, dst string) {
			s.write("Dockerfile", "FROM x\n", 0o644)
			require.NoError(s.T(), os.Mkdir(filepath.Join(dst, "Dockerfile"), 0o755))
		}},
		{name: "link", err: fs.ErrExist, setup: func(src, dst string) {
			require.NoError(s.T(), os.Symlink("t", filepath.Join(src, "l")))
			require.NoError(s.T(), os.WriteFile(filepath.Join(dst, "l"), nil, 0o644))
		}},
		{name: "dir", err: fs.ErrExist, setup: func(src, dst string) {
			require.NoError(s.T(), os.Mkdir(filepath.Join(src, "d"), 0o755))
			require.NoError(s.T(), os.WriteFile(filepath.Join(dst, "d"), nil, 0o644))
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			dst := s.T().TempDir()
			tt.setup(s.dir, dst)
			_, err := Snapshot(s.root(), dst)
			require.ErrorIs(s.T(), err, tt.err)
		})
	}
}

func (s *BuildCtxSuite) TestReadErrors() {
	boom := errors.New("boom")
	tests := []struct {
		name string
		fake fakeTree
	}{
		{name: "read dir", fake: fakeTree{readDirErr: boom}},
		{name: "read link", fake: fakeTree{readLinkErr: boom}},
		{name: "open", fake: fakeTree{openErr: boom}},
		{name: "stat", fake: fakeTree{statErr: boom}},
		{name: "read", fake: fakeTree{readErr: boom}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			tt.fake.MapFS = fstest.MapFS{
				"Dockerfile": {Data: []byte("FROM x\n")},
				"sub/l":      {Data: []byte("t"), Mode: fs.ModeSymlink},
			}
			_, err := digest(tt.fake, readDigest)
			require.ErrorIs(s.T(), err, boom)
		})
	}
}

func (s *BuildCtxSuite) TestCacheDigest() {
	want := s.context()
	var c Cache
	got, err := c.Digest(s.root())
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)

	// The same bytes in place, with the old modification time put back,
	// still read again: the write changed the file's change time.
	p := filepath.Join(s.dir, "Dockerfile")
	fi, err := os.Stat(p)
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.WriteFile(p, []byte("FROM loop-other\n"), 0o644))
	require.NoError(s.T(), os.Chtimes(p, fi.ModTime(), fi.ModTime()))
	want["Dockerfile"] = FileDigest([]byte("FROM loop-other\n"), 0o644)
	got, err = c.Digest(s.root())
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)
}

// TestCacheReadsOnlyChanges: a file whose stat is as it was isn't read
// again, a changed one is, and a failed walk keeps what the cache knew.
func (s *BuildCtxSuite) TestCacheReadsOnlyChanges() {
	boom := errors.New("boom")
	files := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM x\n")},
		"gone":       {Data: []byte("bye")},
	}
	var c Cache
	got, err := c.digest("/ctx", fakeTree{MapFS: files})
	require.NoError(s.T(), err)
	require.Equal(s.T(), Files{
		"Dockerfile": FileDigest([]byte("FROM x\n"), 0),
		"gone":       FileDigest([]byte("bye"), 0),
	}, got)

	// Reading fails from here on, so only what the cache has can answer.
	delete(files, "gone")
	unread := fakeTree{MapFS: files, readErr: boom}
	got, err = c.digest("/ctx", unread)
	require.NoError(s.T(), err)
	require.Equal(s.T(), Files{"Dockerfile": FileDigest([]byte("FROM x\n"), 0)}, got)

	// Another context with the same names is its own.
	_, err = c.digest("/other", unread)
	require.ErrorIs(s.T(), err, boom)

	files["Dockerfile"] = &fstest.MapFile{Data: []byte("FROM yy\n")}
	_, err = c.digest("/ctx", unread)
	require.ErrorIs(s.T(), err, boom)

	// The removed file went from the cache, and the failed walk changed
	// nothing in it.
	files["gone"] = &fstest.MapFile{Data: []byte("bye")}
	files["Dockerfile"] = &fstest.MapFile{Data: []byte("FROM x\n")}
	_, err = c.digest("/ctx", unread)
	require.ErrorIs(s.T(), err, boom)
	delete(files, "gone")
	got, err = c.digest("/ctx", unread)
	require.NoError(s.T(), err)
	require.Equal(s.T(), Files{"Dockerfile": FileDigest([]byte("FROM x\n"), 0)}, got)
}

func (s *BuildCtxSuite) TestSnapshotReadError() {
	boom := errors.New("boom")
	_, err := copyTo(s.T().TempDir())("f", failingFile{readErr: boom}, nil)
	require.ErrorIs(s.T(), err, boom)
}

// fakeTree is a MapFS that fails where told to.
type fakeTree struct {
	fstest.MapFS
	readDirErr, readLinkErr, openErr, statErr, readErr error
}

func (f fakeTree) ReadDir(name string) ([]fs.DirEntry, error) {
	if f.readDirErr != nil && name == "sub" {
		return nil, f.readDirErr
	}
	return f.MapFS.ReadDir(name)
}

func (f fakeTree) ReadLink(name string) (string, error) {
	if f.readLinkErr != nil {
		return "", f.readLinkErr
	}
	return f.MapFS.ReadLink(name)
}

func (f fakeTree) OpenFile(name string) (fs.File, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	file, err := f.Open(name)
	return failingFile{File: file, statErr: f.statErr, readErr: f.readErr}, err
}

type failingFile struct {
	fs.File
	statErr, readErr error
}

func (f failingFile) Stat() (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}

func (f failingFile) Read(b []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.File.Read(b)
}
