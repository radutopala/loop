// Package dockerproxy intercepts agent container -> docker daemon HTTP traffic
// and enforces per-method, per-path, and per-body rules. It shares the
// agentgate.Manager for approval prompts (three-button once/session/deny UI).
//
// Design inspired by agentsh (agentsh.org) — independent implementation.
package dockerproxy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/radutopala/loop/internal/types"
)

// Policy is a compiled, ready-to-match HTTP + body rule set.
// Construct via CompilePolicy; never populate fields directly.
type Policy struct {
	defaultDecision types.Decision
	httpRules       []compiledHTTPRule
	bodyRules       []compiledBodyRule
}

type compiledHTTPRule struct {
	anyMethod bool
	methods   map[string]struct{}
	paths     []*regexp.Regexp
	decision  types.Decision
	message   string
}

type compiledBodyRule struct {
	anyMethod    bool
	methods      map[string]struct{}
	pathRe       *regexp.Regexp
	contentTypes map[string]struct{}
	maxBodyBytes int64
	checks       []compiledJSONCheck
	decision     types.Decision
	message      string
}

type compiledJSONCheck struct {
	segments []pathSegment
	op       string
	values   []string
	valuesRe []*regexp.Regexp
	// resolveSymlinks, when non-nil, is applied to source paths before regex
	// match in the source_path_in op. Stamped per-check by SetSymlinkResolver.
	resolveSymlinks SymlinkResolver
	// parentDecision is the enclosing body rule's decision (copied at compile
	// time). Used by source_path_in: resolve-failure fires the rule only when
	// it's a deny rule, so an allow/approve rule can't accidentally green-light
	// a non-resolvable (suspect) source path.
	parentDecision types.Decision
}

// SymlinkResolver returns the canonical absolute path of a name, resolving
// any symlinks. Implementations should mirror filepath.EvalSymlinks: returning
// a non-nil error when the chain is broken or the target does not exist.
type SymlinkResolver func(path string) (string, error)

// pathSegment is one element of a JSONPath-lite expression.
// Either Name is set (field access) or Wildcard=true (array index wildcard).
type pathSegment struct {
	name     string
	wildcard bool
}

// HTTPMatchResult is what MatchHTTP returns.
type HTTPMatchResult struct {
	Decision types.Decision
	Message  string
	RuleID   string // "default" | "http[N]"
}

// BodyCheckResult is what CheckBody returns. Fired=true means some body rule
// matched and its Decision should be applied immediately, bypassing the parent
// HTTP rule's decision. Fired=false means no body rule fired (continue to the
// HTTP-rule decision).
type BodyCheckResult struct {
	Fired    bool
	Decision types.Decision
	Message  string
	RuleID   string // "body[N]" when Fired
	Skipped  string // reason body-rule eval was skipped ("" when not skipped)
}

// CompilePolicy validates the rule set and returns a Policy ready to match.
// Unknown default decisions fall back to DecisionApprove (the safer default
// for the Docker surface — unknown endpoints prompt rather than auto-allow).
func CompilePolicy(
	defaultDecision types.Decision,
	httpRules []types.HTTPServiceRule,
	bodyRules []types.BodyRule,
) (*Policy, error) {
	p := &Policy{defaultDecision: normalizeDefault(defaultDecision)}

	for i, r := range httpRules {
		if err := validateDecision(r.Decision); err != nil {
			return nil, fmt.Errorf("http_rules[%d]: %w", i, err)
		}
		cr := compiledHTTPRule{decision: r.Decision, message: r.Message}
		cr.anyMethod, cr.methods = compileMethods(r.Methods)
		for j, pat := range r.Paths {
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("http_rules[%d].paths[%d]: %w", i, j, err)
			}
			cr.paths = append(cr.paths, re)
		}
		p.httpRules = append(p.httpRules, cr)
	}

	for i, r := range bodyRules {
		if err := validateDecision(r.Decision); err != nil {
			return nil, fmt.Errorf("body_rules[%d]: %w", i, err)
		}
		method, pathRe, err := parseAppliesTo(r.AppliesTo)
		if err != nil {
			return nil, fmt.Errorf("body_rules[%d].applies_to: %w", i, err)
		}
		cr := compiledBodyRule{
			pathRe:       pathRe,
			maxBodyBytes: r.MaxBodyBytes,
			decision:     r.Decision,
			message:      r.Message,
		}
		cr.anyMethod, cr.methods = compileMethods([]string{method})
		if len(r.ContentTypes) > 0 {
			cr.contentTypes = make(map[string]struct{}, len(r.ContentTypes))
			for _, ct := range r.ContentTypes {
				cr.contentTypes[strings.ToLower(ct)] = struct{}{}
			}
		}
		for j, chk := range r.JSONChecks {
			compiled, err := compileJSONCheck(chk)
			if err != nil {
				return nil, fmt.Errorf("body_rules[%d].json_checks[%d]: %w", i, j, err)
			}
			compiled.parentDecision = r.Decision
			cr.checks = append(cr.checks, compiled)
		}
		p.bodyRules = append(p.bodyRules, cr)
	}

	return p, nil
}

