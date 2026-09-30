//go:build linux

package agentgate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type OSGuardFSSuite struct {
	suite.Suite
	ids      *fakeFSIDs
	fs       *OSGuardFS
	dir      string
	uid, gid int
}

func TestOSGuardFSSuite(t *testing.T) {
	suite.Run(t, new(OSGuardFSSuite))
}

// fakeFSIDs stands in for setgroups/setfsgid/setfsuid so the tests run
// unprivileged. setfs[ug]id return the previous id, and a -1 argument is a
// query that changes nothing — like the kernel.
type fakeFSIDs struct {
	mu           sync.Mutex
	fsuid, fsgid uintptr
	setgroupsErr unix.Errno
	ignoreUID    bool
	ignoreGID    bool
	calls        []uintptr
}

func (f *fakeFSIDs) raw(trap, a1, _, _ uintptr) (uintptr, uintptr, unix.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, trap)
	switch trap {
	case unix.SYS_SETGROUPS:
		return 0, 0, f.setgroupsErr
	case unix.SYS_SETFSGID:
		prev := f.fsgid
		if a1 != ^uintptr(0) && !f.ignoreGID {
			f.fsgid = a1
		}
		return prev, 0, 0
	default: // SYS_SETFSUID
		prev := f.fsuid
		if a1 != ^uintptr(0) && !f.ignoreUID {
			f.fsuid = a1
		}
		return prev, 0, 0
	}
}

// errReader fails every read.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func (s *OSGuardFSSuite) SetupTest() {
	// A sentinel that matches no real id, so a switch that doesn't take is
	// visible even when the tests run as root.
	s.ids = &fakeFSIDs{fsuid: 99999, fsgid: 99999}
	s.fs = NewOSGuardFS()
	s.fs.RawSyscall = s.ids.raw
	// The parent dirs open with RESOLVE_NO_SYMLINKS: resolve TMPDIR first.
	dir, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	s.dir = dir
	s.uid, s.gid = os.Getuid(), os.Getgid()
}

func (s *OSGuardFSSuite) path(parts ...string) string {
	return filepath.Join(append([]string{s.dir}, parts...)...)
}

func (s *OSGuardFSSuite) writeFile(p, content string, mode os.FileMode) {
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.WriteFile(p, []byte(content), mode))
	s.Require().NoError(os.Chmod(p, mode))
}

// repo lays out <dir>/.git with config.lock (the rename source) and,
// when old != "", config (the target).
func (s *OSGuardFSSuite) repo(data, old string) (src, dst string) {
	src, dst = s.path(".git", "config.lock"), s.path(".git", "config")
	s.writeFile(src, data, 0o640)
	if old != "" {
		s.writeFile(dst, old, 0o644)
	}
	return src, dst
}

func (s *OSGuardFSSuite) snapshot(src, dst string) *GuardSnapshot {
	snap, err := s.fs.Snapshot(src, dst, s.uid, s.gid)
	s.Require().NoError(err)
	return snap
}

func (s *OSGuardFSSuite) dirNames(p string) []string {
	entries, err := os.ReadDir(p)
	s.Require().NoError(err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (s *OSGuardFSSuite) TestNewOSGuardFSWiresSyscalls() {
	f := NewOSGuardFS()
	s.Require().NotNil(f.Openat2)
	s.Require().NotNil(f.Openat)
	s.Require().NotNil(f.Fstat)
	s.Require().NotNil(f.Fstatat)
	s.Require().NotNil(f.Fchmod)
	s.Require().NotNil(f.Write)
	s.Require().NotNil(f.Renameat2)
	s.Require().NotNil(f.Unlinkat)
	s.Require().NotNil(f.Rand)
	s.Require().NotNil(f.RawSyscall)
}

// --- setIDs / asUser ---

func (s *OSGuardFSSuite) TestSetIDsSwitchesAndVerifies() {
	s.Require().NoError(s.fs.setIDs(1234, 5678))
	s.Require().Equal(uintptr(1234), s.ids.fsuid)
	s.Require().Equal(uintptr(5678), s.ids.fsgid)
	s.Require().Equal([]uintptr{unix.SYS_SETGROUPS, unix.SYS_SETFSGID, unix.SYS_SETFSUID, unix.SYS_SETFSGID, unix.SYS_SETFSUID}, s.ids.calls)
}

func (s *OSGuardFSSuite) TestSetIDsFailures() {
	cases := []struct {
		name string
		prep func(*fakeFSIDs)
		want unix.Errno
	}{
		{"setgroups fails", func(f *fakeFSIDs) { f.setgroupsErr = unix.EPERM }, unix.EPERM},
		{"fsuid does not take", func(f *fakeFSIDs) { f.ignoreUID = true }, unix.EPERM},
		{"fsgid does not take", func(f *fakeFSIDs) { f.ignoreGID = true }, unix.EPERM},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			c.prep(s.ids)
			s.Require().ErrorIs(s.fs.setIDs(1234, 5678), c.want)
		})
	}
}

