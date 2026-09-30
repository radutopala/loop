package dockerproxy

import (
	"bytes"
	"encoding/json"
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

// errDuplicateKeys rejects a body naming one key twice in an object (see
// hasDuplicateKeys).
var errDuplicateKeys = errors.New("ambiguous request body: duplicate keys")

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

// jsonFrame is one open object or array of a token scan.
type jsonFrame struct {
	keys    map[string]bool // nil for an array
	wantKey bool
}

// hasDuplicateKeys reports whether any object in the JSON document buf holds
// the same key twice. The decoded map keeps only the last copy, as the daemon
// does today; rejecting the body keeps a rule from ever judging one copy
// while the daemon acts on the other. buf must already have parsed; a token
// error ends the scan with false.
func hasDuplicateKeys(buf []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(buf))
	var stack []*jsonFrame
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if d, ok := tok.(json.Delim); ok && (d == '}' || d == ']') {
			stack = stack[:len(stack)-1]
			continue
		}
		if len(stack) > 0 && stack[len(stack)-1].keys != nil {
			top := stack[len(stack)-1]
			if top.wantKey {
				k, _ := tok.(string)
				if top.keys[k] {
					return true
				}
				top.keys[k] = true
				top.wantKey = false
				continue
			}
			top.wantKey = true
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, &jsonFrame{keys: map[string]bool{}, wantKey: true})
		case json.Delim('['):
			stack = append(stack, &jsonFrame{})
		}
	}
}
