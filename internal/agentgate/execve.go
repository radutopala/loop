package agentgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
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
	// Path is the absolute, symlink-resolved file the kernel will run,
	// checked against the policy's exec file rules. The dispatcher only
	// resolves it when the policy has some; empty skips the check.
	Path string
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

	// A file rule that denies exec on the target's path refuses it whatever
	// the command rules say; any other exec rule leaves it to them.
	if req.Path != "" {
		if match, ok := h.Policy.MatchExecFile(req.Path); ok && match.Decision == types.DecisionDeny {
			out := Outcome{Decision: types.DecisionDeny, Reason: match.RuleID}
			h.Auditor.Write(AuditEntry{
				Ts:       h.Now(),
				Channel:  req.ChannelID,
				PID:      req.PID,
				Kind:     "execve",
				Target:   req.Path,
				RuleID:   match.RuleID,
				Decision: string(out.Decision),
				Latency:  h.Now().Sub(start),
			})
			return out
		}
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
				CacheKey: execveCacheKey(effective, match),
				BurstKey: execveBurstKey(effective),
				OnPrompt: func() {
					h.Auditor.Write(AuditEntry{
						Ts:      h.Now(),
						Channel: req.ChannelID,
						PID:     req.PID,
						Kind:    "execve",
						Target:  target,
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
// repeated command. A rule with args patterns keys on its identity and the
// text they matched, never on option values: "git -c a=b commit" and
// "git -c a=b push" under `(commit|push)` get "commit" and "push". Without
// args patterns, argv[0]'s basename + up to 2 extra tokens key it, so
// "git push" doesn't also authorise "git config --global".
func execveCacheKey(argv []string, match MatchResult) string {
	if len(argv) == 0 {
		return ""
	}
	base := filepath.Base(argv[0])
	if match.Matched != nil {
		return fmt.Sprintf("execve:%s:%s:%q", base, match.RuleID, match.Matched)
	}
	rest := argv[1:]
	if len(rest) > 2 {
		rest = rest[:2]
	}
	if len(rest) == 0 {
		return "execve:" + base
	}
	return "execve:" + base + ":" + strings.Join(rest, " ")
}

// execveBurstKey keys the burst memo by the whole argv, so a once-scoped
// decision only covers the byte-identical probes of one PATH search, never
// the next command that shares its session key.
func execveBurstKey(argv []string) string {
	sum := sha256.Sum256([]byte(strings.Join(argv, "\x00")))
	return "execve-argv:" + hex.EncodeToString(sum[:])
}

// optKind says how a wrapper option treats a value. The zero value marks an
// option the wrapper doesn't have.
type optKind uint8

const (
	optFlag     optKind = iota + 1 // takes no value
	optValue                       // takes a value: attached ("-n5", "--adjustment=5") or the next argument
	optOptional                    // takes a value only when attached ("--mount=FILE")
	optSplit                       // env -S: the value is split into arguments read in its place
	optOpaque                      // turns the wrapper away from running its command: help, pid mode, a shell, …
)

// wrapperGrammar is the command line of a transparent wrapper, read the way
// getopt_long with a leading "+" reads it: options up to the first operand
// or "--", clustered short flags, attached or separate values, unambiguous
// long-option prefixes. Values aren't validated — a wrapper that rejects one
// runs nothing, so the command named in its place never executes.
type wrapperGrammar struct {
	short map[byte]optKind
	long  map[string]optKind
	// pre sees each argument before it's read as an option and returns how
	// many arguments it consumed (0: none, read it as usual).
	pre func(argv []string, i int) int
	// operands is how many arguments follow the options before the
	// command (timeout's DURATION, taskset's MASK, chrt's PRIORITY).
	operands int
	// post skips what sits between the operands and the command.
	post func(argv []string, i int) int
	// needOpt marks a wrapper that refuses to run without an option.
	needOpt bool
}

// shortOpts builds a short-option table from a getopt spec ("k:s:fpv"; ':'
// takes a value, '::' an attached one), then marks the opaque ones.
func shortOpts(spec, opaque string) map[byte]optKind {
	m := map[byte]optKind{}
	for i := 0; i < len(spec); i++ {
		kind := optFlag
		if strings.HasPrefix(spec[i+1:], "::") {
			kind = optOptional
		} else if strings.HasPrefix(spec[i+1:], ":") {
			kind = optValue
		}
		m[spec[i]] = kind
		for i+1 < len(spec) && spec[i+1] == ':' {
			i++
		}
	}
	for i := 0; i < len(opaque); i++ {
		m[opaque[i]] = optOpaque
	}
	return m
}

// transparentWrappers are argv[0] basenames that we look through to find the
// "real" command before policy evaluation, each with its option grammar
// (checked against coreutils 9.7, util-linux 2.41 and sudo 1.9's
// parse_args.c). Every one of them execs its command, so that exec traps
// again on its own; unwrapping makes the wrapper's own trap judge the same
// command. script(1) isn't one: it runs `$SHELL -c CMD`, whose exec traps
// with the shell named. We deliberately omit eatmydata (not installed in our
// container image).
var transparentWrappers = map[string]*wrapperGrammar{
	"env": {
		short: func() map[byte]optKind {
			m := shortOpts("a:iC:S:u:v0", "0") // -0 refuses a command
			m['S'] = optSplit
			return m
		}(),
		long: map[string]optKind{
			"argv0": optValue, "ignore-environment": optFlag, "null": optOpaque,
			"unset": optValue, "chdir": optValue, "split-string": optSplit,
			"block-signal": optOptional, "default-signal": optOptional, "ignore-signal": optOptional,
			"list-signal-handling": optFlag, "debug": optFlag, "help": optOpaque, "version": optOpaque,
		},
		post: envAssignments,
	},
	"sudo": {
		// -h is help unless a host follows, which pre consumes; -e, -l, -v,
		// -K edit, list, validate or drop credentials instead of running;
		// -s and -i run the command through a shell.
		short: shortOpts("Aa:BbC:c:D:Eeg:Hh::iKklNnPp:R:r:SsT:t:U:u:Vv", "ehiKlsVv"),
		long: map[string]optKind{
			"background": optFlag, "preserve-env": optOptional, "edit": optOpaque, "set-home": optFlag,
			"login": optOpaque, "remove-timestamp": optOpaque, "list": optOpaque, "preserve-groups": optFlag,
			"shell": optOpaque, "other-user": optValue, "validate": optOpaque, "askpass": optFlag,
			"auth-type": optValue, "bell": optFlag, "close-from": optValue, "login-class": optValue,
			"chdir": optValue, "group": optValue, "help": optOpaque, "host": optValue,
			"reset-timestamp": optFlag, "no-update": optFlag, "non-interactive": optFlag, "prompt": optValue,
			"chroot": optValue, "role": optValue, "stdin": optFlag, "command-timeout": optValue,
			"type": optValue, "user": optValue, "version": optOpaque,
		},
		pre: sudoPre,
	},
	"nice": {
		short: shortOpts("n:", ""),
		long:  map[string]optKind{"adjustment": optValue, "help": optOpaque, "version": optOpaque},
		pre:   niceAdjustment,
	},
	"ionice": {
		// -p, -P, -u act on running processes instead of a command.
		short: shortOpts("c:n:p:P:u:thV", "pPuhV"),
		long: map[string]optKind{
			"class": optValue, "classdata": optValue, "pid": optOpaque, "pgid": optOpaque,
			"ignore": optFlag, "uid": optOpaque, "help": optOpaque, "version": optOpaque,
		},
	},
	"chrt": {
		// -p acts on a running process; -m prints the priority ranges.
		// PRIORITY is required, whatever the policy.
		short: shortOpts("abdD:fimopP:rRT:vhV", "mphV"),
		long: map[string]optKind{
			"all-tasks": optFlag, "batch": optFlag, "deadline": optFlag, "fifo": optFlag,
			"idle": optFlag, "other": optFlag, "rr": optFlag, "reset-on-fork": optFlag,
			"sched-runtime": optValue, "sched-period": optValue, "sched-deadline": optValue,
			"max": optOpaque, "pid": optOpaque, "verbose": optFlag, "help": optOpaque, "version": optOpaque,
		},
		operands: 1,
		post:     skipDashDash,
	},
	"timeout": {
		short: shortOpts("k:s:fpv", ""),
		long: map[string]optKind{
			"foreground": optFlag, "kill-after": optValue, "preserve-status": optFlag,
			"signal": optValue, "verbose": optFlag, "help": optOpaque, "version": optOpaque,
		},
		operands: 1,
	},
	"nohup": {
		long: map[string]optKind{"help": optOpaque, "version": optOpaque},
	},
	"unshare": {
		// Short namespace flags take no value; only their long forms do.
		short: shortOpts("fhVmuinpCTUrR:w:S:G:cl:", "hV"),
		long: map[string]optKind{
			"mount": optOptional, "uts": optOptional, "ipc": optOptional, "net": optOptional,
			"pid": optOptional, "user": optOptional, "cgroup": optOptional, "time": optOptional,
			"mount-proc": optOptional, "mount-binfmt": optOptional, "load-interp": optValue,
			"propagation": optValue, "root": optValue, "wd": optValue, "setuid": optValue,
			"setgid": optValue, "map-user": optValue, "map-group": optValue, "map-root-user": optFlag,
			"map-current-user": optFlag, "map-auto": optFlag, "map-users": optValue, "map-groups": optValue,
			"fork": optFlag, "kill-child": optOptional, "setgroups": optValue, "keep-caps": optFlag,
			"monotonic": optValue, "boottime": optValue, "help": optOpaque, "version": optOpaque,
		},
	},
	"setsid": {
		short: shortOpts("cfwhV", "hV"),
		long: map[string]optKind{
			"ctty": optFlag, "fork": optFlag, "wait": optFlag, "help": optOpaque, "version": optOpaque,
		},
	},
	"taskset": {
		// -p acts on a running process; -c reads the operand as a CPU list.
		short: shortOpts("apchV", "phV"),
		long: map[string]optKind{
			"all-tasks": optFlag, "pid": optOpaque, "cpu-list": optFlag, "help": optOpaque, "version": optOpaque,
		},
		operands: 1,
	},
	"stdbuf": {
		short:   shortOpts("i:o:e:", ""),
		long:    map[string]optKind{"input": optValue, "output": optValue, "error": optValue, "help": optOpaque, "version": optOpaque},
		needOpt: true,
	},
}

// loaderOpts is the dynamic loader's options. ld.so matches them exactly —
// no "=value", no abbreviations — and reads anything else that doesn't start
// with "--" (even "-x") as the program.
var loaderOpts = map[string]optKind{
	"--library-path": optValue, "--glibc-hwcaps-prepend": optValue, "--glibc-hwcaps-mask": optValue,
	"--inhibit-rpath": optValue, "--audit": optValue, "--preload": optValue, "--argv0": optValue,
	"--inhibit-cache": optFlag,
}

// wrapperFor returns the unwrapper for an argv[0] basename, nil when it isn't
// a transparent wrapper. ld-linux* is matched by prefix because its filename
// carries an ABI suffix ("ld-linux-x86-64.so.2", "ld-linux-aarch64.so.1", …).
// The loader matters most: it maps the program itself rather than exec it, so
// its trap is the only one the program gets.
func wrapperFor(base string) func([]string) ([]string, bool) {
	if strings.HasPrefix(base, "ld-linux") {
		return unwrapLoader
	}
	if g, ok := transparentWrappers[base]; ok {
		return g.unwrap
	}
	return nil
}

// unwrapCommand returns the effective argv after stripping any transparent
// wrappers at the head. Recurses for chains like `sudo env FOO=bar rm`.
// A wrapper whose command can't be named for sure — an unknown or malformed
// option, one that turns it away from running a command, nothing left to
// run — is kept as the command, so the caller still applies its rule against
// the wrapper. The loop terminates because every pass shortens argv's total
// length: env -S trades its option for the fields of its value.
func unwrapCommand(argv []string) []string {
	for len(argv) > 0 {
		unwrap := wrapperFor(filepath.Base(argv[0]))
		if unwrap == nil {
			return argv
		}
		payload, ok := unwrap(argv)
		if !ok {
			return argv
		}
		argv = payload
	}
	return argv
}

// unwrap returns the command a wrapper invocation runs; ok is false when it
// can't be named for sure.
func (g *wrapperGrammar) unwrap(argv []string) ([]string, bool) {
	opts := 0
	i := 1
	for i < len(argv) {
		if g.pre != nil {
			if n := g.pre(argv, i); n > 0 {
				i += n
				continue
			}
		}
		tok := argv[i]
		if tok == "--" {
			i++
			break
		}
		if len(tok) < 2 || tok[0] != '-' {
			break
		}
		kind, val, attached := g.option(tok)
		n := 1
		switch kind {
		case optFlag, optOptional:
		case optValue, optSplit:
			if !attached {
				if i+1 >= len(argv) {
					return nil, false
				}
				val, n = argv[i+1], 2
			}
			if kind == optSplit {
				fields, ok := envSplit(val)
				if !ok {
					return nil, false
				}
				// env reads the fields in the option's place, options
				// included.
				argv = slices.Concat(argv[:1], fields, argv[i+n:])
				i, n = 1, 0
			}
		default:
			return nil, false
		}
		opts++
		i += n
	}
	if g.needOpt && opts == 0 {
		return nil, false
	}
	i += g.operands
	if g.post != nil {
		i = g.post(argv, i)
	}
	if i >= len(argv) {
		return nil, false
	}
	return argv[i:], true
}

// option reads one option argument ("--name[=value]" or a "-abc" cluster).
// It returns the kind of the option that decides how the argument ends — the
// first one in a cluster that isn't a plain flag — and its attached value.
// A flag given "=value" reads as unknown, as getopt rejects it.
func (g *wrapperGrammar) option(tok string) (kind optKind, val string, attached bool) {
	if tok[1] == '-' {
		name, val, attached := strings.Cut(tok[2:], "=")
		kind := g.longKind(name)
		if attached && kind == optFlag {
			return 0, "", false
		}
		return kind, val, attached
	}
	for j := 1; j < len(tok); j++ {
		if kind = g.short[tok[j]]; kind != optFlag {
			return kind, tok[j+1:], j+1 < len(tok)
		}
	}
	return optFlag, "", false
}

// longKind resolves a long option name the way getopt_long does: an exact
// match, else the one option it's a prefix of. Unknown and ambiguous names
// yield 0.
func (g *wrapperGrammar) longKind(name string) optKind {
	if kind, ok := g.long[name]; ok {
		return kind
	}
	var kind optKind
	for full, k := range g.long {
		if strings.HasPrefix(full, name) {
			if kind != 0 {
				return 0
			}
			kind = k
		}
	}
	return kind
}

// envAssignments skips env's "-" (an empty environment) and its NAME=VALUE
// arguments: env sets any argument holding a "=", even "=x" or "a/b=c".
func envAssignments(argv []string, i int) int {
	if i < len(argv) && argv[i] == "-" {
		i++
	}
	for i < len(argv) && strings.Contains(argv[i], "=") {
		i++
	}
	return i
}

// skipDashDash skips the "--" chrt allows between its PRIORITY and the
// command.
func skipDashDash(argv []string, i int) int {
	if i < len(argv) && argv[i] == "--" {
		i++
	}
	return i
}

// envSplit splits an env -S value into the arguments env reads from it. Only
// plain blank-separated words are split; a value using env's quotes,
// escapes, ${VAR} expansion or # comments is refused rather than guessed at.
func envSplit(s string) ([]string, bool) {
	if strings.ContainsAny(s, "\\'\"$#\v\f\r") {
		return nil, false
	}
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }), true
}

// sudoPre consumes the arguments sudo reads between its options: "-h HOST"
// (a lone -h followed by a host is no longer help) and NAME=VALUE pairs,
// which sudo accepts anywhere before the command unless they start with
// "/" or "=".
func sudoPre(argv []string, i int) int {
	if argv[i] == "-h" && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") && !isSudoEnvVar(argv[i+1]) {
		return 2
	}
	if isSudoEnvVar(argv[i]) {
		return 1
	}
	return 0
}

// isSudoEnvVar reports whether sudo reads s as a NAME=VALUE pair.
func isSudoEnvVar(s string) bool {
	return s != "" && !strings.ContainsRune("-/=", rune(s[0])) && strings.Contains(s, "=")
}

// niceAdjustment consumes nice's obsolescent "-N", "--N" and "-+N"
// adjustments, which it reads before getopt sees them.
func niceAdjustment(argv []string, i int) int {
	s := argv[i]
	k := 1
	if len(s) > 1 && (s[1] == '-' || s[1] == '+') {
		k = 2
	}
	if len(s) > k && s[0] == '-' && '0' <= s[k] && s[k] <= '9' {
		return 1
	}
	return 0
}

// unwrapLoader returns the program a direct ld.so invocation runs.
func unwrapLoader(argv []string) ([]string, bool) {
	i := 1
	for i < len(argv) {
		tok := argv[i]
		if tok == "--" {
			i++
			break
		}
		if !strings.HasPrefix(tok, "--") {
			break
		}
		switch loaderOpts[tok] {
		case optFlag:
			i++
		case optValue:
			i += 2
		default:
			return nil, false
		}
	}
	if i >= len(argv) {
		return nil, false
	}
	return argv[i:], true
}
