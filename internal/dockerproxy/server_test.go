package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

type ServerSuite struct {
	suite.Suite
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

// --- helpers ---

// fakeApprover captures Request calls and returns a pre-seeded Outcome.
type fakeApprover struct {
	mu      sync.Mutex
	calls   []agentgate.ApprovalRequest
	outcome agentgate.Outcome
}

func (f *fakeApprover) Request(_ context.Context, _ string, req agentgate.ApprovalRequest) agentgate.Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.outcome
}

type capturingAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (c *capturingAuditor) WriteAudit(e AuditEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *capturingAuditor) snapshot() []AuditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]AuditEntry(nil), c.entries...)
}

// upstreamUnix starts an HTTP upstream listening on a freshly-created unix
// socket, and returns its path plus a shutdown func.
func upstreamUnix(t *testing.T, h http.Handler) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return sock, func() {
		_ = srv.Shutdown(context.Background())
		_ = ln.Close()
		_ = os.Remove(sock)
	}
}

func (s *ServerSuite) newServer(policy *Policy, approver Approver, upstreamSock string, auditor Auditor) *Server {
	srv, err := NewServer(ServerConfig{
		CID:        "cid-1",
		ChannelID:  "ch-1",
		Policy:     policy,
		Approver:   approver,
		DockerSock: upstreamSock,
		Auditor:    auditor,
		Now:        time.Now,
	})
	require.NoError(s.T(), err)
	return srv
}

// --- NewServer validation ---

func (s *ServerSuite) TestNewServerRejectsEmptyFields() {
	_, err := NewServer(ServerConfig{})
	require.Error(s.T(), err)

	_, err = NewServer(ServerConfig{CID: "x"})
	require.Error(s.T(), err)

	policy, _ := CompilePolicy(types.DecisionAllow, nil, nil)
	_, err = NewServer(ServerConfig{CID: "x", Policy: policy})
	require.Error(s.T(), err)

	_, err = NewServer(ServerConfig{CID: "x", Policy: policy, Approver: &fakeApprover{}})
	require.Error(s.T(), err)
}

func (s *ServerSuite) TestNewServerDefaultsNow() {
	policy, _ := CompilePolicy(types.DecisionAllow, nil, nil)
	srv, err := NewServer(ServerConfig{
		CID:        "x",
		Policy:     policy,
		Approver:   &fakeApprover{},
		DockerSock: "/tmp/whatever",
	})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), srv.cfg.Now)
}

// --- HTTP rule decisions ---

