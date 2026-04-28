package dockerproxy

import (
	"slices"
	"strings"

	"github.com/radutopala/loop/internal/types"
)

// match returns true when applying the compiled check to the decoded JSON
// body hits at least one matching leaf value.
//
// Nil body never matches anything (nothing to inspect); a missing path does
// not match `present` or `empty_array` — only values the client actually
// sent can trigger a deny.
func (c compiledJSONCheck) match(body any) bool {
	if body == nil {
		return false
	}
	return walkPath(c, body, 0)
}

// walkPath walks segments[depth..] starting at value.
func walkPath(c compiledJSONCheck, value any, depth int) bool {
	if depth == len(c.segments) {
		return evalAtLeaf(c, value)
	}
	seg := c.segments[depth]
	if seg.wildcard {
		arr, ok := value.([]any)
		if !ok {
			return false
		}
		for _, elem := range arr {
			if walkPath(c, elem, depth+1) {
				return true
			}
		}
		return false
	}
	// Field access — value must be a map.
	obj, ok := value.(map[string]any)
	if !ok {
		return false
	}
	next, exists := obj[seg.name]
	if !exists {
		return false
	}
	return walkPath(c, next, depth+1)
}

// evalAtLeaf applies the op to the value that sits at the end of the path.
func evalAtLeaf(c compiledJSONCheck, value any) bool {
	switch c.op {
	case "present":
		return isPresent(value)
	case "empty_array":
		arr, ok := value.([]any)
		return ok && len(arr) == 0
	case "equals":
		return stringMatch(value, func(s string) bool {
			return slices.Contains(c.values, s)
		})
	case "contains_any":
		return stringMatch(value, func(s string) bool {
			return slices.Contains(c.values, s)
		})
	case "starts_with_any":
		return stringMatch(value, func(s string) bool {
			for _, v := range c.values {
				if strings.HasPrefix(s, v) {
					return true
				}
			}
			return false
		})
	case "source_path_in":
		return stringMatch(value, func(s string) bool {
			src := extractSourcePath(s)
			if src == "" {
				return false
			}
			// First, check the literal source string (covers the no-symlink
			// case and the case where the agent submits a denied path
			// outright).
			for _, re := range c.valuesRe {
				if re.MatchString(src) {
					return true
				}
			}
			// Docker's HostConfig.Binds[] overloads the "<source>:<target>[:mode]"
			// string for named volumes — `myvolume:/target:rw` extracts to a
			// source of `myvolume`, which is not a host path. Such strings
			// can never match the deny regexes (all anchored absolute paths
			// like `^/etc(/|$)`) and would predictably fail symlink resolution
			// below, falsely firing the deny via the resolve-failure branch.
			// Skip the symlink fallback for non-absolute sources.
			if !strings.HasPrefix(src, "/") {
				return false
			}
			if c.resolveSymlinks == nil {
				return false
			}
			// Resolve and re-check. An agent that creates `/workdir/link → /`
			// then submits `-v /workdir/link:/host` is the bypass we close
			// here: the literal source doesn't match `^/$` but the resolved
			// one does.
			resolved, err := c.resolveSymlinks(src)
			if err != nil {
				// Path can't be resolved (broken chain, target missing,
				// EACCES, etc.). Fire the rule only when it's a deny —
				// otherwise an allow/approve rule would inadvertently
				// green-light a suspect path. Deny rules are the typical
				// shape for source_path_in.
				return c.parentDecision == types.DecisionDeny
			}
			if resolved == src {
				return false
			}
			for _, re := range c.valuesRe {
				if re.MatchString(resolved) {
					return true
				}
			}
			return false
		})
	}
	return false
}

// stringMatch coerces value to a string (or a bool rendered as "true"/"false")
// and calls pred on it. Arrays at the leaf are unwrapped one level so a
// path "X[*]" that ends in a string-array element is handled consistently.
func stringMatch(value any, pred func(string) bool) bool {
	switch v := value.(type) {
	case string:
		return pred(v)
	case bool:
		if v {
			return pred("true")
		}
		return pred("false")
	case []any:
		// Defensive: treat string arrays at the leaf the same as wildcards.
		for _, elem := range v {
			if stringMatch(elem, pred) {
				return true
			}
		}
	}
	return false
}

// extractSourcePath pulls the source side out of a Docker "Bind" string,
// which is formatted as "source:target[:mode]". Falls back to the raw string
// when no colon is present. The source is the portion the agent may have
// lied about (symlink, absolute root, etc.).
func extractSourcePath(bind string) string {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return ""
	}
	if src, _, ok := strings.Cut(bind, ":"); ok {
		return src
	}
	return bind
}

// isPresent is true when a value is non-nil and non-empty.
// - non-empty string
// - non-empty array (any element)
// - any non-empty map
// - any non-zero number / any bool
// Explicit null and zero-length arrays/objects are "not present".
func isPresent(value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}
