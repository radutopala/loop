//go:build !unix

package buildctx

// nonblock is zero where there are no FIFOs to open.
const nonblock = 0
