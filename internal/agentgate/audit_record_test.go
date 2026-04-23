package agentgate

import "sync"

// recordingAuditor is a thread-safe Auditor that captures every entry. Used
// by handler tests to assert audit emission without writing to disk.
type recordingAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (r *recordingAuditor) Write(e AuditEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

func (r *recordingAuditor) snapshot() []AuditEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AuditEntry, len(r.entries))
	copy(out, r.entries)
	return out
}
