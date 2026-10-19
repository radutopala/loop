package review

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Scope is what a review run reviews: the whole PR, or only what changed
// since the last review of it.
type Scope string

const (
	// ScopeAuto picks per run: incremental when an earlier run reviewed
	// the PR and the changes since are a small part of it, else full.
	ScopeAuto Scope = ""
	// ScopeFull reviews the whole PR.
	ScopeFull Scope = "full"
	// ScopeIncremental reviews the commits since the last reviewed head.
	ScopeIncremental Scope = "incremental"
)

// KnownScope reports whether s is a scope a user may pick.
func KnownScope(s Scope) bool {
	return s == ScopeAuto || s == ScopeFull || s == ScopeIncremental
}

// ScopeResult is the scope a run resolved to. FromSHA is the commit an
// incremental run's range starts at. Rebased is set when that commit is no
// longer an ancestor of the head (a rebase or force-push), so the range
// may also carry changes from the base. Reason says why a run that could
// have been incremental reviews the whole PR.
type ScopeResult struct {
	Scope   Scope
	FromSHA string
	Rebased bool
	Reason  string
}

// incrementalShare is the share of the PR's changed lines above which an
// automatic scope reviews the whole PR: the range wouldn't save much.
const incrementalShare = 0.5

// ResolveScope picks what a run of the PR checked out at worktreePath
// reviews, given the user's choice and the head the last completed run
// reviewed ("" when none did). The changes since lastHead are measured
// on the PR's files only, so commits merged in from the base don't count.
func (g *GitPR) ResolveScope(ctx context.Context, worktreePath, baseRef string, want Scope, lastHead string) (ScopeResult, error) {
	full := func(reason string) (ScopeResult, error) { return ScopeResult{Scope: ScopeFull, Reason: reason}, nil }
	if want == ScopeFull {
		return ScopeResult{Scope: ScopeFull}, nil
	}
	if lastHead == "" {
		return full("no earlier review of this PR")
	}
	out, err := g.Run(ctx, worktreePath, "git", "rev-parse", "HEAD")
	if err != nil {
		return ScopeResult{}, fmt.Errorf("git rev-parse HEAD: %s", strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == lastHead {
		return full("no new commits since the last review")
	}
	if _, err := g.Run(ctx, worktreePath, "git", "cat-file", "-e", lastHead+"^{commit}"); err != nil {
		return full("the last reviewed commit is gone")
	}
	_, err = g.Run(ctx, worktreePath, "git", "merge-base", "--is-ancestor", lastHead, "HEAD")
	rebased := err != nil

	prLines, files, err := g.numstat(ctx, worktreePath, fmt.Sprintf("origin/%s...HEAD", baseRef))
	if err != nil {
		return ScopeResult{}, err
	}
	if len(files) == 0 {
		return full("the PR changes no files")
	}
	// Literal pathspecs, so a path with a glob character in it doesn't
	// match other files.
	since := []string{lastHead, "HEAD", "--"}
	for _, f := range files {
		since = append(since, ":(literal)"+f)
	}
	sinceLines, _, err := g.numstat(ctx, worktreePath, since...)
	if err != nil {
		return ScopeResult{}, err
	}
	if sinceLines == 0 {
		return full("no file of the PR changed since the last review")
	}
	if want == ScopeAuto && float64(sinceLines) > incrementalShare*float64(prLines) {
		return full("the changes since the last review are most of the PR")
	}
	return ScopeResult{Scope: ScopeIncremental, FromSHA: lastHead, Rebased: rebased}, nil
}

// numstat runs `git diff --numstat` with args and returns the lines it
// adds and deletes, and the files it touches. Binary files count as
// touched, with no lines. Renames are off, so a renamed file is listed
// under its new path.
func (g *GitPR) numstat(ctx context.Context, worktreePath string, args ...string) (int, []string, error) {
	out, err := g.Run(ctx, worktreePath, "git", append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat"}, args...)...)
	if err != nil {
		return 0, nil, fmt.Errorf("git diff --numstat: %s", strings.TrimSpace(string(out)))
	}
	lines := 0
	var files []string
	for raw := range bytes.SplitSeq(out, []byte{'\n'}) {
		added, rest, ok := strings.Cut(string(raw), "\t")
		if !ok {
			continue
		}
		deleted, path, ok := strings.Cut(rest, "\t")
		if !ok {
			continue
		}
		a, _ := strconv.Atoi(added)
		d, _ := strconv.Atoi(deleted)
		lines += a + d
		files = append(files, path)
	}
	return lines, files, nil
}
