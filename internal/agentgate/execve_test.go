package agentgate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type ExecveSuite struct {
	suite.Suite
}

func TestExecveSuite(t *testing.T) {
	suite.Run(t, new(ExecveSuite))
}

// --- fakeApprover ---

type fakeApprover struct {
	outcome  Outcome
	captured ApprovalRequest
	calls    int
}

func (f *fakeApprover) Request(_ context.Context, _ string, req ApprovalRequest) Outcome {
	f.calls++
	f.captured = req
	return f.outcome
}

// --- isMemfdPath ---

func (s *ExecveSuite) TestIsMemfdPath() {
	s.Require().True(isMemfdPath("memfd:shell"))
	s.Require().True(isMemfdPath("/memfd:payload"))
	s.Require().False(isMemfdPath(""))
	s.Require().False(isMemfdPath("/usr/bin/bash"))
	s.Require().False(isMemfdPath("memfoo"))
}

// --- execveCacheKey ---

func (s *ExecveSuite) TestExecveCacheKeyEmpty() {
	s.Require().Equal("", execveCacheKey(nil))
}

func (s *ExecveSuite) TestExecveCacheKeyNoRest() {
	s.Require().Equal("execve:ls", execveCacheKey([]string{"/bin/ls"}))
}

func (s *ExecveSuite) TestExecveCacheKeyCapsRestToTwo() {
	got := execveCacheKey([]string{"/usr/bin/git", "push", "origin", "main", "--force"})
	s.Require().Equal("execve:git:push origin", got)
}

func (s *ExecveSuite) TestExecveCacheKeyShortRest() {
	got := execveCacheKey([]string{"/bin/rm", "-rf"})
	s.Require().Equal("execve:rm:-rf", got)
}

// --- isWrapper ---

func (s *ExecveSuite) TestIsWrapperKnownNames() {
	for _, name := range []string{"env", "sudo", "nice", "ionice", "chrt", "timeout", "nohup", "unshare", "setsid", "taskset", "stdbuf", "script"} {
		s.Require().Truef(isWrapper(name), "%q should be a wrapper", name)
	}
}

func (s *ExecveSuite) TestIsWrapperLdLinuxPrefix() {
	s.Require().True(isWrapper("ld-linux-x86-64.so.2"))
	s.Require().True(isWrapper("ld-linux-aarch64.so.1"))
}

func (s *ExecveSuite) TestIsWrapperRejectsUnknown() {
	s.Require().False(isWrapper("bash"))
	s.Require().False(isWrapper("eatmydata"))
}

// --- looksLikeEnvAssignment ---

func (s *ExecveSuite) TestLooksLikeEnvAssignmentAccepts() {
	s.Require().True(looksLikeEnvAssignment("FOO=bar"))
	s.Require().True(looksLikeEnvAssignment("foo_bar=baz"))
	s.Require().True(looksLikeEnvAssignment("HOME="))
	s.Require().True(looksLikeEnvAssignment("X1=1"))
}

func (s *ExecveSuite) TestLooksLikeEnvAssignmentRejects() {
	s.Require().False(looksLikeEnvAssignment("noeq"))
	s.Require().False(looksLikeEnvAssignment("=val"))            // empty key
	s.Require().False(looksLikeEnvAssignment("1FOO=bar"))        // leading digit
	s.Require().False(looksLikeEnvAssignment("FOO-BAR=baz"))     // dash in key
	s.Require().False(looksLikeEnvAssignment("a b=c"))           // space in key
	s.Require().False(looksLikeEnvAssignment("/usr/bin/env=no")) // path-shaped
}

// --- unwrapCommand ---

func (s *ExecveSuite) TestUnwrapCommandEmpty() {
	s.Require().Empty(unwrapCommand(nil))
}

func (s *ExecveSuite) TestUnwrapCommandNonWrapperReturnsAsIs() {
	argv := []string{"/bin/ls", "-al"}
	s.Require().Equal(argv, unwrapCommand(argv))
}

