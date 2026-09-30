package apiauth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
)

// WSProtocol is the WebSocket subprotocol the daemon speaks. Browsers can't
// set headers on a WebSocket, so the UI sends its token as a second
// subprotocol, WSTokenPrefix+token, and the daemon answers with WSProtocol.
const (
	WSProtocol    = "loop"
	WSTokenPrefix = "loop.token."
)

// AgentLookup finds the agent a token was issued to.
type AgentLookup interface {
	Lookup(token string) (Principal, bool)
}

// Authenticator lets through requests that carry the owner token, and
// requests that carry an agent token to a route agents may call.
type Authenticator struct {
	mu          sync.RWMutex
	owner       string
	agents      AgentLookup
	agentRoutes *RouteSet
	public      *RouteSet
}

// NewAuthenticator returns an Authenticator. public routes need no token;
// agentRoutes are the ones an agent token may call.
func NewAuthenticator(owner string, agents AgentLookup, agentRoutes, public *RouteSet) *Authenticator {
	return &Authenticator{owner: owner, agents: agents, agentRoutes: agentRoutes, public: public}
}

// SetOwnerToken replaces the owner token, after a rotation.
func (a *Authenticator) SetOwnerToken(tok string) {
	a.mu.Lock()
	a.owner = tok
	a.mu.Unlock()
}

func (a *Authenticator) isOwner(tok string) bool {
	a.mu.RLock()
	owner := a.owner
	a.mu.RUnlock()
	return owner != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(owner)) == 1
}

// Wrap authenticates every request before next sees it, and puts the
// caller's Principal in the request context.
func (a *Authenticator) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || a.public.Allows(r) {
			next.ServeHTTP(w, r)
			return
		}
		tok := RequestToken(r)
		if tok == "" {
			http.Error(w, "missing API token", http.StatusUnauthorized)
			return
		}
		if a.isOwner(tok) {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), Principal{Kind: KindOwner})))
			return
		}
		var p Principal
		ok := false
		if a.agents != nil {
			p, ok = a.agents.Lookup(tok)
		}
		if !ok {
			http.Error(w, "invalid API token", http.StatusUnauthorized)
			return
		}
		if !a.agentRoutes.Allows(r) {
			http.Error(w, "not available to agents", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// RequestToken returns the token a request carries: a Bearer
// Authorization header, or a WebSocket token subprotocol.
func RequestToken(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(tok)
	}
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for p := range strings.SplitSeq(v, ",") {
			if tok, ok := strings.CutPrefix(strings.TrimSpace(p), WSTokenPrefix); ok {
				return tok
			}
		}
	}
	return ""
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller a request context carries.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// IsAgent reports whether ctx carries an agent caller.
func IsAgent(ctx context.Context) bool {
	p, ok := PrincipalFrom(ctx)
	return ok && p.Kind == KindAgent
}

// RouteSet matches requests against http.ServeMux patterns
// ("POST /api/messages", "GET /api/channels/{id}/queued"), with the mux's
// own matching rules.
type RouteSet struct {
	mux *http.ServeMux
}

// NewRouteSet returns a RouteSet of patterns.
func NewRouteSet(patterns ...string) *RouteSet {
	mux := http.NewServeMux()
	for _, p := range patterns {
		mux.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	return &RouteSet{mux: mux}
}

// Allows reports whether r matches one of the patterns.
func (s *RouteSet) Allows(r *http.Request) bool {
	_, pattern := s.mux.Handler(r)
	return pattern != ""
}
