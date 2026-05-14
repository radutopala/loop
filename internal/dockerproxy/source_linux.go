//go:build linux

package dockerproxy

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
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

// procEnvTerminalKey is the env-var prefix used to mark terminal-pane execs
// inside the agent container. Stamped by [terminal.Manager.CreateSessionWithEnv]
// via Docker exec's Env option.
const procEnvTerminalKey = "LOOP_TERMINAL_LEAF="

// defaultPeerSource walks the /proc tree starting at peerPID, looking for
// LOOP_TERMINAL_LEAF=<leafId> in each process's environment. On hit returns
// "terminal:<leafId>"; on miss (after climbing up to PID 1) returns "".
//
// The container entrypoint (chat agent) runs as PID 1 with no marker, so the
// chat agent always returns "" and the caller defaults to "chat".
func defaultPeerSource(peerPID int) string {
	return walkProcSource(peerPID, os.ReadFile)
}

// walkProcSource is the testable core of defaultPeerSource. readFile is
// expected to read /proc/<pid>/environ and /proc/<pid>/status; tests pass
// a stub mapping to simulate a process tree.
//
// Bounded by maxProcWalkDepth so a pathological /proc state cannot stall a
// request indefinitely.
func walkProcSource(peerPID int, readFile func(string) ([]byte, error)) string {
	const maxProcWalkDepth = 16
	pid := peerPID
	for i := 0; i < maxProcWalkDepth && pid > 1; i++ {
		env, err := readFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err == nil {
			for kv := range bytes.SplitSeq(env, []byte{0}) {
				if bytes.HasPrefix(kv, []byte(procEnvTerminalKey)) {
					return "terminal:" + string(kv[len(procEnvTerminalKey):])
				}
			}
		}
		ppid, err := readProcPPID(pid, readFile)
		if err != nil || ppid == pid {
			return ""
		}
		pid = ppid
	}
	return ""
}

// readProcPPID returns the PPid value from /proc/<pid>/status. Returns an
// error when the file is unreadable or the field is missing.
func readProcPPID(pid int, readFile func(string) ([]byte, error)) (int, error) {
	data, err := readFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("PPid:")) {
			continue
		}
		rest := bytes.TrimSpace(line[len("PPid:"):])
		return strconv.Atoi(string(rest))
	}
	return 0, errors.New("PPid not found in /proc status")
}