func (s *ServerSuite) TestAllowForwardsToUpstream() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Forwarded path keeps the /vN.M prefix — the docker daemon accepts it.
		require.Equal(s.T(), "/v1.41/containers/json", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"Id":"abc"}]`))
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionApprove,
		[]types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/containers/json$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, &fakeApprover{}, sock, auditor)

	req := httptest.NewRequest(http.MethodGet, "/v1.41/containers/json", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusOK, rr.Code)
	require.Contains(s.T(), rr.Body.String(), "abc")
	entries := auditor.snapshot()
	require.Len(s.T(), entries, 1)
	require.Equal(s.T(), "allow", entries[0].Decision)
	require.Equal(s.T(), "/containers/json", entries[0].Path)
}

func (s *ServerSuite) TestDenyReturns403WithoutContactingUpstream() {
	upstreamHit := false
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/swarm/init$"}, Decision: types.DecisionDeny, Message: "no swarm"},
		}, nil)
	require.NoError(s.T(), err)
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, &fakeApprover{}, sock, auditor)

	req := httptest.NewRequest(http.MethodPost, "/swarm/init", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.Contains(s.T(), rr.Body.String(), "no swarm")
	require.False(s.T(), upstreamHit, "upstream should not be contacted on deny")
	require.Equal(s.T(), "deny", auditor.snapshot()[0].Decision)
}

func (s *ServerSuite) TestDenyWithoutMessageUsesDefault() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, sock, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.Contains(s.T(), rr.Body.String(), "denied by policy")
}

func (s *ServerSuite) TestApproveCallsApproverAndForwards() {
	forwarded := false
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"DELETE"}, Paths: []string{"^/containers/[^/]+$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow, Actor: "user-42"}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	req := httptest.NewRequest(http.MethodDelete, "/containers/abc123def456", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusNoContent, rr.Code)
	require.True(s.T(), forwarded)
	require.Len(s.T(), ap.calls, 1)
	require.Equal(s.T(), "docker:DELETE:/containers/*", ap.calls[0].CacheKey)
	require.Equal(s.T(), "DELETE /containers/abc123def456", ap.calls[0].Target)
	require.Equal(s.T(), "approve", auditor.snapshot()[0].Decision)
	require.Equal(s.T(), "user-42", auditor.snapshot()[0].Actor)
}

func (s *ServerSuite) TestApproveCacheHitReportedInAudit() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow, FromCache: true}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	require.Equal(s.T(), http.StatusOK, rr.Code)
	require.Equal(s.T(), "cache-hit", auditor.snapshot()[0].Decision)
}

func (s *ServerSuite) TestApproveDeniedBlocksForward() {
	upstreamHit := false
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionDeny, Reason: "user-denied"}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.False(s.T(), upstreamHit)
	require.Equal(s.T(), "user-denied", auditor.snapshot()[0].Reason)
}

func (s *ServerSuite) TestApproveRateLimitedDecisionInAudit() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionDeny, RateLimited: true, Reason: "rate-limit-pending"}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))
	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.Equal(s.T(), "rate-limit", auditor.snapshot()[0].Decision)
}

// --- Body rules ---

func (s *ServerSuite) TestBodyRuleDenyBlocksForward() {
	upstreamHit := false
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionApprove},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 8192,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionDeny,
			Message:  "no privileged",
		}})
	require.NoError(s.T(), err)
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, &fakeApprover{}, sock, auditor)

	body := bytes.NewBufferString(`{"HostConfig":{"Privileged":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/containers/create", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.Contains(s.T(), rr.Body.String(), "no privileged")
	require.False(s.T(), upstreamHit)
	e := auditor.snapshot()[0]
	require.Equal(s.T(), "deny", e.Decision)
	require.Equal(s.T(), "body[0]", e.BodyRuleID)
}

func (s *ServerSuite) TestBodyRuleBenignBodyFallsThrough() {
	var receivedBody []byte
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 8192,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, sock, nil)

	original := `{"Image":"ubuntu","HostConfig":{"Privileged":false}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(original))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusCreated, rr.Code)
	require.Equal(s.T(), original, string(receivedBody), "body must be forwarded byte-for-byte")
}

func (s *ServerSuite) TestBodyRuleOversizedBodyFallsThroughAndForwardsFullBody() {
	var receivedLen int
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedLen = len(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 32, // tiny cap so we force the "too large" branch
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, sock, nil)

	big := `{"Image":"ubuntu","HostConfig":{"Privileged":true,"Filler":"` + strings.Repeat("X", 200) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusCreated, rr.Code)
	require.Equal(s.T(), len(big), receivedLen, "full body must still reach upstream even when skipped")
}

func (s *ServerSuite) TestBodyRuleInvalidJSONReturns400() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1024,
			JSONChecks:   []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, &fakeApprover{}, sock, auditor)

	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusBadRequest, rr.Code)
	require.Equal(s.T(), "body-eval-error", auditor.snapshot()[0].RuleID)
}

// errReader always returns an I/O error. Used to exercise evaluateBody's
// io.ReadAll failure branch.
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }
func (e *errReader) Close() error               { return nil }

// When the request body errors during read, ServeHTTP must respond 400 (same
// handling as an unparseable body) and audit a body-eval-error entry.
func (s *ServerSuite) TestBodyRuleReadErrorReturns400() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1024,
			JSONChecks:   []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, &fakeApprover{}, sock, auditor)

	req := httptest.NewRequest(http.MethodPost, "/containers/create", nil)
	req.Body = &errReader{err: errors.New("boom")}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusBadRequest, rr.Code)
	snap := auditor.snapshot()
	require.Len(s.T(), snap, 1)
	require.Equal(s.T(), "body-eval-error", snap[0].RuleID)
	require.Contains(s.T(), snap[0].Reason, "boom")
}

