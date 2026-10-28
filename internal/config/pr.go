package config

import "slices"

// Defaults for the ci block.
const (
	DefaultPRMaxAttempts = 3
	DefaultPRPollSec     = 30
	DefaultPRIdlePollSec = 300
)

// PRMergeMethods are the merge methods ci.merge_method accepts.
var PRMergeMethods = []string{"squash", "merge", "rebase"}

// PRConfig is how Loop watches a thread's PR/MR checks: whether a failure
// on the agent's own commit goes back to the agent to fix, how many fixes
// a PR/MR gets, the method "merge when ready" uses (empty: the first the
// repo allows, squash first), how often checks are polled while they run
// and an open PR/MR otherwise. Layered per global/project the same way as
// ReviewConfig.
type PRConfig struct {
	AutoFix     bool   `json:"auto_fix"`
	MaxAttempts int    `json:"max_attempts"`
	MergeMethod string `json:"merge_method"`
	PollSec     int    `json:"poll_sec"`
	IdlePollSec int    `json:"idle_poll_sec"`
}

// jsonPRConfig is the JSON representation, with an AutoFix pointer so an
// unset value inherits the outer layer.
type jsonPRConfig struct {
	AutoFix     *bool  `json:"auto_fix,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	PollSec     int    `json:"poll_sec,omitempty"`
	IdlePollSec int    `json:"idle_poll_sec,omitempty"`
}

// DefaultPRConfig is the ci block when no config sets it.
func DefaultPRConfig() PRConfig {
	return PRConfig{
		AutoFix: true, MaxAttempts: DefaultPRMaxAttempts, PollSec: DefaultPRPollSec,
		IdlePollSec: DefaultPRIdlePollSec,
	}
}

// prConfigOf returns the global ci block with its defaults filled in.
func prConfigOf(jc *jsonPRConfig) PRConfig {
	return DefaultPRConfig().overlay(jc)
}

// overlay returns c with the fields jc sets on top. A non-positive count
// and an unknown merge method are ignored.
func (c PRConfig) overlay(jc *jsonPRConfig) PRConfig {
	if jc == nil {
		return c
	}
	if jc.AutoFix != nil {
		c.AutoFix = *jc.AutoFix
	}
	if jc.MaxAttempts > 0 {
		c.MaxAttempts = jc.MaxAttempts
	}
	if slices.Contains(PRMergeMethods, jc.MergeMethod) {
		c.MergeMethod = jc.MergeMethod
	}
	if jc.PollSec > 0 {
		c.PollSec = jc.PollSec
	}
	if jc.IdlePollSec > 0 {
		c.IdlePollSec = jc.IdlePollSec
	}
	return c
}
