package apiauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type fakeAgents map[string]Principal

func (f fakeAgents) Lookup(tok string) (Principal, bool) {
	p, ok := f[tok]
	return p, ok
}

type MiddlewareSuite struct {
	suite.Suite
	auth *Authenticator
	seen *Principal
}

func TestMiddlewareSuite(t *testing.T) {
	suite.Run(t, new(MiddlewareSuite))
}

func (s *MiddlewareSuite) SetupTest() {
	agents := fakeAgents{"agent-tok": {Kind: KindAgent, ContainerID: "c1", ChannelID: "ch1"}}
	s.auth = NewAuthenticator("owner-tok", agents,
		NewRouteSet("POST /api/messages", "GET /api/channels/{id}/queued"),
		NewRouteSet("GET /api/health", "GET /c/{cap}/{path...}"))
	s.seen = nil
}

func (s *MiddlewareSuite) serve(method, path string, hdr map[string]string) int {
	h := s.auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); ok {
			s.seen = &p
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func (s *MiddlewareSuite) TestDecisions() {
	bearer := func(t string) map[string]string { return map[string]string{"Authorization": "Bearer " + t} }
	tests := []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		want   int
		who    Kind
	}{
		{"health is public", "GET", "/api/health", nil, 200, ""},
		{"content is public", "GET", "/c/cap/x/y.png", nil, 200, ""},
		{"preflight", "OPTIONS", "/api/config", nil, 200, ""},
		{"no token", "GET", "/api/config", nil, 401, ""},
		{"not bearer", "GET", "/api/config", map[string]string{"Authorization": "Basic x"}, 401, ""},
		{"bad token", "GET", "/api/config", bearer("nope"), 401, ""},
		{"owner", "PUT", "/api/config", bearer("owner-tok"), 200, KindOwner},
		{"owner via ws protocol", "GET", "/api/ws/terminal", map[string]string{"Sec-WebSocket-Protocol": "loop, loop.token.owner-tok"}, 200, KindOwner},
		{"agent allowed route", "POST", "/api/messages", bearer("agent-tok"), 200, KindAgent},
		{"agent pattern route", "GET", "/api/channels/ch9/queued", bearer("agent-tok"), 200, KindAgent},
		{"agent wrong method", "GET", "/api/messages", bearer("agent-tok"), 403, ""},
		{"agent owner route", "PUT", "/api/config", bearer("agent-tok"), 403, ""},
		{"agent terminal", "GET", "/api/ws/terminal", map[string]string{"Sec-WebSocket-Protocol": "loop.token.agent-tok"}, 403, ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.seen = nil
			require.Equal(s.T(), tc.want, s.serve(tc.method, tc.path, tc.hdr))
			if tc.who == "" {
				require.Nil(s.T(), s.seen)
				return
			}
			require.NotNil(s.T(), s.seen)
			require.Equal(s.T(), tc.who, s.seen.Kind)
		})
	}
}

func (s *MiddlewareSuite) TestRotateOwner() {
	s.auth.SetOwnerToken("new")
	require.Equal(s.T(), 401, s.serve("GET", "/api/config", map[string]string{"Authorization": "Bearer owner-tok"}))
	require.Equal(s.T(), 200, s.serve("GET", "/api/config", map[string]string{"Authorization": "Bearer new"}))

	s.auth.SetOwnerToken("")
	require.Equal(s.T(), 401, s.serve("GET", "/api/config", map[string]string{"Authorization": "Bearer "}), "an unset owner token matches nothing")
}

func (s *MiddlewareSuite) TestNoAgentLookup() {
	s.auth = NewAuthenticator("owner-tok", nil, NewRouteSet("POST /api/messages"), NewRouteSet())
	require.Equal(s.T(), 401, s.serve("POST", "/api/messages", map[string]string{"Authorization": "Bearer agent-tok"}))
	require.Equal(s.T(), 200, s.serve("POST", "/api/messages", map[string]string{"Authorization": "Bearer owner-tok"}))
}

func (s *MiddlewareSuite) TestRequestToken() {
	req := httptest.NewRequest("GET", "/", nil)
	require.Empty(s.T(), RequestToken(req))
	req.Header.Add("Sec-WebSocket-Protocol", "loop")
	req.Header.Add("Sec-WebSocket-Protocol", "other, loop.token.t1")
	require.Equal(s.T(), "t1", RequestToken(req))
	req.Header.Set("Authorization", "Bearer  t2 ")
	require.Equal(s.T(), "t2", RequestToken(req))
}

func (s *MiddlewareSuite) TestIsAgent() {
	req := httptest.NewRequest("GET", "/", nil)
	require.False(s.T(), IsAgent(req.Context()))
	require.False(s.T(), IsAgent(WithPrincipal(req.Context(), Principal{Kind: KindOwner})))
	require.True(s.T(), IsAgent(WithPrincipal(req.Context(), Principal{Kind: KindAgent})))
}

func (s *MiddlewareSuite) TestClientToken() {
	dir := s.T().TempDir()
	agentPath := filepath.Join(dir, "agent-token")
	ownerDir := filepath.Join(dir, "cfg")
	src := &ClientTokenSource{readFile: os.ReadFile, userConfigDir: func() (string, error) { return ownerDir, nil }, agentTokenPath: agentPath}

	require.Empty(s.T(), src.Token(), "nothing to read")

	owner, err := NewTokenFile(filepath.Join(ownerDir, "loop", "api-token")).Rotate()
	require.NoError(s.T(), err)
	require.Equal(s.T(), owner, src.Token())

	require.NoError(s.T(), os.WriteFile(agentPath, []byte(" \n"), 0o600))
	require.Equal(s.T(), owner, src.Token(), "a blank agent token file is ignored")
	require.NoError(s.T(), os.WriteFile(agentPath, []byte("agent\n"), 0o600))
	require.Equal(s.T(), "agent", src.Token())

	src.agentTokenPath = filepath.Join(dir, "missing")
	src.userConfigDir = func() (string, error) { return "", errors.New("no home") }
	require.Empty(s.T(), src.Token())

	require.NotNil(s.T(), NewClientTokenSource())
}

func (s *MiddlewareSuite) TestTransport() {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	tok := "t1"
	client := &http.Client{Transport: &Transport{Token: func() string { return tok }}}
	for _, next := range []string{"t2", ""} {
		req, err := http.NewRequest("GET", srv.URL, nil)
		require.NoError(s.T(), err)
		resp, err := client.Do(req)
		require.NoError(s.T(), err)
		_ = resp.Body.Close()
		require.Empty(s.T(), req.Header.Get("Authorization"), "the caller's request is left alone")
		tok = next
	}
	req, err := http.NewRequest("GET", srv.URL, nil)
	require.NoError(s.T(), err)
	resp, err := (&http.Client{Transport: &Transport{Base: http.DefaultTransport, Token: func() string { return tok }}}).Do(req)
	require.NoError(s.T(), err)
	_ = resp.Body.Close()
	require.Equal(s.T(), []string{"Bearer t1", "Bearer t2", ""}, got)

	require.Nil(s.T(), AuthHeader(""))
	require.NotNil(s.T(), NewHTTPClient().Transport)
}
