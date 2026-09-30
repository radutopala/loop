// Package unidiff renders the unified diffs shown on approval cards, the
// project trust notice and learn proposal previews.
package unidiff

import (
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff renders a unified diff of a against b with three lines of context,
// labelled from and to. from is "/dev/null" when a didn't exist. Equal
// content gives "".
func Diff(from, to, a, b string) string {
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        lines(a),
		B:        lines(b),
		FromFile: from,
		ToFile:   to,
		Context:  3,
	})
	return diff
}

// lines splits s into newline-terminated lines. difflib.SplitLines would
// add a newline to the last element, turning a file's own final newline
// into an extra empty line and an empty file into one empty line.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.SplitAfter(s, "\n")
	if out[len(out)-1] == "" {
		return out[:len(out)-1]
	}
	out[len(out)-1] += "\n"
	return out
}
