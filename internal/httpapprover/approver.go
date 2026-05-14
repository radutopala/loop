// Package httpapprover implements a shared HTTP-backed Approver used by the
// in-container docker proxy and the in-container seccomp-gate parent process.
// Both POST to the same loop-server endpoint (/api/gate/container-approval)
// with a per-container bearer token; the server looks up the owning Manager
// by token and blocks until the user clicks.
//
// Request's signature is identical to agentgate.Approver and
// dockerproxy.Approver — a single *Approver value satisfies both.
package httpapprover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

// EndpointPath is the fixed server-side path the approver POSTs to.
const EndpointPath = "/api/gate/container-approval"

// RequestBody is the JSON shape the container sends.
type RequestBody struct {
	Kind     string            `json:"kind"`
	Target   string            `json:"target"`
	Source   string            `json:"source,omitempty"`
	Message  string            `json:"message"`
	CacheKey string            `json:"cache_key"`
	Details  map[string]string `json:"details,omitempty"`
}

// ResponseBody is the JSON shape the server returns on 200.
type ResponseBody struct {
	Decision string `json:"decision"` // "allow" | "deny"
	Actor    string `json:"actor"`
	Reason   string `json:"reason"`
}

// Approver posts approval requests to loop-server and blocks until the user
// decides. Satisfies both agentgate.Approver and dockerproxy.Approver.
type Approver struct {
	APIURL string
	Token  string
	Client *http.Client
	Logger *slog.Logger
}

// New builds an Approver with sensible defaults. apiURL should be the bare
// host (e.g. "http://host.docker.internal:8080"); EndpointPath is appended
// internally. Token is the per-container bearer string the resolver issued.
// If client is nil, a Client with a long timeout is used (users may take
// a while to click).
func New(apiURL, token string, client *http.Client, logger *slog.Logger) *Approver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Approver{APIURL: apiURL, Token: token, Client: client, Logger: logger}
}

// Request posts the approval request and blocks until the server responds.
// On any transport error or non-2xx status the outcome is DecisionDeny with
// a Reason tag. Context cancellation returns DecisionDeny with reason
// "cancelled".
func (a *Approver) Request(ctx context.Context, channelID string, req agentgate.ApprovalRequest) agentgate.Outcome {
	// RequestBody has only plain string / string-map fields — json.Marshal
	// cannot fail.
	bodyBytes, _ := json.Marshal(RequestBody{
		Kind:     req.Kind,
		Target:   req.Target,
		Source:   req.Source,
		Message:  req.Message,
		CacheKey: req.CacheKey,
		Details:  req.Details,
	})

	url := a.APIURL + EndpointPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return agentgate.Outcome{Decision: types.DecisionDeny, Reason: "request-build-error:" + err.Error()}
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.Token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.Client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return agentgate.Outcome{Decision: types.DecisionDeny, Reason: "cancelled"}
		}
		a.Logger.Warn("approval http error", "url", url, "err", err)
		return agentgate.Outcome{Decision: types.DecisionDeny, Reason: "http-error:" + err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		a.Logger.Warn("approval http non-200", "url", url, "status", resp.StatusCode)
		return agentgate.Outcome{Decision: types.DecisionDeny, Reason: fmt.Sprintf("http-%d", resp.StatusCode)}
	}

	var out ResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return agentgate.Outcome{Decision: types.DecisionDeny, Reason: "decode-error:" + err.Error()}
	}
	return agentgate.Outcome{
		Decision: decodeDecision(out.Decision),
		Actor:    out.Actor,
		Reason:   out.Reason,
	}
}

// decodeDecision maps the wire string to a types.Decision. Unknown values
// default to Deny (fail-closed).
func decodeDecision(s string) types.Decision {
	switch s {
	case "allow":
		return types.DecisionAllow
	case "deny":
		return types.DecisionDeny
	default:
		return types.DecisionDeny
	}
}
