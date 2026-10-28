package githubapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/radutopala/loop/internal/forge"
)

var _ forge.Forge = (*Client)(nil)

// clock returns the client's time source.
func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// CI returns the PR's head, a summary of its checks and whether it can
// merge.
func (c *Client) CI(ctx context.Context, ref forge.Ref) (*forge.CIState, error) {
	env, err := c.tokenEnv(ctx, ref.Dir, ref.Account)
	if err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, ref.Dir, env,
		"pr", "view", strconv.Itoa(ref.Number),
		"--json", "headRefOid,statusCheckRollup,mergeStateStatus,autoMergeRequest,state,isDraft",
	)
	if err != nil {
		return nil, err
	}
	var raw struct {
		HeadRefOid        string `json:"headRefOid"`
		StatusCheckRollup []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		} `json:"statusCheckRollup"`
		MergeStateStatus string          `json:"mergeStateStatus"`
		AutoMergeRequest json.RawMessage `json:"autoMergeRequest"`
		State            string          `json:"state"`
		IsDraft          bool            `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	st := &forge.CIState{
		HeadSHA:    raw.HeadRefOid,
		MergeState: ghMergeState(raw.MergeStateStatus),
		AutoMerge:  len(raw.AutoMergeRequest) > 0 && string(raw.AutoMergeRequest) != "null",
		Merged:     raw.State == "MERGED",
		Closed:     raw.State == "CLOSED",
	}
	if raw.IsDraft {
		st.MergeState = forge.MergeDraft
	}
	for _, chk := range raw.StatusCheckRollup {
		st.Checks.Total++
		switch ghCheckOutcome(chk.Status, chk.Conclusion, chk.State) {
		case outcomePassed:
			st.Checks.Passed++
		case outcomeFailed:
			st.Checks.Failed++
		case outcomePending:
			st.Checks.Pending++
		}
	}
	return st, nil
}

type checkOutcome int

const (
	outcomeOther checkOutcome = iota
	outcomePassed
	outcomeFailed
	outcomePending
)

// ghCheckOutcome classifies a status-check-rollup entry: a commit status
// has a state, a check run a status and, once completed, a conclusion.
func ghCheckOutcome(status, conclusion, state string) checkOutcome {
	if state != "" {
		switch state {
		case "SUCCESS":
			return outcomePassed
		case "FAILURE", "ERROR":
			return outcomeFailed
		default:
			return outcomePending
		}
	}
	if status != "COMPLETED" {
		return outcomePending
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL":
		return outcomePassed
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return outcomeFailed
	default:
		return outcomeOther
	}
}

// ghMergeState normalises gh's mergeStateStatus.
func ghMergeState(s string) string {
	switch s {
	case "CLEAN", "HAS_HOOKS", "UNSTABLE":
		return forge.MergeClean
	case "BLOCKED":
		return forge.MergeBlocked
	case "BEHIND":
		return forge.MergeBehind
	case "DIRTY":
		return forge.MergeConflict
	case "DRAFT":
		return forge.MergeDraft
	default:
		return forge.MergeUnknown
	}
}

// actionsJobRE pulls the run and job ids out of an Actions job link.
var actionsJobRE = regexp.MustCompile(`/actions/runs/(\d+)(?:/job/(\d+))?`)

// FailedChecks returns the PR's failed checks. gh exits non-zero when a
// check failed, so its output is read whatever the exit.
func (c *Client) FailedChecks(ctx context.Context, ref forge.Ref) ([]forge.FailedCheck, error) {
	env, err := c.tokenEnv(ctx, ref.Dir, ref.Account)
	if err != nil {
		return nil, err
	}
	out, runErr := c.runner.Run(ctx, ref.Dir, env,
		"pr", "checks", strconv.Itoa(ref.Number), "--json", "name,bucket,link,workflow",
	)
	if errors.Is(runErr, ErrGhNotInstalled) {
		return nil, runErr
	}
	var raw []struct {
		Name     string `json:"name"`
		Bucket   string `json:"bucket"`
		Link     string `json:"link"`
		Workflow string `json:"workflow"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("parsing gh pr checks output: %w", err)
	}
	var failed []forge.FailedCheck
	for _, r := range raw {
		if r.Bucket != "fail" {
			continue
		}
		fc := forge.FailedCheck{Name: r.Name, Group: r.Workflow, URL: r.Link}
		if m := actionsJobRE.FindStringSubmatch(r.Link); m != nil {
			fc.RunID, fc.ID = m[1], m[2]
		}
		failed = append(failed, fc)
	}
	return failed, nil
}

