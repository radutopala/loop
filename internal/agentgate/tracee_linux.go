//go:build linux

package agentgate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ProcTracee implements Tracee against a real Linux process via
// process_vm_readv(2) and /proc/<pid>/... Exported syscall fields let tests
// inject fakes without touching a real process.
//
// A ProcTracee is pid-specific — construct one per trap.
type ProcTracee struct {
	PID int

	// ReadMem wraps process_vm_readv(2). Defaults to unix.ProcessVMReadv.
	ReadMem func(pid int, localIov []unix.Iovec, remoteIov []unix.RemoteIovec, flags uint) (int, error)

	// Readlink reads a magic link (/proc/<pid>/fd/<N>, /proc/<pid>/cwd).
	// Defaults to unix.Readlinkat(unix.AT_FDCWD, …).
	Readlink func(path string, buf []byte) (int, error)

	// Lstat and ReadlinkPath back EvalSymlinks' walk. Default to os.Lstat
	// and os.Readlink.
	Lstat        func(path string) (os.FileInfo, error)
	ReadlinkPath func(path string) (string, error)

	// ReadFile reads /proc/<pid>/status for Creds and the thread group id.
	// Defaults to os.ReadFile.
	ReadFile func(path string) ([]byte, error)
}

// NewProcTracee returns a ProcTracee wired to the real syscalls.
func NewProcTracee(pid int) *ProcTracee {
	return &ProcTracee{
		PID:          pid,
		ReadMem:      unix.ProcessVMReadv,
		Readlink:     readlinkDefault,
		Lstat:        os.Lstat,
		ReadlinkPath: os.Readlink,
		ReadFile:     os.ReadFile,
	}
}

func readlinkDefault(path string, buf []byte) (int, error) {
	return unix.Readlinkat(unix.AT_FDCWD, path, buf)
}

// ReadString reads up to PATHMAX bytes from addr and returns the string up to
// the first NULL. process_vm_readv(2) is atomic per iovec — a short read
// means we hit the end of a mapping, which is fine; the C string just ends
// with a NULL before the boundary.
func (t *ProcTracee) ReadString(addr uintptr) (string, error) {
	if addr == 0 {
		return "", nil
	}
	buf := make([]byte, PATHMAX)
	local := []unix.Iovec{{Base: &buf[0], Len: uint64(len(buf))}}
	remote := []unix.RemoteIovec{{Base: addr, Len: len(buf)}}
	n, err := t.ReadMem(t.PID, local, remote, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return "", ErrTraceeGone
		}
		// EFAULT can mean we crossed a mapping boundary — fall through and
		// take whatever we got so far. Zero-length reads are treated as
		// gone below.
		if !errors.Is(err, unix.EFAULT) {
			return "", fmt.Errorf("process_vm_readv: %w", err)
		}
	}
	if n <= 0 {
		return "", ErrTraceeGone
	}
	buf = buf[:n]
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), nil
		}
	}
	// No NULL within PATHMAX — return the full PATHMAX slice. The kernel
	// would reject a path this long itself (ENAMETOOLONG); we fail-closed at
	// the handler by returning the truncated path verbatim.
	return string(buf), nil
}

// ReadBytes reads exactly n bytes at addr. Short reads (EFAULT at a mapping
// boundary, or the process exited mid-read) collapse to ErrTraceeGone so the
// caller fails closed. n must be positive; pass ≤ PATHMAX at call sites.
func (t *ProcTracee) ReadBytes(addr uintptr, n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("agentgate: ReadBytes: n must be positive, got %d", n)
	}
	if addr == 0 {
		return nil, ErrTraceeGone
	}
	buf := make([]byte, n)
	local := []unix.Iovec{{Base: &buf[0], Len: uint64(n)}}
	remote := []unix.RemoteIovec{{Base: addr, Len: n}}
	got, err := t.ReadMem(t.PID, local, remote, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return nil, ErrTraceeGone
		}
		return nil, fmt.Errorf("process_vm_readv: %w", err)
	}
	if got < n {
		return nil, ErrTraceeGone
	}
	return buf, nil
}

