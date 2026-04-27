package agentgate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"syscall"

	"github.com/radutopala/loop/internal/types"
)

// Trap is the dispatcher's view of one seccomp notify event.
//
// The kernel hands the gate a `struct seccomp_notif` (id, pid, flags,
// seccomp_data{nr, arch, ip, args[6]}); the linux-only transport decodes that
// into this portable Trap so the dispatch loop can be unit-tested on any host.
type Trap struct {
	ID      uint64
	PID     int
	Syscall string // canonical syscall name: "connect", "execve", "openat", …
	Args    [6]uint64
}

// TrapResponse is the dispatcher's reply to the kernel.
//
//	Allow=true  → flags=SECCOMP_USER_NOTIF_FLAG_CONTINUE (kernel runs the
//	              syscall normally; errno untouched).
//	Allow=false → flags=0, error=-ErrorNum. ErrorNum=0 defaults to EPERM.
type TrapResponse struct {
	ID       uint64
	Allow    bool
	ErrorNum int32
}

// Transport abstracts seccomp-notify fd I/O. Linux wires this to ioctl
// (SECCOMP_IOCTL_NOTIF_RECV / _SEND); tests inject an in-memory transport.
//
// Recv returning io.EOF means the notify fd has been closed (container exited
// or operator stopped the gate) — callers treat this as normal shutdown.
type Transport interface {
	Recv(ctx context.Context) (Trap, error)
	Send(ctx context.Context, resp TrapResponse) error
	Close() error
}

// TraceeFactory returns a Tracee for the given PID. The dispatcher constructs
// one per trap so test factories can hand out FakeTracees keyed on PID.
type TraceeFactory func(pid int) Tracee

// Server drives the notify dispatcher loop for a single container. One
// gate-server process may hold many Server instances — one per running agent.
type Server struct {
	Transport Transport
	Factory   TraceeFactory
	Execve    *ExecveHandler
	File      *FileHandler
	Connect   *ConnectHandler
	ChannelID string
}

// Close releases the notify transport. After Close, an in-flight Run iteration
// either sees io.EOF on its next Recv (clean shutdown) or returns through the
// caller's process-exit path. Close is the parent's shutdown lever — without
// it, a Run blocked deep in SECCOMP_IOCTL_NOTIF_RECV won't observe ctx
// cancellation, since the kernel waiter doesn't poll Go context state.
func (s *Server) Close() error {
	if s.Transport == nil {
		return nil
	}
	return s.Transport.Close()
}

// Run drives Transport.Recv → Dispatch → Transport.Send until ctx is canceled
// or Transport.Recv returns io.EOF. Per-trap errors during dispatch collapse
// to a Deny TrapResponse (fail-closed); only a Send-side error stops the loop.
func (s *Server) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		trap, err := s.Transport.Recv(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("agentgate: transport recv: %w", err)
		}
		resp := s.Dispatch(ctx, trap)
		if err := s.Transport.Send(ctx, resp); err != nil {
			return fmt.Errorf("agentgate: transport send: %w", err)
		}
	}
}

// Dispatch runs the policy for a single trap and returns the intended reply.
// Does not send — Run owns the send loop.
func (s *Server) Dispatch(ctx context.Context, trap Trap) TrapResponse {
	tracee := s.Factory(trap.PID)
	switch trap.Syscall {
	case "execve", "execveat":
		return s.dispatchExecve(ctx, trap, tracee)
	case "connect":
		return s.dispatchConnect(ctx, trap, tracee)
	case syscallOpenat, syscallOpenat2, syscallRenameat2, syscallUnlinkat,
		syscallLinkat, syscallSymlinkat, syscallFchmodat, syscallFchownat,
		syscallMkdirat:
		return s.dispatchFile(ctx, trap, tracee)
	default:
		// BPF filter + dispatcher must agree; an unknown syscall here is a
		// bug. Fail closed.
		return denyResp(trap.ID, syscall.EPERM)
	}
}

// AtEmptyPath is the execveat(2) / openat2(2) flag meaning "treat dirfd as
// the target; ignore pathname". Mirrors include/uapi/linux/fcntl.h.
const AtEmptyPath = 0x1000

