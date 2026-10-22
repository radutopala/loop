package review

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
)

// BaseUpdate is what UpdateBase did to the local base branch. Skipped says
// why it was left where it was; empty when it was moved or already level
// with origin.
type BaseUpdate struct {
	Updated bool
	Skipped string
}

// UpdateBase fast-forwards the local <baseRef> branch to origin/<baseRef>,
// which Diff has just fetched. A review agent left to pick its own base
// diffs against the local branch, and a stale one counts the base's newer
// commits as the PR's own once the PR has merged them. It never rewrites
// history: a branch that isn't behind origin is left alone. The branches
// are shared by every worktree of the repo, so a branch no worktree has
// checked out is moved in place; one that is checked out is moved only
// when checkedOut allows it and that worktree is clean, with a fast-forward
// merge there, as a pull would. A missing local branch is left missing.
func (g *GitPR) UpdateBase(ctx context.Context, dir, baseRef string, checkedOut bool) (BaseUpdate, error) {
	if dir == "" || baseRef == "" {
		return BaseUpdate{}, fmt.Errorf("dir and baseRef are required")
	}
	branch, remote := "refs/heads/"+baseRef, "refs/remotes/origin/"+baseRef
	local, err := g.revParse(ctx, dir, branch)
	if err != nil {
		return BaseUpdate{Skipped: "there is no local " + baseRef + " branch"}, nil
	}
	upstream, err := g.revParse(ctx, dir, remote)
	if err != nil {
		return BaseUpdate{}, fmt.Errorf("resolve origin/%s: %w", baseRef, err)
	}
	if local == upstream {
		return BaseUpdate{}, nil
	}
	if _, err := g.Run(ctx, dir, "git", "merge-base", "--is-ancestor", local, upstream); err != nil {
		return BaseUpdate{Skipped: baseRef + " isn't behind origin/" + baseRef}, nil
	}
	out, err := g.Run(ctx, dir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return BaseUpdate{}, fmt.Errorf("git worktree list: %s", strings.TrimSpace(string(out)))
	}
	wt := checkoutOf(out, branch)
	if wt == "" {
		// The old value makes the move a compare-and-swap, so a commit
		// landing on the branch meanwhile isn't lost.
		if out, err := g.Run(ctx, dir, "git", "update-ref", branch, upstream, local); err != nil {
			return BaseUpdate{}, fmt.Errorf("git update-ref %s: %s", branch, strings.TrimSpace(string(out)))
		}
		return BaseUpdate{Updated: true}, nil
	}
	if !checkedOut {
		return BaseUpdate{Skipped: baseRef + " is checked out at " + wt + " and review.update_base is off"}, nil
	}
	status, err := g.Run(ctx, wt, "git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return BaseUpdate{}, fmt.Errorf("git status in %s: %s", wt, strings.TrimSpace(string(status)))
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return BaseUpdate{Skipped: baseRef + " is checked out at " + wt + " with uncommitted changes"}, nil
	}
	if out, err := g.Run(ctx, wt, "git", "merge", "--ff-only", remote); err != nil {
		return BaseUpdate{}, fmt.Errorf("git merge --ff-only in %s: %s", wt, strings.TrimSpace(string(out)))
	}
	return BaseUpdate{Updated: true}, nil
}

// revParse resolves ref to its commit in dir.
func (g *GitPR) revParse(ctx context.Context, dir, ref string) (string, error) {
	out, err := g.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	sha := strings.TrimSpace(string(out))
	if err != nil || sha == "" {
		return "", fmt.Errorf("%s doesn't resolve", ref)
	}
	return sha, nil
}

// checkoutOf returns the worktree `git worktree list --porcelain` reports
// with branch checked out, empty when none has it.
func checkoutOf(porcelain []byte, branch string) string {
	path := ""
	sc := bufio.NewScanner(bytes.NewReader(porcelain))
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch "+branch {
			return path
		}
	}
	return ""
}
