// gitinfo.go collects the per-directory git state shown in the sidebar
// (branch, short commit, uncommitted +/- line counts) with as few subprocesses
// as possible. It is the single compute path shared by the branch poller and
// the /api/channels handler, which consumes the poller's snapshot instead of
// recomputing per request.
package api

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// collectGitState gathers the sidebar git state for dir in three subprocesses:
// one `git status --porcelain=v2 -z --branch` (branch, commit oid, untracked
// list — replacing two rev-parse calls and an ls-files) plus the two shortstat
// diffs. Line counts deliberately mirror the Uncommitted Diff panel: staged
// (index vs HEAD) and unstaged (worktree vs index) summed separately — NOT a
// single worktree-vs-HEAD diff, whose semantics differ when a staged hunk is
// reverted in the worktree — and untracked text files add their line count
// (binary files add nothing, via buildUntrackedEntry).
// A non-repo dir returns the zero value, matching the previous behavior of
// the individual helpers.
func collectGitState(ctx context.Context, dir string) gitState {
	if dir == "" {
		return gitState{}
	}

	// --untracked-files=all lists files inside untracked directories
	// individually (parity with `ls-files --others`); -z avoids path quoting.
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v2", "--branch", "--untracked-files=all", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// `git status` reads the worktree, so it fails in situations that
		// leave the repo perfectly identifiable. The one seen in practice is
		// a repo whose .gitattributes routes paths through git-lfs: the
		// repo-local filter.lfs.required makes a missing git-lfs binary a
		// fatal "external filter failed" rather than a warning. Returning the
		// zero value there blanks the channel header and hides the branch
		// picker for a repo git can still describe perfectly well, so fall
		// back to the ref lookups, which never touch the worktree. Diff
		// counts stay zero — that part genuinely could not be computed.
		return refState(ctx, dir)
	}
	st, untracked := parseStatusV2(string(out))

	// Tracked line counts: staged (index vs HEAD) then unstaged (worktree vs index).
	for _, args := range [][]string{
		{"diff", "--cached", "--shortstat"},
		{"diff", "--shortstat"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.Output(); err == nil {
			add, del := parseShortstat(string(out))
			st.DiffAdditions += add
			st.DiffDeletions += del
		}
	}

	// Untracked files count like the panel does (binary-aware).
	for _, uf := range untracked {
		if entry, _ := buildUntrackedEntry(dir, uf); entry != nil {
			st.DiffAdditions += entry.Additions
		}
	}

	return st
}

// refState resolves branch and short commit with plumbing that never reads the
// worktree, so it survives the filter failures that take `git status` down. A
// non-repo dir returns the zero value, matching the status path.
func refState(ctx context.Context, dir string) gitState {
	branch, ok := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return gitState{}
	}
	// --short=7 matches the width parseStatusV2 slices off branch.oid.
	commit, _ := gitOutput(ctx, dir, "rev-parse", "--short=7", "HEAD")
	return gitState{Branch: branch, Commit: commit}
}

// gitOutput runs a git command in dir and returns its trimmed stdout. ok is
// false when the command fails or produces no output.
func gitOutput(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	return s, s != ""
}

// parseStatusV2 extracts the branch name, short commit, and untracked paths
// from NUL-terminated `git status --porcelain=v2 --branch` output. A detached
// HEAD reports "HEAD" (parity with `rev-parse --abbrev-ref HEAD`); an unborn
// branch ("(initial)") reports an empty commit.
func parseStatusV2(out string) (st gitState, untracked []string) {
	for line := range strings.SplitSeq(out, "\x00") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			b := strings.TrimPrefix(line, "# branch.head ")
			if b == "(detached)" {
				b = "HEAD"
			}
			st.Branch = b
		case strings.HasPrefix(line, "# branch.oid "):
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if oid != "(initial)" && len(oid) >= 7 {
				st.Commit = oid[:7]
			}
		case strings.HasPrefix(line, "? "):
			untracked = append(untracked, strings.TrimPrefix(line, "? "))
		}
	}
	return st, untracked
}

// parseShortstat extracts insertion/deletion counts from `git diff --shortstat`
// output ("2 files changed, 10 insertions(+), 3 deletions(-)").
func parseShortstat(out string) (add, del int) {
	for part := range strings.SplitSeq(strings.TrimSpace(out), ",") {
		part = strings.TrimSpace(part)
		var n int
		if strings.Contains(part, "insertion") {
			_, _ = fmt.Sscanf(part, "%d", &n)
			add += n
		} else if strings.Contains(part, "deletion") {
			_, _ = fmt.Sscanf(part, "%d", &n)
			del += n
		}
	}
	return add, del
}