func (s *Server) dispatchExecve(ctx context.Context, trap Trap, tracee Tracee) TrapResponse {
	if s.Execve == nil {
		return denyResp(trap.ID, syscall.EPERM)
	}

	var filenameAddr, argvAddr uintptr
	var dirfd int32
	var flags uint64
	switch trap.Syscall {
	case "execveat":
		dirfd = int32(trap.Args[0])
		filenameAddr = uintptr(trap.Args[1])
		argvAddr = uintptr(trap.Args[2])
		flags = trap.Args[4]
	default: // "execve"
		filenameAddr = uintptr(trap.Args[0])
		argvAddr = uintptr(trap.Args[1])
	}

	filename, err := tracee.ReadString(filenameAddr)
	if err != nil {
		return denyResp(trap.ID, syscall.EPERM)
	}
	// execveat(fd, "", …, AT_EMPTY_PATH) → the exec target is the fd itself.
	// /proc/<pid>/fd/<N> gives the backing path; memfd readlinks start with
	// "memfd:" which ExecveHandler uses to fire its memfd defense.
	if trap.Syscall == "execveat" && filename == "" && (flags&AtEmptyPath) != 0 {
		resolved, err := tracee.ResolveDirfd(dirfd)
		if err != nil {
			return denyResp(trap.ID, syscall.EPERM)
		}
		filename = resolved
	}

	argv, err := tracee.ReadPointerArray(argvAddr, ArgvMax)
	if err != nil {
		return denyResp(trap.ID, syscall.EPERM)
	}

	out := s.Execve.Handle(ctx, ExecveRequest{
		PID:       trap.PID,
		ChannelID: s.ChannelID,
		Syscall:   trap.Syscall,
		Filename:  filename,
		Argv:      argv,
	})
	return decisionResp(trap.ID, out.Decision)
}

func (s *Server) dispatchConnect(ctx context.Context, trap Trap, tracee Tracee) TrapResponse {
	if s.Connect == nil {
		return denyResp(trap.ID, syscall.EPERM)
	}
	// connect(sockfd, *sockaddr, addrlen)
	addrPtr := uintptr(trap.Args[1])
	addrLen := int(trap.Args[2])
	if addrLen < 2 {
		// Short addr — kernel would reject; let it through so errno is
		// kernel-native (EINVAL), not our EPERM.
		return allowResp(trap.ID)
	}
	if addrLen > SunPathMax+2 {
		addrLen = SunPathMax + 2
	}
	raw, err := tracee.ReadBytes(addrPtr, addrLen)
	if err != nil {
		return denyResp(trap.ID, syscall.EPERM)
	}
	path, isUnix := ParseUnixSockaddr(raw)
	if !isUnix {
		// TCP / UDP / other — v1 does not gate these. Allow the kernel to
		// run the syscall.
		return allowResp(trap.ID)
	}
	out := s.Connect.Handle(ctx, ConnectRequest{
		PID:       trap.PID,
		ChannelID: s.ChannelID,
		Path:      path,
	})
	return decisionResp(trap.ID, out.Decision)
}

func (s *Server) dispatchFile(ctx context.Context, trap Trap, tracee Tracee) TrapResponse {
	if s.File == nil {
		return denyResp(trap.ID, syscall.EPERM)
	}
	spec, ok := SyscallByName(trap.Syscall)
	if !ok {
		return denyResp(trap.ID, syscall.EPERM)
	}

	op, path, err := s.resolveFilePath(spec, trap, tracee)
	if err != nil {
		return denyResp(trap.ID, syscall.EPERM)
	}
	out := s.File.Handle(ctx, FileRequest{
		PID:       trap.PID,
		ChannelID: s.ChannelID,
		Syscall:   trap.Syscall,
		Op:        op,
		Path:      path,
	})
	if out.Decision != types.DecisionAllow {
		return decisionResp(trap.ID, out.Decision)
	}

	// Two-path syscalls (renameat2) must pass both the old and new path.
	if spec.SecondaryOp != "" {
		secPath, err := s.resolveSecondaryPath(spec, trap, tracee)
		if err != nil {
			return denyResp(trap.ID, syscall.EPERM)
		}
		secOut := s.File.Handle(ctx, FileRequest{
			PID:       trap.PID,
			ChannelID: s.ChannelID,
			Syscall:   trap.Syscall,
			Op:        spec.SecondaryOp,
			Path:      secPath,
		})
		return decisionResp(trap.ID, secOut.Decision)
	}
	return allowResp(trap.ID)
}

