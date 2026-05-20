# Review Panel

The Review panel loads a GitHub pull request into a local git worktree,
runs an agent review pass against the diff, and lets the user push the
resulting inline comments back to the PR — either one at a time or all
at once.

## Lifecycle

1. **Load** — the FE fetches open PRs (`GET /review/prs` → `gh pr list`)
   and renders them as a clickable list; the user picks one. The backend:
   - Looks the PR up via `gh pr view <number>` for metadata and base/head refs.
   - Resolves the head SHA via `gh pr view --json headRefOid`.
   - Creates a worktree off the PR head branch under the channel's `dir_path`.
   - Fetches the base ref into the parent repo (`git fetch origin <base>`) so
     `origin/<base>` resolves locally for the Run step.
2. **Run** — the user clicks **Run**. The backend launches a single agent
   run inside the worktree using the configured review prompt (`review.prompt`
   or `review.prompt_path` in `~/.loop/config.json`; falls back to a
   built-in default). The prompt tells the agent to compute the diff itself
   with `git diff origin/<base>...HEAD` from the worktree — keeps the
   container `argv` small and lets the agent read individual files for
   context. The agent emits `<review-comment path="..." line="N"
   side="RIGHT|LEFT">...body...</review-comment>` blocks, which are parsed
   and streamed to the FE as they arrive.
3. **Push** — each comment ships with **Push** (single) and a **Push all
   (N)** affordance in the header (when at least one comment is unpushed).
   The backend uses `gh api ... /pulls/N/comments` against the captured
   head SHA so comments anchor to the right commit even if the PR is
   force-pushed later.
4. **Close** — closing the session deletes the in-memory session record
   and removes the worktree on disk. Pushed comments remain on GitHub.

## Concurrency

A second `POST /review/run` while the first is still in flight returns
`202 {"status":"in_progress"}` without restarting the agent. Comments
keep streaming through the existing run.

## Status transitions

Status is broadcast over the WebSocket as `review.status` events so
multiple panes / browser tabs stay in sync without polling.

```
idle ──load──▶ loading ──ok──▶ ready ──run──▶ reviewing ──ok──▶ ready
                  │                            │
                  └──err──▶ error              └──err──▶ error
```

## Configuration

```json
{
  "review": {
    "prompt": "Review the diff between <diff> tags and emit one <review-comment> block per actionable issue. Skip style nits."
  }
}
```

Or, to keep the prompt out of the JSON file:

```json
{ "review": { "prompt_path": "review.md" } }
```

The latter is read from `~/.loop/review/review.md`. Setting both is an
error; setting neither uses the daemon's built-in default prompt.

### Required output format

The parser anchors comments by the tag attributes, so an override prompt
must instruct the agent to emit blocks shaped exactly like:

```xml
<review-comment path="path/to/file" line="N" side="RIGHT">
One paragraph describing the issue.
</review-comment>
```

- `path` is repo-relative.
- `line` is the line on the indicated side of the diff.
- `side` is `"RIGHT"` for added/modified lines (the common case) or
  `"LEFT"` for lines removed from the base. This matches GitHub's
  `pulls/{N}/comments` API, so the value is forwarded as-is on push.

Blocks that fail to parse are silently dropped, and the parser
deduplicates by content hash so re-emitting the same block during
streaming is safe.

## See also

- [api.md](api.md) — full HTTP and event surface.
- [layouts.md](layouts.md) — how to add a Review pane.
