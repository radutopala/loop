//go:build linux

package agentgate

import (
	"context"
	"fmt"
	"io"
	"unsafe"

	"golang.org/x/sys/unix"
)

// seccompData mirrors `struct seccomp_data` in include/uapi/linux/seccomp.h.
// Layout must match the kernel byte-for-byte; a size mismatch means a misread.
// Size: 4 + 4 + 8 + 48 = 64.
type seccompData struct {
	NR                 int32
	Arch               uint32
	InstructionPointer uint64
	Args               [6]uint64
}

// seccompNotif mirrors `struct seccomp_notif`. Size: 8 + 4 + 4 + 64 = 80.
type seccompNotif struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Data  seccompData
}

// seccompNotifResp mirrors `struct seccomp_notif_resp`. Size: 8 + 8 + 4 + 4 = 24.
type seccompNotifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

// Compile-time size guards. If the kernel ever grows `seccomp_notif` we want
// the build to fail here, not at runtime with subtle struct misreads.
const (
	sizeofSeccompNotif     = 80
	sizeofSeccompNotifResp = 24
	sizeofSeccompData      = 64
)

// The "len([N]int{}) < 0" idiom forces a compile error when N is negative,
// which happens iff unsafe.Sizeof differs from the expected constant.
var (
	_ = [1]struct{}{}[sizeofSeccompNotif-unsafe.Sizeof(seccompNotif{})]
	_ = [1]struct{}{}[unsafe.Sizeof(seccompNotif{})-sizeofSeccompNotif]
	_ = [1]struct{}{}[sizeofSeccompNotifResp-unsafe.Sizeof(seccompNotifResp{})]
	_ = [1]struct{}{}[unsafe.Sizeof(seccompNotifResp{})-sizeofSeccompNotifResp]
	_ = [1]struct{}{}[sizeofSeccompData-unsafe.Sizeof(seccompData{})]
	_ = [1]struct{}{}[unsafe.Sizeof(seccompData{})-sizeofSeccompData]
)

// syscallNameByNR maps the kernel's `seccomp_data.nr` to the canonical name
// the portable dispatcher switches on. Keys are the GOARCH-specific unix.SYS_*
// constants; restricted to the same set buildFilter() traps, so an unknown nr
// arriving here is a sign BPF and the map have drifted.
var syscallNameByNR = map[int32]string{
	int32(unix.SYS_EXECVE):    "execve",
	int32(unix.SYS_EXECVEAT):  "execveat",
	int32(unix.SYS_CONNECT):   "connect",
	int32(unix.SYS_OPENAT):    syscallOpenat,
	int32(unix.SYS_OPENAT2):   syscallOpenat2,
	int32(unix.SYS_RENAMEAT2): syscallRenameat2,
	int32(unix.SYS_UNLINKAT):  syscallUnlinkat,
	int32(unix.SYS_LINKAT):    syscallLinkat,
	int32(unix.SYS_SYMLINKAT): syscallSymlinkat,
	int32(unix.SYS_FCHMODAT):  syscallFchmodat,
	int32(unix.SYS_FCHOWNAT):  syscallFchownat,
	int32(unix.SYS_MKDIRAT):   syscallMkdirat,
}

// syscallName translates a kernel syscall nr into the canonical name used by
// the dispatcher. ok=false means "not in our trap set" — the dispatcher's
// default branch denies.
func syscallName(nr int32) (string, bool) {
	name, ok := syscallNameByNR[nr]
	return name, ok
}

// IoctlFunc is the signature of the raw ioctl wrapper. Exposed so tests can
// substitute a fake that writes / inspects the struct buffer without going
// through the kernel.
type IoctlFunc func(fd int, req uintptr, arg unsafe.Pointer) (int, unix.Errno)

// NotifyTransport wraps a seccomp-notify fd with Recv/Send ioctls. It
// implements the portable Transport interface the dispatcher consumes.
type NotifyTransport struct {
	FD      int
	IoctlFn IoctlFunc
	CloseFn func(fd int) error
}

// NewNotifyTransport wires a NotifyTransport to the real ioctl + close
// syscalls. Callers pass the fd returned by FilterInstaller.Install.
func NewNotifyTransport(fd int) *NotifyTransport {
	return &NotifyTransport{
		FD:      fd,
		IoctlFn: rawIoctl,
		CloseFn: unix.Close,
	}
}

