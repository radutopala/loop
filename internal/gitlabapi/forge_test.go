package gitlabapi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/forge"
)

// scriptRunner answers each command by its joined args and records the
// calls.
type scriptRunner struct {
	out   map[string]string
	errs  map[string]error
	calls []string
	envs  [][]string
}

func (r *scriptRunner) Run(_ context.Context, _ string, env []string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.calls = append(r.calls, key)
	r.envs = append(r.envs, env)
	if r.errs[key] != nil {
		return nil, r.errs[key]
	}
	return []byte(r.out[key]), nil
}

const (
	glPID   = "group%2Fsub%2Fproject"
	glMRGet = "api projects/" + glPID + "/merge_requests/5"
	glJobs  = "api projects/" + glPID + "/pipelines/90/jobs?per_page=100"
	glFail  = glJobs + "&scope[]=failed"
	glProj  = "api projects/" + glPID
	glMerge = "api --method PUT projects/" + glPID + "/merge_requests/5/merge"
)

type GitlabForgeSuite struct {
	suite.Suite
	ref forge.Ref
}

func TestGitlabForgeSuite(t *testing.T) {
	suite.Run(t, new(GitlabForgeSuite))
}

func (s *GitlabForgeSuite) SetupTest() {
	s.ref = forge.Ref{Dir: "/w", Number: 5}
}

func (s *GitlabForgeSuite) client(r *scriptRunner) *Client {
	c := NewClientWithRunner(r, "", noFile)
	c.originURL = func(context.Context, string) (string, error) {
		return "git@gitlab.example.com:group/sub/project.git", nil
	}
	return c
}

func (s *GitlabForgeSuite) TestRemotePath() {
	for in, want := range map[string]string{
		"git@gitlab.example.com:group/sub/project.git":         "group/sub/project",
		"https://gitlab.example.com/group/project.git":         "group/project",
		"ssh://git@gitlab.example.com:2222/group/project.git/": "group/project",
		"https://gitlab.example.com":                           "",
		"/srv/repo":                                            "",
	} {
		require.Equal(s.T(), want, remotePath(in), in)
	}
}

func (s *GitlabForgeSuite) TestProjectID() {
	calls := 0
	c := NewClientWithRunner(&scriptRunner{}, "", noFile)
	c.originURL = func(context.Context, string) (string, error) {
		calls++
		return "https://gitlab.example.com/group/sub/project.git", nil
	}
	id, err := c.projectID(context.Background(), "/w")
	require.NoError(s.T(), err)
	require.Equal(s.T(), glPID, id)
	_, _ = c.projectID(context.Background(), "/w")
	require.Equal(s.T(), 1, calls, "resolved once")

	c.originURL = func(context.Context, string) (string, error) { return "", errors.New("no origin") }
	_, err = c.projectID(context.Background(), "/other")
	require.ErrorContains(s.T(), err, "no origin")

	c.originURL = func(context.Context, string) (string, error) { return "/srv/repo", nil }
	_, err = c.projectID(context.Background(), "/local")
	require.ErrorContains(s.T(), err, "no project path")
}

func (s *GitlabForgeSuite) TestGitOriginURL() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not installed")
	}
	dir := s.T().TempDir()
	require.NoError(s.T(), exec.Command("git", "-C", dir, "init", "-q").Run())
	_, err := gitOriginURL(context.Background(), dir)
	require.ErrorContains(s.T(), err, "reading the origin remote")
	require.NoError(s.T(), exec.Command("git", "-C", dir, "remote", "add", "origin", "git@gitlab.example.com:g/p.git").Run())
	u, err := gitOriginURL(context.Background(), dir)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "git@gitlab.example.com:g/p.git", u)

	c := NewClientWithRunner(&scriptRunner{}, "", noFile)
	id, err := c.projectID(context.Background(), dir)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "g%2Fp", id)
}

