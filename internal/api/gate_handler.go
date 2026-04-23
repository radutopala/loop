package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/types"
)

// gateApprovalRequest is the body of POST /api/gate/approvals/{id}.
// Decision is one of "once" | "session" | "deny" | "deny-session".
// AuthorID is the clicking user; the local desktop has no auth layer, so the
// client sends the logged-in user ID when known and we fall back to
// local.DefaultAuthorID otherwise.
type gateApprovalRequest struct {
	Decision string `json:"decision"`
	AuthorID string `json:"author_id"`
}

func (s *Server) handleResolveGateApproval(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.approvalResolver, "gate approval resolver not configured") {
		return
	}

	reqID := r.PathValue("id")
	if reqID == "" {
		http.Error(w, "missing approval request id", http.StatusBadRequest)
		return
	}

	var req gateApprovalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Decision == "" {
		http.Error(w, "missing decision", http.StatusBadRequest)
		return
	}
	actor := req.AuthorID
	if actor == "" {
		actor = local.DefaultAuthorID
	}

	err := s.approvalResolver.Resolve(reqID, req.Decision, actor)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, agentgate.ErrNoSuchRequest):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// ContainerApprovalRouter resolves a bearer token to the Manager owning the
// per-container approval route plus its Discord/Slack channelID. Backed in
// production by *agentgate.MultiManagerResolver; implementations must use
// constant-time comparison to resist token-guessing.
type ContainerApprovalRouter interface {
	ByToken(token string) (containerID string, mgr ContainerApprovalManager, channelID string, ok bool)
}

// ContainerApprovalManager is the subset of *agentgate.Manager this handler
// uses. Splitting it out keeps the test double focused. agentgate.Manager
// satisfies this interface as-is.
type ContainerApprovalManager interface {
	Request(ctx context.Context, channelID string, req agentgate.ApprovalRequest) agentgate.Outcome
}

// containerApprovalRequest is the JSON body the in-container docker proxy
// or seccomp-gate parent sends.
type containerApprovalRequest struct {
	Kind     string            `json:"kind"`
	Target   string            `json:"target"`
	Message  string            `json:"message"`
	CacheKey string            `json:"cache_key"`
	Details  map[string]string `json:"details,omitempty"`
}

// containerApprovalResponse is the JSON body the handler returns on 200.
type containerApprovalResponse struct {
	Decision string `json:"decision"`
	Actor    string `json:"actor"`
	Reason   string `json:"reason"`
}

// handleContainerApproval authenticates an inbound approval-HTTP call from an
// in-container proxy/gate, looks up the Manager by bearer token, and blocks
// on the user click.
func (s *Server) handleContainerApproval(w http.ResponseWriter, r *http.Request) {
	if s.containerApprovalRouter == nil {
		http.Error(w, "container approval router not configured", http.StatusServiceUnavailable)
		return
	}

	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}

	_, mgr, channelID, ok := s.containerApprovalRouter.ByToken(token)
	if !ok {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	var body containerApprovalRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	outcome := mgr.Request(r.Context(), channelID, agentgate.ApprovalRequest{
		Kind:     body.Kind,
		Target:   body.Target,
		Message:  body.Message,
		CacheKey: body.CacheKey,
		Details:  body.Details,
	})

	resp := containerApprovalResponse{
		Decision: encodeDecision(outcome.Decision),
		Actor:    outcome.Actor,
		Reason:   outcome.Reason,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// bearerToken extracts the token from "Bearer <token>". Returns "" if the
// header is missing or malformed.
func bearerToken(auth string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

// encodeDecision maps types.Decision to the wire string.
func encodeDecision(d types.Decision) string {
	if d == types.DecisionAllow {
		return "allow"
	}
	return "deny"
}
