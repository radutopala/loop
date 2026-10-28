package githubapi

import (
	"context"
	"errors"
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
	return []byte(r.out[key]), r.errs[key]
}

const ciView = "pr view 7 --json headRefOid,baseRefName,statusCheckRollup,mergeStateStatus,autoMergeRequest,state,isDraft"

type GithubForgeSuite struct {
	suite.Suite
	ref forge.Ref
}

func TestGithubForgeSuite(t *testing.T) {
	suite.Run(t, new(GithubForgeSuite))
}

func (s *GithubForgeSuite) SetupTest() {
	s.ref = forge.Ref{Dir: "/w", Number: 7}
}

func (s *GithubForgeSuite) TestPR() {
	r := &scriptRunner{out: map[string]string{ciView: `{
		"headRefOid": "abc",
		"baseRefName": "main",
		"statusCheckRollup": [
			{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
			{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "NEUTRAL"},
			{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "FAILURE"},
			{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "TIMED_OUT"},
			{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "CANCELLED"},
			{"__typename": "CheckRun", "status": "IN_PROGRESS", "conclusion": ""},
			{"__typename": "StatusContext", "state": "SUCCESS"},
			{"__typename": "StatusContext", "state": "ERROR"},
			{"__typename": "StatusContext", "state": "PENDING"}
		],
		"mergeStateStatus": "BLOCKED",
		"autoMergeRequest": {"mergeMethod": "SQUASH"},
		"state": "OPEN"
	}`}}
	st, err := NewClientWithRunner(r).Status(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Equal(s.T(), &forge.Status{
		HeadSHA:    "abc",
		BaseRef:    "main",
		Checks:     forge.CheckSummary{Total: 9, Passed: 3, Failed: 3, Pending: 2},
		MergeState: forge.MergeBlocked,
		AutoMerge:  true,
	}, st)
}

func (s *GithubForgeSuite) TestPRStates() {
	tests := []struct {
		name string
		json string
		want forge.Status
	}{
		{"merged", `{"state": "MERGED", "autoMergeRequest": null, "mergeStateStatus": "UNKNOWN"}`, forge.Status{Merged: true, MergeState: forge.MergeUnknown}},
		{"closed", `{"state": "CLOSED", "mergeStateStatus": "CLEAN"}`, forge.Status{Closed: true, MergeState: forge.MergeClean}},
		{"draft wins", `{"state": "OPEN", "isDraft": true, "mergeStateStatus": "CLEAN"}`, forge.Status{MergeState: forge.MergeDraft}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			r := &scriptRunner{out: map[string]string{ciView: tc.json}}
			st, err := NewClientWithRunner(r).Status(context.Background(), s.ref)
			require.NoError(s.T(), err)
			require.Equal(s.T(), &tc.want, st)
		})
	}
}

func (s *GithubForgeSuite) TestMergeStateMapping() {
	for in, want := range map[string]string{
		"CLEAN": forge.MergeClean, "HAS_HOOKS": forge.MergeClean, "UNSTABLE": forge.MergeClean,
		"BLOCKED": forge.MergeBlocked, "BEHIND": forge.MergeBehind, "DIRTY": forge.MergeConflict,
		"DRAFT": forge.MergeDraft, "UNKNOWN": forge.MergeUnknown, "": forge.MergeUnknown,
	} {
		require.Equal(s.T(), want, ghMergeState(in), in)
	}
}

func (s *GithubForgeSuite) TestPRErrors() {
	_, err := NewClientWithRunner(&scriptRunner{errs: map[string]error{ciView: errors.New("boom")}}).Status(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "boom")

	_, err = NewClientWithRunner(&scriptRunner{out: map[string]string{ciView: `{`}}).Status(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "parsing gh pr view output")

	r := &scriptRunner{errs: map[string]error{"auth token --user u": errors.New("no user")}}
	_, err = NewClientWithRunner(r).Status(context.Background(), forge.Ref{Dir: "/w", Account: "u", Number: 7})
	require.ErrorContains(s.T(), err, "no user")
}

