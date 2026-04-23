package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

// Approver is the subset of agentgate.Manager this package depends on.
type Approver interface {
	Request(ctx context.Context, channelID string, req agentgate.ApprovalRequest) agentgate.Outcome
}

// Auditor receives one entry per proxied request (both decisions and denies).
type Auditor interface {
	WriteAudit(entry AuditEntry)
}

// AuditEntry captures one proxied HTTP request's decision trail.
type AuditEntry struct {
	Ts         time.Time
	CID        string
	Channel    string
	Method     string
	Path       string // canonical (post-version-strip)
	RuleID     string
	Decision   string        // "allow" | "deny" | "approve" | "cache-hit" | "rate-limit"
	BodyRuleID string        // when a body rule fired
	Latency    time.Duration // trap-to-decision
	Actor      string        // user ID on approve
	Reason     string        // non-empty on rate-limit / fail paths
}

// Server proxies agent-container Docker API traffic. Lifetime is tied to a
// single agent container: `ServerConfig.CID` and `ChannelID` are captured and
// stamped on every audit entry.
type Server struct {
	cfg      ServerConfig
	policy   *Policy
	upstream *httputil.ReverseProxy
}

// ServerConfig groups the dependencies a Server needs. All fields are required
// except Auditor (nil = discard).
type ServerConfig struct {
	CID        string
	ChannelID  string
	Policy     *Policy
	Approver   Approver
	DockerSock string // e.g. "/var/run/docker.sock"
	Auditor    Auditor
	Now        func() time.Time
}

// NewServer constructs a Server. CID / ChannelID / Policy / Approver / DockerSock
// must be set; returns an error otherwise.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.CID == "" {
		return nil, fmt.Errorf("dockerproxy: CID required")
	}
	if cfg.Policy == nil {
		return nil, fmt.Errorf("dockerproxy: Policy required")
	}
	if cfg.Approver == nil {
		return nil, fmt.Errorf("dockerproxy: Approver required")
	}
	if cfg.DockerSock == "" {
		return nil, fmt.Errorf("dockerproxy: DockerSock required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", cfg.DockerSock)
		},
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	rp := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "docker"
			r.Host = "docker"
		},
		Transport:     transport,
		FlushInterval: 100 * time.Millisecond,
	}
	return &Server{cfg: cfg, policy: cfg.Policy, upstream: rp}, nil
}

// apiVersionRe matches a Docker API version prefix at the start of the path
// followed by '/' (e.g. "/v1.41/containers/json" — captures "/v1.41"). We also
// handle the degenerate case of just "/v1.41" with no trailing slash.
var apiVersionRe = regexp.MustCompile(`^/v\d+(\.\d+)?(/|$)`)

// stripAPIVersionPrefix returns the canonical path (with any /vN.M prefix removed).
// Rules operate against canonical paths so a client pinning v1.41 sees the same
// rule as an un-versioned client.
func stripAPIVersionPrefix(path string) string {
	m := apiVersionRe.FindStringSubmatch(path)
	if m == nil {
		return path
	}
	// m[0] is the matched prefix including the trailing '/' (or nothing at EOS).
	// We want to keep the '/' (if present) so "/v1.41/containers" becomes "/containers".
	rest := path[len(m[0]):]
	if m[2] == "/" {
		return "/" + rest
	}
	return "/" + rest
}

