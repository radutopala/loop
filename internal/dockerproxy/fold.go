package dockerproxy

import (
	"errors"
	"strings"
)

// The daemon decodes request bodies with encoding/json, which matches
// struct field names case-insensitively: {"hostconfig":{"privileged":true}}
// creates a privileged container. Everything that reads a body here must
// match keys the same way, and reject bodies holding two case variants of
// a key it reads: the daemon keeps one of them depending on key order, which
// the decoded map has lost.

var errAmbiguousKeys = errors.New("ambiguous request body: keys differ only in case")

// foldKey returns the key of m equal to key under case folding.
func foldKey(m map[string]any, key string) (string, bool) {
	for k := range m {
		if strings.EqualFold(k, key) {
			return k, true
		}
	}
	return "", false
}

// foldGet returns the value under key (case-insensitive) in m.
func foldGet(m map[string]any, key string) (any, bool) {
	k, ok := foldKey(m, key)
	return m[k], ok
}

// foldString returns the string under key (case-insensitive) in m, empty
// when absent or not a string.
func foldString(m map[string]any, key string) string {
	v, _ := foldGet(m, key)
	s, _ := v.(string)
	return s
}

// hasFoldDuplicates reports whether any object in v holds two keys that
// differ only in case and fold to one of names (lower-cased). Only those
// keys matter: free-form maps such as Labels may legitimately hold "foo"
// and "FOO".
func hasFoldDuplicates(v any, names map[string]bool) bool {
	switch t := v.(type) {
	case map[string]any:
		seen := make(map[string]bool, len(t))
		for k, child := range t {
			lk := strings.ToLower(k)
			if names[lk] && seen[lk] {
				return true
			}
			seen[lk] = true
			if hasFoldDuplicates(child, names) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if hasFoldDuplicates(child, names) {
				return true
			}
		}
	}
	return false
}

// addFoldNames adds the lower-cased names to set.
func addFoldNames(set map[string]bool, names ...string) {
	for _, n := range names {
		set[strings.ToLower(n)] = true
	}
}
