package agentgate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type ConnectSuite struct {
	suite.Suite
}

func TestConnectSuite(t *testing.T) {
	suite.Run(t, new(ConnectSuite))
}

// stubApprover — minimal Approver for connect tests. Mirrors ExecveSuite's
// fake.
type stubApprover struct {
	got ApprovalRequest
	out Outcome
	err error
}

func (a *stubApprover) Request(_ context.Context, _ string, req ApprovalRequest) Outcome {
	a.got = req
	if a.err != nil {
		return Outcome{Decision: types.DecisionDeny, Reason: a.err.Error()}
	}
	return a.out
}

func compileConnectPolicy(t *testing.T, rules []types.PathRule, def types.Decision) *Policy {
	t.Helper()
	p, err := CompilePolicy(def, rules, nil, nil)
	if err != nil {
		t.Fatalf("CompilePolicy: %v", err)
	}
	return p
}

// --- ConnectHandler ---

func (s *ConnectSuite) TestHandleAllowRule() {
	policy := compileConnectPolicy(s.T(),
		[]types.PathRule{{Pattern: "/var/run/loop.sock", Decision: types.DecisionAllow}},
		types.DecisionDeny,
	)
	h := NewConnectHandler(policy, nil)
	got := h.Handle(context.Background(), ConnectRequest{Path: "/var/run/loop.sock"})
	s.Require().Equal(types.DecisionAllow, got.Decision)
	s.Require().Equal("path[0]", got.Reason)
}

func (s *ConnectSuite) TestHandleDenyRule() {
	policy := compileConnectPolicy(s.T(),
		[]types.PathRule{{Pattern: "/etc/evil.sock", Decision: types.DecisionDeny, Message: "no"}},
		types.DecisionAllow,
	)
	h := NewConnectHandler(policy, nil)
	got := h.Handle(context.Background(), ConnectRequest{Path: "/etc/evil.sock"})
	s.Require().Equal(types.DecisionDeny, got.Decision)
}

func (s *ConnectSuite) TestHandleApproveWithoutApproverDenies() {
	policy := compileConnectPolicy(s.T(),
		[]types.PathRule{{Pattern: "/var/run/docker.sock", Decision: types.DecisionApprove, Message: "docker"}},
		types.DecisionAllow,
	)
	h := NewConnectHandler(policy, nil)
	got := h.Handle(context.Background(), ConnectRequest{Path: "/var/run/docker.sock"})
	s.Require().Equal(types.DecisionDeny, got.Decision)
	s.Require().Equal("no-approver", got.Reason)
}

func (s *ConnectSuite) TestHandleApproveDispatchesToApprover() {
	policy := compileConnectPolicy(s.T(),
		[]types.PathRule{{Pattern: "/var/run/docker.sock", Decision: types.DecisionApprove, Message: "docker"}},
		types.DecisionAllow,
	)
	ap := &stubApprover{out: Outcome{Decision: types.DecisionAllow}}
	h := NewConnectHandler(policy, ap)
	got := h.Handle(context.Background(), ConnectRequest{ChannelID: "c1", Path: "/var/run/docker.sock"})
	s.Require().Equal(types.DecisionAllow, got.Decision)
	s.Require().Equal("connect", ap.got.Kind)
	s.Require().Equal("/var/run/docker.sock", ap.got.Target)
	s.Require().Equal("docker", ap.got.Message)
	s.Require().Equal("connect:/var/run/docker.sock", ap.got.CacheKey)
}

func (s *ConnectSuite) TestHandleDefaultDecision() {
	policy := compileConnectPolicy(s.T(), nil, types.DecisionAllow)
	h := NewConnectHandler(policy, nil)
	got := h.Handle(context.Background(), ConnectRequest{Path: "/tmp/unknown.sock"})
	s.Require().Equal(types.DecisionAllow, got.Decision)
	s.Require().Equal("default", got.Reason)
}

// --- audit emission ---

