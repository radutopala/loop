package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ScopeSuite struct {
	suite.Suite
}

func TestScopeSuite(t *testing.T) {
	suite.Run(t, new(ScopeSuite))
}

func (s *ScopeSuite) TestKnownScope() {
	for _, sc := range []Scope{ScopeAuto, ScopeFull, ScopeIncremental} {
		require.True(s.T(), KnownScope(sc), sc)
	}
	require.False(s.T(), KnownScope("partial"))
}

const (
	numstatPR    = "git diff --no-ext-diff --no-textconv --no-renames --numstat origin/main...HEAD"
	numstatSince = "git diff --no-ext-diff --no-textconv --no-renames --numstat old HEAD -- :(literal)a.go :(literal)b.go"
)

func (s *ScopeSuite) TestResolveScope() {
	fail := errors.New("exit 1")
	base := func() map[string]callResponse {
		return map[string]callResponse{
			"git rev-parse HEAD":                    {out: []byte("new\n")},
			"git cat-file -e old^{commit}":          {},
			"git merge-base --is-ancestor old HEAD": {},
			numstatPR:                               {out: []byte("80\t10\ta.go\n-\t-\tb.go\n\n")},
			numstatSince:                            {out: []byte("5\t1\ta.go\n")},
		}
	}
	tests := []struct {
		name     string
		want     Scope
		lastHead string
		edit     func(map[string]callResponse)
		expected ScopeResult
		err      string
	}{
		{name: "full asked", want: ScopeFull, lastHead: "old", expected: ScopeResult{Scope: ScopeFull}},
		{name: "never reviewed", expected: ScopeResult{Scope: ScopeFull, Reason: "no earlier review of this PR"}},
		{name: "incremental", lastHead: "old", expected: ScopeResult{Scope: ScopeIncremental, FromSHA: "old"}},
		{name: "rebased", lastHead: "old",
			edit:     func(m map[string]callResponse) { m["git merge-base --is-ancestor old HEAD"] = callResponse{err: fail} },
			expected: ScopeResult{Scope: ScopeIncremental, FromSHA: "old", Rebased: true}},
		{name: "same head", lastHead: "new", expected: ScopeResult{Scope: ScopeFull, Reason: "no new commits since the last review"}},
		{name: "commit gone", lastHead: "old",
			edit:     func(m map[string]callResponse) { m["git cat-file -e old^{commit}"] = callResponse{err: fail} },
			expected: ScopeResult{Scope: ScopeFull, Reason: "the last reviewed commit is gone"}},
		{name: "no PR files", lastHead: "old",
			edit:     func(m map[string]callResponse) { m[numstatPR] = callResponse{} },
			expected: ScopeResult{Scope: ScopeFull, Reason: "the PR changes no files"}},
		{name: "no PR file changed since", lastHead: "old",
			edit:     func(m map[string]callResponse) { m[numstatSince] = callResponse{out: []byte("garbage\nx\ty\n")} },
			expected: ScopeResult{Scope: ScopeFull, Reason: "no file of the PR changed since the last review"}},
		{name: "most of the PR changed", lastHead: "old",
			edit:     func(m map[string]callResponse) { m[numstatSince] = callResponse{out: []byte("40\t6\ta.go\n")} },
			expected: ScopeResult{Scope: ScopeFull, Reason: "the changes since the last review are most of the PR"}},
		{name: "incremental asked keeps a large range", want: ScopeIncremental, lastHead: "old",
			edit:     func(m map[string]callResponse) { m[numstatSince] = callResponse{out: []byte("40\t6\ta.go\n")} },
			expected: ScopeResult{Scope: ScopeIncremental, FromSHA: "old"}},
		{name: "rev-parse fails", lastHead: "old",
			edit: func(m map[string]callResponse) {
				m["git rev-parse HEAD"] = callResponse{out: []byte("bad\n"), err: fail}
			},
			err: "git rev-parse HEAD: bad"},
		{name: "PR numstat fails", lastHead: "old",
			edit: func(m map[string]callResponse) { m[numstatPR] = callResponse{out: []byte("no base"), err: fail} },
			err:  "git diff --numstat: no base"},
		{name: "since numstat fails", lastHead: "old",
			edit: func(m map[string]callResponse) { m[numstatSince] = callResponse{err: fail} },
			err:  "git diff --numstat: "},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			resp := base()
			if tc.edit != nil {
				tc.edit(resp)
			}
			rr := &recordingRunner{response: resp}
			g := &GitPR{Run: rr.run}
			got, err := g.ResolveScope(context.Background(), "/wt", "main", tc.want, tc.lastHead)
			if tc.err != "" {
				require.EqualError(s.T(), err, tc.err)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.expected, got)
			for _, c := range rr.calls {
				require.Equal(s.T(), "/wt", c.dir)
			}
		})
	}
}

// TestResolveScopeRealGit runs the git side against a real repo: a PR
// branch reviewed at one commit, then extended, then rebased onto a base
// that moved, then reset past the reviewed commit and pruned.
func (s *ScopeSuite) TestResolveScopeRealGit() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not installed")
	}
	dir := s.T().TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(s.T(), err, string(out))
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	lines := func(n int, word string) string { return strings.Repeat(word+"\n", n) }

	git("init", "-q", "-b", "main")
	write("base.txt", "base\n")
	git("add", ".")
	git("commit", "-qm", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("checkout", "-qb", "pr")
	write("a.go", lines(40, "a"))
	git("add", ".")
	git("commit", "-qm", "pr one")
	reviewed := git("rev-parse", "HEAD")

	g := &GitPR{Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}}
	resolve := func() ScopeResult {
		got, err := g.ResolveScope(context.Background(), dir, "main", ScopeAuto, reviewed)
		require.NoError(s.T(), err)
		return got
	}

	require.Equal(s.T(), ScopeResult{Scope: ScopeFull, Reason: "no new commits since the last review"}, resolve())

	write("a.go", lines(40, "a")+"b\n")
	git("commit", "-qam", "pr two")
	require.Equal(s.T(), ScopeResult{Scope: ScopeIncremental, FromSHA: reviewed}, resolve())

	git("checkout", "-q", "main")
	write("base.txt", "base moved\n")
	git("commit", "-qam", "base moved")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("checkout", "-q", "pr")
	git("rebase", "-q", "main")
	require.Equal(s.T(), ScopeResult{Scope: ScopeIncremental, FromSHA: reviewed, Rebased: true}, resolve())

	git("reset", "-q", "--hard", "main")
	write("a.go", "rewritten\n")
	git("add", ".")
	git("commit", "-qm", "rewritten")
	git("reflog", "expire", "--expire=now", "--all")
	git("gc", "-q", "--prune=now")
	require.Equal(s.T(), ScopeResult{Scope: ScopeFull, Reason: "the last reviewed commit is gone"}, resolve())
}