func (s *OSGuardFSSuite) TestSetgroupsErrnoIsReturnedAsIs() {
	s.ids.setgroupsErr = unix.EINVAL
	src, dst := s.repo("x\n", "")
	_, err := s.fs.Snapshot(src, dst, s.uid, s.gid)
	s.Require().ErrorIs(err, unix.EINVAL)
	s.Require().Equal([]uintptr{unix.SYS_SETGROUPS}, s.ids.calls, "no fs access after a failed switch")
}

func (s *OSGuardFSSuite) TestInstallFailsWhenIDsDoNotTake() {
	s.ids.ignoreUID = true
	src, dst := s.repo("x\n", "")
	err := s.fs.Install(&GuardSnapshot{Src: src, Dst: dst, Data: []byte("x\n"), UID: s.uid, GID: s.gid}, false)
	s.Require().ErrorIs(err, unix.EPERM)
	s.Require().NoFileExists(dst)
	s.Require().FileExists(src)
}

// --- Snapshot ---

func (s *OSGuardFSSuite) TestSnapshotReadsSourceAndTarget() {
	src, dst := s.repo("new\n", "old\n")
	snap := s.snapshot(src, dst)

	s.Require().Equal(&GuardSnapshot{
		Src: src, Dst: dst,
		Data: []byte("new\n"), Mode: 0o640,
		Old: []byte("old\n"), OldExists: true,
		UID: s.uid, GID: s.gid,
	}, snap)
	s.Require().Equal(uintptr(s.uid), s.ids.fsuid)
	s.Require().Equal(uintptr(s.gid), s.ids.fsgid)
}

func (s *OSGuardFSSuite) TestSnapshotTargetWithoutContent() {
	cases := []struct {
		name string
		prep func(dst string)
	}{
		{"target missing", func(string) {}},
		{"target is a symlink", func(dst string) {
			s.writeFile(s.path("elsewhere"), "secret\n", 0o644)
			s.Require().NoError(os.Symlink(s.path("elsewhere"), dst))
		}},
		{"target is a directory", func(dst string) {
			s.Require().NoError(os.Mkdir(dst, 0o755))
		}},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			src, dst := s.repo("new\n", "")
			c.prep(dst)
			snap := s.snapshot(src, dst)
			s.Require().Equal([]byte("new\n"), snap.Data)
			s.Require().False(snap.OldExists)
			s.Require().Nil(snap.Old)
		})
	}
}

func (s *OSGuardFSSuite) TestSnapshotRejectsNonRegularSource() {
	cases := []struct {
		name string
		prep func(src string)
	}{
		{"directory", func(src string) { s.Require().NoError(os.Mkdir(src, 0o755)) }},
		{"symlink", func(src string) {
			s.writeFile(s.path("real"), "x\n", 0o644)
			s.Require().NoError(os.Symlink(s.path("real"), src))
		}},
		{"fifo", func(src string) { s.Require().NoError(unix.Mkfifo(src, 0o644)) }},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.Require().NoError(os.Mkdir(s.path(".git"), 0o755))
			src := s.path(".git", "config.lock")
			c.prep(src)
			_, err := s.fs.Snapshot(src, s.path(".git", "config"), s.uid, s.gid)
			s.Require().ErrorIs(err, ErrGuardNotRegular)
		})
	}
}

