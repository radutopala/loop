// unidiff_spec_test.go checks Diff and Count as a spec, against git.
//
// The property: for any texts a and b, git apply run on a file holding a
// with Diff's output for a against b leaves the file holding exactly b,
// byte for byte, a missing final newline, CRLF line ends, unicode and
// repeated lines included. Diff gives "" exactly when a and b are equal,
// and Count(a, b) is the number of + and - lines of that diff.
//
// The spec can fail: TestSpecCanFail runs the checker with a Diff that
// pads a missing final newline, as difflib.SplitLines does, and with a
// Count that misses replaced lines, and requires a counterexample of each.
//
// Trusted base: the git on PATH (its apply, outside any repo, with no
// system or global config) and the stdlib.
package unidiff

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// impl is the pair of functions the checker checks.
type impl struct {
	diff  func(from, to, a, b string) string
	count func(a, b string) (added, removed int)
}

var shipped = impl{diff: Diff, count: Count}

// gitApplier applies patches with git in an empty sandbox.
type gitApplier struct {
	git string
	env []string
}

// newGitApplier finds git, skipping tb when there's none.
func newGitApplier(tb testing.TB) *gitApplier {
	tb.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		tb.Skip("git not installed")
	}
	home := tb.TempDir()
	return &gitApplier{git: git, env: append(os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(home),
	)}
}

// apply runs git apply with patch in a fresh dir that holds x as a, or
// no x when a is nil, and returns what x holds afterwards.
func (g *gitApplier) apply(tb testing.TB, a *string, patch string) (string, error) {
	tb.Helper()
	dir := tb.TempDir()
	x := filepath.Join(dir, "x")
	if a != nil {
		require.NoError(tb, os.WriteFile(x, []byte(*a), 0o644))
	}
	cmd := exec.Command(g.git, "apply", "--whitespace=nowarn", "-")
	cmd.Dir = dir
	cmd.Env = g.env
	cmd.Stdin = strings.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git apply: %w: %s", err, out)
	}
	got, err := os.ReadFile(x)
	if err != nil {
		return "", err
	}
	return string(got), nil
}

// check checks the property for a against b, as an existing file and, when
// a is empty, as a new one. It returns the first way im breaks it.
func (g *gitApplier) check(tb testing.TB, im impl, a, b string) error {
	tb.Helper()
	type start struct {
		from string
		a    *string
	}
	starts := []start{{"a/x", &a}}
	if a == "" {
		starts = append(starts, start{"/dev/null", nil})
	}
	for _, st := range starts {
		patch := im.diff(st.from, "b/x", a, b)
		if (patch == "") != (a == b) {
			return fmt.Errorf("from %s: Diff(%q, %q) = %q: empty iff equal", st.from, a, b, patch)
		}
		if patch == "" {
			continue
		}
		got, err := g.apply(tb, st.a, patch)
		if err != nil {
			return fmt.Errorf("from %s: Diff(%q, %q) = %q: %w", st.from, a, b, patch, err)
		}
		if got != b {
			return fmt.Errorf("from %s: Diff(%q, %q) = %q applies to %q", st.from, a, b, patch, got)
		}
	}
	added, removed := im.count(a, b)
	wantAdded, wantRemoved := countPatch(im.diff("a/x", "b/x", a, b))
	if added != wantAdded || removed != wantRemoved {
		return fmt.Errorf("Count(%q, %q) = %d, %d; the diff has %d, %d", a, b, added, removed, wantAdded, wantRemoved)
	}
	return nil
}

