//go:build !windows

package localserver

import (
	"os"
	"syscall"
)

// probe reports whether p is running: signal 0 checks without signaling.
func probe(p *os.Process) error { return p.Signal(syscall.Signal(0)) }
