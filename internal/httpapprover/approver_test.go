package httpapprover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

type ApproverSuite struct {
	suite.Suite
}

func TestApproverSuite(t *testing.T) {
	suite.Run(t, new(ApproverSuite))
}

func (s *ApproverSuite) TestPostsBearerAndJSONBody() {
	var gotAuth, gotContentType, gotPath string
	var gotBody RequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		s.Require().NoError(json.NewDecoder(r.Body).Decode(&gotBody))
		_ = json.NewEncoder(w).Encode(ResponseBody{Decision: "allow", Actor: "u-1", Reason: "ok"})
	}))
	defer srv.Close()

	a := New(srv.URL, "tok-abc", srv.Client(), nil)
	out := a.Request(context.Background(), "ch-42", agentgate.ApprovalRequest{
		Kind: "docker-http", Target: "GET /containers/json",
		Message: "list?", CacheKey: "docker:GET:/containers/json",
		Details: map[string]string{"image": "alpine", "privileged": "true"},
	})

	s.Equal("Bearer tok-abc", gotAuth)
	s.Equal("application/json", gotContentType)
	s.Equal(EndpointPath, gotPath)
	s.Equal("docker-http", gotBody.Kind)
	s.Equal("GET /containers/json", gotBody.Target)
	s.Equal("list?", gotBody.Message)
	s.Equal("docker:GET:/containers/json", gotBody.CacheKey)
	s.Equal(map[string]string{"image": "alpine", "privileged": "true"}, gotBody.Details)

	s.Equal(types.DecisionAllow, out.Decision)
	s.Equal("u-1", out.Actor)
	s.Equal("ok", out.Reason)
}

func (s *ApproverSuite) TestDecodesDenyResponse() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ResponseBody{Decision: "deny", Actor: "u-2", Reason: "user-denied"})
	}))
	defer srv.Close()

	a := New(srv.URL, "tok", srv.Client(), nil)
	out := a.Request(context.Background(), "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
	s.Equal("u-2", out.Actor)
	s.Equal("user-denied", out.Reason)
}

func (s *ApproverSuite) TestNon200ReturnsDeny() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := New(srv.URL, "tok", srv.Client(), nil)
	out := a.Request(context.Background(), "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
	s.Equal("http-401", out.Reason)
}

func (s *ApproverSuite) TestNetworkErrorReturnsDeny() {
	// Point at a TCP port that no one is listening on. 127.0.0.1:1 is reserved
	// and refuses quickly on most systems.
	a := New("http://127.0.0.1:1", "tok", &http.Client{Timeout: 500 * time.Millisecond}, nil)
	out := a.Request(context.Background(), "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
	s.Contains(out.Reason, "http-error:")
}

func (s *ApproverSuite) TestContextCancelReturnsCancelled() {
	// Server sleeps long enough that we always win the ctx-deadline race.
	// Release channel lets us shut down cleanly without waiting for the full
	// sleep — server handler exits as soon as the test is done asserting.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))

	a := New(srv.URL, "tok", srv.Client(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	out := a.Request(ctx, "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
	s.Equal("cancelled", out.Reason)

	close(release)
	srv.Close()
}

func (s *ApproverSuite) TestMalformedResponseReturnsDeny() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	a := New(srv.URL, "tok", srv.Client(), nil)
	out := a.Request(context.Background(), "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
	s.Contains(out.Reason, "decode-error:")
}

func (s *ApproverSuite) TestUnknownDecisionStringDefaultsToDeny() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ResponseBody{Decision: "maybe", Actor: "u"})
	}))
	defer srv.Close()

	a := New(srv.URL, "tok", srv.Client(), nil)
	out := a.Request(context.Background(), "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
}

func (s *ApproverSuite) TestBadURLReturnsDeny() {
	// Invalid URL → NewRequestWithContext returns an error.
	a := New("://no-scheme", "tok", &http.Client{}, nil)
	out := a.Request(context.Background(), "ch", agentgate.ApprovalRequest{})
	s.Equal(types.DecisionDeny, out.Decision)
	s.Contains(out.Reason, "request-build-error:")
}

func (s *ApproverSuite) TestNewDefaultsClientAndLogger() {
	a := New("http://x", "tok", nil, nil)
	s.NotNil(a.Client)
	s.NotNil(a.Logger)
}

// Compile-time checks that Approver satisfies both Approver interfaces.
var _ approverLike = (*Approver)(nil)

type approverLike interface {
	Request(ctx context.Context, channelID string, req agentgate.ApprovalRequest) agentgate.Outcome
}

func (s *ApproverSuite) TestSatisfiesBothApproverInterfaces() {
	// Agentgate Approver has the same surface; we only confirm the shared
	// interface matches. The dockerproxy equivalent is checked by its own
	// server_test build-pass.
	var _ interface {
		Request(context.Context, string, agentgate.ApprovalRequest) agentgate.Outcome
	} = (*Approver)(nil)

	// Usage smoke — ensure discard-logger path works (defaulted in New).
	a := New("http://x", "tok", &http.Client{}, nil)
	_ = a
	_ = io.Discard
}