// ServeHTTP is the handler entry point. On deny: 403 + message. On approve:
// consult Approver; on allow-from-user: proceed. Hijacked endpoints (attach,
// exec/start) stream raw bytes bidirectionally.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.cfg.Now()
	canonicalPath := stripAPIVersionPrefix(r.URL.Path)

	// HTTP rule match.
	httpRes := s.policy.MatchHTTP(r.Method, canonicalPath)

	// Body-rule evaluation. A body-rule deny always wins (can't be user-overridden).
	bodyResult, decodedBody, bodyErr := s.evaluateBody(r, canonicalPath)
	if bodyErr != nil {
		s.audit(AuditEntry{
			Ts:       start,
			CID:      s.cfg.CID,
			Channel:  s.cfg.ChannelID,
			Method:   r.Method,
			Path:     canonicalPath,
			Decision: "deny",
			RuleID:   "body-eval-error",
			Reason:   bodyErr.Error(),
			Latency:  s.cfg.Now().Sub(start),
		})
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if bodyResult.Fired && bodyResult.Decision == types.DecisionDeny {
		s.audit(AuditEntry{
			Ts:         start,
			CID:        s.cfg.CID,
			Channel:    s.cfg.ChannelID,
			Method:     r.Method,
			Path:       canonicalPath,
			RuleID:     httpRes.RuleID,
			BodyRuleID: bodyResult.RuleID,
			Decision:   "deny",
			Reason:     "body-rule",
			Latency:    s.cfg.Now().Sub(start),
		})
		http.Error(w, bodyResult.Message, http.StatusForbidden)
		return
	}

	// A body-rule approve overrides the HTTP-rule decision: route through
	// the user prompt with Kind="docker-body" and a body-rule-scoped cache
	// key so "Allow for session" applies to the body shape, not just the URL.
	if bodyResult.Fired && bodyResult.Decision == types.DecisionApprove {
		if !s.runApprovalFlow(w, r, start, canonicalPath,
			"docker-body",
			httpRes.RuleID, bodyResult.RuleID,
			bodyResult.Message,
			"docker:"+strings.ToUpper(r.Method)+":body:"+bodyResult.RuleID,
			decodedBody,
		) {
			return
		}
	} else {
		// Apply HTTP rule decision.
		switch httpRes.Decision {
		case types.DecisionDeny:
			s.audit(AuditEntry{
				Ts:       start,
				CID:      s.cfg.CID,
				Channel:  s.cfg.ChannelID,
				Method:   r.Method,
				Path:     canonicalPath,
				RuleID:   httpRes.RuleID,
				Decision: "deny",
				Latency:  s.cfg.Now().Sub(start),
			})
			msg := httpRes.Message
			if msg == "" {
				msg = "docker request denied by policy"
			}
			http.Error(w, msg, http.StatusForbidden)
			return

		case types.DecisionApprove:
			if !s.runApprovalFlow(w, r, start, canonicalPath,
				"docker-http",
				httpRes.RuleID, "",
				httpRes.Message,
				"docker:"+strings.ToUpper(r.Method)+":"+normalizeCachePath(canonicalPath),
				decodedBody,
			) {
				return
			}

		case types.DecisionAllow:
			s.audit(AuditEntry{
				Ts:       start,
				CID:      s.cfg.CID,
				Channel:  s.cfg.ChannelID,
				Method:   r.Method,
				Path:     canonicalPath,
				RuleID:   httpRes.RuleID,
				Decision: "allow",
				Latency:  s.cfg.Now().Sub(start),
			})
		}
	}

	// Forward. Hijacked endpoints are handled as raw TCP tunnels.
	if isHijacking(r.Method, canonicalPath) {
		s.hijackProxy(w, r)
		return
	}
	s.upstream.ServeHTTP(w, r)
}

// runApprovalFlow prompts the user via the Approver and writes the audit entry.
// Returns true when the user allowed and ServeHTTP should continue forwarding;
// false when the request was denied/rate-limited (in which case the response is
// already written and the caller must return immediately). bodyRuleID is empty
// for HTTP-rule prompts and "body[N]" for body-rule prompts.
func (s *Server) runApprovalFlow(
	w http.ResponseWriter, r *http.Request,
	start time.Time, canonicalPath string,
	kind, ruleID, bodyRuleID, message, cacheKey string,
	decodedBody any,
) bool {
	target := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
	outcome := s.cfg.Approver.Request(r.Context(), s.cfg.ChannelID, agentgate.ApprovalRequest{
		Kind:     kind,
		Target:   target,
		Message:  message,
		CacheKey: cacheKey,
		Details:  extractApprovalDetails(r.Method, canonicalPath, decodedBody),
	})
	decision := "approve"
	if outcome.FromCache {
		decision = "cache-hit"
	}
	if outcome.RateLimited {
		decision = "rate-limit"
	}
	if outcome.Decision != types.DecisionAllow {
		s.audit(AuditEntry{
			Ts:         start,
			CID:        s.cfg.CID,
			Channel:    s.cfg.ChannelID,
			Method:     r.Method,
			Path:       canonicalPath,
			RuleID:     ruleID,
			BodyRuleID: bodyRuleID,
			Decision:   decision,
			Actor:      outcome.Actor,
			Reason:     outcome.Reason,
			Latency:    s.cfg.Now().Sub(start),
		})
		http.Error(w, "docker request denied", http.StatusForbidden)
		return false
	}
	s.audit(AuditEntry{
		Ts:         start,
		CID:        s.cfg.CID,
		Channel:    s.cfg.ChannelID,
		Method:     r.Method,
		Path:       canonicalPath,
		RuleID:     ruleID,
		BodyRuleID: bodyRuleID,
		Decision:   decision,
		Actor:      outcome.Actor,
		Latency:    s.cfg.Now().Sub(start),
	})
	return true
}

