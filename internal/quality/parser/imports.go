package parser

import (
	"strings"

	"github.com/odvcencio/gotreesitter"
)

// pythonImportPaths turns a Python import into slash paths. A module
// name "a.b.c" becomes "a/b/c". A relative one keeps its level as "./"
// and "../" steps: ".sib" → "./sib", "..pkg.mod" → "../pkg/mod", and a
// bare "." or ".." names the package itself ("./__init__", "../__init__").
// A whole `from M import x, y` statement gives M and also M/x and M/y,
// since an imported name can be a submodule; names that aren't files
// resolve to nothing.
func pythonImportPaths(raw string) []string {
	from, ok := strings.CutPrefix(stripPythonComments(raw), "from ")
	if !ok {
		return []string{pythonModulePath(strings.Join(strings.Fields(raw), ""))}
	}
	module, names, _ := strings.Cut(from, " import ")
	base := pythonModulePath(strings.Join(strings.Fields(module), ""))
	out := []string{base}
	dir := strings.TrimSuffix(base, "/__init__")
	for _, n := range strings.Split(strings.Trim(strings.TrimSpace(names), "()"), ",") {
		n, _, _ = strings.Cut(strings.TrimSpace(n), " as ")
		if n == "" || n == "*" {
			continue
		}
		out = append(out, dir+"/"+n)
	}
	return out
}

// pythonModulePath is the slash path of one module name, absolute or
// relative.
func pythonModulePath(name string) string {
	dots := len(name) - len(strings.TrimLeft(name, "."))
	rest := strings.ReplaceAll(name[dots:], ".", "/")
	if dots == 0 {
		return rest
	}
	prefix := "./"
	if dots > 1 {
		prefix = strings.Repeat("../", dots-1)
	}
	if rest == "" {
		rest = "__init__"
	}
	return prefix + rest
}

// stripPythonComments drops each line's `#` comment and joins the lines
// with single spaces (a statement in parentheses or after a backslash
// can span several).
func stripPythonComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l, _, _ = strings.Cut(l, "#")
		lines[i] = strings.TrimSuffix(strings.TrimSpace(l), "\\")
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

// rustImportPaths expands a `use` tree into one path per name it brings
// in: "crate::{a::X, b::{self, Y as Z}}" → "crate::a::X", "crate::b",
// "crate::b::Y". Globs and aliases are dropped, as is a leading "::".
func rustImportPaths(raw string) []string {
	var out []string
	for _, p := range expandUseTree(strings.Join(strings.Fields(raw), " ")) {
		if p = cleanUsePath(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandUseTree flattens the brace groups of a use tree. A group is
// always the last part of its path, so whatever follows its closing
// brace is ignored.
func expandUseTree(s string) []string {
	open := strings.IndexByte(s, '{')
	if open < 0 {
		return []string{s}
	}
	prefix := s[:open]
	inner := s[open+1:]
	if end := matchingBrace(inner); end >= 0 {
		inner = inner[:end]
	}
	var out []string
	for _, item := range splitTopLevel(inner) {
		item = strings.TrimSpace(item)
		if item == "" { // a trailing comma
			continue
		}
		for _, e := range expandUseTree(item) {
			out = append(out, prefix+e)
		}
	}
	return out
}

// matchingBrace returns the index of the '}' that closes an already
// opened group in s, or -1 when s is unbalanced.
func matchingBrace(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// splitTopLevel splits s at the commas outside any brace group.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// cleanUsePath drops an alias, a glob, a trailing "self" and a leading
// "::", and the spaces around "::".
func cleanUsePath(p string) string {
	if i := strings.Index(p, " as "); i >= 0 {
		p = p[:i]
	}
	p = strings.ReplaceAll(p, " ", "")
	p = strings.TrimSuffix(p, "*")
	p = strings.TrimSuffix(p, "::")
	p = strings.TrimSuffix(p, "::self")
	return strings.TrimPrefix(p, "::")
}

// isTestAttributed reports whether a Rust function carries a test
// attribute — `#[test]` or a runner's `#[tokio::test]` and the like —
// among the attributes and comments right before it.
func isTestAttributed(lang *gotreesitter.Language, fnDef *gotreesitter.Node, source []byte) bool {
	for n := fnDef.PrevSibling(); n != nil; n = n.PrevSibling() {
		switch n.Type(lang) {
		case "line_comment", "block_comment":
			continue
		case "attribute_item":
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(n.Text(source), "#["), "]"))
			if i := strings.IndexByte(name, '('); i >= 0 {
				name = name[:i]
			}
			if name == "test" || strings.HasSuffix(name, "::test") {
				return true
			}
			continue
		}
		return false
	}
	return false
}