func (s *ConnectSuite) TestHandleEmitsAuditEntry() {
	policy := compileConnectPolicy(s.T(),
		[]types.PathRule{{Pattern: "/var/run/docker.sock", Decision: types.DecisionApprove, Message: "docker"}},
		types.DecisionAllow,
	)
	rec := &recordingAuditor{}
	ap := &stubApprover{out: Outcome{Decision: types.DecisionAllow, Actor: "alice"}}
	h := NewConnectHandler(policy, ap)
	h.Auditor = rec
	// Deterministic clock: each call advances 1ms so Latency is observable.
	var calls int
	h.Now = func() time.Time {
		calls++
		return time.Unix(0, int64(calls)*int64(time.Millisecond))
	}

	got := h.Handle(context.Background(), ConnectRequest{PID: 777, ChannelID: "c1", Path: "/var/run/docker.sock"})
	s.Require().Equal(types.DecisionAllow, got.Decision)

	entries := rec.snapshot()
	s.Require().Len(entries, 1)
	e := entries[0]
	s.Require().Equal("connect", e.Kind)
	s.Require().Equal("/var/run/docker.sock", e.Target)
	s.Require().Equal("path[0]", e.RuleID)
	s.Require().Equal("allow", e.Decision)
	s.Require().Equal("c1", e.Channel)
	s.Require().Equal(777, e.PID, "requesting PID must be plumbed into audit")
	s.Require().Equal("alice", e.PromptedWho)
	s.Require().Greater(e.Latency, time.Duration(0))
	s.Require().False(e.Ts.IsZero())
}

// --- ParseUnixSockaddr ---

func (s *ConnectSuite) TestParseUnixSockaddrRejectsShortBuffer() {
	_, ok := ParseUnixSockaddr([]byte{1})
	s.Require().False(ok)
}

func (s *ConnectSuite) TestParseUnixSockaddrRejectsNonUnix() {
	// AF_INET (2) in little-endian.
	_, ok := ParseUnixSockaddr([]byte{2, 0, 0, 0, 0, 0, 0, 0})
	s.Require().False(ok)
}

func (s *ConnectSuite) TestParseUnixSockaddrEmptyPathIsUnix() {
	buf := []byte{1, 0}
	path, ok := ParseUnixSockaddr(buf)
	s.Require().True(ok)
	s.Require().Equal("", path)
}

func (s *ConnectSuite) TestParseUnixSockaddrPathname() {
	buf := append([]byte{1, 0}, []byte("/var/run/docker.sock")...)
	buf = append(buf, make([]byte, 30)...) // NUL padding
	path, ok := ParseUnixSockaddr(buf)
	s.Require().True(ok)
	s.Require().Equal("/var/run/docker.sock", path)
}

func (s *ConnectSuite) TestParseUnixSockaddrAbstract() {
	// Family=1 (LE), first byte of path=0, then name "myapp".
	buf := append([]byte{1, 0, 0}, []byte("myapp")...)
	path, ok := ParseUnixSockaddr(buf)
	s.Require().True(ok)
	s.Require().Equal("@myapp", path)
}

func (s *ConnectSuite) TestParseUnixSockaddrInteriorNul() {
	// Pathological case: a path containing an embedded NUL. We trim at the
	// first NUL (Unix filesystem semantics).
	buf := append([]byte{1, 0}, []byte("/a/b\x00ignored")...)
	path, ok := ParseUnixSockaddr(buf)
	s.Require().True(ok)
	s.Require().Equal("/a/b", path)
}

// Confirm the sentinel error from a badly wired approver is surfaced via
// Outcome.Reason (exercises the plumbing, not the parser).
func (s *ConnectSuite) TestHandleApproveSurfacesApproverErrorReason() {
	policy := compileConnectPolicy(s.T(),
		[]types.PathRule{{Pattern: "/x.sock", Decision: types.DecisionApprove}},
		types.DecisionAllow,
	)
	ap := &stubApprover{err: errors.New("wire-fault")}
	h := NewConnectHandler(policy, ap)
	got := h.Handle(context.Background(), ConnectRequest{Path: "/x.sock"})
	s.Require().Equal(types.DecisionDeny, got.Decision)
	s.Require().Equal("wire-fault", got.Reason)
}