func (s *ExecveSuite) TestUnwrapCommandEnvStripsAssignments() {
	argv := []string{"env", "FOO=bar", "HOME=/tmp", "/bin/rm", "-rf", "/data"}
	s.Require().Equal([]string{"/bin/rm", "-rf", "/data"}, unwrapCommand(argv))
}

func (s *ExecveSuite) TestUnwrapCommandSudoSkipsFlags() {
	argv := []string{"sudo", "-E", "-u", "nobody", "/bin/rm"}
	// sudo takes flags (starting with '-') then the command. We skip any
	// arg starting with '-' — the "-u" plus its value both get skipped
	// because "nobody" is neither a flag nor an env assignment.
	got := unwrapCommand(argv)
	// First non-dash arg after sudo is "nobody" (not a flag). We can't
	// distinguish "flag value" from "payload" without a per-tool table, so
	// the payload becomes ["nobody", "/bin/rm"]. That's a documented
	// best-effort outcome — the rule ends up seeing "nobody" as argv[0].
	s.Require().Equal([]string{"nobody", "/bin/rm"}, got)
}

func (s *ExecveSuite) TestUnwrapCommandDoubleDashTerminator() {
	argv := []string{"sudo", "--", "-weird-cmd", "arg"}
	s.Require().Equal([]string{"-weird-cmd", "arg"}, unwrapCommand(argv))
}

func (s *ExecveSuite) TestUnwrapCommandChainedWrappers() {
	argv := []string{"sudo", "env", "FOO=bar", "/bin/rm", "-rf"}
	s.Require().Equal([]string{"/bin/rm", "-rf"}, unwrapCommand(argv))
}

func (s *ExecveSuite) TestUnwrapCommandLdLinuxLoader() {
	// ld-linux* is a wrapper by prefix — the loader flags get skipped just
	// like any other wrapper.
	argv := []string{"/lib/ld-linux-aarch64.so.1", "--library-path", "/x", "/bin/bash", "-c", "exit 0"}
	// "--library-path" is a flag; "/x" is not a flag, not an env assign,
	// so payload starts there. The loader case is best-effort; we end up
	// with ["/x", "/bin/bash", …].
	got := unwrapCommand(argv)
	s.Require().Equal([]string{"/x", "/bin/bash", "-c", "exit 0"}, got)
}

func (s *ExecveSuite) TestUnwrapCommandWrapperOnlyKeepsWrapper() {
	// `sudo` with no payload (no non-flag args) is kept so the policy still
	// applies a rule against "sudo" itself.
	argv := []string{"sudo", "-h"}
	s.Require().Equal(argv, unwrapCommand(argv))
}

// --- NewExecveHandler ---

func (s *ExecveSuite) TestNewExecveHandlerStoresFields() {
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil, nil)
	s.Require().NoError(err)
	ap := &fakeApprover{}
	h := NewExecveHandler(policy, ap)
	s.Require().Same(policy, h.Policy)
	s.Require().Same(ap, h.Approver.(*fakeApprover))
}

// --- Handle ---

func (s *ExecveSuite) mustPolicy(defaultDecision types.Decision, cmdRules ...types.CommandRule) *Policy {
	p, err := CompilePolicy(defaultDecision, nil, cmdRules, nil)
	require.NoError(s.T(), err)
	return p
}

func (s *ExecveSuite) TestHandleMemfdDeniedUnconditionally() {
	h := NewExecveHandler(s.mustPolicy(types.DecisionAllow), nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "memfd:shellcode",
		Argv:     []string{"memfd:shellcode"},
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
	s.Require().Equal("memfd-execveat-deny", out.Reason)
}

func (s *ExecveSuite) TestHandleEmptyArgvUsesFilename() {
	// No argv passed; handler must fall back to [Filename] so policy match
	// still happens. Policy allows-by-default so this returns allow.
	h := NewExecveHandler(s.mustPolicy(types.DecisionAllow), nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "/bin/true",
	})
	s.Require().Equal(types.DecisionAllow, out.Decision)
}

