//go:build linux

package main

import (
	"os"

	"github.com/radutopala/loop/internal/syscallwrap"
)

// runSyscallwrap dispatches to the Linux seccomp gate. forwardArgs is what
// cobra passed after the subcommand name ([--] target [args...]); selfArgv
// is the outer argv used to re-exec /proc/self/exe for the child.
func runSyscallwrap(forwardArgs, selfArgv []string) int {
	return syscallwrap.Run(os.Stderr, forwardArgs, selfArgv)
}