func (s *ServerSuite) TestBodyRuleNonJSONSkipped() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/build$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/build$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1024,
			JSONChecks:   []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, sock, nil)

	req := httptest.NewRequest(http.MethodPost, "/build", strings.NewReader(`raw-tar-data`))
	req.Header.Set("Content-Type", "application/x-tar")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusOK, rr.Code)
}

func (s *ServerSuite) TestBodyRuleApprovePromptsAndForwards() {
	forwarded := false
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()

	// HTTP rule allow + body rule approve. With the fix, the body rule's
	// approve overrides the HTTP rule's allow and prompts the user.
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 8192,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Binds[*]", Op: "source_path_in", Values: []string{`^/var/run/docker\.sock$`}},
			},
			Decision: types.DecisionApprove,
			Message:  "container with docker.sock bind",
		}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow, Actor: "user-7"}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	body := `{"Image":"alpine","HostConfig":{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusCreated, rr.Code)
	require.True(s.T(), forwarded)
	require.Len(s.T(), ap.calls, 1)
	require.Equal(s.T(), "docker-body", ap.calls[0].Kind)
	require.Equal(s.T(), "docker:POST:body:body[0]", ap.calls[0].CacheKey)
	require.Equal(s.T(), "container with docker.sock bind", ap.calls[0].Message)
	require.Equal(s.T(), "alpine", ap.calls[0].Details["image"])
	e := auditor.snapshot()[0]
	require.Equal(s.T(), "approve", e.Decision)
	require.Equal(s.T(), "body[0]", e.BodyRuleID)
	require.Equal(s.T(), "user-7", e.Actor)
}

func (s *ServerSuite) TestBodyRuleApproveDeniedBlocksForward() {
	upstreamHit := false
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 8192,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionApprove,
		}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionDeny, Reason: "user-said-no"}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	body := `{"HostConfig":{"Privileged":true}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.False(s.T(), upstreamHit)
	e := auditor.snapshot()[0]
	require.Equal(s.T(), "approve", e.Decision)
	require.Equal(s.T(), "body[0]", e.BodyRuleID)
	require.Equal(s.T(), "user-said-no", e.Reason)
}

func (s *ServerSuite) TestBodyRuleApproveCacheHit() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 8192,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionApprove,
		}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow, FromCache: true}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	body := `{"HostConfig":{"Privileged":true}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusCreated, rr.Code)
	require.Equal(s.T(), "cache-hit", auditor.snapshot()[0].Decision)
}

func (s *ServerSuite) TestBodyRuleApproveRateLimited() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionAllow},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 8192,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionApprove,
		}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionDeny, RateLimited: true}}
	auditor := &capturingAuditor{}
	srv := s.newServer(policy, ap, sock, auditor)

	body := `{"HostConfig":{"Privileged":true}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.Equal(s.T(), "rate-limit", auditor.snapshot()[0].Decision)
}

func (s *ServerSuite) TestBodyRuleNoRulesNoBodyRead() {
	// Hand the request a body whose Read would panic — confirm we never read it
	// when no body rule applies.
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionAllow},
		}, nil) // no body rules
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, sock, nil)

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"hi":1}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(s.T(), http.StatusOK, rr.Code)
}

// --- Path stripping / cache key ---

func (s *ServerSuite) TestStripAPIVersionPrefix() {
	cases := []struct{ in, want string }{
		{"/v1.41/containers/json", "/containers/json"},
		{"/v23.0/info", "/info"},
		{"/v1/info", "/info"},
		{"/containers/json", "/containers/json"},
		{"/v1.41", "/"},
		{"/vX.Y/info", "/vX.Y/info"}, // non-numeric -> not stripped
	}
	for _, c := range cases {
		require.Equal(s.T(), c.want, stripAPIVersionPrefix(c.in), c.in)
	}
}