// ReadPointerArray walks a NULL-terminated array of pointers at addr. Each
// pointer is one word (8 bytes on 64-bit). We read one pointer at a time so
// a short mapping (argv crosses a stack guard page) doesn't lose the tail.
func (t *ProcTracee) ReadPointerArray(addr uintptr, maxEntries int) ([]string, error) {
	if addr == 0 {
		return nil, nil
	}
	if maxEntries <= 0 {
		maxEntries = ArgvMax
	}
	const wordSize = 8
	out := make([]string, 0, 8)
	for i := 0; i < maxEntries; i++ {
		var word [wordSize]byte
		local := []unix.Iovec{{Base: &word[0], Len: wordSize}}
		remote := []unix.RemoteIovec{{Base: addr + uintptr(i*wordSize), Len: wordSize}}
		n, err := t.ReadMem(t.PID, local, remote, 0)
		if err != nil {
			if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
				return nil, ErrTraceeGone
			}
			return nil, fmt.Errorf("process_vm_readv(argv[%d]): %w", i, err)
		}
		if n < wordSize {
			return nil, ErrTraceeGone
		}
		ptr := bytesToPtrLE(word[:])
		if ptr == 0 {
			return out, nil // NULL terminator
		}
		s, err := t.ReadString(ptr)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// bytesToPtrLE decodes a little-endian 8-byte pointer. All Linux architectures
// we target (amd64, arm64) are little-endian; a BE host would need a flip
// here, but we reject non-amd64/arm64 at the BPF arch check.
func bytesToPtrLE(b []byte) uintptr {
	var v uintptr
	for i := 7; i >= 0; i-- {
		v = (v << 8) | uintptr(b[i])
	}
	return v
}

// ResolveDirfd returns the absolute path the dirfd refers to. AtFDCWD →
// /proc/<pid>/cwd. Any other dirfd → /proc/<pid>/fd/<N>.
func (t *ProcTracee) ResolveDirfd(dirfd int32) (string, error) {
	var linkPath string
	if dirfd == AtFDCWD {
		linkPath = "/proc/" + strconv.Itoa(t.PID) + "/cwd"
	} else {
		linkPath = "/proc/" + strconv.Itoa(t.PID) + "/fd/" + strconv.Itoa(int(dirfd))
	}
	buf := make([]byte, PATHMAX)
	n, err := t.Readlink(linkPath, buf)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return "", ErrTraceeGone
		}
		return "", fmt.Errorf("readlink(%s): %w", linkPath, err)
	}
	return string(buf[:n]), nil
}

// maxSymlinkHops matches filepath.EvalSymlinks. It must stay above the
// kernel's own limit (40): a walk that gives up where the kernel wouldn't
// falls back to the unresolved path, which the kernel then follows.
const maxSymlinkHops = 255

// EvalSymlinks resolves path the way the tracee's own lookup would.
//
// The walk runs in the gate's process, where /proc/self and
// /proc/thread-self name the gate, not the tracee. Left alone, a tracee
// reopening one of its fds through /proc/self/fd/N (or /dev/fd/N, which
// links there) would be checked against whatever the gate's fd N is.
// So each step pins those two names to the tracee — also when a symlink
// leads to them — and the fd's magic link then resolves to the real file.
func (t *ProcTracee) EvalSymlinks(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("agentgate: EvalSymlinks: %q is not absolute", path)
	}
	resolved, rest, hops := "/", path, 0
	for rest != "" {
		var comp string
		comp, rest, _ = strings.Cut(strings.TrimLeft(rest, "/"), "/")
		switch comp {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next, err := t.pinProcSelf(filepath.Join(resolved, comp))
		if err != nil {
			return "", err
		}
		fi, err := t.Lstat(next)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}
		if hops++; hops > maxSymlinkHops {
			return "", unix.ELOOP
		}
		link, err := t.ReadlinkPath(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(link) {
			resolved = "/"
		}
		rest = link + "/" + rest
	}
	return resolved, nil
}

// pinProcSelf maps /proc/self to /proc/<tgid> and /proc/thread-self to
// /proc/<tgid>/task/<tid>. The trap's PID is the thread id; /proc/self is
// the thread group, whose fd table a thread that unshared its own
// (CLONE_FILES) doesn't use — so the tgid has to come from the status file.
func (t *ProcTracee) pinProcSelf(p string) (string, error) {
	if p != "/proc/self" && p != "/proc/thread-self" {
		return p, nil
	}
	status, err := t.status()
	if err != nil {
		return "", err
	}
	tgid := status["Tgid"]
	if len(tgid) == 0 {
		return "", fmt.Errorf("agentgate: no Tgid in /proc/%d/status", t.PID)
	}
	if p == "/proc/self" {
		return "/proc/" + tgid[0], nil
	}
	return "/proc/" + tgid[0] + "/task/" + strconv.Itoa(t.PID), nil
}

// Creds returns the tracee's fsuid and fsgid (the fourth field of the
// Uid:/Gid: lines in /proc/<pid>/status).
func (t *ProcTracee) Creds() (int, int, error) {
	status, err := t.status()
	if err != nil {
		return 0, 0, err
	}
	uid, err := statusID(status, "Uid")
	if err != nil {
		return 0, 0, err
	}
	gid, err := statusID(status, "Gid")
	if err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func statusID(status map[string][]string, key string) (int, error) {
	f := status[key]
	if len(f) != 4 {
		return 0, fmt.Errorf("agentgate: malformed %s line in /proc status", key)
	}
	return strconv.Atoi(f[3])
}

// status parses /proc/<pid>/status into key → whitespace-split fields.
func (t *ProcTracee) status() (map[string][]string, error) {
	raw, err := t.ReadFile("/proc/" + strconv.Itoa(t.PID) + "/status")
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return nil, ErrTraceeGone
		}
		return nil, err
	}
	out := map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[k] = strings.Fields(v)
		}
	}
	return out, nil
}
