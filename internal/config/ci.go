package config

import "slices"

// Defaults for the ci block.
const (
	DefaultCIMaxAttempts = 3
	DefaultCIPollSec     = 30
)

// CIMergeMethods are the merge methods ci.merge_method accepts.
var CIMergeMethods = []string{"squash", "merge", "rebase"}

// CIConfig is how Loop watches a thread's PR/MR checks: whether a failure
// on the agent's own commit goes back to the agent to fix, how many fixes
// a PR/MR gets, the method "merge when ready" uses (empty: the first the
// repo allows, squash first) and how often checks are polled while they run.
// Layered per global/project the same way as ReviewConfig.
type CIConfig struct {
	AutoFix     bool   `json:"auto_fix"`
	MaxAttempts int    `json:"max_attempts"`
	MergeMethod string `json:"merge_method"`
	PollSec     int    `json:"poll_sec"`
}

// jsonCIConfig is the JSON representation, with an AutoFix pointer so an
// unset value inherits the outer layer.
type jsonCIConfig struct {
	AutoFix     *bool  `json:"auto_fix,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	PollSec     int    `json:"poll_sec,omitempty"`
}

// DefaultCIConfig is the ci block when no config sets it.
func DefaultCIConfig() CIConfig {
	return CIConfig{AutoFix: true, MaxAttempts: DefaultCIMaxAttempts, PollSec: DefaultCIPollSec}
}

// ciConfigOf returns the global ci block with its defaults filled in.
func ciConfigOf(jc *jsonCIConfig) CIConfig {
	return DefaultCIConfig().overlay(jc)
}

// overlay returns c with the fields jc sets on top. A non-positive count
// and an unknown merge method are ignored.
func (c CIConfig) overlay(jc *jsonCIConfig) CIConfig {
	if jc == nil {
		return c
	}
	if jc.AutoFix != nil {
		c.AutoFix = *jc.AutoFix
	}
	if jc.MaxAttempts > 0 {
		c.MaxAttempts = jc.MaxAttempts
	}
	if slices.Contains(CIMergeMethods, jc.MergeMethod) {
		c.MergeMethod = jc.MergeMethod
	}
	if jc.PollSec > 0 {
		c.PollSec = jc.PollSec
	}
	return c
}
