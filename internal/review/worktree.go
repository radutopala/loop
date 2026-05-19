package review

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// CommandRunner executes a command in a given directory and returns the
// combined output. Mirrors the worktree.CommandRunner signature so the
// existing worktree.ExecCommandRunner can be reused as the default impl.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// PRWorktree adds and removes a git worktree that checks out a pull
// request's head commit in detached HEAD mode. Detached avoids polluting
// the user's branch list with one-off review branches — the worktree is
// purely a read-only sandbox for the review agent. Diff produces a
// merge-base unified patch between origin/<baseRef> and the worktree's
// HEAD — the same view GitHub shows on the PR.
type PRWorktree interface {
	Add(ctx context.Context, parentDir string, prNum int) (worktreePath string, err error)
	Diff(ctx context.Context, parentDir, worktreePath, baseRef string) ([]byte, error)
	Remove(ctx context.Context, parentDir, worktreePath string) error
}

// GitPRWorktree is the default PRWorktree backed by shelling out to git.
type GitPRWorktree struct {
	Run CommandRunner
}

// Add fetches the PR head from origin (refs/pull/<n>/head) and creates a
// detached worktree at <parentDir>/.worktrees/pr-<n>. Idempotent: if the
// worktree already exists, the existing path is returned.
func (g *GitPRWorktree) Add(ctx context.Context, parentDir string, prNum int) (string, error) {
	if parentDir == "" || prNum <= 0 {
		return "", fmt.Errorf("parentDir and prNum are required")
	}
	worktreePath := filepath.Join(parentDir, ".worktrees", fmt.Sprintf("pr-%d", prNum))

	// git fetch origin refs/pull/<n>/head — pulls the latest PR head into FETCH_HEAD.
	fetchSpec := fmt.Sprintf("refs/pull/%d/head", prNum)
	if out, err := g.Run(ctx, parentDir, "git", "fetch", "origin", fetchSpec); err != nil {
		return "", fmt.Errorf("git fetch %s: %s", fetchSpec, strings.TrimSpace(string(out)))
	}

	// Create the worktree in detached HEAD at the freshly-fetched commit.
	out, err := g.Run(ctx, parentDir, "git", "worktree", "add", "--detach", worktreePath, "FETCH_HEAD")
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// "already exists" is fine — caller may be re-loading the same PR.
		if strings.Contains(msg, "already exists") {
			return worktreePath, nil
		}
		return "", fmt.Errorf("git worktree add: %s", msg)
	}
	return worktreePath, nil
}

// Diff computes the unified patch between origin/<baseRef> and the
// worktree's HEAD using `git diff origin/<baseRef>...HEAD`. The base ref
// is fetched first from the parent repo to ensure it's up to date — the
// worktree shares the parent's git dir so the fetched ref is visible
// from inside the worktree.
func (g *GitPRWorktree) Diff(ctx context.Context, parentDir, worktreePath, baseRef string) ([]byte, error) {
	if parentDir == "" || worktreePath == "" || baseRef == "" {
		return nil, fmt.Errorf("parentDir, worktreePath, and baseRef are required")
	}
	if out, err := g.Run(ctx, parentDir, "git", "fetch", "origin", baseRef); err != nil {
		return nil, fmt.Errorf("git fetch %s: %s", baseRef, strings.TrimSpace(string(out)))
	}
	out, err := g.Run(ctx, worktreePath, "git", "diff", fmt.Sprintf("origin/%s...HEAD", baseRef))
	if err != nil {
		return nil, fmt.Errorf("git diff: %s", strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Remove tears down a worktree created by Add. Best-effort prune cleans
// up the worktree metadata even if the directory was already gone.
func (g *GitPRWorktree) Remove(ctx context.Context, parentDir, worktreePath string) error {
	if parentDir == "" || worktreePath == "" {
		return fmt.Errorf("parentDir and worktreePath are required")
	}
	out, err := g.Run(ctx, parentDir, "git", "worktree", "remove", "--force", worktreePath)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// If the worktree was already removed (e.g. user deleted the dir
		// manually) prune is enough to clean the metadata — don't fail.
		if !strings.Contains(msg, "is not a working tree") &&
			!strings.Contains(msg, "does not exist") {
			return fmt.Errorf("git worktree remove: %s", msg)
		}
	}
	if out, err := g.Run(ctx, parentDir, "git", "worktree", "prune"); err != nil {
		return fmt.Errorf("git worktree prune: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
