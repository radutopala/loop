//go:build !linux

package dockerproxy

import "net"

// readPeerPID is a non-Linux stub. dockerproxy runs only inside the Linux
// agent container in production; this stub exists so the package compiles
// for host-side tooling builds on macOS / Windows.
func readPeerPID(_ *net.UnixConn) (int, error) {
	return 0, nil
}

// defaultPeerSource is a non-Linux stub that always returns "". Callers
// fall back to "chat".
func defaultPeerSource(_ int) string {
	return ""
}