func (s *GithubForgeSuite) TestFailedChecks() {
	const cmd = "pr checks 7 --json name,bucket,link,workflow"
	out := `[
		{"name": "lint", "bucket": "fail", "link": "https://github.com/o/r/actions/runs/11/job/22", "workflow": "ci"},
		{"name": "deploy", "bucket": "fail", "link": "https://github.com/o/r/actions/runs/33", "workflow": "cd"},
		{"name": "ext", "bucket": "fail", "link": "https://ci.example.com/b/1"},
		{"name": "test", "bucket": "pass", "link": "https://github.com/o/r/actions/runs/11/job/23", "workflow": "ci"}
	]`
	want := []forge.FailedCheck{
		{ID: "22", Name: "lint", Group: "ci", URL: "https://github.com/o/r/actions/runs/11/job/22", RunID: "11"},
		{Name: "deploy", Group: "cd", URL: "https://github.com/o/r/actions/runs/33", RunID: "33"},
		{Name: "ext", URL: "https://ci.example.com/b/1"},
	}
	tests := []struct {
		name    string
		out     string
		err     error
		want    []forge.FailedCheck
		wantErr string
	}{
		{name: "exit 0", out: out, want: want},
		{name: "gh exits 1 on a failed check", out: out, err: errors.New("gh pr checks: exit status 1"), want: want},
		{name: "error without output", err: errors.New("gh pr checks: no checks"), wantErr: "no checks"},
		{name: "not installed", out: out, err: ErrGhNotInstalled, wantErr: "not installed"},
		{name: "bad output", out: "{", wantErr: "parsing gh pr checks output"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			r := &scriptRunner{out: map[string]string{cmd: tc.out}, errs: map[string]error{cmd: tc.err}}
			got, err := NewClientWithRunner(r).FailedChecks(context.Background(), s.ref)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}

	r := &scriptRunner{errs: map[string]error{"auth token --user u": errors.New("no user")}}
	_, err := NewClientWithRunner(r).FailedChecks(context.Background(), forge.Ref{Dir: "/w", Account: "u", Number: 7})
	require.ErrorContains(s.T(), err, "no user")
}

func (s *GithubForgeSuite) TestLog() {
	r := &scriptRunner{
		out: map[string]string{
			"run view --job 22 --log-failed": "job log\n",
			"run view 33 --log-failed":       "run log\n",
		},
		errs: map[string]error{"run view --job 44 --log-failed": errors.New("gone")},
	}
	c := NewClientWithRunner(r)
	got, err := c.Log(context.Background(), s.ref, forge.FailedCheck{ID: "22", RunID: "11"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "job log", got)
	got, err = c.Log(context.Background(), s.ref, forge.FailedCheck{RunID: "33"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "run log", got)
	got, err = c.Log(context.Background(), s.ref, forge.FailedCheck{Name: "ext"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), got)
	_, err = c.Log(context.Background(), s.ref, forge.FailedCheck{ID: "44"})
	require.ErrorContains(s.T(), err, "gone")

	r = &scriptRunner{errs: map[string]error{"auth token --user u": errors.New("no user")}}
	_, err = NewClientWithRunner(r).Log(context.Background(), forge.Ref{Dir: "/w", Account: "u"}, forge.FailedCheck{ID: "1"})
	require.ErrorContains(s.T(), err, "no user")
}

func (s *GithubForgeSuite) TestRerun() {
	r := &scriptRunner{}
	c := NewClientWithRunner(r)
	require.NoError(s.T(), c.Rerun(context.Background(), s.ref, forge.FailedCheck{RunID: "11"}))
	require.Equal(s.T(), []string{"run rerun 11 --failed"}, r.calls)
	require.ErrorContains(s.T(), c.Rerun(context.Background(), s.ref, forge.FailedCheck{Name: "ext"}), "isn't an Actions run")
}

func (s *GithubForgeSuite) TestMergeCommands() {
	r := &scriptRunner{out: map[string]string{"auth token --user u": "tok"}}
	c := NewClientWithRunner(r)
	ref := forge.Ref{Dir: "/w", Account: "u", Number: 7}
	require.NoError(s.T(), c.EnableAutoMerge(context.Background(), ref, "squash"))
	require.NoError(s.T(), c.DisableAutoMerge(context.Background(), ref))
	require.NoError(s.T(), c.Merge(context.Background(), ref, "rebase"))
	require.Equal(s.T(), []string{
		"auth token --user u", "pr merge 7 --auto --squash",
		"auth token --user u", "pr merge 7 --disable-auto",
		"auth token --user u", "pr merge 7 --rebase",
	}, r.calls)
	require.Equal(s.T(), []string{"GH_TOKEN=tok"}, r.envs[1])

	r = &scriptRunner{errs: map[string]error{"auth token --user u": errors.New("no user")}}
	require.ErrorContains(s.T(), NewClientWithRunner(r).Merge(context.Background(), ref, "merge"), "no user")
}

func (s *GithubForgeSuite) TestUnresolvedThreads() {
	cmd := "api graphql -f query=" + unresolvedThreadsQuery + " -F owner={owner} -F name={repo} -F number=7" +
		" --jq .data.repository.pullRequest.reviewThreads.nodes"
	r := &scriptRunner{out: map[string]string{cmd: `[{"id": "T1", "isResolved": false}, {"id": "T2", "isResolved": true}, {"id": "T3", "isResolved": false}]`}}
	ids, err := NewClientWithRunner(r).UnresolvedThreads(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"T1", "T3"}, ids)

	_, err = NewClientWithRunner(&scriptRunner{errs: map[string]error{cmd: errors.New("boom")}}).UnresolvedThreads(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "boom")
	_, err = NewClientWithRunner(&scriptRunner{out: map[string]string{cmd: "{"}}).UnresolvedThreads(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "parsing gh review threads")
	_, err = NewClientWithRunner(&scriptRunner{errs: map[string]error{"auth token --user u": errors.New("no user")}}).
		UnresolvedThreads(context.Background(), forge.Ref{Dir: "/w", Account: "u", Number: 7})
	require.ErrorContains(s.T(), err, "no user")
}

func (s *GithubForgeSuite) TestMergeSettings() {
	cmd := "api graphql -f query=" + mergeSettingsQuery + " -F owner={owner} -F name={repo} --jq .data.repository"
	r := &scriptRunner{out: map[string]string{cmd: `{"autoMergeAllowed": true, "squashMergeAllowed": true, "mergeCommitAllowed": false, "rebaseMergeAllowed": true}`}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewClientWithRunner(r)
	c.now = func() time.Time { return now }
	want := forge.MergeSettings{AutoMerge: true, Methods: []string{"squash", "rebase"}}

	got, err := c.MergeSettings(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)
	now = now.Add(9 * time.Minute)
	got, err = c.MergeSettings(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)
	require.Len(s.T(), r.calls, 1, "cached")
	now = now.Add(2 * time.Minute)
	_, err = c.MergeSettings(context.Background(), s.ref)
	require.NoError(s.T(), err)
	require.Len(s.T(), r.calls, 2, "expired")

	_, err = NewClientWithRunner(&scriptRunner{errs: map[string]error{cmd: errors.New("boom")}}).MergeSettings(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "boom")
	_, err = NewClientWithRunner(&scriptRunner{out: map[string]string{cmd: "{"}}).MergeSettings(context.Background(), s.ref)
	require.ErrorContains(s.T(), err, "parsing gh merge settings")
	_, err = NewClientWithRunner(&scriptRunner{errs: map[string]error{"auth token --user u": errors.New("no user")}}).
		MergeSettings(context.Background(), forge.Ref{Dir: "/w", Account: "u"})
	require.ErrorContains(s.T(), err, "no user")
}

func (s *GithubForgeSuite) TestRealClock() {
	require.WithinDuration(s.T(), time.Now(), NewClientWithRunner(&scriptRunner{}).clock(), time.Minute)
}
