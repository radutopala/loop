package agentgate

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// Approver is the minimum surface an ExecveHandler needs from the approval
// pipeline. *Manager satisfies it; tests pass fakes.
type Approver interface {
	Request(ctx context.Context, channelID string, req ApprovalRequest) Outcome
}

// ExecveRequest is the post-read view of an execve(2)/execveat(2) trap.
//
// The server-side notify dispatcher is responsible for:
//   - walking argv via ProcessVMReadv and filling Argv (incl. argv[0])
//   - resolving execveat(AT_EMPTY_PATH) by reading /proc/<pid>/fd/<dirfd>
//     into Filename (which may therefore be "memfd:…")
//
// The handler operates on the resolved representation only; it does not touch
// tracee memory directly.
type ExecveRequest struct {
	PID       int
	ChannelID string
	Syscall   string // "execve" | "execveat"
	Filename  string // argv[0] for execve; /proc-fd link target for execveat+AT_EMPTY_PATH
	Argv      []string
}

// ExecveHandler evaluates execve/execveat traps against the policy. Construct
// via NewExecveHandler; the zero value is not ready to use.
//
// PeerSource maps the tracee PID to an approval-source identifier ("chat"
// vs "terminal:<leafId>") so the server can route prompts to the right UI
// surface. nil disables lookup and every prompt is attributed to chat.
type ExecveHandler struct {
	Policy     *Policy
	Approver   Approver
	Auditor    Auditor
	PeerSource PeerSourceLookup
	Now        func() time.Time
}

// NewExecveHandler wires a handler to its policy + approval source.
// Auditor defaults to NopAuditor; the server factory swaps in a real sink.
func NewExecveHandler(policy *Policy, approver Approver) *ExecveHandler {
	return &ExecveHandler{
		Policy:   policy,
		Approver: approver,
		Auditor:  NopAuditor{},
		Now:      time.Now,
	}
}

// Handle decides an exec request. Returns Allow or Deny; Approve is resolved
// internally via the Approver.
func (h *ExecveHandler) Handle(ctx context.Context, req ExecveRequest) Outcome {
	start := h.Now()
	// memfd+execveat defense. An empty filename + a dirfd backing anonymous
	// memory is how shellcode-in-memfd attacks execute. No rule override —
	// this is policy, not a judgement call.
	if isMemfdPath(req.Filename) {
		out := Outcome{Decision: types.DecisionDeny, Reason: "memfd-execveat-deny"}
		h.Auditor.Write(AuditEntry{
			Ts:       h.Now(),
			Channel:  req.ChannelID,
			PID:      req.PID,
			Kind:     "execve",
			Target:   req.Filename,
			RuleID:   out.Reason,
			Decision: string(out.Decision),
			Latency:  h.Now().Sub(start),
		})
		return out
	}

	argv := req.Argv
	if len(argv) == 0 {
		argv = []string{req.Filename}
	}
	effective := unwrapCommand(argv)

	match := h.Policy.MatchCommand(effective[0], effective[1:])
	target := strings.Join(effective, " ")
	var out Outcome
	if match.Decision == types.DecisionApprove {
		if h.Approver == nil {
			out = Outcome{Decision: types.DecisionDeny, Reason: "no-approver"}
		} else {
			out = h.Approver.Request(ctx, req.ChannelID, ApprovalRequest{
				Kind:     "execve",
				Target:   target,
				Source:   sourceForPID(req.PID, h.PeerSource),
				Message:  match.Message,
				CacheKey: execveCacheKey(effective),
			})
		}
	} else {
		out = Outcome{Decision: match.Decision, Reason: match.RuleID}
	}
	h.Auditor.Write(AuditEntry{
		Ts:          h.Now(),
		Channel:     req.ChannelID,
		PID:         req.PID,
		Kind:        "execve",
		Target:      target,
		RuleID:      match.RuleID,
		Decision:    string(out.Decision),
		PromptedWho: out.Actor,
		Latency:     h.Now().Sub(start),
	})
	return out
}

// isMemfdPath recognises the two shapes a /proc/<pid>/fd readlink can take for
// an anonymous memfd: "memfd:<name>" (typical) and "/memfd:<name>" (observed
// on some kernels when mount namespaces prepend a leading slash).
func isMemfdPath(p string) bool {
	return strings.HasPrefix(p, "memfd:") || strings.HasPrefix(p, "/memfd:")
}

// execveCacheKey produces a stable key so "Allow for session" can cover a
// repeated command. We include argv[0]'s basename + up to 2 extra tokens so
// "git push" doesn't also authorise "git config --global".
func execveCacheKey(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	base := filepath.Base(argv[0])
	rest := argv[1:]
	if len(rest) > 2 {
		rest = rest[:2]
	}
	if len(rest) == 0 {
		return "execve:" + base
	}
	return "execve:" + base + ":" + strings.Join(rest, " ")
}

// transparentWrappers are argv[0] basenames that we look through to find the
// "real" command before policy evaluation. We deliberately omit eatmydata
// (not installed in our container image).
var transparentWrappers = map[string]struct{}{
	"env":     {},
	"sudo":    {},
	"nice":    {},
	"ionice":  {},
	"chrt":    {},
	"timeout": {},
	"nohup":   {},
	"unshare": {},
	"setsid":  {},
	"taskset": {},
	"stdbuf":  {},
	"script":  {},
}

// isWrapper returns true when basename matches a transparent wrapper.
// ld-linux* is matched by prefix because its filename carries an ABI suffix
// ("ld-linux-x86-64.so.2", "ld-linux-aarch64.so.1", …).
func isWrapper(base string) bool {
	if strings.HasPrefix(base, "ld-linux") {
		return true
	}
	_, ok := transparentWrappers[base]
	return ok
}

// unwrapCommand returns the effective argv after stripping any transparent
// wrappers at the head. Recurses for chains like `sudo env FOO=bar rm`.
// The loop terminates because it either returns early or strips ≥1 entry.
func unwrapCommand(argv []string) []string {
	for len(argv) > 0 {
		base := filepath.Base(argv[0])
		if !isWrapper(base) {
			return argv
		}
		i := 1
		for i < len(argv) {
			a := argv[i]
			if a == "--" {
				i++
				break
			}
			if strings.HasPrefix(a, "-") {
				i++
				continue
			}
			if base == "env" && looksLikeEnvAssignment(a) {
				i++
				continue
			}
			break
		}
		if i >= len(argv) {
			// Wrapper with no payload. Keep the wrapper as the command so
			// the caller still applies its rule against the wrapper.
			return argv
		}
		argv = argv[i:]
	}
	return argv
}

// looksLikeEnvAssignment returns true for "KEY=val"-style tokens that precede
// a command under env(1). The key must be a non-empty identifier composed of
// [A-Za-z_][A-Za-z0-9_]* — otherwise we treat the token as the command.
func looksLikeEnvAssignment(s string) bool {
	eq := strings.Index(s, "=")
	if eq <= 0 {
		return false
	}
	key := s[:eq]
	for i, r := range key {
		if r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') {
			continue
		}
		if i > 0 && '0' <= r && r <= '9' {
			continue
		}
		return false
	}
	return true
}
