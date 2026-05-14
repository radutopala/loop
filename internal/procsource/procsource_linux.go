//go:build linux

package procsource

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// procEnvTerminalKey is the env-var prefix used to mark terminal-pane execs
// inside the agent container. Stamped by [terminal.Manager.CreateSessionWithEnv]
// via Docker exec's Env option.
const procEnvTerminalKey = "LOOP_TERMINAL_LEAF="

// maxProcWalkDepth bounds how far up the /proc tree we climb before giving
// up. Real chat/terminal chains are <6 hops; the limit guards against
// pathological /proc state that could stall a request indefinitely.
const maxProcWalkDepth = 16

// lookup is the production Linux implementation of [Lookup]. Walks /proc
// starting at pid; returns "terminal:<leafId>" on hit or "" on miss.
func lookup(pid int) string {
	return walkProcSource(pid, os.ReadFile)
}

// walkProcSource is the testable core. readFile is expected to read
// /proc/<pid>/environ and /proc/<pid>/status; tests pass a stub mapping to
// simulate a process tree.
func walkProcSource(peerPID int, readFile func(string) ([]byte, error)) string {
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
