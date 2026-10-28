package events

import "github.com/radutopala/loop/internal/forge"

// CI attention values: what a thread's sidebar pill shows of its PR/MR's CI.
const (
	// CIAttentionFailed is a failure that needs the owner: it isn't being
	// fixed automatically.
	CIAttentionFailed = "failed"
	// CIAttentionFixing is a failure the thread's agent was sent to fix.
	CIAttentionFixing = "fixing"
)

// CIEventData is the payload for channel.ci events: the CI of the thread's
// PR (Kind "pr") or MR (Kind "mr"). AutoFix is the effective auto-fix
// setting, AutoFixDefault the config's. MergeArmed is the method "merge
// when ready" was armed with, MergeMethod the one a merge would use now. Alert, when set, is a new failure the owner
// is told of in a notification.
type CIEventData struct {
	ChannelID      string              `json:"channel_id"`
	Kind           string              `json:"kind"`
	Number         int                 `json:"number"`
	URL            string              `json:"url,omitempty"`
	HeadSHA        string              `json:"head_sha"`
	Checks         forge.CheckSummary  `json:"checks"`
	Failed         []forge.FailedCheck `json:"failed,omitempty"`
	MergeState     string              `json:"merge_state"`
	AutoMerge      bool                `json:"auto_merge"`
	Merged         bool                `json:"merged,omitempty"`
	Closed         bool                `json:"closed,omitempty"`
	MergeArmed     string              `json:"merge_armed,omitempty"`
	MergeMethods   []string            `json:"merge_methods,omitempty"`
	MergeMethod    string              `json:"merge_method,omitempty"`
	Attention      string              `json:"ci_attention"`
	AutoFix        bool                `json:"auto_fix"`
	AutoFixDefault bool                `json:"auto_fix_default"`
	Attempts       int                 `json:"attempts"`
	MaxAttempts    int                 `json:"max_attempts"`
	Alert          string              `json:"alert,omitempty"`
}
