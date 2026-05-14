// Package procsource maps an in-container process ID to an approval-source
// identifier used by the loop server to route approval prompts to the right
// UI surface (chat agent vs. a specific terminal pane).
//
// Two surfaces use this lookup:
//   - dockerproxy: the peer of a unix-socket connection (SO_PEERCRED → PID).
//   - syscallwrap/agentgate: the tracee PID from a seccomp notify event.
//
// Both interpret the result the same way:
//   - "" — attribution not possible (non-Linux, missing /proc, no marker).
//     Callers default to "chat".
//   - "terminal:<leafId>" — the process (or one of its ancestors) carries
//     LOOP_TERMINAL_LEAF=<leafId> in its environment; stamped on a
//     terminal-pane exec by terminal.Manager.CreateSessionWithEnv.
package procsource

// Lookup walks the process tree starting at pid and returns the
// approval-source string described in the package doc. On non-Linux builds
// the stub returns "".
func Lookup(pid int) string { return lookup(pid) }

// SourceFor wraps Lookup with the canonical "chat" fallback. It is the
// canonical way to derive the Source field for an ApprovalRequest:
//
//   - pid <= 0 (no peer info) → "chat"
//   - lookup returns "" (no marker found) → "chat"
//   - otherwise → the lookup result, typically "terminal:<leafId>"
func SourceFor(pid int) string {
	return sourceForWith(pid, Lookup)
}

// sourceForWith is the testable core of SourceFor. lookup is injected so
// tests can drive the marker-found branch deterministically without
// depending on the test runner's own /proc state.
func sourceForWith(pid int, lookup func(int) string) string {
	if pid <= 0 {
		return "chat"
	}
	if s := lookup(pid); s != "" {
		return s
	}
	return "chat"
}
