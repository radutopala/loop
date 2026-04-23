//go:build linux

package agentgate

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

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

	// EvalSymlinksFn defaults to filepath.EvalSymlinks.
	EvalSymlinksFn func(path string) (string, error)
}

// NewProcTracee returns a ProcTracee wired to the real syscalls.
func NewProcTracee(pid int) *ProcTracee {
	return &ProcTracee{
		PID:            pid,
		ReadMem:        unix.ProcessVMReadv,
		Readlink:       readlinkDefault,
		EvalSymlinksFn: filepath.EvalSymlinks,
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

// EvalSymlinks delegates to filepath.EvalSymlinks (overridable for tests).
func (t *ProcTracee) EvalSymlinks(path string) (string, error) {
	return t.EvalSymlinksFn(path)
}
