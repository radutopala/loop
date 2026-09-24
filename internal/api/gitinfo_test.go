package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GitInfoSuite struct {
	suite.Suite
}

func TestGitInfoSuite(t *testing.T) {
	suite.Run(t, new(GitInfoSuite))
}

func (s *GitInfoSuite) git(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())
}

func (s *GitInfoSuite) TestCollectGitStateEmptyDir() {
	require.Equal(s.T(), gitState{}, collectGitState(context.Background(), "", "", gitState{}))
}

func (s *GitInfoSuite) TestCollectGitStateNonRepo() {
	require.Equal(s.T(), gitState{}, collectGitState(context.Background(), s.T().TempDir(), "", gitState{}))
}

func (s *GitInfoSuite) TestCollectGitStateCleanRepo() {
	dir := initGitRepo(s.T())
	st := collectGitState(context.Background(), dir, "", gitState{})
	require.NotEmpty(s.T(), st.Branch)
	require.Len(s.T(), st.Commit, 7)
	require.Zero(s.T(), st.DiffAdditions)
	require.Zero(s.T(), st.DiffDeletions)
}

// TestCollectGitStateCounts mirrors the Uncommitted Diff panel semantics:
// staged + unstaged tracked line counts summed, untracked text files counted
// as additions (including files inside untracked directories), binary
// untracked files contributing nothing.
func (s *GitInfoSuite) TestCollectGitStateCounts() {
	dir := initGitRepo(s.T())

	// Commit a second file first, then leave an unstaged -1 change on it.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644))
	s.git(dir, "add", "a.txt")
	s.git(dir, "commit", "-m", "add a")
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)) // -1 line

	// Staged change: a new 2-line file added to the index (index vs HEAD: +2).
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("s1\ns2\n"), 0o644))
	s.git(dir, "add", "staged.txt")

	// Untracked: a text file, a file inside an untracked dir, and a binary.
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\ny\nz\n"), 0o644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n1\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0x00, 0x01, 0x02, '\n'}, 0o644))

	st := collectGitState(context.Background(), dir, "", gitState{})
	require.NotEmpty(s.T(), st.Branch)
	require.Len(s.T(), st.Commit, 7)
	// staged: +2, unstaged: -1, untracked: 3 (new.txt) + 1 (sub/nested.txt), binary: 0.
	require.Equal(s.T(), 2+3+1, st.DiffAdditions)
	require.Equal(s.T(), 1, st.DiffDeletions)
}

func (s *GitInfoSuite) TestCollectGitStateDetachedHead() {
	dir := initGitRepo(s.T())
	s.git(dir, "checkout", "--detach", "HEAD")
	st := collectGitState(context.Background(), dir, "", gitState{})
	// Parity with `rev-parse --abbrev-ref HEAD` on a detached head.
	require.Equal(s.T(), "HEAD", st.Branch)
	require.Len(s.T(), st.Commit, 7)
}

func (s *GitInfoSuite) TestCollectGitStateUnbornBranch() {
	dir := s.T().TempDir()
	s.git(dir, "init")
	st := collectGitState(context.Background(), dir, "", gitState{})
	require.NotEmpty(s.T(), st.Branch)
	require.Empty(s.T(), st.Commit) // "(initial)" oid → no commit yet
}

// TestCollectGitStateSubjectAndBase covers a worktree-style checkout: the
// commit's subject, and the commits it's ahead of and behind its base.
func (s *GitInfoSuite) TestCollectGitStateSubjectAndBase() {
	dir := initGitRepo(s.T())
	s.git(dir, "branch", "-M", "main")
	s.git(dir, "checkout", "-b", "feat")
	s.git(dir, "commit", "--allow-empty", "-m", "feat one")
	s.git(dir, "commit", "--allow-empty", "-m", "feat two")
	s.git(dir, "checkout", "main")
	s.git(dir, "commit", "--allow-empty", "-m", "main moved on")
	s.git(dir, "checkout", "feat")

	st := collectGitState(context.Background(), dir, "main", gitState{})
	require.Equal(s.T(), "feat two", st.Subject)
	require.Equal(s.T(), "main", st.SyncBase)
	require.Equal(s.T(), 2, st.BaseAhead)
	require.Equal(s.T(), 1, st.BaseBehind)

	// No base: nothing to count against.
	st = collectGitState(context.Background(), dir, "", gitState{})
	require.Empty(s.T(), st.SyncBase)
	require.Zero(s.T(), st.BaseAhead)
	require.Zero(s.T(), st.BaseBehind)

	// A base that no longer exists counts nothing.
	st = collectGitState(context.Background(), dir, "gone", gitState{})
	require.Empty(s.T(), st.SyncBase)
	require.Zero(s.T(), st.BaseAhead)
	require.Zero(s.T(), st.BaseBehind)
}

func (s *GitInfoSuite) TestCollectGitStateReusesSubject() {
	dir := initGitRepo(s.T())
	st := collectGitState(context.Background(), dir, "", gitState{})
	require.Equal(s.T(), "init", st.Subject)

	// Same commit as last time: its subject is reused, not looked up again.
	st = collectGitState(context.Background(), dir, "", gitState{Commit: st.Commit, Subject: "cached"})
	require.Equal(s.T(), "cached", st.Subject)

	// A new commit is looked up.
	st = collectGitState(context.Background(), dir, "", gitState{Commit: "0000000", Subject: "cached"})
	require.Equal(s.T(), "init", st.Subject)
}

