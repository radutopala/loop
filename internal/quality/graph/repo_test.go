package graph

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

func (s *EnumerateSuite) TestFindRepo() {
	top := s.T().TempDir()
	s.writeTree(top, map[string]string{
		"main/.git/HEAD":                   "",
		"main/.git/worktrees/wt/commondir": "../..\n",
		"main/src/x.go":                    "",
		"wt/.git":                          "gitdir: ../main/.git/worktrees/wt\n",
		"abs/.git":                         "gitdir: " + filepath.Join(top, "absgit") + "\n",
		"absgit/commondir":                 filepath.Join(top, "main", ".git") + "\n",
		"sub/.git":                         "gitdir: ../main/.git/modules/sub\n",
		"bad/.git":                         "not a gitdir line\n",
	})
	mainGit := filepath.Join(top, "main", ".git")
	tests := []struct {
		name     string
		dir      string
		wantRoot string
		wantCom  string
		wantMain string
	}{
		{"main checkout subdir", "main/src", "main", mainGit, "main"},
		{"linked worktree, relative paths", "wt", "wt", mainGit, "main"},
		{"linked worktree, absolute paths", "abs", "abs", mainGit, "main"},
		{"submodule has its own git dir", "sub", "sub", filepath.Join(mainGit, "modules", "sub"), "sub"},
		{"unparsable .git file", "bad", "bad", "", "bad"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			repo, ok := FindRepo(filepath.Join(top, filepath.FromSlash(tc.dir)))
			require.True(s.T(), ok)
			require.Equal(s.T(), filepath.Join(top, tc.wantRoot), repo.Root)
			require.Equal(s.T(), tc.wantCom, repo.CommonDir)
			require.Equal(s.T(), filepath.Join(top, tc.wantMain), repo.MainRoot())
		})
	}
}

func (s *EnumerateSuite) TestFindRepoUnreadableGitFile() {
	top := s.T().TempDir()
	s.writeTree(top, map[string]string{"wt/.git": "gitdir: x\n"})
	repo, ok := findRepo(os.Stat, func(string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}, filepath.Join(top, "wt"))
	require.True(s.T(), ok)
	require.Equal(s.T(), Repo{Root: filepath.Join(top, "wt")}, repo)
}

func (s *EnumerateSuite) TestFindRepoOutsideARepo() {
	repo, ok := findRepo(func(string) (fs.FileInfo, error) {
		return nil, fs.ErrNotExist
	}, missingFileReader, "/a/b")
	require.False(s.T(), ok)
	require.Equal(s.T(), Repo{}, repo)
}
