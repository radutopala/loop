package dockerproxy

import (
	"path"
	"regexp"
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
	// Case-insensitive, as the daemon decodes (see fold.go).
	next, exists := foldGet(obj, seg.name)
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
	case "not_in":
		return stringMatch(value, func(s string) bool {
			return !slices.Contains(c.values, s)
		})
	case "capability_not_in":
		return stringMatch(value, func(s string) bool {
			return !slices.Contains(c.values, normalizeCapability(s))
		})
	case "source_path_in":
		return stringMatch(value, func(s string) bool {
			return c.sourcePathIn(extractSourcePath(s))
		})
	case "source_path_not_in":
		return stringMatch(value, func(s string) bool {
			return c.sourcePathNotIn(extractSourcePath(s))
		})
	}
	return false
}

// sourcePathIn reports whether a bind source matches the check's regexes,
// literally or once symlinks are resolved. An agent that creates
// `/workdir/link → /` then submits `-v /workdir/link:/host` is the bypass the
// resolution closes: the literal source doesn't match `^/$` but the resolved
// one does. A resolved source matching an Except regex never fires.
func (c compiledJSONCheck) sourcePathIn(src string) bool {
	literal := matchAny(c.valuesRe, src)
	// Docker's HostConfig.Binds[] overloads the "<source>:<target>[:mode]"
	// string for named volumes — `myvolume:/target:rw` extracts to a source
	// of `myvolume`, which is not a host path and would predictably fail
	// symlink resolution, falsely firing a deny. Only the literal applies.
	if !strings.HasPrefix(src, "/") || (literal && len(c.exceptRe) == 0) {
		return literal
	}
	resolved := path.Clean(src)
	if c.resolveSymlinks != nil {
		r, err := c.resolveSymlinks(src)
		if err != nil {
			// Path can't be resolved (broken chain, target missing, EACCES,
			// etc.). Fire a deny rule regardless — otherwise an allow/approve
			// rule would inadvertently green-light a suspect path.
			return literal || c.parentDecision == types.DecisionDeny
		}
		resolved = path.Clean(r)
	}
	if matchAny(c.exceptRe, resolved) {
		return false
	}
	return literal || matchAny(c.valuesRe, resolved)
}

// sourcePathNotIn reports whether a host-path bind source lies outside the
// check's regexes once symlinks are resolved. Named volumes never fire. A
// source that can't be resolved fires unless the rule allows: it can't be
// shown to be inside.
func (c compiledJSONCheck) sourcePathNotIn(src string) bool {
	if !strings.HasPrefix(src, "/") {
		return false
	}
	resolved := path.Clean(src)
	if c.resolveSymlinks != nil {
		r, err := c.resolveSymlinks(src)
		if err != nil {
			return c.parentDecision != types.DecisionAllow
		}
		resolved = path.Clean(r)
	}
	return !matchAny(c.valuesRe, resolved)
}

// matchAny reports whether any of res matches s.
func matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
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

// normalizeCapability maps a capability name to the form the daemon grants
// it under: case-insensitive, with an optional CAP_ prefix, so "cap_sys_admin"
// and "Sys_Admin" both mean SYS_ADMIN. "ALL" stays "ALL".
func normalizeCapability(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.TrimPrefix(s, "CAP_")
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
