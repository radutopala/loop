package types

// Decision is the outcome of a policy match for a gate request.
type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
	DecisionApprove Decision = "approve"
)

// PathRule matches unix-socket connect(2) targets by absolute path.
type PathRule struct {
	Pattern  string   `json:"pattern"`
	Decision Decision `json:"decision"`
	Message  string   `json:"message,omitempty"`
}

// CommandRule matches execve(2)/execveat(2) by basename (glob) and argv pattern (regex).
// Empty Commands or ArgsPatterns means "match any".
type CommandRule struct {
	Commands     []string `json:"commands,omitempty"`
	ArgsPatterns []string `json:"args_patterns,omitempty"`
	Decision     Decision `json:"decision"`
	Message      string   `json:"message,omitempty"`
}

// FileRule matches openat(2) / renameat2(2) / unlinkat(2) / ... by resolved absolute path
// (doublestar glob) and operation set. Empty Paths or Operations means "match any".
type FileRule struct {
	Paths      []string `json:"paths,omitempty"`
	Operations []string `json:"operations,omitempty"`
	Decision   Decision `json:"decision"`
	Message    string   `json:"message,omitempty"`
}

// HTTPServiceRule matches Docker HTTP requests by method (or "*") and path regex.
type HTTPServiceRule struct {
	Methods  []string `json:"methods"`
	Paths    []string `json:"paths"`
	Decision Decision `json:"decision"`
	Message  string   `json:"message,omitempty"`
}

// BodyRule inspects the JSON body of a Docker HTTP request to deny container-escape shapes
// (bind-mounts of sensitive host paths, --privileged, host namespaces, dangerous caps, ...).
type BodyRule struct {
	AppliesTo    string      `json:"applies_to"`
	ContentTypes []string    `json:"content_types,omitempty"`
	MaxBodyBytes int64       `json:"max_body_bytes,omitempty"`
	JSONChecks   []JSONCheck `json:"json_checks"`
	Decision     Decision    `json:"decision"`
	Message      string      `json:"message,omitempty"`
}

// JSONCheck is a single field-level assertion within a BodyRule.
// Op is one of: "source_path_in", "equals", "contains_any", "starts_with_any",
// "present", "empty_array".
type JSONCheck struct {
	Path   string   `json:"path"`
	Op     string   `json:"op"`
	Values []string `json:"values,omitempty"`
}

// RateLimits caps prompt volume per gate-enabled container.
type RateLimits struct {
	Pending   int `json:"pending"`
	PerMinute int `json:"per_minute"`
	Total     int `json:"total"`
}

// AuditConfig controls gate decision logging.
//
// Verbose controls what gets written to the rotating jsonl:
//
//   - Verbose=false (default): only denies (silent or after a click) and
//     user-clicked allows land in the log. Silent allows — the "policy says
//     allow and nobody asked" path including cache hits — are suppressed to
//     keep the audit trail focused on events an operator actually wants to
//     review.
//   - Verbose=true: every decision is logged, including silent allows and
//     cache hits. Intended for debugging rule authoring or exporting a full
//     trace to an external SIEM.
type AuditConfig struct {
	RetentionDays int  `json:"retention_days"`
	Verbose       bool `json:"verbose"`
}

// WatchdogConfig controls the fail-closed watchdog.
type WatchdogConfig struct {
	TrapTimeoutSec int `json:"trap_timeout_sec"`
}
