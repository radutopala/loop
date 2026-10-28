// Package forge holds the forge-neutral view of a pull/merge request's CI
// and merging that githubapi and gitlabapi each implement: the check
// summary, the failed checks and their logs, re-runs, and merge when ready.
package forge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kinds of change request, as PRInfo.Kind and the API report them.
const (
	KindPR = "pr"
	KindMR = "mr"
)

// Normalised merge states.
const (
	MergeClean    = "clean"
	MergeBlocked  = "blocked"
	MergeBehind   = "behind"
	MergeConflict = "conflict"
	MergeDraft    = "draft"
	MergeUnknown  = "unknown"
)

// Merge methods, in the order one is picked when none is configured.
var Methods = []string{"squash", "merge", "rebase"}

// Ref addresses one PR/MR: the checkout the CLI runs in, the account it
// runs as (a gh user, or a glab config dir; "" for the CLI's own) and the
// PR number or MR IID.
type Ref struct {
	Dir     string
	Account string
	Number  int
}

// CheckSummary counts a head's checks. A cancelled or skipped check counts
// only toward Total.
type CheckSummary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

// CIState is a PR/MR's head, its checks and whether it can merge.
type CIState struct {
	HeadSHA    string
	Checks     CheckSummary
	MergeState string
	AutoMerge  bool
	Merged     bool
	Closed     bool
}

// FailedCheck is one failed check (GitHub) or job (GitLab).
type FailedCheck struct {
	// ID is the GitHub Actions job id, or the GitLab job id.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Group is the workflow (GitHub) or stage (GitLab).
	Group string `json:"group,omitempty"`
	URL   string `json:"url,omitempty"`
	// RunID is what a re-run takes: the GitHub Actions run id, or the
	// GitLab job id.
	RunID string `json:"run_id,omitempty"`
	// Infra marks a GitLab failure reason that is the runner's, not the
	// code's.
	Infra bool `json:"infra,omitempty"`
}

// Label is the check as a prompt or notification names it.
func (c FailedCheck) Label() string {
	if c.Group == "" {
		return c.Name
	}
	return c.Group + " / " + c.Name
}

// MergeSettings is what a repo allows: the forge's own auto-merge, and the
// merge methods, in Methods order.
type MergeSettings struct {
	AutoMerge bool
	Methods   []string
}

// Forge is a forge's CI and merge operations on one PR/MR.
type Forge interface {
	CI(ctx context.Context, ref Ref) (*CIState, error)
	FailedChecks(ctx context.Context, ref Ref) ([]FailedCheck, error)
	// Log returns the tail of the check's failure log.
	Log(ctx context.Context, ref Ref, c FailedCheck) (string, error)
	Rerun(ctx context.Context, ref Ref, c FailedCheck) error
	MergeSettings(ctx context.Context, ref Ref) (MergeSettings, error)
	EnableAutoMerge(ctx context.Context, ref Ref, method string) error
	DisableAutoMerge(ctx context.Context, ref Ref) error
	Merge(ctx context.Context, ref Ref, method string) error
}

// PickMethod returns the merge method to use: configured when the repo
// allows it, else the first the repo allows.
func PickMethod(configured string, s MergeSettings) (string, error) {
	if configured != "" {
		if !slices.Contains(s.Methods, configured) {
			return "", fmt.Errorf("the repo doesn't allow %s merges", configured)
		}
		return configured, nil
	}
	for _, m := range Methods {
		if slices.Contains(s.Methods, m) {
			return m, nil
		}
	}
	return "", fmt.Errorf("the repo allows no merge method")
}

// Limits of a log tail.
const (
	tailLines = 200
	tailBytes = 16 << 10
)

// Tail returns the last 200 lines of log, at most 16 KB of them.
func Tail(log string) string {
	log = strings.TrimRight(log, "\n")
	lines := strings.Split(log, "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > tailBytes {
		out = out[len(out)-tailBytes:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return out
}

// timestampRE matches the timestamps CI runners put in their logs, so two
// runs of the same failure hash alike.
var timestampRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?Z?|\d+(\.\d+)?m?s\b`)

// LogHash returns a short hash of log with its timestamps and durations
// removed.
func LogHash(log string) string {
	sum := sha256.Sum256([]byte(timestampRE.ReplaceAllString(log, "")))
	return hex.EncodeToString(sum[:8])
}

// settingsTTL is how long a repo's merge settings are reused.
const settingsTTL = 10 * time.Minute

// SettingsCache keeps each repo's merge settings for ten minutes. The zero
// value is ready to use.
type SettingsCache struct {
	mu      sync.Mutex
	entries map[string]settingsEntry
}

type settingsEntry struct {
	settings MergeSettings
	at       time.Time
}

// Get returns key's settings as of now, from fetch when they're not cached
// or are stale. A fetch error isn't cached.
func (c *SettingsCache) Get(key string, now time.Time, fetch func() (MergeSettings, error)) (MergeSettings, error) {
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if ok && now.Sub(e.at) < settingsTTL {
		return e.settings, nil
	}
	s, err := fetch()
	if err != nil {
		return MergeSettings{}, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]settingsEntry{}
	}
	c.entries[key] = settingsEntry{settings: s, at: now}
	c.mu.Unlock()
	return s, nil
}