func (s *ExecveSuite) TestHandleRuleAllowReturnsAllow() {
	policy := s.mustPolicy(types.DecisionDeny, types.CommandRule{
		Commands: []string{"ls"},
		Decision: types.DecisionAllow,
	})
	h := NewExecveHandler(policy, nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "/bin/ls",
		Argv:     []string{"/bin/ls"},
	})
	s.Require().Equal(types.DecisionAllow, out.Decision)
	s.Require().Equal("cmd[0]", out.Reason)
}

func (s *ExecveSuite) TestHandleRuleDenyReturnsDeny() {
	policy := s.mustPolicy(types.DecisionAllow, types.CommandRule{
		Commands:     []string{"rm"},
		ArgsPatterns: []string{`-rf /.*`},
		Decision:     types.DecisionDeny,
		Message:      "rm -rf absolute path",
	})
	h := NewExecveHandler(policy, nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "/bin/rm",
		Argv:     []string{"/bin/rm", "-rf", "/data"},
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
	s.Require().Equal("cmd[0]", out.Reason)
}

func (s *ExecveSuite) TestHandleRuleApproveDispatchesToApprover() {
	policy := s.mustPolicy(types.DecisionAllow, types.CommandRule{
		Commands: []string{"git"},
		Decision: types.DecisionApprove,
		Message:  "git write-side op",
	})
	ap := &fakeApprover{outcome: Outcome{Decision: types.DecisionAllow, Actor: "u1"}}
	h := NewExecveHandler(policy, ap)
	out := h.Handle(context.Background(), ExecveRequest{
		ChannelID: "chan",
		Filename:  "/usr/bin/git",
		Argv:      []string{"/usr/bin/git", "push", "origin"},
	})
	s.Require().Equal(types.DecisionAllow, out.Decision)
	s.Require().Equal("u1", out.Actor)
	s.Require().Equal(1, ap.calls)
	s.Require().Equal("execve", ap.captured.Kind)
	s.Require().Equal("/usr/bin/git push origin", ap.captured.Target)
	s.Require().Equal("git write-side op", ap.captured.Message)
	s.Require().Equal("execve:git:push origin", ap.captured.CacheKey)
}

// TestHandleRuleApproveBlocksUntilApproverReturns pins the blocking contract:
// when an execve hits an approve rule, Handle must not return until the
// Approver (which proxies to the Discord/Slack prompt) resolves. If this
// ever regressed — e.g. the approver call became fire-and-forget — the
// syscall would resume before the user clicked, defeating the gate.
func (s *ExecveSuite) TestHandleRuleApproveBlocksUntilApproverReturns() {
	policy := s.mustPolicy(types.DecisionAllow, types.CommandRule{
		Commands:     []string{"git"},
		ArgsPatterns: []string{`^commit(\s|$)`},
		Decision:     types.DecisionApprove,
	})

	entered := make(chan struct{})
	release := make(chan Outcome)
	ap := &blockingApprover{entered: entered, release: release}

	h := NewExecveHandler(policy, ap)

	result := make(chan Outcome, 1)
	go func() {
		result <- h.Handle(context.Background(), ExecveRequest{
			ChannelID: "chan",
			Filename:  "/usr/bin/git",
			Argv:      []string{"/usr/bin/git", "commit", "-m", "feat: multi\nline\nmessage"},
		})
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		s.FailNow("Approver.Request was not called")
	}

	// Handle must still be blocked — the approver hasn't returned yet.
	select {
	case out := <-result:
		s.FailNowf("Handle returned before Approver resolved", "outcome: %+v", out)
	case <-time.After(20 * time.Millisecond):
	}

	release <- Outcome{Decision: types.DecisionAllow, Actor: "u-42"}

	select {
	case out := <-result:
		s.Require().Equal(types.DecisionAllow, out.Decision)
		s.Require().Equal("u-42", out.Actor)
	case <-time.After(time.Second):
		s.FailNow("Handle did not return after Approver resolved")
	}
}

type blockingApprover struct {
	entered chan<- struct{}
	release <-chan Outcome
}

func (b *blockingApprover) Request(_ context.Context, _ string, _ ApprovalRequest) Outcome {
	b.entered <- struct{}{}
	return <-b.release
}

func (s *ExecveSuite) TestHandleApproveWithoutApproverDenies() {
	policy := s.mustPolicy(types.DecisionAllow, types.CommandRule{
		Commands: []string{"git"},
		Decision: types.DecisionApprove,
	})
	h := NewExecveHandler(policy, nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "/usr/bin/git",
		Argv:     []string{"/usr/bin/git", "push"},
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
	s.Require().Equal("no-approver", out.Reason)
}

func (s *ExecveSuite) TestHandleUnknownDefaultDecisionFallsBackToAllow() {
	// Build a policy whose defaultDecision is the unknown "" (normalised
	// to Allow by CompilePolicy) and no command rules — exercising the
	// "match returned nothing" branch. The default-allow path should fire.
	policy := s.mustPolicy("")
	h := NewExecveHandler(policy, nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "/bin/bash",
		Argv:     []string{"/bin/bash"},
	})
	s.Require().Equal(types.DecisionAllow, out.Decision)
}

// --- audit emission ---

// stepClock returns a func() time.Time that advances 1ms per call.
func stepClock() func() time.Time {
	var n int
	return func() time.Time {
		n++
		return time.Unix(0, int64(n)*int64(time.Millisecond))
	}
}

func (s *ExecveSuite) TestHandleEmitsAuditEntryForRuleMatch() {
	policy := s.mustPolicy(types.DecisionAllow, types.CommandRule{
		Commands: []string{"rm"},
		Decision: types.DecisionDeny,
	})
	rec := &recordingAuditor{}
	h := NewExecveHandler(policy, nil)
	h.Auditor = rec
	h.Now = stepClock()

	out := h.Handle(context.Background(), ExecveRequest{
		PID:       9001,
		ChannelID: "c1",
		Filename:  "/bin/rm",
		Argv:      []string{"/bin/rm", "-rf", "/data"},
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)

	entries := rec.snapshot()
	s.Require().Len(entries, 1)
	e := entries[0]
	s.Require().Equal("execve", e.Kind)
	s.Require().Equal("/bin/rm -rf /data", e.Target)
	s.Require().Equal("cmd[0]", e.RuleID)
	s.Require().Equal("deny", e.Decision)
	s.Require().Equal("c1", e.Channel)
	s.Require().Equal(9001, e.PID, "requesting PID must be plumbed into audit")
	s.Require().Greater(e.Latency, time.Duration(0))
}

func (s *ExecveSuite) TestHandleEmitsAuditEntryForMemfdDeny() {
	rec := &recordingAuditor{}
	h := NewExecveHandler(s.mustPolicy(types.DecisionAllow), nil)
	h.Auditor = rec
	h.Now = stepClock()

	out := h.Handle(context.Background(), ExecveRequest{
		PID:       9002,
		ChannelID: "c2",
		Filename:  "memfd:shellcode",
		Argv:      []string{"memfd:shellcode"},
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)

	entries := rec.snapshot()
	s.Require().Len(entries, 1)
	e := entries[0]
	s.Require().Equal("execve", e.Kind)
	s.Require().Equal("memfd:shellcode", e.Target)
	s.Require().Equal("memfd-execveat-deny", e.RuleID)
	s.Require().Equal("deny", e.Decision)
	s.Require().Equal("c2", e.Channel)
	s.Require().Equal(9002, e.PID, "requesting PID must be plumbed into audit")
}

func (s *ExecveSuite) TestHandleUnwrapsBeforeMatch() {
	// `env FOO=bar rm -rf /` should match the `rm` rule after unwrap.
	policy := s.mustPolicy(types.DecisionAllow, types.CommandRule{
		Commands:     []string{"rm"},
		ArgsPatterns: []string{`^-rf `},
		Decision:     types.DecisionDeny,
	})
	h := NewExecveHandler(policy, nil)
	out := h.Handle(context.Background(), ExecveRequest{
		Filename: "/usr/bin/env",
		Argv:     []string{"/usr/bin/env", "FOO=bar", "/bin/rm", "-rf", "/data"},
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
}
