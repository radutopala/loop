//go:build !linux

package dockerproxy

import (
	"net"

	"github.com/radutopala/loop/internal/procsource"
)

// readPeerPID is a non-Linux stub. dockerproxy runs only inside the Linux
// agent container in production; this stub exists so the package compiles
// for host-side tooling builds on macOS / Windows.
func readPeerPID(_ *net.UnixConn) (int, error) {
	return 0, nil
}

// defaultPeerSource delegates to [procsource.Lookup] (a no-op stub on
// non-Linux builds). Wrapped so the ServerConfig default lookup uses the
// same shared logic as the agentgate handlers.
func defaultPeerSource(pid int) string {
	return procsource.Lookup(pid)
}
