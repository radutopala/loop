package agentgate

// PeerSourceLookup maps a tracee PID inside the agent container to an
// approval-source identifier. Production wires this to
// [procsource.Lookup]; tests can pass a stub. nil disables lookup and the
// handler defaults every prompt to "chat".
type PeerSourceLookup func(pid int) string

// sourceForPID is the canonical mapping from a tracee PID + optional lookup
// to the Source field on an ApprovalRequest:
//
//   - pid <= 0 or lookup nil → "chat"
//   - lookup returns "" → "chat"
//   - otherwise → the lookup result (typically "terminal:<leafId>")
//
// Mirrors dockerproxy.sourceForPeer and procsource.SourceFor so all three
// approval surfaces compute Source the same way.
func sourceForPID(pid int, lookup PeerSourceLookup) string {
	if pid <= 0 || lookup == nil {
		return "chat"
	}
	if s := lookup(pid); s != "" {
		return s
	}
	return "chat"
}