// countPatch counts patch's + and - lines, past its --- and +++ header.
func countPatch(patch string) (added, removed int) {
	inHunk := false
	for line := range strings.Lines(patch) {
		switch {
		case strings.HasPrefix(line, "@@ "):
			inHunk = true
		case !inHunk:
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}

// genText builds a text from r out of a few lines, repeats likely: empty,
// unicode, a lone \r inside a line and a line that looks like a diff
// marker. Line ends are \n or \r\n, and the final one may be missing.
func genText(r *rand.Rand) string {
	pool := []string{"a", "b", "a", "", "ünï€😀", "x\ry", "\\ No newline at end of file", "--- a/x", "  ind"}
	var buf bytes.Buffer
	crlf := r.IntN(3) == 0
	n := r.IntN(9)
	for i := range n {
		buf.WriteString(pool[r.IntN(len(pool))])
		if i < n-1 || r.IntN(3) > 0 {
			if crlf {
				buf.WriteString("\r\n")
			} else {
				buf.WriteString("\n")
			}
		}
	}
	return buf.String()
}

// genPair builds b as an edit of a, so most pairs share lines.
func genPair(r *rand.Rand) (string, string) {
	a := genText(r)
	if r.IntN(4) == 0 {
		return a, genText(r)
	}
	ls := strings.SplitAfter(a, "\n")
	for range r.IntN(4) {
		i := r.IntN(len(ls))
		switch r.IntN(3) {
		case 0:
			ls = append(ls[:i], ls[i+1:]...)
		case 1:
			ls = append(ls[:i], append([]string{genText(r)}, ls[i:]...)...)
		default:
			ls[i] = strings.TrimSuffix(ls[i], "\n")
		}
		if len(ls) == 0 {
			ls = []string{""}
		}
	}
	return a, strings.Join(ls, "")
}

// specCases is the fixed cases, then n generated ones.
func specCases(n int) [][2]string {
	cases := [][2]string{
		{"", ""},
		{"", "a\n"},
		{"a\n", ""},
		{"", "a"},
		{"a", ""},
		{"a", "a\n"},
		{"a\n", "a"},
		{"a", "b"},
		{"a\nb", "a\nc"},
		{"1\n2\n3\n4\n5\n6\n7\n8\n9", "1\n2\n3\n4\n5\n6\n7\n8\n9\n"},
		{"a\r\nb\r\n", "a\nb\n"},
		{"a\r\nb", "a\r\nb\r\n"},
		{"x\r", "x\r\n"},
		{"ü\n€\n", "ü\n😀\n"},
		{"a\na\na\na\na\na\na\na\n", "a\na\na\na\nb\na\na\na\na\n"},
		{"\n\n\n", "\n\n"},
	}
	r := rand.New(rand.NewPCG(1, 2))
	for range n {
		a, b := genPair(r)
		cases = append(cases, [2]string{a, b})
	}
	return cases
}

type UnidiffSpecSuite struct {
	suite.Suite
}

func TestUnidiffSpecSuite(t *testing.T) {
	suite.Run(t, new(UnidiffSpecSuite))
}

// TestSpec checks the property for the fixed and generated cases.
func (s *UnidiffSpecSuite) TestSpec() {
	g := newGitApplier(s.T())
	for _, c := range specCases(150) {
		require.NoError(s.T(), g.check(s.T(), shipped, c[0], c[1]))
	}
}

// paddedDiff is Diff as it would be with difflib.SplitLines's padding: a
// missing final newline is added, so it doesn't show.
func paddedDiff(from, to, a, b string) string {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return difflib.SplitLines(strings.TrimSuffix(s, "\n"))
	}
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: split(a), B: split(b), FromFile: from, ToFile: to, Context: 3})
	return diff
}

// TestSpecCanFail: the checker finds a counterexample for each broken
// implementation.
func (s *UnidiffSpecSuite) TestSpecCanFail() {
	g := newGitApplier(s.T())
	cases := []struct {
		name string
		im   impl
	}{
		{"diff pads a missing final newline", impl{diff: paddedDiff, count: Count}},
		{"count misses replaced lines", impl{diff: Diff, count: func(a, b string) (int, int) {
			added, removed := Count(a, b)
			return added - min(added, removed), removed - min(added, removed)
		}}},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			found := false
			for _, p := range specCases(150) {
				if g.check(s.T(), c.im, p[0], p[1]) != nil {
					found = true
					break
				}
			}
			require.True(s.T(), found, "no counterexample")
		})
	}
}

// FuzzDiffAppliesWithGit checks the property for any pair of texts without
// a NUL byte, which would make git treat them as binary. Seeds are in
// testdata/fuzz/FuzzDiffAppliesWithGit.
func FuzzDiffAppliesWithGit(f *testing.F) {
	g := newGitApplier(f)
	f.Fuzz(func(t *testing.T, a, b string) {
		if strings.ContainsRune(a+b, 0) {
			return
		}
		require.NoError(t, g.check(t, shipped, a, b))
	})
}
