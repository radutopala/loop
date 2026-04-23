//go:build linux

package syscallwrap

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/radutopala/loop/internal/agentgate"
)

// runChild installs the seccomp filter, hands the notify fd to the parent
// over the pre-inherited socketpair fd, and execs the target binary. Ordering
// is load-bearing — see the stepwise comments inside.
func (a *app) runChild() error {
	target, err := parseArgs(a.args)
	if err != nil {
		return err
	}
	// execve(2) does not consult $PATH — without this, a bare `claude` (the
	// default image CMD) turns into `execve("claude", ...)` which the kernel
	// resolves relative to cwd only and returns ENOENT for. Without the gate,
	// entrypoint.sh's `su-exec "$AGENT_USER" "$@"` did the PATH lookup via
	// execvp; we have to emulate that here. argv stays unchanged so the
	// program sees its original argv[0].
	binPath, err := resolveTarget(target[0], a.lookPath)
	if err != nil {
		return fmt.Errorf("lookup target %q: %w", target[0], err)
	}
	channelID := a.getenv(envChannelID)

	// 1) Open the parent socketpair peer from the inherited fd. The parent
	//    attached childFD at os.ProcAttr.ExtraFiles[0] which lands at fd 3.
	conn, err := a.parentConn()
	if err != nil {
		return fmt.Errorf("open parent fd: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// 2) Lock to an OS thread so the seccomp filter installs against the
	//    thread we will exec from. The kernel associates the filter with
	//    the calling thread plus TSYNC'd siblings; only the locked thread's
	//    exec is safely covered.
	a.lockOSThread()

	// 3) Set PR_SET_PDEATHSIG(SIGKILL). If the parent dies (panic, OOM,
	//    signal) the kernel delivers SIGKILL to us. Combined with the
	//    parent running as root and us as the agent uid, the agent cannot
	//    orphan the notify fd by killing the parent — see docs/gates.md
	//    "Parent-kill / orphan-fd attack" for the full rationale.
	if err := a.setPdeathsig(syscall.SIGKILL); err != nil {
		return fmt.Errorf("set pdeathsig: %w", err)
	}

	// 4) Install the seccomp filter. Returns the notify fd we hand to the
	//    parent. After this point, any syscall in our trap-set returns
	//    unfiltered (our own thread is the filter owner) — but once we
	//    exec the target, the filter covers it and all descendants.
	notifyFD, err := a.install()
	if err != nil {
		return fmt.Errorf("install filter: %w", err)
	}

	// 5) Send the notify fd to the parent over SCM_RIGHTS. sendmsg is not
	//    in the trap set so this is safe post-install. The parent will
	//    call Server.Run and start servicing traps.
	if err := a.send(conn, channelID, notifyFD); err != nil {
		_ = a.closeFD(notifyFD)
		return fmt.Errorf("handshake send: %w", err)
	}

	// 6) Close our local copy of the fd — the kernel dup'd it into the
	//    parent via SCM_RIGHTS and we no longer need it. Closing early
	//    avoids a dangling descriptor across exec.
	if err := a.closeFD(notifyFD); err != nil {
		return fmt.Errorf("close local notify fd: %w", err)
	}

	// 7) Wait for the parent's ack. This gives the parent time to register
	//    the fd on Server before we exec — otherwise the first trapped
	//    syscall of the target could race registration.
	if err := a.readAck(conn); err != nil {
		return fmt.Errorf("handshake ack: %w", err)
	}

	// 8) syscall.Exec replaces our process image with the target. The
	//    filter carries over to the new binary and every descendant by
	//    kernel inheritance. On success this call never returns. Use the
	//    PATH-resolved path for the exec while leaving argv untouched so
	//    the target sees its own name as argv[0].
	return a.exec(binPath, target, a.environ())
}

// resolveTarget mimics execvp: if name has a slash it's already an explicit
// path (absolute or relative) and we pass it through; otherwise we run it
// through the lookPath callback (exec.LookPath in production) to consult
// $PATH. Matches su-exec's behavior in the non-gated code path.
func resolveTarget(name string, lookPath func(string) (string, error)) (string, error) {
	if strings.ContainsRune(name, '/') {
		return name, nil
	}
	return lookPath(name)
}

// defaultParentConn turns the inherited child-end fd (3) into a *net.UnixConn.
// os.NewFile promotes the raw fd into an *os.File; net.FileConn dup's it and
// returns an abstract connection. The UnixConn cast is safe because the
// parent created the pair as AF_UNIX/SOCK_STREAM.
func defaultParentConn() (*net.UnixConn, error) {
	f := os.NewFile(uintptr(childHandshakeFD), "loop-syscallwrap-parent")
	c, err := net.FileConn(f)
	// net.FileConn dup'd the fd; close our File copy unconditionally.
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("inherited fd is not a unix conn: %T", c)
	}
	return uc, nil
}

// defaultSetPdeathsig installs a SIGKILL parent-death signal. prctl(2) with
// PR_SET_PDEATHSIG ties it to the calling thread's parent; we call this
// after LockOSThread so the locked thread is the one the kernel watches.
func defaultSetPdeathsig(sig syscall.Signal) error {
	return unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(sig), 0, 0, 0)
}

// sendHandshake writes the wire-format registration to the parent over the
// socketpair:
//
//	[4 bytes big-endian length N] [N bytes UTF-8 channel-id]
//
// The notify fd is passed out-of-band via SCM_RIGHTS on the same sendmsg.
// Stream unix sockets either commit the full write or return an error, so no
// short-write loop is needed.
func sendHandshake(conn *net.UnixConn, channelID string, notifyFD int) error {
	body := []byte(channelID)
	hdr := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(body)))
	copy(hdr[4:], body)
	oob := unix.UnixRights(notifyFD)
	_, _, err := conn.WriteMsgUnix(hdr, oob, nil)
	return err
}

// readAck blocks for a single ACK byte. Anything other than 0x01 (including
// a short read or EOF) means the parent refused registration.
func readAck(conn *net.UnixConn) error {
	var buf [1]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		return err
	}
	if n != 1 || buf[0] != agentgate.AckByte {
		return fmt.Errorf("unexpected ack: n=%d byte=0x%02x", n, buf[0])
	}
	return nil
}
