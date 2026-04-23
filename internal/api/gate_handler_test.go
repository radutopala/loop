package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/types"
)

// gateResolverCall captures a single Resolve invocation for assertion.
type gateResolverCall struct {
	reqID    string
	decision string
	actor    string
}

type fakeGateResolver struct {
	mu    sync.Mutex
	calls []gateResolverCall
	err   error
}

func (f *fakeGateResolver) Resolve(reqID, decision, actorID string) error {
	f.mu.Lock()
	f.calls = append(f.calls, gateResolverCall{reqID: reqID, decision: decision, actor: actorID})
	f.mu.Unlock()
	return f.err
}

func (f *fakeGateResolver) snapshot() []gateResolverCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gateResolverCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (s *ServerSuite) TestGateApprovalResolverNotConfigured() {
	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/req-1", `{"decision":"once"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestGateApprovalMissingDecision() {
	r := &fakeGateResolver{}
	s.srv.SetApprovalResolver(r)

	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/req-1", `{}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Empty(s.T(), r.snapshot())
}

func (s *ServerSuite) TestGateApprovalInvalidJSON() {
	r := &fakeGateResolver{}
	s.srv.SetApprovalResolver(r)

	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/req-1", `not-json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Empty(s.T(), r.snapshot())
}

func (s *ServerSuite) TestGateApprovalOKWithActor() {
	r := &fakeGateResolver{}
	s.srv.SetApprovalResolver(r)

	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/req-42",
		`{"decision":"session","author_id":"user-7"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	calls := r.snapshot()
	require.Len(s.T(), calls, 1)
	require.Equal(s.T(), "req-42", calls[0].reqID)
	require.Equal(s.T(), "session", calls[0].decision)
	require.Equal(s.T(), "user-7", calls[0].actor)
}

func (s *ServerSuite) TestGateApprovalOKDefaultActor() {
	r := &fakeGateResolver{}
	s.srv.SetApprovalResolver(r)

	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/req-8",
		`{"decision":"deny"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	calls := r.snapshot()
	require.Len(s.T(), calls, 1)
	require.Equal(s.T(), local.DefaultAuthorID, calls[0].actor)
}

func (s *ServerSuite) TestGateApprovalUnknownRequestReturns404() {
	r := &fakeGateResolver{err: agentgate.ErrNoSuchRequest}
	s.srv.SetApprovalResolver(r)

	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/ghost",
		`{"decision":"once"}`)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestGateApprovalOtherErrorReturns400() {
	r := &fakeGateResolver{err: errors.New("invalid decision")}
	s.srv.SetApprovalResolver(r)

	rec := s.testRequest(http.MethodPost, "/api/gate/approvals/req-1",
		`{"decision":"bogus"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// TestGateHandlerRejectsEmptyReqID confirms that a bare POST with no id
// segment returns 400. The mux wouldn't route "/api/gate/approvals/" to our
// handler, so we invoke it directly to cover the explicit-empty branch.
func TestGateHandlerRejectsEmptyReqID(t *testing.T) {
	srv := &Server{approvalResolver: &fakeGateResolver{}}
	req, err := http.NewRequest(http.MethodPost, "/api/gate/approvals/", nil)
	require.NoError(t, err)
	rec := newRecorder()
	srv.handleResolveGateApproval(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ----------------------------------------------------------------------------
// POST /api/gate/container-approval
// ----------------------------------------------------------------------------

type byTokenCall struct {
	token     string
	channelID string
	req       agentgate.ApprovalRequest
}

type fakeContainerRouter struct {
	mu          sync.Mutex
	calls       []byTokenCall
	mgr         ContainerApprovalManager
	channelID   string
	containerID string
	knownToken  string
}

func (f *fakeContainerRouter) ByToken(token string) (string, ContainerApprovalManager, string, bool) {
	f.mu.Lock()
	f.calls = append(f.calls, byTokenCall{token: token})
	f.mu.Unlock()
	if token != f.knownToken {
		return "", nil, "", false
	}
	return f.containerID, f.mgr, f.channelID, true
}

type fakeContainerManager struct {
	mu      sync.Mutex
	calls   []byTokenCall
	outcome agentgate.Outcome
	delay   time.Duration
}

func (m *fakeContainerManager) Request(ctx context.Context, channelID string, req agentgate.ApprovalRequest) agentgate.Outcome {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return agentgate.Outcome{Decision: types.DecisionDeny, Reason: "cancelled"}
		}
	}
	m.mu.Lock()
	m.calls = append(m.calls, byTokenCall{channelID: channelID, req: req})
	m.mu.Unlock()
	return m.outcome
}

func (m *fakeContainerManager) snapshot() []byTokenCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]byTokenCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (s *ServerSuite) TestContainerApprovalNotConfigured() {
	rec := s.testRequest(http.MethodPost, "/api/gate/container-approval", `{}`)
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestContainerApprovalMissingBearer() {
	s.srv.SetContainerApprovalRouter(&fakeContainerRouter{})
	req := httptest.NewRequest(http.MethodPost, "/api/gate/container-approval", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusUnauthorized, rec.Code)
}

func (s *ServerSuite) TestContainerApprovalWrongBearer() {
	r := &fakeContainerRouter{knownToken: "real-tok"}
	s.srv.SetContainerApprovalRouter(r)
	req := httptest.NewRequest(http.MethodPost, "/api/gate/container-approval", nil)
	req.Header.Set("Authorization", "Bearer wrong-tok")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusUnauthorized, rec.Code)
}

func (s *ServerSuite) TestContainerApprovalMalformedAuthHeader() {
	s.srv.SetContainerApprovalRouter(&fakeContainerRouter{knownToken: "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/gate/container-approval", nil)
	req.Header.Set("Authorization", "Basic abc") // not Bearer
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusUnauthorized, rec.Code)
}

func (s *ServerSuite) TestContainerApprovalHappyAllow() {
	mgr := &fakeContainerManager{outcome: agentgate.Outcome{
		Decision: types.DecisionAllow, Actor: "u-3", Reason: "",
	}}
	r := &fakeContainerRouter{
		knownToken: "tok-1", mgr: mgr, channelID: "ch-42", containerID: "cid-1",
	}
	s.srv.SetContainerApprovalRouter(r)

	body := `{"kind":"docker-http","target":"GET /containers/json","message":"list?","cache_key":"docker:GET:/containers/json","details":{"image":"alpine","privileged":"true"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/gate/container-approval", bytesReader(body))
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp containerApprovalResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "allow", resp.Decision)
	require.Equal(s.T(), "u-3", resp.Actor)

	calls := mgr.snapshot()
	require.Len(s.T(), calls, 1)
	require.Equal(s.T(), "ch-42", calls[0].channelID)
	require.Equal(s.T(), "docker-http", calls[0].req.Kind)
	require.Equal(s.T(), "GET /containers/json", calls[0].req.Target)
	require.Equal(s.T(), map[string]string{"image": "alpine", "privileged": "true"}, calls[0].req.Details)
}

func (s *ServerSuite) TestContainerApprovalHappyDeny() {
	mgr := &fakeContainerManager{outcome: agentgate.Outcome{
		Decision: types.DecisionDeny, Actor: "u-9", Reason: "user-denied",
	}}
	r := &fakeContainerRouter{
		knownToken: "tok-1", mgr: mgr, channelID: "ch-42", containerID: "cid-1",
	}
	s.srv.SetContainerApprovalRouter(r)

	req := httptest.NewRequest(http.MethodPost, "/api/gate/container-approval", bytesReader(`{"kind":"x","target":"t"}`))
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp containerApprovalResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "deny", resp.Decision)
	require.Equal(s.T(), "user-denied", resp.Reason)
}

func (s *ServerSuite) TestContainerApprovalMalformedJSON() {
	mgr := &fakeContainerManager{}
	r := &fakeContainerRouter{knownToken: "tok", mgr: mgr, channelID: "ch", containerID: "c"}
	s.srv.SetContainerApprovalRouter(r)

	req := httptest.NewRequest(http.MethodPost, "/api/gate/container-approval", bytesReader(`not-json`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Empty(s.T(), mgr.snapshot())
}

func (s *ServerSuite) TestSetContainerApprovalRouter() {
	srv := nilServer()
	require.Nil(s.T(), srv.containerApprovalRouter)
	r := &fakeContainerRouter{}
	srv.SetContainerApprovalRouter(r)
	require.Same(s.T(), r, srv.containerApprovalRouter)
}

func bytesReader(s string) *bytes.Reader {
	return bytes.NewReader([]byte(s))
}
