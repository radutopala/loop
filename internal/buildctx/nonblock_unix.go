//go:build unix

package buildctx

import "syscall"

// nonblock keeps opening a FIFO from blocking, so readFile can refuse it.
const nonblock = syscall.O_NONBLOCK
