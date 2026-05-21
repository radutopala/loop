package review

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CommandRunner executes a command in a given directory and returns the
// combined output. Mirrors the worktree.CommandRunner signature so the
// existing worktree.ExecCommandRunner can be reused as the default impl.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// PR adds and removes a git worktree that checks out a pull request's
// head commit in detached HEAD mode. Detached avoids polluting the
// user's branch list with one-off review branches — the worktree is
// purely a read-only sandbox for the review agent. Diff produces a
// merge-base unified patch between origin/<baseRef> and the worktree's
// HEAD — the same view GitHub shows on the PR.
type PR interface {
	Add(ctx context.Context, parentDir string, prNum int) (worktreePath string, err error)
	Refresh(ctx context.Context, parentDir, worktreePath string, prNum int) error
	Diff(ctx context.Context, parentDir, worktreePath, baseRef string, comments []*Comment) ([]byte, error)
	Remove(ctx context.Context, parentDir, worktreePath string) error
}

// GitPR is the default PR implementation backed by shelling out to git.
type GitPR struct {
	Run CommandRunner
}

// Add fetches the PR head from origin (refs/pull/<n>/head) and creates a
// detached worktree at <parentDir>/.worktrees/pr-<n>. Idempotent: if the
// worktree already exists, the existing path is returned.
func (g *GitPR) Add(ctx context.Context, parentDir string, prNum int) (string, error) {
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

// Refresh fast-forwards an existing worktree to the latest PR head by
// re-fetching `refs/pull/<n>/head` and running `git checkout --detach
// FETCH_HEAD` inside the worktree. Used by the sync button to pick up
// new commits pushed to the PR without tearing the worktree down.
//
// The fetch runs inside worktreePath, not parentDir: since git 2.5
// FETCH_HEAD is per-worktree, so fetching from the parent would leave
// the worktree's FETCH_HEAD stale and the checkout would treat the
// literal string "FETCH_HEAD" as a pathspec ("--detach does not take
// a path argument 'FETCH_HEAD'").
func (g *GitPR) Refresh(ctx context.Context, parentDir, worktreePath string, prNum int) error {
	if parentDir == "" || worktreePath == "" || prNum <= 0 {
		return fmt.Errorf("parentDir, worktreePath, and prNum are required")
	}
	fetchSpec := fmt.Sprintf("refs/pull/%d/head", prNum)
	if out, err := g.Run(ctx, worktreePath, "git", "fetch", "origin", fetchSpec); err != nil {
		return fmt.Errorf("git fetch %s: %s", fetchSpec, strings.TrimSpace(string(out)))
	}
	if out, err := g.Run(ctx, worktreePath, "git", "checkout", "--detach", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("git checkout: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Diff computes the unified patch between origin/<baseRef> and the
// worktree's HEAD using `git diff origin/<baseRef>...HEAD`. The base ref
// is fetched first from the parent repo to ensure it's up to date — the
// worktree shares the parent's git dir so the fetched ref is visible
// from inside the worktree.
//
// When comments is non-empty, the unified-context number `-U<n>` is
// widened so every commented (path, line) lands inside a hunk rather
// than a context gap. We do a cheap first pass at `-U0` to discover
// changed line ranges per file, compute the largest distance from any
// comment to the nearest change, and use that as the final `-U`. With
// no comments — or when default -U=3 already covers them — we skip the
// pre-pass and run the standard `git diff`.
func (g *GitPR) Diff(ctx context.Context, parentDir, worktreePath, baseRef string, comments []*Comment) ([]byte, error) {
	if parentDir == "" || worktreePath == "" || baseRef == "" {
		return nil, fmt.Errorf("parentDir, worktreePath, and baseRef are required")
	}
	if out, err := g.Run(ctx, parentDir, "git", "fetch", "origin", baseRef); err != nil {
		return nil, fmt.Errorf("git fetch %s: %s", baseRef, strings.TrimSpace(string(out)))
	}
	baseSpec := fmt.Sprintf("origin/%s...HEAD", baseRef)

	needed := 0
	if len(comments) > 0 {
		skinny, err := g.Run(ctx, worktreePath, "git", "diff", "-U0", baseSpec)
		if err != nil {
			return nil, fmt.Errorf("git diff -U0: %s", strings.TrimSpace(string(skinny)))
		}
		needed = computeContextNeeded(skinny, comments)
	}

	args := []string{"diff"}
	if needed > defaultUnifiedContext {
		args = append(args, fmt.Sprintf("-U%d", needed))
	}
	args = append(args, baseSpec)
	out, err := g.Run(ctx, worktreePath, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %s", strings.TrimSpace(string(out)))
	}
	return out, nil
}

// defaultUnifiedContext is git's built-in `-U` value. We only widen
// beyond this when comments outside default hunks need to be absorbed.
const defaultUnifiedContext = 3

// fileChangeSpans holds the line ranges (per side) covered by hunks
// for one file, as parsed from a `git diff -U0` output. Counts default
// to 1 when the @@ header omits them; counts of 0 (pure insertions /
// pure deletions) collapse to a single-line marker at the start line.
type fileChangeSpans struct {
	newSpans [][2]int
	oldSpans [][2]int
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseChangedRanges walks a `git diff -U0` output and records the
// hunk ranges per file path (using the b/ path, so renames map to the
// new name). Files with no hunks (binary, mode-only) get an empty
// entry so callers can distinguish "in diff but no line ranges" from
// "not in diff at all".
func parseChangedRanges(diff []byte) map[string]*fileChangeSpans {
	result := make(map[string]*fileChangeSpans)
	var current *fileChangeSpans
	for raw := range bytes.SplitSeq(diff, []byte{'\n'}) {
		line := string(raw)
		if strings.HasPrefix(line, "diff --git ") {
			// "diff --git a/<old> b/<new>" — the b/ portion is the new
			// path even for renames; that's what comments target.
			if _, after, ok := strings.Cut(line, " b/"); ok {
				current = &fileChangeSpans{}
				result[after] = current
			} else {
				current = nil
			}
			continue
		}
		if current == nil {
			continue
		}
		m := hunkHeaderRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		oldStart, _ := strconv.Atoi(m[1])
		oldCount := 1
		if m[2] != "" {
			oldCount, _ = strconv.Atoi(m[2])
		}
		newStart, _ := strconv.Atoi(m[3])
		newCount := 1
		if m[4] != "" {
			newCount, _ = strconv.Atoi(m[4])
		}
		if newCount > 0 {
			current.newSpans = append(current.newSpans, [2]int{newStart, newStart + newCount - 1})
		} else {
			current.newSpans = append(current.newSpans, [2]int{newStart, newStart})
		}
		if oldCount > 0 {
			current.oldSpans = append(current.oldSpans, [2]int{oldStart, oldStart + oldCount - 1})
		} else {
			current.oldSpans = append(current.oldSpans, [2]int{oldStart, oldStart})
		}
	}
	return result
}

// computeContextNeeded returns the smallest `-U<n>` such that every
// comment whose file is in the diff lands within a hunk. Comments on
// files not in the diff are ignored — they have no anchor and will be
// surfaced by the frontend's outside-of-diff section instead.
func computeContextNeeded(skinnyDiff []byte, comments []*Comment) int {
	ranges := parseChangedRanges(skinnyDiff)
	needed := 0
	for _, c := range comments {
		if c == nil {
			continue
		}
		r, ok := ranges[c.Path]
		if !ok {
			continue
		}
		spans := r.newSpans
		if c.Side == "LEFT" {
			spans = r.oldSpans
		}
		if len(spans) == 0 {
			continue
		}
		dist := -1
		for _, span := range spans {
			var d int
			switch {
			case c.Line < span[0]:
				d = span[0] - c.Line
			case c.Line > span[1]:
				d = c.Line - span[1]
			default:
				d = 0
			}
			if dist < 0 || d < dist {
				dist = d
			}
		}
		if dist > needed {
			needed = dist
		}
	}
	return needed
}

// ShouldRediff reports whether a freshly-emitted comment on
// (path, line, side) needs the diff to be re-rendered with widened
// unified context. Returns true iff path is already in the diff but
// line falls outside every existing hunk on that side — exactly the
// case where growing `-U` will absorb the comment into a hunk.
//
// Path-absent comments return false: re-diffing won't add the file (it
// has no changes vs base), so they stay in the FE's outside-of-diff
// section. Lines already inside a hunk also return false — no work
// needed.
func ShouldRediff(rawDiff []byte, path string, line int, side string) bool {
	ranges := parseChangedRanges(rawDiff)
	r, ok := ranges[path]
	if !ok {
		return false
	}
	spans := r.newSpans
	if side == "LEFT" {
		spans = r.oldSpans
	}
	for _, span := range spans {
		if line >= span[0] && line <= span[1] {
			return false
		}
	}
	return true
}

// Remove tears down a worktree created by Add. Best-effort prune cleans
// up the worktree metadata even if the directory was already gone.
func (g *GitPR) Remove(ctx context.Context, parentDir, worktreePath string) error {
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
