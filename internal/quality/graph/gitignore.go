package graph

import (
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// readFileFunc is the file-read abstraction the gitignore layer depends on.
// Production uses os.ReadFile; tests inject fakes to provoke read errors
// without touching the filesystem.
type readFileFunc func(name string) ([]byte, error)

// gitignoreRule is one parsed .gitignore line.
type gitignoreRule struct {
	// pattern is the glob with any "!" prefix, leading "/" and trailing
	// "/" removed.
	pattern string
	// negate marks a "!pattern" line, which re-includes what an earlier
	// rule ignored.
	negate bool
	// dirOnly marks a trailing-"/" line, which matches directories only.
	dirOnly bool
	// anchored marks a pattern with a "/" at its start or middle. It
	// matches against the path relative to its .gitignore's directory;
	// an unanchored pattern matches the basename at any depth below it.
	anchored bool
}

// matches reports whether the rule applies to rel, the slash-separated
// path relative to the rule's .gitignore directory.
func (r gitignoreRule) matches(rel string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	target := path.Base(rel)
	if r.anchored {
		target = rel
	}
	// Git's "dir/**" matches what is inside dir, not dir itself, so a
	// later "!dir/keep" can still re-include a file there. doublestar's
	// "**" also matches zero segments, which would prune dir whole.
	if prefix, ok := strings.CutSuffix(r.pattern, "/**"); ok {
		if self, _ := doublestar.Match(prefix, target); self {
			return false
		}
	}
	ok, _ := doublestar.Match(r.pattern, target)
	return ok
}

// loadGitignore reads and parses one ignore file. A missing or unreadable
// file has no rules: the scan goes on without it, wherever it sits,
// rather than failing.
func loadGitignore(readFile readFileFunc, gitignorePath string) []gitignoreRule {
	data, err := readFile(gitignorePath)
	if err != nil {
		return nil
	}
	return parseGitignore(string(data))
}

// parseGitignore parses a .gitignore body into rules, in file order.
// Blank and comment lines are skipped; a backslash before a leading "#"
// or "!" makes it literal. Other backslash escapes stay in the pattern
// for the glob matcher. As in git, leading spaces are part of the
// pattern and trailing ones are dropped unless escaped ("foo\ ").
func parseGitignore(content string) []gitignoreRule {
	var rules []gitignoreRule
	for raw := range strings.SplitSeq(content, "\n") {
		line := trimTrailingSpaces(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r gitignoreRule
		if rest, ok := strings.CutPrefix(line, "!"); ok {
			r.negate = true
			line = rest
		} else if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		}
		if rest, ok := strings.CutSuffix(line, "/"); ok {
			r.dirOnly = true
			line = rest
		}
		r.anchored = strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		r.pattern = escapeBraces(line)
		rules = append(rules, r)
	}
	return rules
}

// trimTrailingSpaces drops trailing spaces, stopping at one escaped with
// a backslash.
func trimTrailingSpaces(s string) string {
	for strings.HasSuffix(s, " ") {
		trimmed := s[:len(s)-1]
		if backslashes := len(trimmed) - len(strings.TrimRight(trimmed, `\`)); backslashes%2 == 1 {
			return s
		}
		s = trimmed
	}
	return s
}

// escapeBraces makes "{" and "}" literal. Git's wildmatch has no brace
// alternation, but doublestar would read "*.{js,ts}" as two patterns.
// Characters already escaped with a backslash are copied as they are.
func escapeBraces(p string) string {
	if !strings.ContainsAny(p, "{}") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '\\' && i+1 < len(p):
			b.WriteByte(c)
			i++
			b.WriteByte(p[i])
		case c == '{' || c == '}':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// gitignoreSet holds the rules of every .gitignore found so far, keyed by
// the slash-terminated directory that holds the file ("" for the root).
type gitignoreSet map[string][]gitignoreRule

// ignored applies git's precedence to rel: the rules of each ancestor
// directory's .gitignore, root first, with the last matching rule
// winning, so a deeper file and a later line override earlier ones.
func (s gitignoreSet) ignored(rel string, isDir bool) bool {
	ignored := false
	prefix := ""
	for {
		for _, r := range s[prefix] {
			if r.matches(rel[len(prefix):], isDir) {
				ignored = !r.negate
			}
		}
		next := strings.IndexByte(rel[len(prefix):], '/')
		if next < 0 {
			return ignored
		}
		prefix = rel[:len(prefix)+next+1]
	}
}