func (s *GitlabForgeSuite) TestCI() {
	r := &scriptRunner{out: map[string]string{
		glMRGet: `{"sha": "abc", "state": "opened", "detailed_merge_status": "ci_still_running", "merge_when_pipeline_succeeds": true, "head_pipeline": {"id": 90}}`,
		glJobs: `[
			{"status": "success"}, {"status": "failed"}, {"status": "failed", "allow_failure": true},
			{"status": "running"}, {"status": "pending"}, {"status": "canceled"}, {"status": "skipped"}, {"status": "manual"}
		]`,
	}}
	r.out["x"] = ""
	st, err := s.client(r).CI(context.Background(), forge.Ref{Dir: "/w", Account: "/cfg", Number: 5})
	require.NoError(s.T(), err)
	require.Equal(s.T(), &forge.CIState{
		HeadSHA:    "abc",
		Checks:     forge.CheckSummary{Total: 8, Passed: 2, Failed: 1, Pending: 2},
		MergeState: forge.MergeBlocked,
		AutoMerge:  true,
	}, st)
	require.Equal(s.T(), []string{"GLAB_CONFIG_DIR=/cfg"}, r.envs[0])
}

func (s *GitlabForgeSuite) TestCIStates() {
	tests := []struct {
		name string
		mr   string
		want forge.CIState
	}{
		{"merged, no pipeline", `{"state": "merged", "detailed_merge_status": "not_open"}`, forge.CIState{Merged: true, MergeState: forge.MergeUnknown}},
		{"closed", `{"state": "closed", "detailed_merge_status": "mergeable"}`, forge.CIState{Closed: true, MergeState: forge.MergeClean}},
		{"locked", `{"state": "locked"}`, forge.CIState{Closed: true, MergeState: forge.MergeUnknown}},
		{"draft wins", `{"state": "opened", "draft": true, "detailed_merge_status": "mergeable"}`, forge.CIState{MergeState: forge.MergeDraft}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			st, err := s.client(&scriptRunner{out: map[string]string{glMRGet: tc.mr}}).CI(context.Background(), s.ref)
			require.NoError(s.T(), err)
			require.Equal(s.T(), &tc.want, st)
		})
	}
}

func (s *GitlabForgeSuite) TestMergeStateMapping() {
	want := map[string]string{
		"mergeable":   forge.MergeClean,
		"need_rebase": forge.MergeBehind,
		"conflict":    forge.MergeConflict, "broken_status": forge.MergeConflict,
		"draft_status": forge.MergeDraft,
		"checking":     forge.MergeUnknown, "unchecked": forge.MergeUnknown, "preparing": forge.MergeUnknown, "not_open": forge.MergeUnknown,
	}
	for _, b := range []string{"ci_must_pass", "ci_still_running", "not_approved", "discussions_not_resolved",
		"blocked_status", "external_status_checks", "requested_changes", "jira_association_missing",
		"merge_request_blocked", "security_policy_violations", "status_checks_must_pass",
		"locked_paths", "locked_lfs_files", "title_regex", "merge_time", "commits_status"} {
		want[b] = forge.MergeBlocked
	}
	for in, w := range want {
		require.Equal(s.T(), w, glMergeState(in), in)
	}
}

func (s *GitlabForgeSuite) TestCIErrors() {
	tests := []struct {
		name    string
		r       *scriptRunner
		wantErr string
	}{
		{"mr call", &scriptRunner{errs: map[string]error{glMRGet: errors.New("glab: 404 Not Found")}}, "404"},
		{"mr JSON", &scriptRunner{out: map[string]string{glMRGet: "{"}}, "parsing the merge request"},
		{"jobs call", &scriptRunner{out: map[string]string{glMRGet: `{"head_pipeline": {"id": 90}}`}, errs: map[string]error{glJobs: errors.New("boom")}}, "boom"},
		{"jobs JSON", &scriptRunner{out: map[string]string{glMRGet: `{"head_pipeline": {"id": 90}}`, glJobs: "{"}}, "parsing the pipeline's jobs"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			_, err := s.client(tc.r).CI(context.Background(), s.ref)
			require.ErrorContains(s.T(), err, tc.wantErr)
		})
	}
	c := s.client(&scriptRunner{})
	c.originURL = func(context.Context, string) (string, error) { return "", errors.New("no origin") }
	_, err := c.CI(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "no origin")
}

