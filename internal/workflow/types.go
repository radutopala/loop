package workflow

// RunContext holds the runtime data available to Go text/template expressions
// in node prompts, scripts, and conditions.
type RunContext struct {
	Inputs      map[string]string // user-provided inputs keyed by name
	NodeOutputs map[string]string // node_id → output text
	RunMeta     RunMeta
}

// RunMeta holds metadata about the current workflow run.
type RunMeta struct {
	RunID        string
	Branch       string
	WorktreePath string
}
