//go:build !linux

package agentgate

// DefaultFileCacheSize is defined on non-Linux so callers can reference it
// unconditionally; NewServer is a no-op stub here because NotifyTransport
// requires seccomp notify ioctls that only Linux provides.
const DefaultFileCacheSize = 1024

// NewServer is a build stub for non-Linux. The gate only runs inside the
// container (always Linux), so the host-side non-Linux build path never
// reaches this — but the function must exist for the tree to compile.
func NewServer(_ *Policy, _ Approver, _ Auditor, _ PeerSourceLookup, _ string, _ int) *Server {
	return nil
}
