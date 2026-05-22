package workflow

// RunContext holds the runtime data available to Go text/template expressions
// in node prompts, scripts, and conditions.
type RunContext struct {
	Inputs      map[string]string // user-provided inputs keyed by name
	NodeOutputs map[string]string // node_id → output text
	RunMeta     RunMeta
	// ChannelID is the channel that owns the run, exposed to templates as
	// {{.ChannelID}}. Seeded review/review-fix loops embed this in their
	// bash commands.
	ChannelID string
	// Iteration is the 0-based index of the current loop iteration when
	// inside a loop node's body. Zero outside any loop.
	Iteration int
	// Review carries parsed output from a bash node whose ID is "review"
	// running `loop review run`. Used by the seeded review/review-fix loops
	// to drive their stop condition.
	Review ReviewState
}

// RunMeta holds metadata about the current workflow run.
type RunMeta struct {
	RunID        string
	Branch       string
	WorktreePath string
}

// ReviewState mirrors the JSON shape emitted by `loop review run --wait` and
// adds same-as-prev tracking used by the loop's stop condition.
type ReviewState struct {
	NoComments   bool            // true when the iteration produced zero comments
	Comments     []ReviewComment // raw comments from the latest iteration
	CommentsJSON string          // raw JSON of Comments, for prompt embedding
	IDs          []string        // sorted comment IDs from this iteration
	PrevIDs      []string        // IDs captured from the prior iteration
	SameAsPrev   bool            // IDs == PrevIDs (and len > 0) — fix made no progress
}

// ReviewComment is a single review finding emitted by `loop review run`.
type ReviewComment struct {
	ID       string `json:"id"`
	Severity string `json:"severity,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Body     string `json:"body,omitempty"`
}
