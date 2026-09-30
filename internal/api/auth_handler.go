package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/radutopala/loop/internal/apiauth"
)

// contentCapTTL is how long a content link works. The UI mints a new one
// well before then.
const contentCapTTL = time.Hour

// Content link kinds.
const (
	capKindRaw        = "raw"
	capKindPlayground = "playground"
)

// playgroundCSP sandboxes playground pages like the iframe does, minus
// allow-same-origin.
const playgroundCSP = "sandbox allow-scripts allow-forms allow-modals allow-popups allow-downloads"

// AuthDeps are the credentials the API checks.
type AuthDeps struct {
	// OwnerToken is the user's token: the desktop app, the CLI and host tools.
	OwnerToken string
	// RotateOwnerToken writes a new owner token and returns it.
	RotateOwnerToken func() (string, error)
	// Agents finds the agent container a token was issued to.
	Agents apiauth.AgentLookup
	// Caps signs content links.
	Caps *apiauth.Signer
}

// WithAuth makes the API require the owner token or an agent token.
func WithAuth(d AuthDeps) Option {
	return func(s *Server) {
		s.auth = newAuthenticator(d.OwnerToken, d.Agents)
		s.rotateOwnerToken = d.RotateOwnerToken
		s.caps = d.Caps
	}
}

// WithWorkflowBashLocal records that workflow bash nodes run on the host, so
// agents may not start workflows.
func WithWorkflowBashLocal(local bool) Option {
	return func(s *Server) { s.workflowBashLocal = local }
}

func newAuthenticator(owner string, agents apiauth.AgentLookup) *apiauth.Authenticator {
	return apiauth.NewAuthenticator(owner, agents, apiauth.NewRouteSet(agentRoutes...), apiauth.NewRouteSet(publicRoutes...))
}

// handleRotateToken replaces the owner token. Clients that held the old one
// get a 401 and read the new one from the token file.
func (s *Server) handleRotateToken(w http.ResponseWriter, _ *http.Request) {
	if s.rotateOwnerToken == nil {
		http.Error(w, "token rotation not configured", http.StatusNotImplemented)
		return
	}
	tok, err := s.rotateOwnerToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.auth.SetOwnerToken(tok)
	w.WriteHeader(http.StatusNoContent)
}

type contentCapRequest struct {
	Kind      string `json:"kind"`       // "raw" or "playground"
	ChannelID string `json:"channel_id"` // raw: the channel; playground: set for a project playground
	Root      int    `json:"root"`       // raw: the channel root index
	Name      string `json:"name"`       // playground: its name
}

type contentCapResponse struct {
	BaseURL      string `json:"base_url"`
	ExpiresInSec int    `json:"expires_in_sec"`
}

// handleCreateContentCap returns a base URL, /c/{cap}/, under which the
// browser can load one channel root's files or one playground without an
// Authorization header: iframes, images, video and <base href> can't send one.
func (s *Server) handleCreateContentCap(w http.ResponseWriter, r *http.Request) {
	if s.caps == nil {
		http.Error(w, "content links not configured", http.StatusNotImplemented)
		return
	}
	var req contentCapRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var c apiauth.Cap
	switch req.Kind {
	case capKindRaw:
		if req.ChannelID == "" {
			http.Error(w, "channel_id is required", http.StatusBadRequest)
			return
		}
		c = apiauth.Cap{Kind: capKindRaw, ChannelID: req.ChannelID, Scope: strconv.Itoa(req.Root)}
	case capKindPlayground:
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		c = apiauth.Cap{Kind: capKindPlayground, ChannelID: req.ChannelID, Scope: req.Name}
	default:
		http.Error(w, "kind must be raw or playground", http.StatusBadRequest)
		return
	}
	writeHTTPJSON(w, http.StatusOK, contentCapResponse{
		BaseURL:      "/c/" + s.caps.Mint(c, contentCapTTL) + "/",
		ExpiresInSec: int(contentCapTTL / time.Second),
	}, s.logger)
}

// handleContentCap serves GET /c/{cap}/{path...}: the file at path under
// what the capability names.
func (s *Server) handleContentCap(w http.ResponseWriter, r *http.Request) {
	if s.caps == nil {
		http.NotFound(w, r)
		return
	}
	tok := r.PathValue("cap")
	c, err := s.caps.Verify(tok)
	if err != nil {
		http.Error(w, "link expired or invalid", http.StatusForbidden)
		return
	}
	rel := r.PathValue("path")
	switch c.Kind {
	case capKindRaw:
		r2 := r.Clone(r.Context())
		r2.SetPathValue("id", c.ChannelID)
		r2.SetPathValue("root", c.Scope)
		r2.SetPathValue("path", rel)
		s.handleRawFile(w, r2)
	case capKindPlayground:
		pgDir, err := s.playground.playgroundDirFor(r, c.ChannelID, c.Scope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// An opaque origin: agent-written pages must not share the app's
		// origin (they would when the UI is served by the same host) and
		// read its stored token. The console bridge uses postMessage, which
		// works across origins.
		w.Header().Set("Content-Security-Policy", playgroundCSP)
		if rel == "" {
			s.playground.renderPlaygroundIndex(w, pgDir, "/c/"+tok+"/")
			return
		}
		s.playground.servePlaygroundFile(w, pgDir, rel)
	default:
		http.NotFound(w, r)
	}
}

// playgroundDirFor returns the dir of playground name: a project playground
// of channelID, or a global one when channelID is empty.
func (s *playgroundService) playgroundDirFor(r *http.Request, channelID, name string) (string, error) {
	if channelID == "" {
		return s.validatePlaygroundDir(name)
	}
	dirPath, err := s.projectPlaygroundDir(r.Context(), channelID)
	if err != nil {
		return "", err
	}
	return s.playgroundDirIn(filepath.Join(dirPath, ".loop", "playground"), name)
}