// resolveFilePath reads the primary path, resolves relative paths against
// dirfd, classifies openat flags if needed, and dereferences symlinks.
//
// Path-read and dirfd failures surface as errors so the dispatcher fails
// closed — we can't make a policy decision without a path. EvalSymlinks
// failure is different: it fires when a component doesn't exist or a link
// is dangling, which is also what the kernel would hit when it runs the
// syscall. Falling back to the cleaned absolute path lets policy evaluate
// against what the agent asked for; on allow, the kernel returns its own
// ENOENT/ELOOP/etc. This matters for ld.so's library search — musl probes
// /lib/libX.so.N, /usr/lib/libX.so.N, …; every miss must be ENOENT, not
// EPERM, or the loader aborts on the first probe.
func (s *Server) resolveFilePath(spec SyscallSpec, trap Trap, tracee Tracee) (string, string, error) {
	raw, err := tracee.ReadString(uintptr(trap.Args[spec.PathArgIdx]))
	if err != nil {
		return "", "", err
	}
	op := spec.PrimaryOp
	if op == "" {
		flags, err := s.readOpenatFlags(spec, trap, tracee)
		if err != nil {
			return "", "", err
		}
		op = ClassifyOpenatFlags(flags)
	}
	abs, err := absolutize(raw, int32(trap.Args[spec.DirfdArgIdx]), tracee)
	if err != nil {
		return "", "", err
	}
	resolved, err := tracee.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	return op, filepath.Clean(resolved), nil
}

func (s *Server) resolveSecondaryPath(spec SyscallSpec, trap Trap, tracee Tracee) (string, error) {
	raw, err := tracee.ReadString(uintptr(trap.Args[spec.SecondPathIdx]))
	if err != nil {
		return "", err
	}
	abs, err := absolutize(raw, int32(trap.Args[spec.SecondDirfdIdx]), tracee)
	if err != nil {
		return "", err
	}
	resolved, err := tracee.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	return filepath.Clean(resolved), nil
}

// readOpenatFlags fetches the flags word for openat/openat2 traps.
//
//	openat:  flags are args[2] directly.
//	openat2: args[2] is a pointer to `struct open_how`; flags at offset 0 (u64).
func (s *Server) readOpenatFlags(spec SyscallSpec, trap Trap, tracee Tracee) (uint64, error) {
	if spec.FlagsArgIdx >= 0 {
		return trap.Args[spec.FlagsArgIdx], nil
	}
	// openat2 — read 8 bytes from *open_how.
	buf, err := tracee.ReadBytes(uintptr(trap.Args[2]), 8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for i := 7; i >= 0; i-- {
		v = (v << 8) | uint64(buf[i])
	}
	return v, nil
}

// absolutize turns a raw path into an absolute one by joining against the
// dirfd's resolved path when relative. An empty path with AT_FDCWD or a
// numeric dirfd is rare enough to treat as "use the dirfd directly".
func absolutize(path string, dirfd int32, tracee Tracee) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	base, err := tracee.ResolveDirfd(dirfd)
	if err != nil {
		return "", err
	}
	if path == "" {
		return base, nil
	}
	return filepath.Join(base, path), nil
}

// --- reply helpers ---

func allowResp(id uint64) TrapResponse {
	return TrapResponse{ID: id, Allow: true}
}

func denyResp(id uint64, errno syscall.Errno) TrapResponse {
	return TrapResponse{ID: id, Allow: false, ErrorNum: int32(errno)}
}

func decisionResp(id uint64, d types.Decision) TrapResponse {
	if d == types.DecisionAllow {
		return allowResp(id)
	}
	return denyResp(id, syscall.EPERM)
}
