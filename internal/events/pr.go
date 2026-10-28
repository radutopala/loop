package events

import "github.com/radutopala/loop/internal/forge"

// PR attention values: what a thread's sidebar pill shows of its PR/MR.
const (
	// PRAttentionFailed is a CI failure that needs the owner: it isn't being
	// fixed automatically.
	PRAttentionFailed = "failed"
	// PRAttentionFixing is a CI failure the thread's agent was sent to fix.
	PRAttentionFixing = "fixing"
	// PRAttentionConflict is a merge conflict with the base, which is always
	// the owner's to resolve.
	PRAttentionConflict = "conflict"
)

// PRFlags is what a thread's sidebar pills show of its PR/MR: its attention
// (a PRAttention*) and its unresolved review threads.
type PRFlags struct {
	Attention         string
	UnresolvedThreads int
}

// PR reasons: why the watcher acted as it did on the head's failure.
const (
	// PRReasonNotAgent: the head isn't a commit the thread's agent made.
	PRReasonNotAgent = "not_agent_commit"
	// PRReasonAutoFixOff: auto-fix is off for the thread.
	PRReasonAutoFixOff = "auto_fix_off"
	// PRReasonMaxAttempts: the fixes sent reached ci.max_attempts.
	PRReasonMaxAttempts = "max_attempts"
	// PRReasonNoNewCommit: the last fix pushed no new commit.
	PRReasonNoNewCommit = "no_new_commit"
	// PRReasonSameFailure: the same failure came back after a fix.
	PRReasonSameFailure = "same_failure"
	// PRReasonInfraRerun: only infrastructure failures; re-run once.
	PRReasonInfraRerun = "infra_rerun"
	// PRReasonInfraAgain: infrastructure failures again after the re-run.
	PRReasonInfraAgain = "infra_again"
	// PRReasonFixSent: the failure went to the agent automatically.
	PRReasonFixSent = "fix_sent"
	// PRReasonFixAsked: the owner sent the failure to the agent.
	PRReasonFixAsked = "fix_asked"
)

// PREventData is the payload for channel.pr events: the status of the thread's
// PR (Kind "pr") or MR (Kind "mr"). AutoFix is the effective auto-fix
// setting, AutoFixDefault the config's. MergeArmed is the method "merge
// when ready" was armed with, MergeMethod the one a merge would use now.
// Reason says why the watcher acted as it did on a failure (a PRReason*),
// and CheckoutSHA is the commit the thread's checkout is on.
// UnresolvedThreads counts the PR/MR's unresolved review threads. Alert,
// when set, is a new failure the owner is told of in a notification; a
// merge conflict and review threads show in the sidebar and PR/MR tab only.
type PREventData struct {
	ChannelID         string              `json:"channel_id"`
	Kind              string              `json:"kind"`
	Number            int                 `json:"number"`
	URL               string              `json:"url,omitempty"`
	HeadSHA           string              `json:"head_sha"`
	BaseRef           string              `json:"base_ref,omitempty"`
	Checks            forge.CheckSummary  `json:"checks"`
	Failed            []forge.FailedCheck `json:"failed,omitempty"`
	MergeState        string              `json:"merge_state"`
	AutoMerge         bool                `json:"auto_merge"`
	Merged            bool                `json:"merged,omitempty"`
	Closed            bool                `json:"closed,omitempty"`
	MergeArmed        string              `json:"merge_armed,omitempty"`
	MergeMethods      []string            `json:"merge_methods,omitempty"`
	MergeMethod       string              `json:"merge_method,omitempty"`
	Attention         string              `json:"pr_attention"`
	AutoFix           bool                `json:"auto_fix"`
	AutoFixDefault    bool                `json:"auto_fix_default"`
	Attempts          int                 `json:"attempts"`
	MaxAttempts       int                 `json:"max_attempts"`
	Reason            string              `json:"reason,omitempty"`
	CheckoutSHA       string              `json:"checkout_sha,omitempty"`
	UnresolvedThreads int                 `json:"unresolved_threads,omitempty"`
	Alert             string              `json:"alert,omitempty"`
}