func (s *OSGuardFSSuite) TestSnapshotRejectsLargeSource() {
	src, dst := s.repo(strings.Repeat("a", maxGuardedFileSize+1), "")
	_, err := s.fs.Snapshot(src, dst, s.uid, s.gid)
	s.Require().ErrorIs(err, ErrGuardTooLarge)
}

func (s *OSGuardFSSuite) TestSnapshotAcceptsSourceAtTheLimit() {
	src, dst := s.repo(strings.Repeat("a", maxGuardedFileSize), "")
	snap := s.snapshot(src, dst)
	s.Require().Len(snap.Data, maxGuardedFileSize)
}

func (s *OSGuardFSSuite) TestSnapshotRejectsSourceThatGrowsAfterFstat() {
	// Fstat reports a small file; the read then sees more than the cap.
	src, dst := s.repo(strings.Repeat("a", maxGuardedFileSize+1), "")
	s.fs.Fstat = func(fd int, st *unix.Stat_t) error {
		err := unix.Fstat(fd, st)
		st.Size = 1
		return err
	}
	_, err := s.fs.Snapshot(src, dst, s.uid, s.gid)
	s.Require().ErrorIs(err, ErrGuardTooLarge)
}

func (s *OSGuardFSSuite) TestSnapshotMissingSource() {
	s.Require().NoError(os.Mkdir(s.path(".git"), 0o755))
	_, err := s.fs.Snapshot(s.path(".git", "nope"), s.path(".git", "config"), s.uid, s.gid)
	s.Require().ErrorIs(err, unix.ENOENT)
}

func (s *OSGuardFSSuite) TestSnapshotRefusesSymlinkedParents() {
	cases := []struct {
		name     string
		src, dst func() string
	}{
		{"source parent", func() string { return s.path("link", "config.lock") }, func() string { return s.path(".git", "config") }},
		{"target parent", func() string { return s.path(".git", "config.lock") }, func() string { return s.path("link", "config") }},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.repo("x\n", "old\n")
			s.Require().NoError(os.Symlink(s.path(".git"), s.path("link")))
			_, err := s.fs.Snapshot(c.src(), c.dst(), s.uid, s.gid)
			s.Require().ErrorIs(err, unix.ELOOP)
		})
	}
}

func (s *OSGuardFSSuite) TestSnapshotMissingTargetDir() {
	src, _ := s.repo("x\n", "")
	_, err := s.fs.Snapshot(src, s.path("missing", "config"), s.uid, s.gid)
	s.Require().ErrorIs(err, unix.ENOENT)
}

func (s *OSGuardFSSuite) TestSnapshotInjectedFailures() {
	cases := []struct {
		name string
		prep func(f *OSGuardFS)
		want error
	}{
		{"fstat fails", func(f *OSGuardFS) {
			f.Fstat = func(int, *unix.Stat_t) error { return unix.EIO }
		}, unix.EIO},
		{"target open fails", func(f *OSGuardFS) {
			f.Openat = func(dirfd int, path string, flags int, mode uint32) (int, error) {
				if path == "config" {
					return -1, unix.EACCES
				}
				return unix.Openat(dirfd, path, flags, mode)
			}
		}, unix.EACCES},
		{"read fails", func(f *OSGuardFS) {
			// Hand back a directory fd that claims to be a regular file:
			// the read then fails with EISDIR.
			f.Openat = func(dirfd int, _ string, _ int, _ uint32) (int, error) {
				return unix.Openat(dirfd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			}
			f.Fstat = func(fd int, st *unix.Stat_t) error {
				err := unix.Fstat(fd, st)
				st.Mode = st.Mode&^unix.S_IFMT | unix.S_IFREG
				st.Size = 0
				return err
			}
		}, unix.EISDIR},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			src, dst := s.repo("new\n", "old\n")
			c.prep(s.fs)
			_, err := s.fs.Snapshot(src, dst, s.uid, s.gid)
			s.Require().ErrorIs(err, c.want)
		})
	}
}

