//go:build component

package component

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

func registerCISteps(ctx *godog.ScenarioContext, tc *TestContext) {
	ctx.Step(`^(gh|glab) reports (?:PR #|MR !)(\d+) with a failing "([^"]*)" check$`, tc.fakeForgeCI)
	ctx.Step(`^(gh|glab) should have been called with "([^"]*)"$`, tc.assertFakeCLICall)
	ctx.Step(`^a GET to "([^"]*)" should soon have JSON "([^"]*)" equal to "([^"]*)"$`, tc.pollGETJSON)
	ctx.Step(`^the channel list should soon show the current channel's "([^"]*)" as "([^"]*)"$`, tc.pollChannelListField)
}

// ciHeadSHA is the head commit the fake forges report.
const ciHeadSHA = "4f1c2d9e8b7a6f5e4d3c2b1a0f9e8d7c6b5a4f3e"

// fakeCLICase is one branch of a fake CLI: args matching the shell case
// pattern print out.
type fakeCLICase struct {
	pattern string
	out     string
}

// fakeForgeCI puts a gh or glab on the daemon's PATH that reports change
// number open on whatever branch is asked about, its head with one failed
// check named check and one passed, and accepts merges. Every call is
// logged for assertFakeCLICall.
func (tc *TestContext) fakeForgeCI(bin string, number int, check string) error {
	n := strconv.Itoa(number)
	js := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	var cases []fakeCLICase
	if bin == "gh" {
		cases = []fakeCLICase{
			{`"pr view "*"headRefOid,statusCheckRollup"*`, js(map[string]any{
				"headRefOid": ciHeadSHA, "mergeStateStatus": "BLOCKED", "autoMergeRequest": nil, "state": "OPEN", "isDraft": false,
				"statusCheckRollup": []map[string]string{
					{"status": "COMPLETED", "conclusion": "FAILURE"},
					{"status": "COMPLETED", "conclusion": "SUCCESS"},
				},
			})},
			{`"pr view "*`, js(map[string]any{
				"number": number, "url": "https://github.com/example/repo/pull/" + n,
				"baseRefName": "main", "headRefName": "feat", "state": "OPEN", "title": "Fix the build", "isDraft": false,
			})},
			{`"pr checks "*`, js([]map[string]string{
				{"name": check, "bucket": "fail", "link": "https://github.com/example/repo/actions/runs/9/job/123", "workflow": "CI"},
				{"name": "test", "bucket": "pass", "link": "https://github.com/example/repo/actions/runs/9/job/124", "workflow": "CI"},
			})},
			{`"api graphql "*`, js(map[string]bool{
				"autoMergeAllowed": true, "squashMergeAllowed": true, "mergeCommitAllowed": true, "rebaseMergeAllowed": false,
			})},
			{`"run view "*`, check + ": missing comma"},
			{`"run rerun "*`, ""},
			{`"pr merge "*`, ""},
		}
	} else {
		cases = []fakeCLICase{
			{`"api --method "*`, "{}"},
			{`"mr list "*`, js([]map[string]any{{
				"iid": number, "web_url": "https://gitlab.example.com/example/group/project/-/merge_requests/" + n,
				"target_branch": "main", "source_branch": "feat", "state": "opened", "title": "Fix the build", "draft": false,
			}})},
			{`"api projects/"*"/trace"`, check + ": missing comma"},
			{`"api projects/"*"/pipelines/5/jobs"*`, js([]map[string]any{
				{"id": 981, "name": check, "stage": "test", "status": "failed", "web_url": "https://gitlab.example.com/example/group/project/-/jobs/981", "allow_failure": false, "failure_reason": "script_failure"},
				{"id": 982, "name": "test", "stage": "test", "status": "success", "web_url": "https://gitlab.example.com/example/group/project/-/jobs/982", "allow_failure": false},
			})},
			{`"api projects/"*"/merge_requests/` + n + `"`, js(map[string]any{
				"sha": ciHeadSHA, "state": "opened", "draft": false, "detailed_merge_status": "ci_must_pass",
				"merge_when_pipeline_succeeds": false, "head_pipeline": map[string]int{"id": 5},
			})},
			{`"api projects/"*`, js(map[string]string{"merge_method": "merge", "squash_option": "default_off"})},
		}
	}
	return tc.fakeCLIWithCases(bin, cases)
}

// fakeCLILog is where a fake bin logs its calls, one line of args each.
func fakeCLILog(bin string) string {
	return "/tmp/loop-bdd-fake-" + bin + ".log"
}

// fakeCLIWithCases installs a fake bin in /usr/local/bin that logs its
// args and prints the out of the first case they match, failing for
// anything else. cleanup removes it and its log. A real bin on PATH is left
// alone: the step fails.
func (tc *TestContext) fakeCLIWithCases(bin string, cases []fakeCLICase) error {
	if found, err := exec.LookPath(bin); err == nil {
		return fmt.Errorf("a %s is already on PATH at %s", bin, found)
	}
	log := fakeCLILog(bin)
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\necho \"$*\" >> %s\ncase \"$*\" in\n", log)
	for _, c := range cases {
		fmt.Fprintf(&b, "  %s) printf '%%s\\n' '%s'; exit 0;;\n", c.pattern, c.out)
	}
	fmt.Fprintf(&b, "esac\necho \"fake %s: unsupported: $*\" >&2\nexit 1\n", bin)
	path := "/usr/local/bin/" + bin
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		return err
	}
	tc.FakeBinPaths = append(tc.FakeBinPaths, path, log)
	return nil
}

// assertFakeCLICall checks the fake bin was called with exactly args.
func (tc *TestContext) assertFakeCLICall(bin, args string) error {
	data, err := os.ReadFile(fakeCLILog(bin))
	if err != nil {
		return fmt.Errorf("reading the fake %s's calls: %w", bin, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == args {
			return nil
		}
	}
	return fmt.Errorf("%s wasn't called with %q; calls:\n%s", bin, args, data)
}

// pollGETJSON GETs path until the JSON at the dotted field reads want; a
// missing field reads "".
func (tc *TestContext) pollGETJSON(path, field, want string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := tc.doRequest(http.MethodGet, path, ""); err != nil {
			return err
		}
		got := ""
		if v, ok := jsonAt(tc.LastJSON, field); ok {
			got = fmt.Sprintf("%v", v)
		}
		if got == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("GET %s: JSON %q should be %q, got %q: %s", path, field, want, got, string(tc.LastBody))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pollChannelListField lists the channels until the current channel's
// field reads want.
func (tc *TestContext) pollChannelListField(field, want string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := tc.doRequest(http.MethodGet, "/api/channels", ""); err != nil {
			return err
		}
		var channels []map[string]any
		if err := json.Unmarshal(tc.LastBody, &channels); err != nil {
			return fmt.Errorf("parsing the channel list: %w: %s", err, string(tc.LastBody))
		}
		got := ""
		for _, ch := range channels {
			if ch["channel_id"] == tc.ChannelID {
				if v, ok := ch[field]; ok {
					got = fmt.Sprintf("%v", v)
				}
			}
		}
		if got == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("channel %s: %q should be %q, got %q", tc.ChannelID, field, want, got)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// jsonAt returns the value at a dotted path into decoded JSON, a number
// segment indexing an array: "ci.failed.0.name".
func jsonAt(v any, path string) (any, bool) {
	for seg := range strings.SplitSeq(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			v = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			v = node[i]
		default:
			return nil, false
		}
	}
	return v, true
}