func (s *GitlabForgeSuite) TestFailedChecks() {
	r := &scriptRunner{out: map[string]string{
		glMRGet: `{"head_pipeline": {"id": 90}}`,
		glFail: `[
			{"id": 1, "name": "lint", "stage": "test", "status": "failed", "web_url": "https://gitlab.example.com/j/1", "failure_reason": "script_failure"},
			{"id": 2, "name": "e2e", "stage": "test", "status": "failed", "web_url": "https://gitlab.example.com/j/2", "failure_reason": "runner_system_failure"},
			{"id": 3, "name": "flaky", "stage": "test", "status": "failed", "allow_failure": true},
			{"id": 4, "name": "retried", "stage": "test", "status": "success"}
		]`,
	}}
	got, err := s.client(r).FailedChecks(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []forge.FailedCheck{
		{ID: "1", Name: "lint", Group: "test", URL: "https://gitlab.example.com/j/1", RunID: "1"},
		{ID: "2", Name: "e2e", Group: "test", URL: "https://gitlab.example.com/j/2", RunID: "2", Infra: true},
	}, got)

	got, err = s.client(&scriptRunner{out: map[string]string{glMRGet: `{}`}}).FailedChecks(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got, "no pipeline")

	_, err = s.client(&scriptRunner{errs: map[string]error{glMRGet: errors.New("boom")}}).FailedChecks(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "boom")

	_, err = s.client(&scriptRunner{out: map[string]string{glMRGet: `{"head_pipeline": {"id": 90}}`}, errs: map[string]error{glFail: errors.New("jobs")}}).FailedChecks(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "jobs")
}

