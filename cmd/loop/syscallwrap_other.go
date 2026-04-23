//go:build !linux

package main

import (
	"fmt"
	"os"
)

// runSyscallwrap is a no-op stub on non-Linux hosts. The real wrapper needs
// seccomp-user-notify, which is a Linux kernel feature; the subcommand only
// makes sense when the compiled binary lives inside the agent container
// (which is always Linux). We still want the binary to compile everywhere so
// `go build ./cmd/loop` works on macOS dev machines.
func runSyscallwrap(_, _ []string) int {
	fmt.Fprintln(os.Stderr, "loop syscallwrap: not supported on this platform (Linux-only)")
	return 1
}
