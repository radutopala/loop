//go:build linux

package agentgate

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Seccomp data field offsets. Mirrors `struct seccomp_data` in
// include/uapi/linux/seccomp.h. We only read .nr and .arch.
const (
	sdOffsetNR   uint32 = 0
	sdOffsetArch uint32 = 4
)

// Kernel-defined audit arch tags (include/uapi/linux/audit.h). We don't import
// a seccomp helper lib; these are carried verbatim from the kernel header.
const (
	auditArchX86_64  uint32 = 0xC000003E
	auditArchAArch64 uint32 = 0xC00000B7
)

// Seccomp return values (include/uapi/linux/seccomp.h). x/sys/unix does not
// export these as of v0.41.
const (
	seccompRetKillProcess uint32 = 0x80000000
	seccompRetAllow       uint32 = 0x7fff0000
	seccompRetUserNotif   uint32 = 0x7fc00000
	seccompRetErrno       uint32 = 0x00050000
)

// auditArchFor maps a Go GOARCH string to the kernel audit arch tag. Unsupported
// architectures return an error rather than a default — a mismatched filter is
// worse than refusing to start.
func auditArchFor(goarch string) (uint32, error) {
	switch goarch {
	case "amd64":
		return auditArchX86_64, nil
	case "arm64":
		return auditArchAArch64, nil
	default:
		return 0, fmt.Errorf("agentgate: unsupported GOARCH %q (want amd64 or arm64)", goarch)
	}
}

// TrapSyscalls returns the syscall numbers the gate traps via
// SECCOMP_RET_USER_NOTIF on the current GOARCH.
func TrapSyscalls() []uint32 {
	return []uint32{
		uint32(unix.SYS_CONNECT),
		uint32(unix.SYS_EXECVE),
		uint32(unix.SYS_EXECVEAT),
		uint32(unix.SYS_OPENAT),
		uint32(unix.SYS_OPENAT2),
		uint32(unix.SYS_RENAMEAT2),
		uint32(unix.SYS_UNLINKAT),
		uint32(unix.SYS_LINKAT),
		uint32(unix.SYS_SYMLINKAT),
		uint32(unix.SYS_FCHMODAT),
		uint32(unix.SYS_FCHOWNAT),
		uint32(unix.SYS_MKDIRAT),
	}
}

// DenySyscalls returns the syscall numbers the gate rejects at the BPF layer
// (SECCOMP_RET_ERRNO = EPERM). io_uring lets the agent perform file / network
// ops without re-entering the syscall layer — deny the whole family rather
// than chase per-op filtering.
func DenySyscalls() []uint32 {
	return []uint32{
		uint32(unix.SYS_IO_URING_SETUP),
		uint32(unix.SYS_IO_URING_ENTER),
		uint32(unix.SYS_IO_URING_REGISTER),
	}
}

// buildFilter assembles the seccomp BPF program.
//
// Layout:
//
//	ld   [arch]                   ; seccomp_data.arch
//	jeq  expectedArch, jt=1, jf=0 ; skip the kill when arch matches
//	ret  KILL_PROCESS             ; mismatched arch — kill the process
//	ld   [nr]                     ; seccomp_data.nr
//	for nr in trap:
//	    jeq nr, jt=0, jf=1        ; fall through to the RET on match
//	    ret USER_NOTIF
//	for nr in deny:
//	    jeq nr, jt=0, jf=1
//	    ret ERRNO(EPERM)
//	ret  ALLOW
//
// Inlining the RET after each JEQ (rather than packing all RETs at the end)
// keeps jump distances trivially 0/1. Program size is 5 + 2*(len(trap)+len(deny))
// instructions — comfortably below the 4096-instruction seccomp limit.
func buildFilter(expectedArch uint32, trap, deny []uint32) []unix.SockFilter {
	const (
		ldW  = uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS)
		jeqK = uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K)
		retK = uint16(unix.BPF_RET | unix.BPF_K)
	)

	prog := make([]unix.SockFilter, 0, 4+2*(len(trap)+len(deny))+1)

	prog = append(prog,
		unix.SockFilter{Code: ldW, K: sdOffsetArch},
		unix.SockFilter{Code: jeqK, Jt: 1, Jf: 0, K: expectedArch},
		unix.SockFilter{Code: retK, K: seccompRetKillProcess},
		unix.SockFilter{Code: ldW, K: sdOffsetNR},
	)

	for _, nr := range trap {
		prog = append(prog,
			unix.SockFilter{Code: jeqK, Jt: 0, Jf: 1, K: nr},
			unix.SockFilter{Code: retK, K: seccompRetUserNotif},
		)
	}
	for _, nr := range deny {
		prog = append(prog,
			unix.SockFilter{Code: jeqK, Jt: 0, Jf: 1, K: nr},
			unix.SockFilter{Code: retK, K: seccompRetErrno | uint32(unix.EPERM)},
		)
	}
	prog = append(prog, unix.SockFilter{Code: retK, K: seccompRetAllow})

	return prog
}

