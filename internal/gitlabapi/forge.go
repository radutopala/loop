package gitlabapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/gitutil"
)

// GitLab goes through `glab api` and the REST API, which stays the same
// across glab releases where subcommand flags don't.

var _ forge.Forge = (*Client)(nil)

// infraFailures are job failure reasons that are the runner's, not the
// code's: a re-run, not a fix.
var infraFailures = []string{"runner_system_failure", "stuck_or_timeout_failure", "scheduler_failure"}

// clock returns the client's time source.
func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// gitOriginURL returns dir's origin remote URL.
func gitOriginURL(ctx context.Context, dir string) (string, error) {
	out, err := gitutil.Command(ctx, dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("reading the origin remote: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// projectID returns dir's project as the API addresses it: the origin
// remote's path, URL-encoded. It is resolved once per dir.
func (c *Client) projectID(ctx context.Context, dir string) (string, error) {
	c.mu.Lock()
	id, ok := c.projects[dir]
	c.mu.Unlock()
	if ok {
		return id, nil
	}
	origin := c.originURL
	if origin == nil {
		origin = gitOriginURL
	}
	u, err := origin(ctx, dir)
	if err != nil {
		return "", err
	}
	p := remotePath(u)
	if p == "" {
		return "", fmt.Errorf("no project path in the origin remote")
	}
	id = url.PathEscape(p)
	c.mu.Lock()
	if c.projects == nil {
		c.projects = map[string]string{}
	}
	c.projects[dir] = id
	c.mu.Unlock()
	return id, nil
}

// remotePath returns a git remote URL's repo path, without a leading slash
// or a .git suffix: group/sub/project.
func remotePath(u string) string {
	var p string
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		j := strings.IndexByte(rest, '/')
		if j < 0 {
			return ""
		}
		p = rest[j+1:]
	} else if j := strings.IndexByte(u, ':'); j > 0 {
		p = u[j+1:]
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	return strings.Trim(p, "/")
}

// api runs `glab api` as ref's account. method is "" for GET.
func (c *Client) api(ctx context.Context, ref forge.Ref, method, endpoint string, fields ...string) ([]byte, error) {
	var env []string
	if ref.Account != "" {
		env = []string{"GLAB_CONFIG_DIR=" + ref.Account}
	}
	args := []string{"api"}
	if method != "" {
		args = append(args, "--method", method)
	}
	args = append(args, endpoint)
	for _, f := range fields {
		args = append(args, "-F", f)
	}
	return c.runner.Run(ctx, ref.Dir, env, args...)
}

// glMR is the part of a merge request CI and merging read.
type glMR struct {
	SHA                       string `json:"sha"`
	State                     string `json:"state"`
	Draft                     bool   `json:"draft"`
	DetailedMergeStatus       string `json:"detailed_merge_status"`
	MergeWhenPipelineSucceeds bool   `json:"merge_when_pipeline_succeeds"`
	HeadPipeline              *struct {
		ID int `json:"id"`
	} `json:"head_pipeline"`
}

// glJob is a pipeline job.
type glJob struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Stage         string `json:"stage"`
	Status        string `json:"status"`
	WebURL        string `json:"web_url"`
	AllowFailure  bool   `json:"allow_failure"`
	FailureReason string `json:"failure_reason"`
}

// mr fetches the merge request, with the project id it lives in.
func (c *Client) mr(ctx context.Context, ref forge.Ref) (string, *glMR, error) {
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return "", nil, err
	}
	out, err := c.api(ctx, ref, "", fmt.Sprintf("projects/%s/merge_requests/%d", pid, ref.Number))
	if err != nil {
		return "", nil, err
	}
	var mr glMR
	if err := json.Unmarshal(out, &mr); err != nil {
		return "", nil, fmt.Errorf("parsing the merge request: %w", err)
	}
	return pid, &mr, nil
}

// jobs fetches a pipeline's jobs; query narrows them.
func (c *Client) jobs(ctx context.Context, ref forge.Ref, pid string, pipeline int, query string) ([]glJob, error) {
	out, err := c.api(ctx, ref, "", fmt.Sprintf("projects/%s/pipelines/%d/jobs?per_page=100%s", pid, pipeline, query))
	if err != nil {
		return nil, err
	}
	var jobs []glJob
	if err := json.Unmarshal(out, &jobs); err != nil {
		return nil, fmt.Errorf("parsing the pipeline's jobs: %w", err)
	}
	return jobs, nil
}

// CI returns the MR's head, a summary of its head pipeline's jobs and
// whether it can merge. A job allowed to fail counts as passed.
func (c *Client) CI(ctx context.Context, ref forge.Ref) (*forge.CIState, error) {
	pid, mr, err := c.mr(ctx, ref)
	if err != nil {
		return nil, err
	}
	st := &forge.CIState{
		HeadSHA:    mr.SHA,
		MergeState: glMergeState(mr.DetailedMergeStatus),
		AutoMerge:  mr.MergeWhenPipelineSucceeds,
		Merged:     mr.State == "merged",
		Closed:     mr.State == "closed" || mr.State == "locked",
	}
	if mr.Draft {
		st.MergeState = forge.MergeDraft
	}
	if mr.HeadPipeline == nil {
		return st, nil
	}
	jobs, err := c.jobs(ctx, ref, pid, mr.HeadPipeline.ID, "")
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		st.Checks.Total++
		switch j.Status {
		case "success":
			st.Checks.Passed++
		case "failed":
			if j.AllowFailure {
				st.Checks.Passed++
			} else {
				st.Checks.Failed++
			}
		case "created", "pending", "running", "preparing", "waiting_for_resource", "waiting_for_callback", "scheduled":
			st.Checks.Pending++
		}
	}
	return st, nil
}

