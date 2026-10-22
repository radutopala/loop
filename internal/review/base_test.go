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

type BaseSuite struct {
	suite.Suite
}

func TestBaseSuite(t *testing.T) {
	suite.Run(t, new(BaseSuite))
}

func (s *BaseSuite) TestUpdateBaseRequiresInputs() {
	g := &GitPR{Run: (&recordingRunner{}).run}
	_, err := g.UpdateBase(context.Background(), "", "main", false)
	require.Error(s.T(), err)
	_, err = g.UpdateBase(context.Background(), "/repo", "", false)
	require.Error(s.T(), err)
}

// TestUpdateBaseGitErrors covers each git step failing, which the real
// repo below can't easily make happen.
func (s *BaseSuite) TestUpdateBaseGitErrors() {
	const (
		local    = "git rev-parse --verify --quiet refs/heads/main^{commit}"
		remote   = "git rev-parse --verify --quiet refs/remotes/origin/main^{commit}"
		list     = "git worktree list --porcelain"
		checkout = "worktree /repo\nHEAD aaa\nbranch refs/heads/main\n"
	)
	fail := callResponse{out: []byte("boom"), err: errors.New("exit 1")}
	behind := map[string]callResponse{local: {out: []byte("aaa\n")}, remote: {out: []byte("bbb\n")}}
	with := func(extra map[string]callResponse) map[string]callResponse {
		m := map[string]callResponse{}
		for k, v := range behind {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name     string
		response map[string]callResponse
		want     string
	}{
		{"origin's branch doesn't resolve", map[string]callResponse{local: {out: []byte("aaa\n")}, remote: fail}, "resolve origin/main"},
		{"listing the worktrees fails", with(map[string]callResponse{list: fail}), "git worktree list: boom"},
		{"moving the branch fails", with(map[string]callResponse{"git update-ref refs/heads/main bbb aaa": fail}), "git update-ref refs/heads/main: boom"},
		{"reading the checkout's status fails", with(map[string]callResponse{list: {out: []byte(checkout)}, "git status --porcelain --untracked-files=no": fail}), "git status in /repo: boom"},
		{"the fast-forward fails", with(map[string]callResponse{list: {out: []byte(checkout)}, "git merge --ff-only refs/remotes/origin/main": fail}), "git merge --ff-only in /repo: boom"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			g := &GitPR{Run: (&recordingRunner{response: tc.response}).run}
			_, err := g.UpdateBase(context.Background(), "/repo/.worktrees/pr-1", "main", true)
			require.ErrorContains(s.T(), err, tc.want)
		})
	}
}

func (s *BaseSuite) TestCheckoutOf() {
	porcelain := []byte("worktree /repo\nHEAD aaa\nbranch refs/heads/main\n\nworktree /repo/.worktrees/pr-1\nHEAD bbb\ndetached\n\nworktree /repo/.worktrees/feat\nHEAD ccc\nbranch refs/heads/feat\n")
	require.Equal(s.T(), "/repo", checkoutOf(porcelain, "refs/heads/main"))
	require.Equal(s.T(), "/repo/.worktrees/feat", checkoutOf(porcelain, "refs/heads/feat"))
	require.Empty(s.T(), checkoutOf(porcelain, "refs/heads/other"))
}

// TestUpdateBaseRealGit runs against a real repo with a review worktree,
// as the daemon has: origin's main moves ahead, and the local main is
// moved, or left, depending on where it is checked out.
func (s *BaseSuite) TestUpdateBaseRealGit() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("git not installed")
	}
	// Resolved, as git reports it: macOS's temp dir is behind a symlink.
	root, err := filepath.EvalSymlinks(s.T().TempDir())
	require.NoError(s.T(), err)
	repo := filepath.Join(root, "repo")
	review := filepath.Join(root, "review")
	require.NoError(s.T(), os.Mkdir(repo, 0o755))
	gitIn := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(s.T(), err, string(out))
		return strings.TrimSpace(string(out))
	}
	git := func(args ...string) string { return gitIn(repo, args...) }
	write := func(name, content string) {
		require.NoError(s.T(), os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644))
	}

	git("init", "-q", "-b", "main")
	write("base.txt", "base\n")
	git("add", ".")
	git("commit", "-qm", "base")
	stale := git("rev-parse", "HEAD")
	git("worktree", "add", "-q", "--detach", review, "HEAD")
	// origin's main moves on: a commit only the remote-tracking ref has.
	git("commit", "-q", "--allow-empty", "-m", "merged upstream")
	upstream := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/main", upstream)
	git("reset", "-q", "--hard", stale)

	g := &GitPR{Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}}
	update := func(checkedOut bool) BaseUpdate {
		got, err := g.UpdateBase(context.Background(), review, "main", checkedOut)
		require.NoError(s.T(), err)
		return got
	}
	main := func() string { return git("rev-parse", "main") }

	// Checked out in the repo and not allowed: left alone.
	require.Equal(s.T(), BaseUpdate{Skipped: "main is checked out at " + repo + " and review.update_base is off"}, update(false))
	require.Equal(s.T(), stale, main())

	// Allowed, but the checkout has uncommitted changes: left alone.
	write("base.txt", "edited\n")
	require.Equal(s.T(), BaseUpdate{Skipped: "main is checked out at " + repo + " with uncommitted changes"}, update(true))
	require.Equal(s.T(), stale, main())
	git("checkout", "-q", "--", "base.txt")

	// Allowed and clean: fast-forwarded there, the working tree with it.
	require.Equal(s.T(), BaseUpdate{Updated: true}, update(true))
	require.Equal(s.T(), upstream, main())
	require.Equal(s.T(), upstream, git("rev-parse", "HEAD"))

	// Level with origin: nothing to do.
	require.Equal(s.T(), BaseUpdate{}, update(true))

	// Not checked out anywhere: moved in place, whatever the setting.
	git("switch", "-q", "--detach")
	git("update-ref", "refs/heads/main", stale)
	require.Equal(s.T(), BaseUpdate{Updated: true}, update(false))
	require.Equal(s.T(), upstream, main())

	// Ahead of origin, with a commit of its own: never rewritten.
	git("switch", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "local only")
	ahead := main()
	git("switch", "-q", "--detach")
	require.Equal(s.T(), BaseUpdate{Skipped: "main isn't behind origin/main"}, update(true))
	require.Equal(s.T(), ahead, main())

	// No local branch of that name: left missing.
	require.Equal(s.T(), BaseUpdate{Skipped: "there is no local develop branch"}, func() BaseUpdate {
		got, err := g.UpdateBase(context.Background(), review, "develop", true)
		require.NoError(s.T(), err)
		return got
	}())
}
