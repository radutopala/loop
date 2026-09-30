//go:build linux

package agentgate

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// OSGuardFS is the production GuardFS. Every file access runs on a
// throwaway OS thread whose fsuid/fsgid are the tracee's and whose
// supplementary groups are cleared: the kernel checks permissions as the
// agent (dropping the gate's DAC override with a non-zero fsuid), and new
// files come out owned by the agent. Parent directories open with
// RESOLVE_NO_SYMLINKS: the paths arrive already resolved, so a symlink
// there means one was swapped in since.
//
// The syscall fields exist for fault injection in tests.
type OSGuardFS struct {
	Openat2   func(dirfd int, path string, how *unix.OpenHow) (int, error)
	Openat    func(dirfd int, path string, flags int, mode uint32) (int, error)
	Fstat     func(fd int, st *unix.Stat_t) error
	Fstatat   func(dirfd int, path string, st *unix.Stat_t, flags int) error
	Fchmod    func(fd int, mode uint32) error
	Write     func(fd int, p []byte) (int, error)
	Renameat2 func(olddirfd int, oldpath string, newdirfd int, newpath string, flags uint) error
	Unlinkat  func(dirfd int, path string, flags int) error
	Rand      io.Reader

	// RawSyscall backs setIDs (setgroups, setfsgid, setfsuid).
	RawSyscall func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err unix.Errno)
}

// NewOSGuardFS returns an OSGuardFS wired to the real syscalls.
func NewOSGuardFS() *OSGuardFS {
	return &OSGuardFS{
		Openat2:    unix.Openat2,
		Openat:     unix.Openat,
		Fstat:      unix.Fstat,
		Fstatat:    unix.Fstatat,
		Fchmod:     unix.Fchmod,
		Write:      unix.Write,
		Renameat2:  unix.Renameat2,
		Unlinkat:   unix.Unlinkat,
		Rand:       rand.Reader,
		RawSyscall: unix.RawSyscall,
	}
}

// setIDs sets the calling thread's fs ids and clears its supplementary
// groups. Raw syscalls: Go's syscall.Setgroups would apply to every
// thread in the process.
func (f *OSGuardFS) setIDs(uid, gid int) error {
	if _, _, errno := f.RawSyscall(unix.SYS_SETGROUPS, 0, 0, 0); errno != 0 {
		return errno
	}
	_, _, _ = f.RawSyscall(unix.SYS_SETFSGID, uintptr(gid), 0, 0)
	_, _, _ = f.RawSyscall(unix.SYS_SETFSUID, uintptr(uid), 0, 0)
	// setfsuid/setfsgid report failure only by not changing the id; read
	// both back (-1 queries) to be sure the switch took.
	gotGID, _, _ := f.RawSyscall(unix.SYS_SETFSGID, ^uintptr(0), 0, 0)
	gotUID, _, _ := f.RawSyscall(unix.SYS_SETFSUID, ^uintptr(0), 0, 0)
	if int(gotUID) != uid || int(gotGID) != gid {
		return unix.EPERM
	}
	return nil
}

// asUser runs fn on a fresh OS thread switched to uid/gid. The thread is
// never unlocked, so it exits with the goroutine and its ids can't leak
// to other goroutines.
func (f *OSGuardFS) asUser(uid, gid int, fn func() error) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := f.setIDs(uid, gid); err != nil {
			errc <- err
			return
		}
		errc <- fn()
	}()
	return <-errc
}

func (f *OSGuardFS) openDir(path string) (int, error) {
	return f.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
}

// readRegular opens name under dirfd without following a final symlink and
// reads it. ok=false (with a nil error) when it isn't a regular file.
func (f *OSGuardFS) readRegular(dirfd int, name string) (data []byte, st unix.Stat_t, ok bool, err error) {
	fd, err := f.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, st, false, nil
		}
		return nil, st, false, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	if err := f.Fstat(fd, &st); err != nil {
		return nil, st, false, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, st, false, nil
	}
	if st.Size > maxGuardedFileSize {
		return nil, st, false, ErrGuardTooLarge
	}
	data, err = io.ReadAll(io.LimitReader(file, maxGuardedFileSize+1))
	if err != nil {
		return nil, st, false, err
	}
	if len(data) > maxGuardedFileSize {
		return nil, st, false, ErrGuardTooLarge
	}
	return data, st, true, nil
}