// glMergeState normalises detailed_merge_status.
func glMergeState(s string) string {
	switch s {
	case "mergeable":
		return forge.MergeClean
	case "ci_must_pass", "ci_still_running", "not_approved", "discussions_not_resolved",
		"blocked_status", "external_status_checks", "requested_changes", "jira_association_missing",
		"merge_request_blocked", "security_policy_violations", "status_checks_must_pass",
		"locked_paths", "locked_lfs_files", "title_regex", "merge_time", "commits_status":
		return forge.MergeBlocked
	case "need_rebase":
		return forge.MergeBehind
	case "conflict", "broken_status":
		return forge.MergeConflict
	case "draft_status":
		return forge.MergeDraft
	default:
		return forge.MergeUnknown
	}
}

// FailedChecks returns the failed jobs of the MR's head pipeline, leaving
// out the ones allowed to fail.
func (c *Client) FailedChecks(ctx context.Context, ref forge.Ref) ([]forge.FailedCheck, error) {
	pid, mr, err := c.mr(ctx, ref)
	if err != nil || mr.HeadPipeline == nil {
		return nil, err
	}
	jobs, err := c.jobs(ctx, ref, pid, mr.HeadPipeline.ID, "&scope[]=failed")
	if err != nil {
		return nil, err
	}
	var failed []forge.FailedCheck
	for _, j := range jobs {
		if j.AllowFailure || j.Status != "failed" {
			continue
		}
		id := strconv.Itoa(j.ID)
		failed = append(failed, forge.FailedCheck{
			ID: id, Name: j.Name, Group: j.Stage, URL: j.WebURL, RunID: id,
			Infra: slices.Contains(infraFailures, j.FailureReason),
		})
	}
	return failed, nil
}

// Log returns the tail of the job's trace.
func (c *Client) Log(ctx context.Context, ref forge.Ref, fc forge.FailedCheck) (string, error) {
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return "", err
	}
	out, err := c.api(ctx, ref, "", fmt.Sprintf("projects/%s/jobs/%s/trace", pid, fc.ID))
	if err != nil {
		return "", err
	}
	return forge.Tail(string(out)), nil
}

