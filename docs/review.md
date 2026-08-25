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
   commits any leftover changes via `git add -u` (tracked-only). Because
   these are real workflow runs, each node's input/output (and the `fix`
   prompt node's Claude session id) is inspectable in the Workflows panel —
   see [Per-Node Run View](workflows.md#per-node-run-view). A chip
   in the panel header mirrors the workflow events
   (`workflow.node_started`, `workflow.run_completed`, …) so the
   operator can see `review iter 2/3 — running`, `fixing — iter 2/3`,
   `paused at gate — …`, `done — 0 comments remaining`, or
   `stopped — no progress (same findings)` without leaving the Review
   panel. The agent reports findings by calling `ReportFindings`; the
   daemon reads that tool call off the agent's output stream and posts
   each finding into the review session, broadcasting it to the FE as it
   arrives. An override prompt can instead use the
   `report_review_findings` MCP tool — see
   [Required output format](#required-output-format).
3. **Push** — each comment ships with **Push** (single) and a **Push all
   (N)** affordance in the header (when at least one comment is unpushed).
   The backend uses `gh api ... /pulls/N/comments` against the captured
   head SHA so comments anchor to the right commit even if the PR is
   force-pushed later.
4. **Close** — closing the session deletes the in-memory session record
   and removes the worktree on disk. Pushed comments remain on GitHub.

## Forking the chat session

By default a review run starts from a **fresh** Claude session: the
reviewer sees the diff and the prompt, nothing else. That is usually what
you want — a reviewer with no memory of how the code was written has no
sunk cost in it.

When the chat's context *is* the point (a long design discussion, a
constraint the diff can't show), the dropdown next to the Run button
switches the run to fork it:

- **Fresh session** — the default, described above.
- **Fork chat session** — forks whatever session the channel's chat is on
  at the moment the run starts. Resolved per run, not when you pick it,
  so a session that rolls over (compaction, its own fork) is picked up.
- **Fork session id…** — forks the id typed into the adjacent input.
  Useful for replaying a review against an older conversation.

It is always a **fork**, never a resume: the review agent gets a copy of
the conversation and its own turns land in a new session, so the chat you
are still typing into is untouched.

Mechanically, the daemon copies the source session's transcript from the
channel's Claude project dir into the worktree's before launching, because
Claude Code keys session files by CWD and the review runs rooted in the PR
worktree. The agent is then started with `--resume <id> --fork-session`.
If the fork can't be resolved or staged — no chat session yet, an id with
no transcript on disk — the Run fails up front with a `400` rather than
leaving the session stuck in `reviewing`.

The choice is stored on the in-memory review session (it resets when the
daemon restarts) rather than on the run request, because the Run button
dispatches a workflow whose `loop review run` step has nowhere to carry
per-run options. See [`PUT /review/fork`](api.md).

## Navigating comments

The diff view offers two granularities of navigation, because a review with
twenty comments spread over four files is painful to scroll by hand:

- **Toolbar prev/next** (top of the diff, always visible) steps **file to
  file**, skipping files with no comments. The counter reads
  `n / m commented`, where `m` folds in unique out-of-diff paths so it
  matches every commented entity on screen.
- **Floating prev/next** (`review-comment-nav`, pinned bottom-right over the
  scroll) steps **comment to comment**, in render order: files top-to-bottom,
  within a file by the diff line each comment anchors to, then out-of-diff
  comments last. Jumping to a comment in a collapsed file expands that file
  first and moves the file rail's highlight with it. The widget only appears
  once the session has at least one anchored comment.

The floating counter re-measures on scroll and reports whichever comment sits
nearest the viewport's midpoint, so it stays honest when the user scrolls by
hand rather than by button. A comment whose line falls outside every hunk has
no row to render under and is excluded from the count — the backend widens
`git diff -U` enough that this should not happen in practice.

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
endpoint from a shell or a workflow `bash` node. The agent container
exports both `CHANNEL_ID` and `API_URL`, and the CLI falls back to them,
so the seeded review workflows' bash body is simply:

```sh
loop review run --pr {{.Inputs.pr}} --wait
```

`--pr` is optional (blank via the seeded `pr` input) — leave it blank to
review the channel's already-loaded review, or pass a PR number/URL to load
and review that PR. The load is **idempotent**: `loop review run` first GETs
the channel's review session and skips the (destructive, worktree-rebuilding)
load when it's already on that PR. So the Review panel can pre-load a PR and
pass its number as the `pr` input for traceability without triggering a second
load. Any session-lookup failure falls back to loading.

| Flag | Default | Description |
|---|---|---|
| `--channel-id` | `$CHANNEL_ID` | Channel whose review session to drive. Falls back to the container-injected `$CHANNEL_ID`; required only if neither is set. |
| `--api-url` | `$API_URL` then `http://localhost:8222` | Daemon URL. The agent container already exports `$API_URL`. |
| `--pr` | (none) | PR number (`567`) or URL (`.../pull/567`) to **load** into the channel's review session (fetch PR + create its worktree) before running. Omitted → review whatever the channel already has loaded. |
| `--wait` | `false` | Block until the session reaches a terminal status (`ready` or `error`) and emit the JSON envelope to stdout. Without `--wait`, the command exits 0 immediately after the `202`. |
| `--timeout` | `60m` | Bound on the total `--wait` time. Enforced inside the HTTP client, not just between polls, so a hung response can't outlive the deadline. Transient transport errors (TCP reset, momentary daemon restart, proxy 502) back off and retry instead of failing the whole loop. Sits above the daemon-side review ceiling (50m) so the daemon flips first with a meaningful error rather than the CLI's generic timeout. |

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

Findings reach the daemon two ways, both landing in the same ingest path.

The default prompt is the bare `/code-review` slash command, which
reports through Claude Code's own **`ReportFindings`** tool. The daemon
intercepts that tool call on the agent's stream, so nothing has to round
-trip through HTTP. Findings need a repo-relative `file` and a 1-based
`line`: the tool's schema treats `line` as optional, but a finding
without one can't be anchored in the diff and is dropped, so the default
system prompt requires it. `summary` and `failure_scenario` are joined
into the comment body.

An override prompt that is *not* a slash command gets no system prompt
from the daemon, so it must state its own contract. Either instruct the
agent to call `ReportFindings` as above, or use the
**`report_review_findings`** MCP tool (registered in every agent
container) with the full findings list. Each MCP finding carries:

- `path` — repo-relative file path.
- `line` — the 1-based line on the indicated side of the diff.
- `side` — `"RIGHT"` for added/modified lines (the common case, and the
  default when omitted) or `"LEFT"` for lines removed from the base.
  This matches GitHub's `pulls/{N}/comments` API, so the value is
  forwarded as-is on push.
- `body` — one paragraph describing the issue.

Malformed findings (empty path/body, non-positive line) are skipped, and
the daemon deduplicates by a stable content hash of path/line/body, so
re-reporting the same finding — in the same call, over both channels, or
in a later run — is safe.

## See also

- [api.md](api.md) — full HTTP and event surface.
- [layouts.md](layouts.md) — how to add a Review pane.
