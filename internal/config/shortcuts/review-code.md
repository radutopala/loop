Review all uncommitted and untracked code changes in the current working directory.

## Steps

1. Run `git status` to identify modified, staged, and untracked files.
2. Run `git diff` to see unstaged changes and `git diff --cached` for staged changes.
3. Determine the main branch: use `main` if it exists, otherwise `master`.
4. Run `git diff <main-branch>...HEAD` to see the full diff of this branch against the base.
5. Review every changed file for:
   - **Correctness** -- logic errors, off-by-one, nil/null dereferences, missing error handling
   - **Security** -- injection risks, hardcoded secrets, unsafe input handling
   - **Tests** -- are new/changed functions covered? Flag untested code paths
   - **Style** -- naming, formatting, dead code, leftover debug statements
   - **Consistency** -- does the change follow existing patterns in the codebase?

## Output

For each file with findings, report:
- File path and a one-line summary of what changed
- Specific issues as bullet points with line references where possible

End with a short verdict: ship-ready, needs minor fixes, or needs rework.
Be concise. Skip files with no issues.