// --- IsRegular ---

func (s *OSGuardFSSuite) TestIsRegular() {
	s.writeFile(s.path("file"), "x", 0o644)
	s.Require().NoError(os.Mkdir(s.path("dir"), 0o755))
	s.Require().NoError(os.Symlink(s.path("file"), s.path("link")))
	s.Require().NoError(unix.Mkfifo(s.path("fifo"), 0o644))
	cases := []struct {
		name string
		want bool
	}{
		{"file", true},
		{"dir", false},
		{"link", false},
		{"fifo", false},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			got, err := s.fs.IsRegular(s.path(c.name))
			s.Require().NoError(err)
			s.Require().Equal(c.want, got)
		})
	}
	_, err := s.fs.IsRegular(s.path("missing"))
	s.Require().ErrorIs(err, unix.ENOENT)
}

func (s *OSGuardFSSuite) TestIsRegularDoesNotFollowOrSwitchIDs() {
	var gotDirfd, gotFlags int
	var gotPath string
	s.fs.Fstatat = func(dirfd int, path string, st *unix.Stat_t, flags int) error {
		gotDirfd, gotPath, gotFlags = dirfd, path, flags
		return unix.EACCES
	}
	_, err := s.fs.IsRegular("/work/x")
	s.Require().ErrorIs(err, unix.EACCES)
	s.Require().Equal(unix.AT_FDCWD, gotDirfd)
	s.Require().Equal("/work/x", gotPath)
	s.Require().Equal(unix.AT_SYMLINK_NOFOLLOW, gotFlags)
	s.Require().Empty(s.ids.calls)
}

// --- Install ---

func (s *OSGuardFSSuite) TestInstallWritesSnapshotAndRemovesSource() {
	src, dst := s.repo("new\n", "old\n")
	snap := s.snapshot(src, dst)
	// The agent rewrites the source after the decision: Install must write
	// what was read, not what is there now — and leave the rewrite alone.
	s.Require().NoError(os.WriteFile(src, []byte("evil\n"), 0o640))

	s.Require().NoError(s.fs.Install(snap, false))

	got, err := os.ReadFile(dst)
	s.Require().NoError(err)
	s.Require().Equal("new\n", string(got))
	fi, err := os.Stat(dst)
	s.Require().NoError(err)
	s.Require().Equal(os.FileMode(0o640), fi.Mode().Perm())
	got, err = os.ReadFile(src)
	s.Require().NoError(err)
	s.Require().Equal("evil\n", string(got))
	s.Require().Equal([]string{"config", "config.lock"}, s.dirNames(s.path(".git")))
}

func (s *OSGuardFSSuite) TestInstallRemovesUnchangedSource() {
	src, dst := s.repo("new\n", "old\n")
	snap := s.snapshot(src, dst)
	s.Require().NoError(s.fs.Install(snap, false))
	s.Require().NoFileExists(src)
	s.Require().Equal([]string{"config"}, s.dirNames(s.path(".git")))
}

func (s *OSGuardFSSuite) TestInstallAppliesModeDespiteUmask() {
	src, dst := s.repo("#!/bin/sh\n", "")
	s.Require().NoError(os.Chmod(src, 0o777))
	snap := s.snapshot(src, dst)
	s.Require().NoError(s.fs.Install(snap, true))
	fi, err := os.Stat(dst)
	s.Require().NoError(err)
	s.Require().Equal(os.FileMode(0o777), fi.Mode().Perm())
}

func (s *OSGuardFSSuite) TestInstallNoReplaceOntoExistingTarget() {
	src, dst := s.repo("new\n", "old\n")
	snap := s.snapshot(src, dst)
	err := s.fs.Install(snap, true)
	s.Require().ErrorIs(err, unix.EEXIST)
	got, err := os.ReadFile(dst)
	s.Require().NoError(err)
	s.Require().Equal("old\n", string(got))
	s.Require().FileExists(src)
	s.Require().ElementsMatch([]string{"config", "config.lock"}, s.dirNames(s.path(".git")), "temp file removed")
}

