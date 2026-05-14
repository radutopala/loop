//go:build linux

package dockerproxy

import (
	"net"
	"syscall"

	"github.com/radutopala/loop/internal/procsource"
)

// rawSyscallConn is the subset of syscall.RawConn readPeerPID needs.
// Tests substitute a fake to drive the Control / GetsockoptUcred error paths.
type rawSyscallConn interface {
	Control(f func(fd uintptr)) error
}

// readPeerPID returns the SO_PEERCRED-derived peer PID for the given
// unix-domain socket connection.
func readPeerPID(uc *net.UnixConn) (int, error) {
	return readPeerPIDWith(uc.SyscallConn, getsockoptUcredPID)
}

// readPeerPIDWith is the testable core. rawConn is the underlying
// SyscallConn accessor (production: *net.UnixConn.SyscallConn); getPID
// is the SO_PEERCRED reader. Tests inject stubs to exercise the
// SyscallConn / Control / Getsockopt error paths.
func readPeerPIDWith(rawConn func() (syscall.RawConn, error), getPID func(fd uintptr) (int, error)) (int, error) {
	raw, err := rawConn()
	if err != nil {
		return 0, err
	}
	return peerPIDFromRaw(raw, getPID)
}

// peerPIDFromRaw is the injection-friendly core: takes a RawConn-shaped
// value and a pid-getter (production: syscall.GetsockoptUcred). Returns
// (pid, error) using the same precedence as the inline form — controlErr
// first, then the syscall err captured inside Control.
func peerPIDFromRaw(raw rawSyscallConn, getPID func(fd uintptr) (int, error)) (int, error) {
	var pid int
	var sopErr error
	controlErr := raw.Control(func(fd uintptr) {
		p, e := getPID(fd)
		if e != nil {
			sopErr = e
			return
		}
		pid = p
	})
	if controlErr != nil {
		return 0, controlErr
	}
	if sopErr != nil {
		return 0, sopErr
	}
	return pid, nil
}

// getsockoptUcredPID is the production pid-getter — wraps the syscall so
// peerPIDFromRaw stays platform-agnostic for the test.
func getsockoptUcredPID(fd uintptr) (int, error) {
	cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(cred.Pid), nil
}

// defaultPeerSource is the production peer-source lookup. Delegates to the
// shared [procsource.Lookup] which walks /proc looking for the
// LOOP_TERMINAL_LEAF marker. Kept as a thin wrapper so tests can swap
// PeerSource via ServerConfig without depending on the shared package's
// build tags.
func defaultPeerSource(peerPID int) string {
	return procsource.Lookup(peerPID)
}