// FilterInstaller compiles and installs the seccomp filter on the current
// thread. Construct via NewFilterInstaller. The prctl / seccomp fields are
// exposed for test injection — production callers should not touch them.
type FilterInstaller struct {
	GOARCH  string
	Trap    []uint32
	Deny    []uint32
	Prctl   func(option int, arg2, arg3, arg4, arg5 uintptr) error
	Seccomp func(op, flags uint, prog *unix.SockFprog) (uintptr, unix.Errno)
}

// NewFilterInstaller returns an installer wired to real syscalls.
func NewFilterInstaller() *FilterInstaller {
	return &FilterInstaller{
		GOARCH:  runtime.GOARCH,
		Trap:    TrapSyscalls(),
		Deny:    DenySyscalls(),
		Prctl:   unix.Prctl,
		Seccomp: rawSeccomp,
	}
}

// Install installs the filter on the current thread with TSYNC (applies to
// every sibling thread) and NEW_LISTENER (returns a notify fd the gate-server
// reads to receive syscall events). The returned fd is non-blocking-capable;
// the caller owns it.
//
// Install must run on an OS-locked goroutine (see runtime.LockOSThread) —
// seccomp filters apply to the calling thread, and a subsequent exec on a
// different thread would run unfiltered.
func (fi *FilterInstaller) Install() (int, error) {
	arch, err := auditArchFor(fi.GOARCH)
	if err != nil {
		return -1, err
	}

	prog := buildFilter(arch, fi.Trap, fi.Deny)
	fprog := unix.SockFprog{
		Len:    uint16(len(prog)),
		Filter: &prog[0],
	}

	// PR_SET_NO_NEW_PRIVS is required before loading a filter unless the
	// caller holds CAP_SYS_ADMIN. Idempotent — safe to call repeatedly.
	if err := fi.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return -1, fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}

	// TSYNC + NEW_LISTENER must also set TSYNC_ESRCH: otherwise the kernel
	// rejects the combination with EINVAL because the two flags overload
	// the syscall return value (NEW_LISTENER wants fd, TSYNC wants the
	// first out-of-sync thread id). TSYNC_ESRCH makes sync-failure report
	// via errno (ESRCH) instead, resolving the collision. Supported since
	// Linux 5.7.
	r, errno := fi.Seccomp(
		uint(unix.SECCOMP_SET_MODE_FILTER),
		uint(unix.SECCOMP_FILTER_FLAG_NEW_LISTENER|unix.SECCOMP_FILTER_FLAG_TSYNC|unix.SECCOMP_FILTER_FLAG_TSYNC_ESRCH),
		&fprog,
	)
	if errno != 0 {
		return -1, fmt.Errorf("seccomp(SET_MODE_FILTER, NEW_LISTENER|TSYNC|TSYNC_ESRCH): %w", errno)
	}
	return int(r), nil
}

// rawSeccomp is the production seccomp(2) wrapper. It exists as a function
// value so the test suite can replace it via FilterInstaller.Seccomp.
func rawSeccomp(op, flags uint, prog *unix.SockFprog) (uintptr, unix.Errno) {
	r, _, errno := unix.Syscall(
		unix.SYS_SECCOMP,
		uintptr(op),
		uintptr(flags),
		uintptr(unsafe.Pointer(prog)),
	)
	return r, errno
}