// SetSymlinkResolver wires a symlink resolver into every source_path_in check
// of every body rule. Source paths are resolved (via r) before the regex match
// so an agent that submits a Bind whose source side is a symlink to a denied
// path cannot bypass the rule. Pass nil to disable resolution (the original
// behaviour — match the literal source string).
//
// Resolve failures are treated as a positive match for deny rules only; allow/
// approve rules with source_path_in are not auto-fired on failure (so a missing
// or broken path does not accidentally green-light a request).
func (p *Policy) SetSymlinkResolver(r SymlinkResolver) {
	for i := range p.bodyRules {
		for j := range p.bodyRules[i].checks {
			if p.bodyRules[i].checks[j].op == "source_path_in" {
				p.bodyRules[i].checks[j].resolveSymlinks = r
			}
		}
	}
}

// MatchHTTP evaluates method+path rules. Path should already have the Docker
// API version prefix (e.g. /v1.41) stripped by the caller.
func (p *Policy) MatchHTTP(method, path string) HTTPMatchResult {
	method = strings.ToUpper(method)
	for i, r := range p.httpRules {
		if !r.anyMethod {
			if _, ok := r.methods[method]; !ok {
				continue
			}
		}
		for _, re := range r.paths {
			if re.MatchString(path) {
				return HTTPMatchResult{
					Decision: r.decision,
					Message:  r.message,
					RuleID:   fmt.Sprintf("http[%d]", i),
				}
			}
		}
	}
	return HTTPMatchResult{Decision: p.defaultDecision, RuleID: "default"}
}

// CheckBody evaluates body rules whose AppliesTo matches the current request.
// When a rule's body JSON triggers any of its JSONChecks, the rule fires and
// its Decision is returned; when no rule fires, returns Fired=false.
//
// contentType is the raw Content-Type header value; body is the already-buffered
// request body (nil or empty when there is none). Body parsing is the caller's
// responsibility — we pass a pre-decoded value to keep CheckBody allocation-free
// on the hot path.
func (p *Policy) CheckBody(method, path, contentType string, body any) BodyCheckResult {
	method = strings.ToUpper(method)
	normalizedCT := normalizeContentType(contentType)
	for i, r := range p.bodyRules {
		if !r.anyMethod {
			if _, ok := r.methods[method]; !ok {
				continue
			}
		}
		if !r.pathRe.MatchString(path) {
			continue
		}
		if len(r.contentTypes) > 0 {
			if _, ok := r.contentTypes[normalizedCT]; !ok {
				return BodyCheckResult{Skipped: "content-type-mismatch"}
			}
		}
		for _, chk := range r.checks {
			if chk.match(body) {
				return BodyCheckResult{
					Fired:    true,
					Decision: r.decision,
					Message:  r.message,
					RuleID:   fmt.Sprintf("body[%d]", i),
				}
			}
		}
	}
	return BodyCheckResult{}
}

// MaxBodyBytes returns the largest MaxBodyBytes across body rules whose
// AppliesTo matches method+path. 0 means "no body-rule applies" — caller may
// skip body parsing.
func (p *Policy) MaxBodyBytes(method, path string) int64 {
	method = strings.ToUpper(method)
	var best int64
	for _, r := range p.bodyRules {
		if !r.anyMethod {
			if _, ok := r.methods[method]; !ok {
				continue
			}
		}
		if !r.pathRe.MatchString(path) {
			continue
		}
		if r.maxBodyBytes > best {
			best = r.maxBodyBytes
		}
	}
	return best
}