func (s *OSGuardFSSuite) TestInstallHandlesShortWrites() {
	data := strings.Repeat("0123456789", 10)
	src, dst := s.repo(data, "")
	snap := s.snapshot(src, dst)
	var writes int
	s.fs.Write = func(fd int, p []byte) (int, error) {
		writes++
		return unix.Write(fd, p[:min(len(p), 7)])
	}
	s.Require().NoError(s.fs.Install(snap, false))
	got, err := os.ReadFile(dst)
	s.Require().NoError(err)
	s.Require().Equal(data, string(got))
	s.Require().Equal(15, writes)
}

func (s *OSGuardFSSuite) TestInstallEmptyFile() {
	src, dst := s.repo("", "")
	snap := s.snapshot(src, dst)
	s.fs.Write = func(int, []byte) (int, error) {
		s.FailNow("nothing to write")
		return 0, nil
	}
	s.Require().NoError(s.fs.Install(snap, false))
	got, err := os.ReadFile(dst)
	s.Require().NoError(err)
	s.Require().Empty(got)
}

func (s *OSGuardFSSuite) TestInstallTempNameAndFlags() {
	src, dst := s.repo("new\n", "")
	snap := s.snapshot(src, dst)
	s.fs.Rand = bytes.NewReader([]byte{0xde, 0xad, 0xbe, 0xef, 0, 1, 2, 3})
	var gotTmp string
	var gotFlags int
	s.fs.Openat = func(dirfd int, path string, flags int, mode uint32) (int, error) {
		if flags&unix.O_CREAT != 0 {
			gotTmp, gotFlags = path, flags
		}
		return unix.Openat(dirfd, path, flags, mode)
	}
	var gotRenameFlags uint
	s.fs.Renameat2 = func(od int, op string, nd int, np string, flags uint) error {
		gotRenameFlags = flags
		return unix.Renameat2(od, op, nd, np, flags)
	}
	s.Require().NoError(s.fs.Install(snap, true))
	s.Require().Equal(".config.loop-gate-deadbeef00010203", gotTmp)
	s.Require().Equal(unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, gotFlags)
	s.Require().Equal(uint(unix.RENAME_NOREPLACE), gotRenameFlags)
}

func (s *OSGuardFSSuite) TestInstallInjectedFailures() {
	cases := []struct {
		name string
		prep func(f *OSGuardFS)
		want error
	}{
		{"rand fails", func(f *OSGuardFS) { f.Rand = errReader{err: unix.EIO} }, unix.EIO},
		{"temp open fails", func(f *OSGuardFS) {
			f.Openat = func(int, string, int, uint32) (int, error) { return -1, unix.EACCES }
		}, unix.EACCES},
		{"write fails", func(f *OSGuardFS) {
			f.Write = func(int, []byte) (int, error) { return 0, unix.ENOSPC }
		}, unix.ENOSPC},
		{"fchmod fails", func(f *OSGuardFS) {
			f.Fchmod = func(int, uint32) error { return unix.EPERM }
		}, unix.EPERM},
		{"rename fails", func(f *OSGuardFS) {
			f.Renameat2 = func(int, string, int, string, uint) error { return unix.EXDEV }
		}, unix.EXDEV},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			src, dst := s.repo("new\n", "old\n")
			snap := s.snapshot(src, dst)
			c.prep(s.fs)
			s.Require().ErrorIs(s.fs.Install(snap, false), c.want)
			got, err := os.ReadFile(dst)
			s.Require().NoError(err)
			s.Require().Equal("old\n", string(got))
			s.Require().FileExists(src)
			s.Require().ElementsMatch([]string{"config", "config.lock"}, s.dirNames(s.path(".git")), "no temp file left behind")
		})
	}
}