func (s *GitlabForgeSuite) TestLogAndRerun() {
	trace := "api projects/" + glPID + "/jobs/1/trace"
	retry := "api --method POST projects/" + glPID + "/jobs/1/retry"
	r := &scriptRunner{out: map[string]string{trace: "a\nb\n"}}
	c := s.client(r)
	got, err := c.Log(context.Background(), s.ref, forge.FailedCheck{ID: "1"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "a\nb", got)
	require.NoError(s.T(), c.Rerun(context.Background(), s.ref, forge.FailedCheck{ID: "1"}))
	require.Equal(s.T(), []string{trace, retry}, r.calls)

	_, err = s.client(&scriptRunner{errs: map[string]error{trace: errors.New("gone")}}).Log(context.Background(), s.ref, forge.FailedCheck{ID: "1"})
	require.ErrorContains(s.T(), err, "gone")

	bad := s.client(&scriptRunner{})
	bad.originURL = func(context.Context, string) (string, error) { return "", errors.New("no origin") }
	_, err = bad.Log(context.Background(), s.ref, forge.FailedCheck{ID: "1"})
	require.ErrorContains(s.T(), err, "no origin")
	require.ErrorContains(s.T(), bad.Rerun(context.Background(), s.ref, forge.FailedCheck{ID: "1"}), "no origin")
	require.ErrorContains(s.T(), bad.DisableAutoMerge(context.Background(), s.ref), "no origin")
	require.ErrorContains(s.T(), bad.Merge(context.Background(), s.ref, "merge"), "no origin")
	require.ErrorContains(s.T(), bad.EnableAutoMerge(context.Background(), s.ref, "merge"), "no origin")
	_, err = bad.MergeSettings(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "no origin")
}

func (s *GitlabForgeSuite) TestMergeSettings() {
	tests := []struct {
		name    string
		project string
		want    []string
	}{
		{"merge commits, squash allowed", `{"merge_method": "merge", "squash_option": "default_off"}`, []string{"squash", "merge"}},
		{"never squash", `{"merge_method": "merge", "squash_option": "never"}`, []string{"merge"}},
		{"always squash", `{"merge_method": "merge", "squash_option": "always"}`, []string{"squash"}},
		{"rebase merge", `{"merge_method": "rebase_merge", "squash_option": "default_on"}`, []string{"squash", "merge", "rebase"}},
		{"fast-forward", `{"merge_method": "ff", "squash_option": "never"}`, []string{"merge", "rebase"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, err := s.client(&scriptRunner{out: map[string]string{glProj: tc.project}}).MergeSettings(context.Background(), s.ref)
			require.NoError(s.T(), err)
			require.Equal(s.T(), forge.MergeSettings{AutoMerge: true, Methods: tc.want}, got)
		})
	}

	r := &scriptRunner{out: map[string]string{glProj: `{"merge_method": "merge"}`}}
	c := s.client(r)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	_, _ = c.MergeSettings(context.Background(), s.ref)
	_, _ = c.MergeSettings(context.Background(), s.ref)
	require.Len(s.T(), r.calls, 1, "cached")

	_, err := s.client(&scriptRunner{errs: map[string]error{glProj: errors.New("boom")}}).MergeSettings(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "boom")
	_, err = s.client(&scriptRunner{out: map[string]string{glProj: "{"}}).MergeSettings(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "parsing the project")
}

func (s *GitlabForgeSuite) TestMergeCalls() {
	tests := []struct {
		name    string
		do      func(c *Client) error
		project string
		errs    map[string]error
		want    []string
		wantErr string
	}{
		{
			name: "auto-merge squash",
			do:   func(c *Client) error { return c.EnableAutoMerge(context.Background(), s.ref, "squash") },
			want: []string{glMerge + " -F squash=true -F auto_merge=true"},
		},
		{
			name: "auto-merge falls back on older GitLab",
			do:   func(c *Client) error { return c.EnableAutoMerge(context.Background(), s.ref, "merge") },
			errs: map[string]error{glMerge + " -F squash=false -F auto_merge=true": errors.New("glab: 400 Bad Request (HTTP 400)")},
			want: []string{glMerge + " -F squash=false -F auto_merge=true", glMerge + " -F squash=false -F merge_when_pipeline_succeeds=true"},
		},
		{
			name:    "auto-merge other error",
			do:      func(c *Client) error { return c.EnableAutoMerge(context.Background(), s.ref, "merge") },
			errs:    map[string]error{glMerge + " -F squash=false -F auto_merge=true": errors.New("glab: 405 Method Not Allowed (HTTP 405)")},
			want:    []string{glMerge + " -F squash=false -F auto_merge=true"},
			wantErr: "HTTP 405",
		},
		{
			name: "auto-merge 422 falls back",
			do:   func(c *Client) error { return c.EnableAutoMerge(context.Background(), s.ref, "merge") },
			errs: map[string]error{glMerge + " -F squash=false -F auto_merge=true": errors.New("(HTTP 422)")},
			want: []string{glMerge + " -F squash=false -F auto_merge=true", glMerge + " -F squash=false -F merge_when_pipeline_succeeds=true"},
		},
		{
			name: "cancel",
			do:   func(c *Client) error { return c.DisableAutoMerge(context.Background(), s.ref) },
			want: []string{"api --method POST projects/" + glPID + "/merge_requests/5/cancel_merge_when_pipeline_succeeds"},
		},
		{
			name: "merge now",
			do:   func(c *Client) error { return c.Merge(context.Background(), s.ref, "merge") },
			want: []string{glMerge + " -F squash=false"},
		},
		{
			name:    "rebase where the project rebases",
			do:      func(c *Client) error { return c.Merge(context.Background(), s.ref, "rebase") },
			project: `{"merge_method": "ff"}`,
			want:    []string{glProj, glMerge + " -F squash=false"},
		},
		{
			name:    "rebase refused",
			do:      func(c *Client) error { return c.EnableAutoMerge(context.Background(), s.ref, "rebase") },
			project: `{"merge_method": "merge"}`,
			want:    []string{glProj},
			wantErr: "doesn't rebase",
		},
		{
			name:    "rebase settings error",
			do:      func(c *Client) error { return c.Merge(context.Background(), s.ref, "rebase") },
			errs:    map[string]error{glProj: errors.New("boom")},
			want:    []string{glProj},
			wantErr: "boom",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			r := &scriptRunner{out: map[string]string{glProj: tc.project}, errs: tc.errs}
			err := tc.do(s.client(r))
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tc.want, r.calls)
		})
	}
}

func (s *GitlabForgeSuite) TestRealClock() {
	require.WithinDuration(s.T(), time.Now(), NewClientWithRunner(&scriptRunner{}, "", os.ReadFile).clock(), time.Minute)
}
