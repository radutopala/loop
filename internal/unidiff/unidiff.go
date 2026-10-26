// Package unidiff renders the unified diffs shown on approval cards, the
// project trust notice and learn proposal previews.
package unidiff

import (
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff renders a unified diff of a against b with three lines of context,
// labelled from and to. from is "/dev/null" when a didn't exist. Equal
// content gives "". Spec: unidiff_spec_test.go (git apply turns a into b).
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
// into an extra empty line and an empty file into one empty line. A last
// line without a newline carries git's noNewline marker instead, so it
// differs from the same line with one and the diff shows the change.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.SplitAfter(s, "\n")
	if out[len(out)-1] == "" {
		return out[:len(out)-1]
	}
	out[len(out)-1] += "\n" + noNewline
	return out
}

// noNewline follows a last line that has no newline, as in git's diffs.
const noNewline = "\\ No newline at end of file\n"

// Count returns how many lines Diff shows as added and removed going from
// a to b.
func Count(a, b string) (added, removed int) {
	for _, op := range difflib.NewMatcher(lines(a), lines(b)).GetOpCodes() {
		if op.Tag != 'e' {
			added += op.J2 - op.J1
			removed += op.I2 - op.I1
		}
	}
	return added, removed
}
