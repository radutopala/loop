---
title: Review Panel
---
The Review panel loads a GitHub pull request into a local git worktree,
runs an agent review pass against the diff, and lets the user push the
resulting inline comments back to the PR — either one at a time or all
at once.

## Enabling

The panel is gated behind `review.enabled` (default `false`). Set it in
`~/.loop/config.json` (or a project override) to opt in:

```jsonc
"review": {
  "enabled": true
}
```

When disabled, the FE hides the Review tab from the panel picker and the
backend returns `403` for `/review/*` requests. The flag is layered
per-global / per-project / per-worktree the same way as `github.gh_user`.

## Lifecycle

1. **Load** — the FE fetches open PRs (`GET /review/prs` → `gh pr list`)
   and renders them as a clickable list; the user picks one. The backend:
   - Looks the PR up via `gh pr view <number>` for metadata and base/head refs.
   - Resolves the head SHA via `gh pr view --json headRefOid`.
   - Creates a worktree off the PR head branch under the channel's `dir_path`.
   - Fetches the base ref into the parent repo (`git fetch origin <base>`) so
     `origin/<base>` resolves locally for the Run step.
2. **Run** — the user clicks the primary **Run** button. The button is a
   **split button** with a caret dropdown that picks one of two modes:

   - **Run review** — a single review pass (the original one-shot behavior).
   - **Run review + fix loop** — review → fix → re-review, capped at
     `max_iterations` (set via the small numeric input next to the button,
     1–10, default `1`). Each iteration runs the same review prompt as the
     one-shot mode; comments stream live into the panel as they arrive.
     The loop stops early when an iteration returns zero comments
     **or** the same comment-id set as the previous iteration
     (`SameAsPrev` gate — guards against the agent ignoring "do not
     re-emit" instructions). The mode and max-iter value persist in
     `localStorage` so they survive reloads.

   Both modes are backed by seeded workflows (`review-loop`,
   `review-fix-loop`) shipped via `fsmigrate`; the fix-loop body runs
   `review → fix → verify` per iteration, where `verify` stages and
   commits any leftover changes via `git add -u` (tracked-only). A chip
   in the panel header mirrors the workflow events
   (`workflow.node_started`, `workflow.run_completed`, …) so the
   operator can see `review iter 2/3 — running`, `fixing — iter 2/3`,
   `paused at gate — …`, `done — 0 comments remaining`, or
   `stopped — no progress (same findings)` without leaving the Review
   panel. The agent emits `<review-comment path="..." line="N"
   side="RIGHT|LEFT">...body...</review-comment>` blocks, which are
   parsed and streamed to the FE as they arrive.
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

While a fix loop is active, the primary Run button is disabled on the
FE: the workflow controls the channel's worktree (review session +
auto-commits), and starting a second concurrent run mid-loop would
race those operations.

## Gate approvals during a fix loop

When the fix step trips a security-gate rule with `decision: approve`
(`gates.agentgate` / `gates.docker_proxy`, see [gates.md](gates.md)),
the loop pauses and the workflow run enters `paused` status. The
`ApprovalCard` renders **inline inside the Review panel** when no Chat
panel is mounted in the current layout, so operators working in a
Review-only layout can resolve the gate without switching layouts.
When a Chat panel **is** mounted, the chip flips to
`paused at gate — see chat` and the card renders in chat as it
already does for other agent runs.

## CLI

The host-side `loop review run` subcommand drives the same async
endpoint from a shell or a workflow `bash` node. Inside the seeded
review workflows the bash body looks like:

```sh
loop review run --channel-id $CHANNEL_ID --api-url $API_URL --wait
```

| Flag | Default | Description |
|---|---|---|
| `--channel-id` | (required) | Channel whose review session to drive. |
| `--api-url` | `$API_URL` then `http://localhost:8222` | Daemon URL. The agent container already exports `$API_URL`. |
| `--wait` | `false` | Block until the session reaches a terminal status (`ready` or `error`) and emit the JSON envelope to stdout. Without `--wait`, the command exits 0 immediately after the `202`. |
| `--timeout` | `30m` | Bound on the total `--wait` time. Enforced inside the HTTP client, not just between polls, so a hung response can't outlive the deadline. Transient transport errors (TCP reset, momentary daemon restart, proxy 502) back off and retry instead of failing the whole loop. |

The emitted JSON shape is `{"status":"ready","no_comments":bool,"comments":[...]}` — the same payload used by the workflow body parser to populate `{{.Review.*}}` templates inside the seeded loops.

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