func (s *GitInfoSuite) TestCollectGitStateUpstream() {
	origin := initGitRepo(s.T())
	s.git(origin, "branch", "-M", "main")
	clone := filepath.Join(s.T().TempDir(), "clone")
	s.git(origin, "clone", "-q", origin, clone)
	s.git(clone, "config", "user.email", "test@test.com")
	s.git(clone, "config", "user.name", "Test")
	s.git(clone, "commit", "--allow-empty", "-m", "local")
	s.git(origin, "commit", "--allow-empty", "-m", "remote one")
	s.git(origin, "commit", "--allow-empty", "-m", "remote two")
	s.git(clone, "fetch", "-q")

	st := collectGitState(context.Background(), clone, "", gitState{})
	require.Equal(s.T(), "origin/main", st.Upstream)
	require.Equal(s.T(), 1, st.Ahead)
	require.Equal(s.T(), 2, st.Behind)

	// A branch without an upstream has none.
	st = collectGitState(context.Background(), origin, "", gitState{})
	require.Empty(s.T(), st.Upstream)
	require.Zero(s.T(), st.Ahead)
	require.Zero(s.T(), st.Behind)
}

func (s *GitInfoSuite) TestParseShortstat() {
	add, del := parseShortstat(" 2 files changed, 10 insertions(+), 3 deletions(-)\n")
	require.Equal(s.T(), 10, add)
	require.Equal(s.T(), 3, del)

	add, del = parseShortstat("")
	require.Zero(s.T(), add)
	require.Zero(s.T(), del)

	add, del = parseShortstat(" 1 file changed, 1 insertion(+)\n")
	require.Equal(s.T(), 1, add)
	require.Zero(s.T(), del)
}

func (s *GitInfoSuite) TestParseStatusV2() {
	out := "# branch.oid 0123456789abcdef0123456789abcdef01234567\x00" +
		"# branch.head main\x00" +
		"# branch.upstream origin/main\x00" +
		"# branch.ab +3 -1\x00" +
		"? new file.txt\x00" +
		"? sub/nested.txt\x00" +
		"1 .M N... 100644 100644 100644 abc def README.md\x00"
	st, untracked := parseStatusV2(out)
	require.Equal(s.T(), "main", st.Branch)
	require.Equal(s.T(), "0123456", st.Commit)
	require.Equal(s.T(), "origin/main", st.Upstream)
	require.Equal(s.T(), 3, st.Ahead)
	require.Equal(s.T(), 1, st.Behind)
	require.Equal(s.T(), []string{"new file.txt", "sub/nested.txt"}, untracked)
}

// TestCollectGitStateFilterFailure covers the git-lfs case: a repo whose
// .gitattributes routes a tracked path through a filter whose binary is not on
// PATH, with filter.<name>.required set. `git status` exits non-zero there, but
// the branch and commit are still resolvable — the header and branch picker
// depend on getting them.
func (s *GitInfoSuite) TestCollectGitStateFilterFailure() {
	dir := initGitRepo(s.T())
	bin := filepath.Join(dir, "a.bin")
	require.NoError(s.T(), os.WriteFile(bin, []byte("data\n"), 0o644))
	s.git(dir, "add", "a.bin")
	s.git(dir, "commit", "-m", "bin")

	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.bin filter=brokenlfs\n"), 0o644))
	s.git(dir, "config", "filter.brokenlfs.clean", "loop-test-missing-filter-binary")
	s.git(dir, "config", "filter.brokenlfs.required", "true")
	// Invalidate the index stat cache so status must run the clean filter.
	future := time.Now().Add(2 * time.Second)
	require.NoError(s.T(), os.Chtimes(bin, future, future))

	// Precondition: the status command the poller relies on really does fail.
	status := exec.Command("git", "status", "--porcelain=v2", "--branch", "--untracked-files=all", "-z")
	status.Dir = dir
	require.Error(s.T(), status.Run(), "expected git status to fail with a missing required filter")

	st := collectGitState(context.Background(), dir, "", gitState{})
	require.NotEmpty(s.T(), st.Branch, "branch must survive a git status failure")
	require.Len(s.T(), st.Commit, 7)
	require.Zero(s.T(), st.DiffAdditions)
	require.Zero(s.T(), st.DiffDeletions)
}

// TestRefStateNonRepo keeps the fallback's non-repo behaviour identical to the
// status path's.
func (s *GitInfoSuite) TestRefStateNonRepo() {
	require.Equal(s.T(), gitState{}, refState(context.Background(), s.T().TempDir()))
}

// TestRefStateDetachedHead mirrors parseStatusV2, which reports "HEAD" for a
// detached checkout.
func (s *GitInfoSuite) TestRefStateDetachedHead() {
	dir := initGitRepo(s.T())
	s.git(dir, "checkout", "--detach")
	st := refState(context.Background(), dir)
	require.Equal(s.T(), "HEAD", st.Branch)
	require.Len(s.T(), st.Commit, 7)
}

// TestGitOutputEmptyOutput covers the ok=false path for a command that
// succeeds but prints nothing — a clean repo has no diff to name.
func (s *GitInfoSuite) TestGitOutputEmptyOutput() {
	dir := initGitRepo(s.T())
	out, ok := gitOutput(context.Background(), dir, "diff", "--name-only")
	require.False(s.T(), ok)
	require.Empty(s.T(), out)
}