func (s *OSGuardFSSuite) TestInstallMissingTargetDir() {
	src, dst := s.repo("new\n", "")
	snap := s.snapshot(src, dst)
	snap.Dst = s.path("missing", "config")
	s.Require().ErrorIs(s.fs.Install(snap, false), unix.ENOENT)
	s.Require().FileExists(src)
}

func (s *OSGuardFSSuite) TestInstallKeepsSwappedSource() {
	src, dst := s.repo("new\n", "")
	snap := s.snapshot(src, dst)
	// Swap a different inode in under the source name after the decision.
	other := s.path(".git", "other")
	s.writeFile(other, "swapped\n", 0o644)
	s.Require().NoError(os.Rename(other, src))

	s.Require().NoError(s.fs.Install(snap, false))
	got, err := os.ReadFile(src)
	s.Require().NoError(err)
	s.Require().Equal("swapped\n", string(got))
	got, err = os.ReadFile(dst)
	s.Require().NoError(err)
	s.Require().Equal("new\n", string(got))
}

func (s *OSGuardFSSuite) TestInstallSourceGoneIsBestEffort() {
	cases := []struct {
		name string
		prep func(src string)
	}{
		{"source dir removed", func(src string) {
			s.Require().NoError(os.RemoveAll(filepath.Dir(src)))
		}},
		{"source name removed", func(src string) {
			s.Require().NoError(os.Remove(src))
		}},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			src := s.path("staging", "hook")
			dst := s.path(".git", "hooks", "pre-commit")
			s.writeFile(src, "#!/bin/sh\n", 0o755)
			s.Require().NoError(os.MkdirAll(filepath.Dir(dst), 0o755))
			snap := s.snapshot(src, dst)
			c.prep(src)
			s.Require().NoError(s.fs.Install(snap, false))
			got, err := os.ReadFile(dst)
			s.Require().NoError(err)
			s.Require().Equal("#!/bin/sh\n", string(got))
		})
	}
}

func (s *OSGuardFSSuite) TestInstallSourceReadFailureKeepsSource() {
	src, dst := s.repo("new\n", "")
	snap := s.snapshot(src, dst)
	s.fs.Fstat = func(int, *unix.Stat_t) error { return unix.EIO }
	s.Require().NoError(s.fs.Install(snap, false))
	s.Require().FileExists(src)
	s.Require().FileExists(dst)
}

func (s *OSGuardFSSuite) TestInstallRemoveSourceChecksContent() {
	cases := []struct {
		name    string
		prep    func(src string)
		removed bool
	}{
		{"rewritten with other bytes", func(src string) {
			s.Require().NoError(os.WriteFile(src, []byte("other\n"), 0o644))
		}, false},
		{"replaced by a dir", func(src string) {
			s.Require().NoError(os.Remove(src))
			s.Require().NoError(os.Mkdir(src, 0o755))
		}, false},
		// A new inode with the approved bytes is indistinguishable from the
		// original on mounts that renumber inodes; removing it is what the
		// rename would have done.
		{"new inode, same bytes", func(src string) {
			s.Require().NoError(os.Remove(src))
			s.Require().NoError(os.WriteFile(src, []byte("new\n"), 0o644))
		}, true},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			src, dst := s.repo("new\n", "")
			snap := s.snapshot(src, dst)
			c.prep(src)
			s.Require().NoError(s.fs.Install(snap, false))
			_, err := os.Lstat(src)
			s.Require().Equal(c.removed, os.IsNotExist(err))
		})
	}
}

func (s *OSGuardFSSuite) TestInstallUnlinkErrorIsIgnored() {
	src, dst := s.repo("new\n", "")
	snap := s.snapshot(src, dst)
	var unlinked []string
	s.fs.Unlinkat = func(_ int, path string, _ int) error {
		unlinked = append(unlinked, path)
		return errors.New("unlink refused")
	}
	s.Require().NoError(s.fs.Install(snap, false))
	s.Require().Equal([]string{"config.lock"}, unlinked)
	s.Require().FileExists(src)
}
