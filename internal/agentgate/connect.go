package agentgate

import (
	"context"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// ConnectRequest is the post-read view of a connect(2) trap against a unix
// socket. The server-side dispatcher reads sockaddr_un.sun_path from tracee
// memory, strips the leading NULL for abstract sockets, and fills Path.
//
// Non-unix sockets (AF_INET, AF_INET6, …) are not gated in v1 — the dispatcher
// short-circuits before construction.
type ConnectRequest struct {
	PID       int
	ChannelID string
	Path      string // absolute filesystem path, or "@<name>" for abstract sockets
}

// ConnectHandler evaluates connect(2) traps against path_rules. Construct via
// NewConnectHandler; the zero value is not ready to use.
//
// PeerSource maps the tracee PID to an approval-source identifier ("chat"
// vs "terminal:<leafId>") so the server can route prompts to the right UI
// surface. nil disables lookup and every prompt is attributed to chat.
type ConnectHandler struct {
	Policy     *Policy
	Approver   Approver
	Auditor    Auditor
	PeerSource PeerSourceLookup
	Now        func() time.Time
}

// NewConnectHandler wires a handler to its policy + approval source.
// Auditor defaults to NopAuditor; callers can assign a real sink after
// construction (the server factory does this). Now defaults to time.Now.
func NewConnectHandler(policy *Policy, approver Approver) *ConnectHandler {
	return &ConnectHandler{
		Policy:   policy,
		Approver: approver,
		Auditor:  NopAuditor{},
		Now:      time.Now,
	}
}

// Handle decides a connect request. Returns Allow or Deny; Approve is
// resolved internally via the Approver.
func (h *ConnectHandler) Handle(ctx context.Context, req ConnectRequest) Outcome {
	start := h.Now()
	match := h.Policy.MatchPath(req.Path)
	var out Outcome
	if match.Decision == types.DecisionApprove {
		if h.Approver == nil {
			out = Outcome{Decision: types.DecisionDeny, Reason: "no-approver"}
		} else {
			out = h.Approver.Request(ctx, req.ChannelID, ApprovalRequest{
				Kind:     "connect",
				Target:   req.Path,
				Source:   sourceForPID(req.PID, h.PeerSource),
				Message:  match.Message,
				CacheKey: "connect:" + req.Path,
				OnPrompt: func() {
					h.Auditor.Write(AuditEntry{
						Ts:      h.Now(),
						Channel: req.ChannelID,
						PID:     req.PID,
						Kind:    "connect",
						Target:  req.Path,
						RuleID:  match.RuleID,
						Event:   "request",
					})
				},
			})
		}
	} else {
		out = Outcome{Decision: match.Decision, Reason: match.RuleID}
	}
	h.Auditor.Write(AuditEntry{
		Ts:          h.Now(),
		Channel:     req.ChannelID,
		PID:         req.PID,
		Kind:        "connect",
		Target:      req.Path,
		RuleID:      match.RuleID,
		Decision:    string(out.Decision),
		PromptedWho: out.Actor,
		Latency:     h.Now().Sub(start),
	})
	return out
}

// Sockaddr layout constants. Mirrors `struct sockaddr_un` from
// include/uapi/linux/un.h:
//
//	uint16 sun_family
//	char   sun_path[108]
const (
	// AfUnix is the sa_family_t value for unix-domain sockets.
	AfUnix uint16 = 1
	// SunPathMax caps sockaddr_un.sun_path at its kernel-defined size.
	SunPathMax = 108
)

// ParseUnixSockaddr decodes a raw sockaddr buffer (at least 2 bytes for the
// family). Returns ok=false when the family is not AF_UNIX — callers allow
// such connects in v1 (TCP gating is out of scope). An abstract socket (first
// path byte is NULL) is returned as "@<name>".
func ParseUnixSockaddr(buf []byte) (path string, isUnix bool) {
	if len(buf) < 2 {
		return "", false
	}
	family := uint16(buf[0]) | uint16(buf[1])<<8
	if family != AfUnix {
		return "", false
	}
	pathBytes := buf[2:]
	if len(pathBytes) == 0 {
		return "", true
	}
	if pathBytes[0] == 0 {
		// Abstract socket. Length is carried by addrlen; we render as "@…"
		// to match ss(8) / netstat(8) output.
		name := trimTrailingNuls(pathBytes[1:])
		return "@" + name, true
	}
	return trimTrailingNuls(pathBytes), true
}

// trimTrailingNuls drops trailing NUL bytes from a byte slice and returns the
// remainder as a string. Used for sockaddr_un.sun_path, which is fixed-width
// and NUL-padded.
func trimTrailingNuls(b []byte) string {
	i := len(b)
	for i > 0 && b[i-1] == 0 {
		i--
	}
	// If the path contains interior NULs (shouldn't happen for valid unix
	// paths), strings.IndexByte finds the first one.
	s := string(b[:i])
	if j := strings.IndexByte(s, 0); j >= 0 {
		s = s[:j]
	}
	return s
}
