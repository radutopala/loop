//go:build windows

package localserver

import "os"

// probe is a no-op: on Windows os.FindProcess already fails for a process
// that isn't running.
func probe(_ *os.Process) error { return nil }
