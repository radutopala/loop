//go:build !linux

package procsource

// lookup is the non-Linux stub. The procsource caller surfaces (dockerproxy
// peer attribution, agentgate handler PeerSource) only run inside the Linux
// agent container in production; this stub exists so the package compiles
// for host-side tooling builds on macOS / Windows.
func lookup(_ int) string { return "" }
