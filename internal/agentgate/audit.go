package agentgate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AuditEntry is one decision record. Marshalled as JSON, one per line, to the
// audit sink. Extra holds rule-specific context (e.g. body-rule check path).
// Privacy: callers must strip secrets from Target / Extra; argv payloads
// containing credentials should be fingerprinted (SHA-256) by the caller, not
// logged verbatim.
//
// Event distinguishes the two flavors of record: empty means a decision record
// (the existing shape, written after the gate resolves the outcome), and
// "request" means a pre-decision record emitted the moment a prompt is handed
// to the approver. Pre-decision records carry Ts, Kind, Target, RuleID, and
// the request context; Decision/PromptedWho/Latency are zero because no
// resolution has happened yet.
type AuditEntry struct {
	Ts          time.Time         `json:"ts"`
	CID         string            `json:"cid,omitempty"`
	Channel     string            `json:"channel,omitempty"`
	PID         int               `json:"pid,omitempty"`
	Kind        string            `json:"kind"`
	Target      string            `json:"target"`
	RuleID      string            `json:"rule_id"`
	Decision    string            `json:"decision"`
	Event       string            `json:"event,omitempty"`
	PromptedWho string            `json:"prompted_who,omitempty"`
	Latency     time.Duration     `json:"latency_ns,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// Auditor writes decision records. Implementations must be safe for concurrent use.
type Auditor interface {
	Write(e AuditEntry)
}

// NopAuditor discards every entry. Use as a placeholder when audit is disabled.
type NopAuditor struct{}

func (NopAuditor) Write(AuditEntry) {}

// MultiAuditor fans out each entry to all wrapped auditors.
type MultiAuditor struct{ sinks []Auditor }

// NewMultiAuditor returns a MultiAuditor over the provided sinks.
func NewMultiAuditor(sinks ...Auditor) *MultiAuditor {
	return &MultiAuditor{sinks: sinks}
}

// Write fans e out to every sink.
func (m *MultiAuditor) Write(e AuditEntry) {
	for _, s := range m.sinks {
		s.Write(e)
	}
}

// FileAuditor writes one JSON object per line to
// "<dir>/agentgate-YYYY-MM-DD.jsonl", rotating at UTC midnight.
// RetentionDays > 0 prunes older files on rotation; 0 disables pruning.
// Verbose=false (default) suppresses silent allow decisions (see Write).
type FileAuditor struct {
	dir           string
	retentionDays int
	verbose       bool
	now           func() time.Time

	mu      sync.Mutex
	file    *os.File
	dateKey string
}

// NewFileAuditor opens (or creates) the file for today and returns an Auditor.
// dir is created if it doesn't exist (0o750). The open file handle stays alive
// until Close; concurrent callers serialize through a mutex.
func NewFileAuditor(dir string, retentionDays int, verbose bool) (*FileAuditor, error) {
	a := &FileAuditor{
		dir:           dir,
		retentionDays: retentionDays,
		verbose:       verbose,
		now:           func() time.Time { return time.Now().UTC() },
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("agentgate: mkdir audit dir: %w", err)
	}
	if err := a.rotate(a.now()); err != nil {
		return nil, err
	}
	return a, nil
}

// Write serializes e as a single-line JSON record and appends to the current
// day's file, rotating if the UTC date has changed.
//
// When verbose is false, silent allow decisions are dropped — concretely, any
// entry whose Decision=="allow" AND PromptedWho=="". That covers both the
// "policy said allow, nobody asked" path and cache hits (applyResolution
// doesn't stamp an actor on cache-hit outcomes). Denies are always logged
// regardless of prompt origin, so every rejection remains traceable.
func (a *FileAuditor) Write(e AuditEntry) {
	if !a.verbose && e.Decision == "allow" && e.PromptedWho == "" {
		return
	}
	if e.Ts.IsZero() {
		e.Ts = a.now()
	}
	// AuditEntry has no unmarshallable fields (no chan/func/circular refs),
	// so json.Marshal cannot fail — error is not checked.
	line, _ := json.Marshal(e)

	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	if dateKey(now) != a.dateKey {
		_ = a.rotateLocked(now)
	}
	if a.file == nil {
		return
	}
	_, _ = a.file.Write(append(line, '\n'))
}

// Close closes the underlying file.
func (a *FileAuditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}

// rotate opens the file for day `t` and closes the previous one.
// Caller must NOT hold a.mu.
func (a *FileAuditor) rotate(t time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rotateLocked(t)
}

// rotateLocked does the rotation assuming the caller holds a.mu.
func (a *FileAuditor) rotateLocked(t time.Time) error {
	if a.file != nil {
		_ = a.file.Close()
		a.file = nil
	}
	path := filepath.Join(a.dir, "agentgate-"+dateKey(t)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("agentgate: open audit file: %w", err)
	}
	a.file = f
	a.dateKey = dateKey(t)
	a.pruneLocked(t)
	return nil
}

// pruneLocked removes files older than retentionDays. Caller holds a.mu.
// Files not matching the "agentgate-YYYY-MM-DD.jsonl" pattern are ignored.
func (a *FileAuditor) pruneLocked(now time.Time) {
	if a.retentionDays <= 0 {
		return
	}
	cutoff := now.AddDate(0, 0, -a.retentionDays)
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasPrefix(name, "agentgate-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, "agentgate-"), ".jsonl")
		t, err := time.Parse("2006-01-02", stamp)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(a.dir, name))
		}
	}
}

func dateKey(t time.Time) string { return t.UTC().Format("2006-01-02") }