// Snapshot implements GuardFS.
func (f *OSGuardFS) Snapshot(src, dst string, uid, gid int) (*GuardSnapshot, error) {
	snap := &GuardSnapshot{Src: src, Dst: dst, UID: uid, GID: gid}
	err := f.asUser(uid, gid, func() error {
		srcDir, err := f.openDir(filepath.Dir(src))
		if err != nil {
			return err
		}
		defer unix.Close(srcDir)
		data, st, ok, err := f.readRegular(srcDir, filepath.Base(src))
		if err != nil {
			return err
		}
		if !ok {
			return ErrGuardNotRegular
		}
		snap.Data = data
		snap.Mode = os.FileMode(st.Mode & 0o777)

		dstDir, err := f.openDir(filepath.Dir(dst))
		if err != nil {
			return err
		}
		defer unix.Close(dstDir)
		old, _, ok, err := f.readRegular(dstDir, filepath.Base(dst))
		switch {
		case errors.Is(err, unix.ENOENT):
			return nil
		case err != nil:
			return err
		}
		// A non-regular target (a symlink, a dir) has no content to diff;
		// the rename replaces the entry (or fails with the kernel's errno).
		snap.Old, snap.OldExists = old, ok
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// IsRegular implements GuardFS. The gate's own view is the tracee's: same
// mount namespace, and path is already resolved.
func (f *OSGuardFS) IsRegular(path string) (bool, error) {
	var st unix.Stat_t
	if err := f.Fstatat(unix.AT_FDCWD, path, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, err
	}
	return st.Mode&unix.S_IFMT == unix.S_IFREG, nil
}

// Install implements GuardFS.
func (f *OSGuardFS) Install(snap *GuardSnapshot, noReplace bool) error {
	return f.asUser(snap.UID, snap.GID, func() error {
		dstDir, err := f.openDir(filepath.Dir(snap.Dst))
		if err != nil {
			return err
		}
		defer unix.Close(dstDir)

		var suffix [8]byte
		if _, err := io.ReadFull(f.Rand, suffix[:]); err != nil {
			return err
		}
		tmp := "." + filepath.Base(snap.Dst) + ".loop-gate-" + hex.EncodeToString(suffix[:])
		fd, err := f.Openat(dstDir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(snap.Mode))
		if err != nil {
			return err
		}
		err = f.fill(fd, snap)
		_ = unix.Close(fd)
		if err == nil {
			var flags uint
			if noReplace {
				flags = unix.RENAME_NOREPLACE
			}
			err = f.Renameat2(dstDir, tmp, dstDir, filepath.Base(snap.Dst), flags)
		}
		if err != nil {
			_ = f.Unlinkat(dstDir, tmp, 0)
			return err
		}
		f.removeSource(snap)
		return nil
	})
}

// fill writes the approved bytes and sets the source's mode (the create
// mode went through the umask).
func (f *OSGuardFS) fill(fd int, snap *GuardSnapshot) error {
	for data := snap.Data; len(data) > 0; {
		n, err := f.Write(fd, data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return f.Fchmod(fd, uint32(snap.Mode))
}

// removeSource unlinks the rename's source, as the rename would have — but
// only while the name still holds the bytes that were installed. Content,
// not dev/ino: inode numbers aren't stable on FUSE host mounts (Docker
// Desktop's file sharing renumbers them between lookups). Best effort: the
// target is already in place.
func (f *OSGuardFS) removeSource(snap *GuardSnapshot) {
	srcDir, err := f.openDir(filepath.Dir(snap.Src))
	if err != nil {
		return
	}
	defer unix.Close(srcDir)
	name := filepath.Base(snap.Src)
	data, _, ok, err := f.readRegular(srcDir, name)
	if err != nil || !ok || !bytes.Equal(data, snap.Data) {
		return
	}
	_ = f.Unlinkat(srcDir, name, 0)
}

// ProcProcess is the production ProcessLookup, reading /proc under Root.
// The gate shares the tracee's pid namespace, so trap pids resolve there.
type ProcProcess struct {
	Root string
}

// NewProcProcess returns a ProcProcess reading /proc.
func NewProcProcess() *ProcProcess {
	return &ProcProcess{Root: "/proc"}
}

// Lookup implements ProcessLookup.
func (p *ProcProcess) Lookup(pid int) (ProcessInfo, error) {
	dir := filepath.Join(p.Root, strconv.Itoa(pid))
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return ProcessInfo{}, err
	}
	start, err := procStartTime(stat)
	if err != nil {
		return ProcessInfo{}, err
	}
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil {
		return ProcessInfo{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return ProcessInfo{}, err
	}
	cmdline := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	return ProcessInfo{Exe: exe, Cmdline: cmdline, StartTime: start}, nil
}

// procStartTime reads field 22 (starttime) of /proc/<pid>/stat. The comm
// field before it is parenthesised and may hold spaces and parentheses, so
// fields count from the last ")".
func procStartTime(stat []byte) (uint64, error) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, errProcStat
	}
	fields := strings.Fields(string(stat[i+1:]))
	// fields[0] is field 3 (state); starttime is field 22.
	if len(fields) < 20 {
		return 0, errProcStat
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

var errProcStat = errors.New("agentgate: malformed /proc/<pid>/stat")