// Log returns the tail of the failed steps' log: the job's when known,
// else the run's. A check outside Actions has none.
func (c *Client) Log(ctx context.Context, ref forge.Ref, fc forge.FailedCheck) (string, error) {
	var args []string
	switch {
	case fc.ID != "":
		args = []string{"run", "view", "--job", fc.ID, "--log-failed"}
	case fc.RunID != "":
		args = []string{"run", "view", fc.RunID, "--log-failed"}
	default:
		return "", nil
	}
	env, err := c.tokenEnv(ctx, ref.Dir, ref.Account)
	if err != nil {
		return "", err
	}
	out, err := c.runner.Run(ctx, ref.Dir, env, args...)
	if err != nil {
		return "", err
	}
	return forge.Tail(string(out)), nil
}

// Rerun re-runs the failed jobs of the check's Actions run.
func (c *Client) Rerun(ctx context.Context, ref forge.Ref, fc forge.FailedCheck) error {
	if fc.RunID == "" {
		return fmt.Errorf("%s isn't an Actions run", fc.Name)
	}
	return c.run(ctx, ref, "run", "rerun", fc.RunID, "--failed")
}

// MergeSettings returns whether the repo allows auto-merge and which
// merge methods, reused for ten minutes.
func (c *Client) MergeSettings(ctx context.Context, ref forge.Ref) (forge.MergeSettings, error) {
	return c.settings.Get(ref.Dir+"\x00"+ref.Account, c.clock(), func() (forge.MergeSettings, error) {
		env, err := c.tokenEnv(ctx, ref.Dir, ref.Account)
		if err != nil {
			return forge.MergeSettings{}, err
		}
		out, err := c.runner.Run(ctx, ref.Dir, env,
			"repo", "view", "--json", "autoMergeAllowed,squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed",
		)
		if err != nil {
			return forge.MergeSettings{}, err
		}
		var raw struct {
			AutoMergeAllowed   bool `json:"autoMergeAllowed"`
			SquashMergeAllowed bool `json:"squashMergeAllowed"`
			MergeCommitAllowed bool `json:"mergeCommitAllowed"`
			RebaseMergeAllowed bool `json:"rebaseMergeAllowed"`
		}
		if err := json.Unmarshal(out, &raw); err != nil {
			return forge.MergeSettings{}, fmt.Errorf("parsing gh repo view output: %w", err)
		}
		s := forge.MergeSettings{AutoMerge: raw.AutoMergeAllowed}
		for _, m := range []struct {
			name string
			on   bool
		}{{"squash", raw.SquashMergeAllowed}, {"merge", raw.MergeCommitAllowed}, {"rebase", raw.RebaseMergeAllowed}} {
			if m.on {
				s.Methods = append(s.Methods, m.name)
			}
		}
		return s, nil
	})
}

// EnableAutoMerge has GitHub merge the PR with method once it can.
func (c *Client) EnableAutoMerge(ctx context.Context, ref forge.Ref, method string) error {
	return c.run(ctx, ref, "pr", "merge", strconv.Itoa(ref.Number), "--auto", "--"+method)
}

// DisableAutoMerge cancels the PR's auto-merge.
func (c *Client) DisableAutoMerge(ctx context.Context, ref forge.Ref) error {
	return c.run(ctx, ref, "pr", "merge", strconv.Itoa(ref.Number), "--disable-auto")
}

// Merge merges the PR now with method.
func (c *Client) Merge(ctx context.Context, ref forge.Ref, method string) error {
	return c.run(ctx, ref, "pr", "merge", strconv.Itoa(ref.Number), "--"+method)
}

// run runs a gh command as ref's account, for its error only.
func (c *Client) run(ctx context.Context, ref forge.Ref, args ...string) error {
	env, err := c.tokenEnv(ctx, ref.Dir, ref.Account)
	if err != nil {
		return err
	}
	_, err = c.runner.Run(ctx, ref.Dir, env, args...)
	return err
}