// parseAppliesTo parses "METHOD pathRegex" (e.g. "POST ^/containers/create$").
// Whitespace-tolerant; method becomes uppercased. The outer TrimSpace
// guarantees pathPat is non-empty whenever a separator is found.
func parseAppliesTo(s string) (string, *regexp.Regexp, error) {
	s = strings.TrimSpace(s)
	idx := strings.IndexAny(s, " \t")
	if idx <= 0 {
		return "", nil, fmt.Errorf("expected \"METHOD pathRegex\", got %q", s)
	}
	method := strings.ToUpper(s[:idx])
	pathPat := strings.TrimSpace(s[idx+1:])
	re, err := regexp.Compile(pathPat)
	if err != nil {
		return "", nil, fmt.Errorf("path regex: %w", err)
	}
	return method, re, nil
}

func compileMethods(methods []string) (any bool, set map[string]struct{}) {
	if len(methods) == 0 {
		return true, nil
	}
	set = make(map[string]struct{}, len(methods))
	for _, m := range methods {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m == "*" {
			return true, nil
		}
		set[m] = struct{}{}
	}
	return false, set
}

func compileJSONCheck(c types.JSONCheck) (compiledJSONCheck, error) {
	segments, err := parseJSONPath(c.Path)
	if err != nil {
		return compiledJSONCheck{}, fmt.Errorf("path %q: %w", c.Path, err)
	}
	compiled := compiledJSONCheck{segments: segments, op: c.Op, values: append([]string(nil), c.Values...)}
	switch c.Op {
	case "source_path_in":
		for j, v := range c.Values {
			re, err := regexp.Compile(v)
			if err != nil {
				return compiledJSONCheck{}, fmt.Errorf("values[%d] regex: %w", j, err)
			}
			compiled.valuesRe = append(compiled.valuesRe, re)
		}
	case "equals", "contains_any", "starts_with_any":
		// Values is a simple string set; no pre-compilation needed.
		if len(c.Values) == 0 {
			return compiledJSONCheck{}, fmt.Errorf("op %q requires at least one value", c.Op)
		}
	case "present", "empty_array":
		// No values; the check is purely structural.
	default:
		return compiledJSONCheck{}, fmt.Errorf("unknown op %q", c.Op)
	}
	return compiled, nil
}

// parseJSONPath accepts "a.b.c", "a.b[*]", "a[*].b", "a.b[*].c.d".
// Rejects: empty, leading dot, trailing dot, bare "[*]", nested brackets.
func parseJSONPath(s string) ([]pathSegment, error) {
	if s == "" {
		return nil, fmt.Errorf("empty path")
	}
	var segs []pathSegment
	i := 0
	for i < len(s) {
		// Read field name up to '.' or '['.
		j := i
		for j < len(s) && s[j] != '.' && s[j] != '[' {
			j++
		}
		if j == i {
			return nil, fmt.Errorf("empty field at offset %d", i)
		}
		segs = append(segs, pathSegment{name: s[i:j]})
		i = j
		// Zero or more "[*]" suffixes.
		for i < len(s) && s[i] == '[' {
			if i+2 >= len(s) || s[i+1] != '*' || s[i+2] != ']' {
				return nil, fmt.Errorf("expected [*] at offset %d", i)
			}
			segs = append(segs, pathSegment{wildcard: true})
			i += 3
		}
		if i < len(s) {
			if s[i] != '.' {
				return nil, fmt.Errorf("expected '.' or '[' at offset %d, got %q", i, s[i])
			}
			i++ // consume '.'
			if i == len(s) {
				return nil, fmt.Errorf("trailing dot")
			}
		}
	}
	return segs, nil
}

func normalizeDefault(d types.Decision) types.Decision {
	switch d {
	case types.DecisionAllow, types.DecisionDeny, types.DecisionApprove:
		return d
	default:
		return types.DecisionApprove
	}
}

func validateDecision(d types.Decision) error {
	switch d {
	case types.DecisionAllow, types.DecisionDeny, types.DecisionApprove:
		return nil
	default:
		return fmt.Errorf("unknown decision %q", d)
	}
}

func normalizeContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return ct
}