func (s *ServerSuite) TestNormalizeCachePath() {
	require.Equal(s.T(), "/containers/*/exec", normalizeCachePath("/containers/abc123def456/exec"))
	require.Equal(s.T(), "/containers/*", normalizeCachePath("/containers/abc123def456"))
	require.Equal(s.T(), "/exec/*/start", normalizeCachePath("/exec/fedcba987654/start"))
	require.Equal(s.T(), "/containers/json", normalizeCachePath("/containers/json"))
}

// --- Upstream unreachable ---

func (s *ServerSuite) TestUpstreamUnreachableReturns502() {
	// A socket that was never bound.
	missing := filepath.Join(s.T().TempDir(), "nothing.sock")
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/containers/json$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, missing, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/containers/json", nil))
	require.Equal(s.T(), http.StatusBadGateway, rr.Code)
}

// --- Auditor nil safety ---

func (s *ServerSuite) TestAuditorNilSafe() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/x$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	srv := s.newServer(policy, &fakeApprover{}, sock, nil) // nil auditor

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(s.T(), http.StatusOK, rr.Code)
}

// --- Request body still readable after body-rule pass ---

func (s *ServerSuite) TestApproveWithBodyForwardsBodyUnchanged() {
	var received json.RawMessage
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil && !errors.Is(err, io.EOF) {
			panic(err)
		}
		received = b
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionApprove},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 4096,
			JSONChecks:   []types.JSONCheck{{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow, Actor: "u"}}
	srv := s.newServer(policy, ap, sock, nil)

	orig := `{"Image":"ubuntu","HostConfig":{"Privileged":false}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(orig))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusCreated, rr.Code)
	require.Equal(s.T(), orig, string(received))
}

// TestApproveContainerCreateAttachesDetails verifies the approve path passes
// a Details map with image / privileged / network_mode extracted from the
// `POST /containers/create` body, so the prompt UI can render them.
func (s *ServerSuite) TestApproveContainerCreateAttachesDetails() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/create$"}, Decision: types.DecisionApprove},
		},
		[]types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 4096,
			JSONChecks:   []types.JSONCheck{{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"never-fires"}}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
	srv := s.newServer(policy, ap, sock, nil)

	body := `{"Image":"alpine:3.20","HostConfig":{"NetworkMode":"host","Privileged":true,"Binds":["/etc:/host-etc:ro"]}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(s.T(), http.StatusCreated, rr.Code)

	require.Len(s.T(), ap.calls, 1)
	d := ap.calls[0].Details
	require.NotNil(s.T(), d)
	require.Equal(s.T(), "alpine:3.20", d["image"])
	require.Equal(s.T(), "host", d["network_mode"])
	require.Equal(s.T(), "true", d["privileged"])
	require.Contains(s.T(), d["binds"], "/etc:/host-etc:ro")
}

// TestApproveWithoutBodyHasNilDetails covers the cache-key-only path: when
// no body rule applies (cap == 0) the approver receives no Details.
func (s *ServerSuite) TestApproveWithoutBodyHasNilDetails() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"DELETE"}, Paths: []string{"^/containers/[^/]+$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
	srv := s.newServer(policy, ap, sock, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/containers/abc123def456", nil))
	require.Equal(s.T(), http.StatusNoContent, rr.Code)
	require.Len(s.T(), ap.calls, 1)
	require.Nil(s.T(), ap.calls[0].Details)
}

func (s *ServerSuite) TestIsHijackingClassification() {
	require.True(s.T(), isHijacking(http.MethodPost, "/containers/abc123/attach"))
	require.True(s.T(), isHijacking(http.MethodPost, "/exec/xyz789/start"))
	require.False(s.T(), isHijacking(http.MethodGet, "/containers/abc123/attach"))
	require.False(s.T(), isHijacking(http.MethodPost, "/containers/abc123/start"))
}
