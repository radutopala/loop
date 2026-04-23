// Package agentgate enforces a policy on agent-container syscalls via seccomp
// RET_USER_NOTIF. This file defines the transport-agnostic policy matcher;
// platform-specific handlers (connect, execve, file-ops) consume it.
package agentgate

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/radutopala/loop/internal/types"
)

// Policy is a compiled, ready-to-match rule set.
// Construct via CompilePolicy; never populate fields directly.
type Policy struct {
	defaultDecision types.Decision

	pathRules []compiledPathRule
	cmdRules  []compiledCommandRule
	fileRules []compiledFileRule
}

type compiledPathRule struct {
	pattern  string
	decision types.Decision
	message  string
}

type compiledCommandRule struct {
	commands []string // basenames (glob-ready)
	args     []*regexp.Regexp
	decision types.Decision
	message  string
}

type compiledFileRule struct {
	paths    []string // doublestar globs
	ops      map[string]struct{}
	decision types.Decision
	message  string
}

// MatchResult is what a matcher returns to the handler that called it.
// Message is the rule's message (empty when DefaultDecision fires).
type MatchResult struct {
	Decision types.Decision
	Message  string
	RuleID   string // "default" | "path[0]" | "cmd[2]" | "file[7]"
}

// CompilePolicy validates the rule set and returns a Policy ready to match.
// Returns an error on malformed regex, unknown operations, or empty rules.
// Unknown default_decision ("" or other) becomes DecisionAllow.
func CompilePolicy(
	defaultDecision types.Decision,
	pathRules []types.PathRule,
	cmdRules []types.CommandRule,
	fileRules []types.FileRule,
) (*Policy, error) {
	p := &Policy{defaultDecision: normalizeDefault(defaultDecision)}

	for i, r := range pathRules {
		if r.Pattern == "" {
			return nil, fmt.Errorf("path_rules[%d]: pattern is required", i)
		}
		if err := validateDecision(r.Decision); err != nil {
			return nil, fmt.Errorf("path_rules[%d]: %w", i, err)
		}
		p.pathRules = append(p.pathRules, compiledPathRule{
			pattern:  r.Pattern,
			decision: r.Decision,
			message:  r.Message,
		})
	}

	for i, r := range cmdRules {
		if err := validateDecision(r.Decision); err != nil {
			return nil, fmt.Errorf("command_rules[%d]: %w", i, err)
		}
		compiled := compiledCommandRule{
			commands: append([]string(nil), r.Commands...),
			decision: r.Decision,
			message:  r.Message,
		}
		for j, pat := range r.ArgsPatterns {
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("command_rules[%d].args_patterns[%d]: %w", i, j, err)
			}
			compiled.args = append(compiled.args, re)
		}
		p.cmdRules = append(p.cmdRules, compiled)
	}

	for i, r := range fileRules {
		if err := validateDecision(r.Decision); err != nil {
			return nil, fmt.Errorf("file_rules[%d]: %w", i, err)
		}
		compiled := compiledFileRule{
			paths:    append([]string(nil), r.Paths...),
			ops:      make(map[string]struct{}, len(r.Operations)),
			decision: r.Decision,
			message:  r.Message,
		}
		for _, op := range r.Operations {
			if !validFileOp(op) {
				return nil, fmt.Errorf("file_rules[%d]: unknown operation %q", i, op)
			}
			compiled.ops[op] = struct{}{}
		}
		// Validate each glob by running it once against a dummy path.
		for j, g := range compiled.paths {
			if _, err := doublestar.Match(g, "/"); err != nil {
				return nil, fmt.Errorf("file_rules[%d].paths[%d]: %w", i, j, err)
			}
		}
		p.fileRules = append(p.fileRules, compiled)
	}

	return p, nil
}

// MatchPath evaluates connect(2) path rules. Absolute socket path expected.
func (p *Policy) MatchPath(path string) MatchResult {
	for i, r := range p.pathRules {
		if r.pattern == path {
			return MatchResult{Decision: r.decision, Message: r.message, RuleID: fmt.Sprintf("path[%d]", i)}
		}
	}
	return p.defaultResult()
}

// MatchCommand evaluates execve(2) rules against argv[0] basename + joined argv[1:].
func (p *Policy) MatchCommand(argv0 string, argvRest []string) MatchResult {
	base := filepath.Base(argv0)
	joined := strings.Join(argvRest, " ")

	for i, r := range p.cmdRules {
		if !matchAnyGlob(base, r.commands) {
			continue
		}
		if !matchAnyRegex(joined, r.args) {
			continue
		}
		return MatchResult{Decision: r.decision, Message: r.message, RuleID: fmt.Sprintf("cmd[%d]", i)}
	}
	return p.defaultResult()
}

// MatchFile evaluates file-op rules. Path must be cleaned + absolute + symlinks
// resolved by the caller. op must be one of: read, write, create, delete, stat,
// list, chmod, chown, link.
func (p *Policy) MatchFile(op, path string) MatchResult {
	for i, r := range p.fileRules {
		if len(r.ops) > 0 {
			if _, ok := r.ops[op]; !ok {
				continue
			}
		}
		if !matchAnyDoublestar(path, r.paths) {
			continue
		}
		return MatchResult{Decision: r.decision, Message: r.message, RuleID: fmt.Sprintf("file[%d]", i)}
	}
	return p.defaultResult()
}

func (p *Policy) defaultResult() MatchResult {
	return MatchResult{Decision: p.defaultDecision, RuleID: "default"}
}

// matchAnyGlob returns true when s matches any pattern, or when patterns is empty.
func matchAnyGlob(s string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		ok, err := filepath.Match(pat, s)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func matchAnyRegex(s string, res []*regexp.Regexp) bool {
	if len(res) == 0 {
		return true
	}
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func matchAnyDoublestar(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		ok, err := doublestar.Match(pat, path)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func normalizeDefault(d types.Decision) types.Decision {
	switch d {
	case types.DecisionAllow, types.DecisionDeny, types.DecisionApprove:
		return d
	default:
		return types.DecisionAllow
	}
}

func validateDecision(d types.Decision) error {
	switch d {
	case types.DecisionAllow, types.DecisionDeny, types.DecisionApprove:
		return nil
	default:
		return fmt.Errorf("unknown decision %q (must be allow|deny|approve)", d)
	}
}

var knownFileOps = map[string]struct{}{
	"read":   {},
	"write":  {},
	"create": {},
	"delete": {},
	"stat":   {},
	"list":   {},
	"chmod":  {},
	"chown":  {},
	"link":   {},
}

func validFileOp(op string) bool {
	_, ok := knownFileOps[op]
	return ok
}
