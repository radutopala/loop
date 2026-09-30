package apiauth

import (
	"net/http"
	"os"
	"strings"
)

// AgentTokenPath is where the daemon puts an agent container's token,
// readable only by the agent user. It's a file rather than an env var so
// it doesn't show in `docker inspect`.
const AgentTokenPath = "/run/loop/api-token"

// ClientTokenSource finds the token a client of the API sends.
type ClientTokenSource struct {
	readFile       func(string) ([]byte, error)
	userConfigDir  func() (string, error)
	agentTokenPath string
}

// NewClientTokenSource returns a ClientTokenSource for this machine.
func NewClientTokenSource() *ClientTokenSource {
	return &ClientTokenSource{readFile: os.ReadFile, userConfigDir: os.UserConfigDir, agentTokenPath: AgentTokenPath}
}

// Token returns the agent token inside an agent container, else the owner
// token on the host, else "" (the request then fails with a 401 that says
// what's missing).
func (s *ClientTokenSource) Token() string {
	if b, err := s.readFile(s.agentTokenPath); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok
		}
	}
	path, err := OwnerTokenPath(s.userConfigDir)
	if err != nil {
		return ""
	}
	f := NewTokenFile(path)
	f.readFile = s.readFile
	tok, _ := f.Load()
	return tok
}

// Transport adds the API token to each request. The token is read per
// request, so a rotated owner token is picked up without a restart.
type Transport struct {
	// Base sends the request; nil means http.DefaultTransport.
	Base http.RoundTripper
	// Token returns the token to send, or "" to send none.
	Token func() string
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if h := AuthHeader(t.Token()); h != nil {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", h.Get("Authorization"))
	}
	return base.RoundTrip(r)
}

// AuthHeader returns the header that sends tok, for WebSocket dials, or nil
// when tok is empty.
func AuthHeader(tok string) http.Header {
	if tok == "" {
		return nil
	}
	return http.Header{"Authorization": {"Bearer " + tok}}
}

// NewHTTPClient returns a client that sends this machine's API token.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: &Transport{Token: NewClientTokenSource().Token}}
}
