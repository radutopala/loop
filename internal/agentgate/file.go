package agentgate

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// FileOp enumerates the operations a FileRule can match. See policy.go for
// the canonical set (knownFileOps). Defined here as named constants so
// callers don't stringly-type every call site.
const (
	OpRead   = "read"
	OpWrite  = "write"
	OpCreate = "create"
	OpDelete = "delete"
	OpStat   = "stat"
	OpList   = "list"
	OpChmod  = "chmod"
	OpChown  = "chown"
	OpLink   = "link"
)

// FileRequest is the post-read view of a file-op trap. The server-side
// dispatcher is responsible for reading path strings out of tracee memory,
// resolving dirfds via /proc/<pid>/fd, and following symlinks; Handle
// operates on the resolved absolute path only.
type FileRequest struct {
	PID       int
	ChannelID string
	Syscall   string // "openat" | "openat2" | "renameat2" | "unlinkat" | …
	Op        string // one of Op* constants above
	Path      string // absolute, cleaned, symlink-resolved
}

// FileHandler evaluates file-op traps. Construct via NewFileHandler.
type FileHandler struct {
	Policy   *Policy
	Approver Approver
	Cache    *FileDecisionCache
	Auditor  Auditor
	Now      func() time.Time
}

// NewFileHandler wires the policy, approval source, and decision cache
// together. cacheSize <= 0 is clamped to 1024 (the default).
// Auditor defaults to NopAuditor; the server factory swaps in a real sink.
func NewFileHandler(policy *Policy, approver Approver, cacheSize int) *FileHandler {
	return &FileHandler{
		Policy:   policy,
		Approver: approver,
		Cache:    NewFileDecisionCache(cacheSize),
		Auditor:  NopAuditor{},
		Now:      time.Now,
	}
}

// Handle decides a file-op request.
//
// Fast path: a FIFO cache collapses repeated (op, path) pairs to a single
// map lookup, so a `find /work` pass over 10K files evaluates the policy
// ~once per (op, path) instead of 10K times.
//
// Decisions from Approve are not blanket-cached: only a definitive Allow
// from the user populates the per-handler cache, mirroring the Manager's
// "Allow for session" semantic one level up.
//
// Audit emission: only first-miss decisions write an AuditEntry. Cache hits
// are silent — a `find /work` pass would otherwise dump thousands of
// near-identical records per directory walk, drowning the real signal (the
// one first-miss entry already carries RuleID + Latency). Operators who need
// hit-rate telemetry should read the cache's Len()/metrics, not the audit log.
func (h *FileHandler) Handle(ctx context.Context, req FileRequest) Outcome {
	start := h.Now()
	path := filepath.Clean(req.Path)
	target := req.Op + " " + path
	key := fileCacheKey{op: req.Op, path: path}

	if d, ok := h.Cache.Get(key); ok {
		return Outcome{Decision: d, FromCache: true, Reason: "cache-hit"}
	}

	match := h.Policy.MatchFile(req.Op, path)
	var out Outcome
	if match.Decision == types.DecisionApprove {
		if h.Approver == nil {
			out = Outcome{Decision: types.DecisionDeny, Reason: "no-approver"}
		} else {
			out = h.Approver.Request(ctx, req.ChannelID, ApprovalRequest{
				Kind:     "file",
				Target:   target,
				Message:  match.Message,
				CacheKey: "file:" + req.Op + ":" + path,
			})
			if out.Decision == types.DecisionAllow && out.FromCache {
				// Plumb through so handler cache agrees with approval cache.
				h.Cache.Put(key, types.DecisionAllow)
			}
		}
	} else {
		h.Cache.Put(key, match.Decision)
		out = Outcome{Decision: match.Decision, Reason: match.RuleID}
	}
	h.Auditor.Write(AuditEntry{
		Ts:          h.Now(),
		Channel:     req.ChannelID,
		PID:         req.PID,
		Kind:        "file",
		Target:      target,
		RuleID:      match.RuleID,
		Decision:    string(out.Decision),
		PromptedWho: out.Actor,
		Latency:     h.Now().Sub(start),
	})
	return out
}

// FileDecisionCache is a bounded FIFO of (op, path) → decision. Oldest entry
// evicted when full. Safe for concurrent use.
//
// The plan calls this an LRU; FIFO is adequate in practice (the hot case is
// a directory walk that hits the same key thousands of times — both FIFO
// and true LRU collapse that to a single eval + N cheap hits).
type FileDecisionCache struct {
	mu    sync.Mutex
	max   int
	data  map[fileCacheKey]types.Decision
	order []fileCacheKey
}

type fileCacheKey struct {
	op, path string
}

// NewFileDecisionCache returns a cache with the given capacity. Non-positive
// sizes fall back to 1024.
func NewFileDecisionCache(size int) *FileDecisionCache {
	if size <= 0 {
		size = 1024
	}
	return &FileDecisionCache{
		max:  size,
		data: map[fileCacheKey]types.Decision{},
	}
}

// Get returns the cached decision for k, or ok=false when absent.
func (c *FileDecisionCache) Get(k fileCacheKey) (types.Decision, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.data[k]
	return d, ok
}

// Put stores a decision for k. Re-putting an existing key refreshes its
// value in place (no reorder). When at capacity, the oldest entry is evicted.
func (c *FileDecisionCache) Put(k fileCacheKey, d types.Decision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data[k]; ok {
		c.data[k] = d
		return
	}
	if len(c.order) >= c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.data, oldest)
	}
	c.data[k] = d
	c.order = append(c.order, k)
}

// Reset drops every entry. Use on policy hot-reload to invalidate stale
// decisions.
func (c *FileDecisionCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = map[fileCacheKey]types.Decision{}
	c.order = nil
}

// Len returns the number of cached entries.
func (c *FileDecisionCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.data)
}
