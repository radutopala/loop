package graph

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// statFunc is the stat abstraction the repo locator depends on.
type statFunc func(name string) (fs.FileInfo, error)

// Repo is a git checkout found on disk, without running git.
type Repo struct {
	// Root is the working tree root: the nearest directory that holds a
	// .git dir or file.
	Root string
	// CommonDir is the git dir the checkout's worktrees share (the main
	// checkout's .git dir), or "" when a worktree's .git file can't be
	// read.
	CommonDir string
}

// MainRoot is the main checkout's working tree: for a linked worktree,
// the checkout that holds the shared .git dir; otherwise Root.
func (r Repo) MainRoot() string {
	if filepath.Base(r.CommonDir) == ".git" {
		return filepath.Dir(r.CommonDir)
	}
	return r.Root
}

// FindRepo returns the git checkout that holds dir, walking up from it.
func FindRepo(dir string) (Repo, bool) {
	return findRepo(os.Stat, os.ReadFile, dir)
}

// findRepo walks up from dir, which must be absolute, to the nearest
// directory with a .git entry.
func findRepo(stat statFunc, readFile readFileFunc, dir string) (Repo, bool) {
	for d := filepath.Clean(dir); ; {
		dotGit := filepath.Join(d, ".git")
		if info, err := stat(dotGit); err == nil {
			if info.IsDir() {
				return Repo{Root: d, CommonDir: dotGit}, true
			}
			return Repo{Root: d, CommonDir: linkedCommonDir(readFile, d, dotGit)}, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return Repo{}, false
		}
		d = parent
	}
}

// linkedCommonDir follows a .git file ("gitdir: <dir>") and that dir's
// commondir file to the shared git dir. A submodule's git dir has no
// commondir file and is its own common dir.
func linkedCommonDir(readFile readFileFunc, root, dotGit string) string {
	data, err := readFile(dotGit)
	if err != nil {
		return ""
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	data, err = readFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return filepath.Clean(gitDir)
	}
	common := strings.TrimSpace(string(data))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common)
}