// Rerun retries the job.
func (c *Client) Rerun(ctx context.Context, ref forge.Ref, fc forge.FailedCheck) error {
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return err
	}
	_, err = c.api(ctx, ref, "POST", fmt.Sprintf("projects/%s/jobs/%s/retry", pid, fc.ID))
	return err
}

// MergeSettings returns the project's merge methods: squash unless the
// project never squashes, merge unless it always does, and rebase when the
// project merges by rebase or fast-forward. GitLab always has auto-merge.
// Reused for ten minutes.
func (c *Client) MergeSettings(ctx context.Context, ref forge.Ref) (forge.MergeSettings, error) {
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return forge.MergeSettings{}, err
	}
	return c.settings.Get(pid+"\x00"+ref.Account, c.clock(), func() (forge.MergeSettings, error) {
		out, err := c.api(ctx, ref, "", "projects/"+pid)
		if err != nil {
			return forge.MergeSettings{}, err
		}
		var raw struct {
			MergeMethod  string `json:"merge_method"`
			SquashOption string `json:"squash_option"`
		}
		if err := json.Unmarshal(out, &raw); err != nil {
			return forge.MergeSettings{}, fmt.Errorf("parsing the project: %w", err)
		}
		s := forge.MergeSettings{AutoMerge: true}
		if raw.SquashOption != "never" {
			s.Methods = append(s.Methods, "squash")
		}
		if raw.SquashOption != "always" {
			s.Methods = append(s.Methods, "merge")
		}
		if raw.MergeMethod == "rebase_merge" || raw.MergeMethod == "ff" {
			s.Methods = append(s.Methods, "rebase")
		}
		return s, nil
	})
}

// mergeFields are the merge call's fields for method. GitLab merges by the
// project's own merge method, so rebase is only accepted where that method
// rebases.
func (c *Client) mergeFields(ctx context.Context, ref forge.Ref, method string) ([]string, error) {
	if method == "rebase" {
		s, err := c.MergeSettings(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(s.Methods, "rebase") {
			return nil, fmt.Errorf("the project's merge method doesn't rebase")
		}
	}
	return []string{"squash=" + strconv.FormatBool(method == "squash")}, nil
}

// EnableAutoMerge has GitLab merge the MR with method once its pipeline
// passes. GitLab versions before auto_merge reject it, and get
// merge_when_pipeline_succeeds instead.
func (c *Client) EnableAutoMerge(ctx context.Context, ref forge.Ref, method string) error {
	fields, err := c.mergeFields(ctx, ref, method)
	if err != nil {
		return err
	}
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("projects/%s/merge_requests/%d/merge", pid, ref.Number)
	_, err = c.api(ctx, ref, "PUT", endpoint, append(fields, "auto_merge=true")...)
	if err != nil && isParamRejected(err) {
		_, err = c.api(ctx, ref, "PUT", endpoint, append(fields, "merge_when_pipeline_succeeds=true")...)
	}
	return err
}

// isParamRejected reports a GitLab answer to an unknown parameter.
func isParamRejected(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "HTTP 400") || strings.Contains(msg, "HTTP 422")
}

// DisableAutoMerge cancels the MR's auto-merge.
func (c *Client) DisableAutoMerge(ctx context.Context, ref forge.Ref) error {
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return err
	}
	_, err = c.api(ctx, ref, "POST", fmt.Sprintf("projects/%s/merge_requests/%d/cancel_merge_when_pipeline_succeeds", pid, ref.Number))
	return err
}

// Merge merges the MR now with method.
func (c *Client) Merge(ctx context.Context, ref forge.Ref, method string) error {
	fields, err := c.mergeFields(ctx, ref, method)
	if err != nil {
		return err
	}
	pid, err := c.projectID(ctx, ref.Dir)
	if err != nil {
		return err
	}
	_, err = c.api(ctx, ref, "PUT", fmt.Sprintf("projects/%s/merge_requests/%d/merge", pid, ref.Number), fields...)
	return err
}
