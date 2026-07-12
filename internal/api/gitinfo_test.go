package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
	require.Equal(s.T(), gitState{}, collectGitState(context.Background(), ""))
}

func (s *GitInfoSuite) TestCollectGitStateNonRepo() {
	require.Equal(s.T(), gitState{}, collectGitState(context.Background(), s.T().TempDir()))
}

func (s *GitInfoSuite) TestCollectGitStateCleanRepo() {
	dir := initGitRepo(s.T())
	st := collectGitState(context.Background(), dir)
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

	st := collectGitState(context.Background(), dir)
	require.NotEmpty(s.T(), st.Branch)
	require.Len(s.T(), st.Commit, 7)
	// staged: +2, unstaged: -1, untracked: 3 (new.txt) + 1 (sub/nested.txt), binary: 0.
	require.Equal(s.T(), 2+3+1, st.DiffAdditions)
	require.Equal(s.T(), 1, st.DiffDeletions)
}

func (s *GitInfoSuite) TestCollectGitStateDetachedHead() {
	dir := initGitRepo(s.T())
	s.git(dir, "checkout", "--detach", "HEAD")
	st := collectGitState(context.Background(), dir)
	// Parity with `rev-parse --abbrev-ref HEAD` on a detached head.
	require.Equal(s.T(), "HEAD", st.Branch)
	require.Len(s.T(), st.Commit, 7)
}

func (s *GitInfoSuite) TestCollectGitStateUnbornBranch() {
	dir := s.T().TempDir()
	s.git(dir, "init")
	st := collectGitState(context.Background(), dir)
	require.NotEmpty(s.T(), st.Branch)
	require.Empty(s.T(), st.Commit) // "(initial)" oid → no commit yet
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
		"? new file.txt\x00" +
		"? sub/nested.txt\x00" +
		"1 .M N... 100644 100644 100644 abc def README.md\x00"
	st, untracked := parseStatusV2(out)
	require.Equal(s.T(), "main", st.Branch)
	require.Equal(s.T(), "0123456", st.Commit)
	require.Equal(s.T(), []string{"new file.txt", "sub/nested.txt"}, untracked)
}