// rawIoctl is the production SYS_IOCTL wrapper. Exists as a top-level function
// so it's substitutable via NotifyTransport.IoctlFn in tests.
func rawIoctl(fd int, req uintptr, arg unsafe.Pointer) (int, unix.Errno) {
	r, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	return int(r), errno
}

// Recv blocks on SECCOMP_IOCTL_NOTIF_RECV until the kernel delivers a trap
// or the fd is closed (EBADF → io.EOF, the shutdown signal for Server.Run).
//
// EINTR and ENOENT are retried:
//
//   - EINTR: Go's runtime async-preemption signal (SIGURG since Go 1.14)
//     routinely interrupts blocking syscalls on long-lived M's. Treating
//     EINTR as shutdown would silently kill the dispatcher on any
//     sufficiently long workload (observed: `make coverage-check` inside a
//     gated container hung every trapped openat once the dispatcher exited).
//   - ENOENT: the kernel returns this when the trapped tracee died before we
//     called RECV. The trap is gone; loop to pick up the next notification.
//
// A ctx already past its deadline short-circuits before the syscall, and the
// retry loop re-checks ctx so shutdown still unblocks Recv promptly.
func (n *NotifyTransport) Recv(ctx context.Context) (Trap, error) {
	var notif seccompNotif
	for {
		if err := ctx.Err(); err != nil {
			return Trap{}, err
		}
		_, errno := n.IoctlFn(n.FD, unix.SECCOMP_IOCTL_NOTIF_RECV, unsafe.Pointer(&notif))
		switch errno {
		case 0:
			// fall through to decode
		case unix.EBADF:
			return Trap{}, io.EOF
		case unix.EINTR, unix.ENOENT:
			continue
		default:
			return Trap{}, fmt.Errorf("ioctl(SECCOMP_IOCTL_NOTIF_RECV): %w", errno)
		}
		break
	}
	name, ok := syscallName(notif.Data.NR)
	if !ok {
		// BPF filter traps more nrs than our map covers → bug. Surface it as
		// a synthetic name so the dispatcher's default branch denies and the
		// audit log records the nr for debugging.
		name = fmt.Sprintf("nr_%d", notif.Data.NR)
	}
	return Trap{
		ID:      notif.ID,
		PID:     int(notif.PID),
		Syscall: name,
		Args:    notif.Data.Args,
	}, nil
}

// Send delivers a TrapResponse via SECCOMP_IOCTL_NOTIF_SEND. Allow=true sets
// the CONTINUE flag so the kernel runs the syscall normally; Allow=false sets
// a negative errno (defaulting to -EPERM when ErrorNum is zero).
//
// EINTR and ENOENT are handled like Recv:
//
//   - EINTR: Go's async preemption (SIGURG) can interrupt the ioctl; retry
//     so a spurious signal doesn't leave the tracee wedged in
//     seccomp_do_user_notification.
//   - ENOENT: the tracee died between Recv and Send. There's no one left to
//     unblock, so returning nil is the sensible outcome — the trap is
//     resolved by the tracee's exit.
func (n *NotifyTransport) Send(ctx context.Context, resp TrapResponse) error {
	r := seccompNotifResp{ID: resp.ID}
	if resp.Allow {
		r.Flags = unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE
	} else {
		errnum := resp.ErrorNum
		if errnum == 0 {
			errnum = int32(unix.EPERM)
		}
		r.Error = -errnum
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, errno := n.IoctlFn(n.FD, unix.SECCOMP_IOCTL_NOTIF_SEND, unsafe.Pointer(&r))
		switch errno {
		case 0:
			return nil
		case unix.EINTR:
			continue
		case unix.ENOENT:
			return nil
		default:
			return fmt.Errorf("ioctl(SECCOMP_IOCTL_NOTIF_SEND): %w", errno)
		}
	}
}

// Close closes the notify fd. Any Recv blocked on the fd from another
// goroutine returns io.EOF once the kernel unblocks it.
func (n *NotifyTransport) Close() error {
	return n.CloseFn(n.FD)
}

// NewProcTraceeFactory returns a TraceeFactory that constructs a fresh
// ProcTracee per trap. Used to wire Server.Factory in production.
func NewProcTraceeFactory() TraceeFactory {
	return func(pid int) Tracee {
		return NewProcTracee(pid)
	}
}