// evaluateBody buffers the request body (up to the largest applicable MaxBodyBytes),
// JSON-decodes it, and runs body rules. The request body is re-attached so the
// forwarded request sees it byte-for-byte. The decoded JSON value is returned
// alongside the BodyCheckResult so the approve path can summarise it for the
// user (image, binds, privileged, ...). decodedBody is nil when the body was
// skipped (no cap, oversize, non-JSON) or when the body had no JSON content.
func (s *Server) evaluateBody(r *http.Request, canonicalPath string) (BodyCheckResult, any, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return BodyCheckResult{}, nil, nil
	}
	cap := s.policy.MaxBodyBytes(r.Method, canonicalPath)
	if cap == 0 {
		return BodyCheckResult{}, nil, nil
	}
	// Read up to cap+1 bytes so we can tell when the body exceeded the cap.
	buf, err := io.ReadAll(io.LimitReader(r.Body, cap+1))
	if err != nil {
		return BodyCheckResult{}, nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(buf)) > cap {
		// Drain remaining bytes, re-attach the full original for forwarding.
		rest, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		full := make([]byte, 0, len(buf)+len(rest))
		full = append(full, buf...)
		full = append(full, rest...)
		r.Body = io.NopCloser(bytes.NewReader(full))
		r.ContentLength = int64(len(full))
		return BodyCheckResult{Skipped: "body-too-large"}, nil, nil
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.ContentLength = int64(len(buf))

	// Only parse when Content-Type indicates JSON. Any body-rule with an explicit
	// ContentTypes list will also gate itself, but this is a cheap early-out.
	ct := normalizeContentType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		return BodyCheckResult{Skipped: "not-json"}, nil, nil
	}
	var decoded any
	if err := json.Unmarshal(buf, &decoded); err != nil {
		return BodyCheckResult{}, nil, fmt.Errorf("parse body: %w", err)
	}
	return s.policy.CheckBody(r.Method, canonicalPath, r.Header.Get("Content-Type"), decoded), decoded, nil
}

// normalizeCachePath collapses dynamic URL segments (container IDs, image IDs,
// exec IDs) so a "session" approval covers every instance of the same action.
// e.g. /containers/abc123/exec → /containers/*/exec
var (
	dynamicSegmentMidRe = regexp.MustCompile(`/[0-9a-f]{12,}/`)
	dynamicSegmentEndRe = regexp.MustCompile(`/[0-9a-f]{12,}$`)
)

func normalizeCachePath(path string) string {
	// Apply mid-path substitution repeatedly to handle adjacent hex segments.
	for {
		replaced := dynamicSegmentMidRe.ReplaceAllString(path, "/*/")
		if replaced == path {
			break
		}
		path = replaced
	}
	return dynamicSegmentEndRe.ReplaceAllString(path, "/*")
}

// isHijacking reports whether method+path is a Docker endpoint that upgrades
// to a raw byte stream (attach, exec start). These must not flow through the
// ReverseProxy — they need explicit connection hijacking.
func isHijacking(method, canonicalPath string) bool {
	if method != http.MethodPost {
		return false
	}
	switch {
	case hijackAttachRe.MatchString(canonicalPath):
		return true
	case hijackExecStartRe.MatchString(canonicalPath):
		return true
	}
	return false
}

var (
	hijackAttachRe    = regexp.MustCompile(`^/containers/[^/]+/attach$`)
	hijackExecStartRe = regexp.MustCompile(`^/exec/[^/]+/start$`)
	execCreateRe      = regexp.MustCompile(`^/containers/[^/]+/exec$`)
)

func (s *Server) audit(e AuditEntry) {
	if s.cfg.Auditor == nil {
		return
	}
	s.cfg.Auditor.WriteAudit(e)
}

// errHijackNotSupported is returned when the underlying ResponseWriter doesn't
// implement http.Hijacker (e.g. http/2, which the unix-socket listener never sees).
var errHijackNotSupported = errors.New("dockerproxy: hijack not supported")
