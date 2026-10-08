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
     The loop stops early when the session has no comments **or** an
     iteration leaves the session's comment-id set as it was
     (`SameAsPrev` gate). The dedup pass after each review folds the
     findings it reports again, so a round with nothing new ends the
     loop. A finding the daemon drops as a duplicate (see
     [Required output format](#required-output-format)) doesn't change the
     set either. The mode and max-iter value persist in
     `localStorage` so they survive reloads.

   Both modes are backed by seeded workflows (`review-loop`,
   `review-fix-loop`) shipped via `fsmigrate`; the fix-loop body runs
   `review → fix → verify` per iteration, where `verify` stages and
   commits any leftover changes via `git add -u` (tracked-only). Because
   these are real workflow runs, each node's input/output (and the `fix`
   prompt node's Claude session id) is inspectable in the Workflows panel —
   see [Per-Node Run View](workflows.md#per-node-run-view).

   Every review run, a one-shot Run and each loop round alike, ends with
   the **dedup pass** before the session turns ready. The review isn't
   told which comments already exist, and a run often re-finds an issue on
   another line, in another file, or framed differently (a symptom rather
   than its cause), which the ingest-time pass (see
   [Required output format](#required-output-format)) doesn't catch. The
   dedup pass shows every comment in the session to a read-only model run
   (no Bash, no edits). The comments the run just added are marked new,
   and the model checks each of them against every other comment (GitHub
   ones included) instead of regrouping the whole session, which an
   earlier pass already did. A run that added no comment skips the pass. Each body is cut at 600 characters in the prompt;
   the run can read a cut one in full with `get_review_comments`. The
   model groups them by root cause (same issue
   means one change fixes both; when fixing the kept comment's root cause
   also resolves another comment, that one is a duplicate even if it has
   a narrower fix of its own), keeps the most severe and specific
   comment in each group, and says what the others add. The daemon deletes
   the others from the panel and, if they were pushed, from the PR, and
   appends that note to the kept comment as an `Also flagged:` paragraph.
   The note is only added when the kept comment is an unpushed agent
   finding; comments on GitHub are never edited. The pass only ever
   deletes agent findings: comments read from GitHub can be kept, but are
   never dropped. Findings about the same code path that still need
   separate fixes are reported as related, and nothing is deleted for
   them. A comment that bundles several issues duplicates a comment that
   reports one of them: the model folds the single-issue comment into the
   bundle, or, when the single one is the better keeper, rewrites an
   unpushed bundled agent finding to only the issues nothing else covers.
   The pass also re-checks where each surviving agent finding is
   anchored, against the file read with line numbers, and moves an
   unpushed one that sits a few lines off (on a blank line, a closing
   brace or a neighbouring statement) to the statement it is about, within
   20 lines. It then checks each agent finding it keeps (only the new
   ones, after a review run) against the code and records a verdict on
   it: `real`, `false_positive` (the code doesn't do what the comment
   says, or the behaviour is intended, handled elsewhere or what the PR
   sets out to do) or `already_fixed`, with a one-sentence reason citing
   the line it read. Moves and verdicts are claims about the code, so
   Loop keeps one only when the pass opened that comment's file with the
   Read tool; one made from the comment text alone is dropped (and
   logged). The diff card shows the
   verdict as a badge (verified, false positive, already fixed) with the
   reason on hover, and `get_review_comments` lists it. A verdict deletes
   nothing, and it is set on pushed findings too, since it stays local.
   The session records which comment each deleted one was
   folded into, and `loop review run --wait` reports it as `superseded`.
   The loop's stop check compares the comments the pass leaves with the
   previous round's, and counts a deleted comment as the one it was
   folded into, so a round whose new findings all fold into earlier ones,
   whichever of the two the pass keeps, ends the loop. If the pass fails,
   it is logged and the run's findings stay as reported. In
   `review-fix-loop` the pass is over before the fix step, so that step
   isn't handed reworded repeats of findings it already fixed.

   A chip
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
3. **Push** — each comment ships with **Push** (single), and the header's
   **All ▾** menu carries **Push all to GitHub (N)** (when at least one
   comment is unpushed). The backend uses `gh api ... /pulls/N/comments` against the
   captured head SHA so comments anchor to the right commit even if the PR
   is force-pushed later. A pushed comment stays one comment: when Sync or
   a run reads the PR's comments back, GitHub's copy of it is folded into
   the finding, which takes its link and outdated/resolved state. GitHub
   is not the only exit — see
   [Handing a finding to the agent](#handing-a-finding-to-the-agent).
4. **Close** — closing the session deletes the in-memory session record
   and removes the worktree on disk. Pushed comments remain on GitHub.

## Model and effort

Two dropdowns next to the Run button pick the **model** and the
**reasoning effort** the review agent runs with. **Default** follows the
config's `claude_model` / `claude_effort` for the channel and names the
value it resolves to. A reviewer that thinks longer finds more, so
`high`, `xhigh` and `max` are marked **recommended**. Lower levels are fine
for a quick pass but miss findings.

The effort also goes to the `/code-review` skill as its level
(`/code-review high`). Left to pick its own level, the skill reviews a
`--effort medium` run at its high level. With Claude Code 2.1.294 on
Opus 5 models, the level picks one of four ways to review:

| Level | How the skill reviews |
|---|---|
| `low` | A short review in one pass. |
| `medium`, `high` | One careful pass over the diff, at most 15 findings. Both levels get the same instructions; only the model's effort differs. |
| `xhigh` | 10 review angles one after another, then a sweep for gaps, all in one context. |
| `max` | Finder subagents per angle, a subagent that verifies each candidate, then a sweep. The only level that uses subagents. |

One run per level on a 26-file diff (+1100/−311), Opus 5.5:

| Level | Time | Findings | Cost |
|---|---|---|---|
| `low` | 35 s | 0 | $0.30 |
| `medium` | 114 s | 8 | $0.83 |
| `high` | 214 s | 7 | $1.31 |
| `xhigh` | 523 s | 14 | $3.27 |
| `max` | over 30 min, stopped | – | – |

These are single runs, so take the numbers as rough. `low` is too
shallow for a PR. `medium` and `high` find about as much, so `medium` is
the cheaper choice for a loop that runs several rounds. `xhigh` found
about twice as many at about 2.5 times the cost of `high`. `max` was
stopped after 30 minutes still reading code with 10 subagents, before it
reported anything.

The review isn't told which comments the session already holds. A list of
them in its system prompt didn't stop it reporting some of them again,
even on a fresh session, and it grew with every comment. The
[dedup pass](#lifecycle) after every run folds the repeats instead.

The choice applies to review runs only; the chat's own model/effort
override (the composer pill) is separate. Like the fork choice below, it is
stored on the in-memory review session, survives the refresh every run
starts with, and resets when the daemon restarts. See
[`PUT /review/agent`](api.md).

## Forking the chat session

By default a review run **forks the chat session**: the reviewer starts
from a copy of the channel's conversation, so it knows the design
discussion and the constraints the diff can't show. The dropdown next to the
Run button changes that:

- **Fork chat session** — the default. Forks whatever session the channel's
  chat is on at the moment the run starts. Resolved per run, not when you
  pick it, so a session that rolls over (compaction, its own fork) is
  picked up. If the channel has no chat session yet, the run starts fresh.
- **Fresh session** — the reviewer sees the diff and the prompt, nothing
  else. A reviewer with no memory of how the code was written has no sunk
  cost in it.
- **Fork session id…** — forks the id typed into the adjacent input.
  Useful for replaying a review against an older conversation.

It is always a **fork**, never a resume: the review agent gets a copy of
the conversation and its own turns land in a new session, so the chat you
are still typing into is untouched.

Mechanically, the daemon copies the source session's transcript from the
channel's Claude project dir into the worktree's before launching, because
Claude Code keys session files by CWD and the review runs rooted in the PR
worktree. The agent is then started with `--resume <id> --fork-session`.
If the fork can't be resolved or staged — an id with no transcript on
disk, say — the Run fails up front with a `400` rather than leaving the
session stuck in `reviewing`.

The choice is stored on the in-memory review session (loading a PR resets
it to the default, and so does a daemon restart) rather than on the run
request, because the Run button dispatches a workflow whose
`loop review run` step has nowhere to carry per-run options. See [`PUT /review/fork`](api.md).

## Handing a finding to the agent

A finding can also go to the channel's chat instead of (or before) GitHub.
Five affordances do that, and they differ in **who writes the message** and
**whether it is sent**. Address all and Check all sit in the header's
**All ▾** menu, next to Push all to GitHub; each shows only when it has
comments to act on:

| Button | Message | Sent? |
|--------|---------|-------|
| **Discuss** | The finding quoted under its `path:line`, with a blank line below it | No — it lands in the composer for you to finish |
| **Why?** | The same quote, with `Please explain why we need this.` filled into that blank line | Yes, straight away |
| **Address** | `Please address this review comment from the PR:` plus file, line, side, PR number, head SHA and author, then the quoted body and the run transcripts | Yes, straight away |
| **Address all (N)** | The same request over every unpushed finding: the metadata header once, then a numbered block per finding, then the run transcripts | Yes, straight away |
| **Check all (N)** | A request to clean up, then verify, every finding this session's runs reported (not the GitHub comments): PR number, head SHA and the PR checkout, then steps — run `dedup_review_findings`, read what is left with `get_review_comments`, check each against the code (real / false positive / already fixed), show the verdicts as a [chat component](chat.md#components) with a card per comment (its category when it has one), a verdict filter and a link to each pushed comment's GitHub thread (no severity unless the comment states one), then reply with what the dedup pass removed and the count per verdict; change nothing else | Yes, straight away |

Discuss and Why? are one builder and one send path differing by a single
string, so the quote, the transcripts and the spacing cannot drift apart
between them. Discuss exists for the ask you have to phrase yourself; Why? is
the ask common enough to be worth a button, and since it leaves nothing to
type it sends rather than parking a finished message in the composer.

**Address** is the one that asks for a change, so it carries the most: the
metadata the agent needs to find the line *and* the transcripts that explain
the finding. It used to be two buttons — a "Push to chat" that sent the
metadata and an Address that sent the transcripts — which asked the user to
choose between two phrasings of the same request. They are one button now.
Unlike the old Push to chat it stays on a comment for its whole life, so it is
still there for a finding already filed to GitHub; only **Push to GitHub**
collapses into the `on github` marker once pushed.

**Check all** doesn't quote the findings. The agent first runs the
[dedup pass](#lifecycle), so it doesn't check the same issue twice, then reads
what is left itself with `get_review_comments`, which acts on the same channel's
session, so the request stays short however many findings there are and each
body arrives whole. The message carries no count, since the dedup pass changes
it. It
names the PR checkout because the review runs in the PR worktree, which need not
be the chat's working directory.

All five first dispatch `loop:open-panel`, so a Chat panel is mounted in the
current layout — anchored to the right of the Review panel when one has to be
created, so the answer arrives beside the diff it is about.

### Every form carries the run transcripts

The panel keeps a finding's verdict; the reasoning behind it lives only in the
transcript of the run that produced it. So the four forms that quote findings append every run
transcript the session knows, oldest first — inside the quote for Discuss and
Why?, as a trailing list for Address and Address all. The transcripts
belong to the session rather than to any one finding, which is why the batch
lists them once at the end rather than under every block:

```
> internal/api/x.go:12
> leaks the lock on the error path
>
> transcripts of the review runs that produced this, oldest first:
> /home/u/.claude/projects/-repo--worktrees-pr-7/<session-id>.jsonl

Please explain why we need this.
```

Full paths rather than bare ids, because Claude keys transcripts by the
agent's CWD — the PR worktree — which is not a directory the chat agent can
derive from its own channel. They resolve as-is inside an agent container,
which runs with `HOME` set to the host home and `~/.claude` bind-mounted at
its host path.

Each run records its id as it completes, and the first one records the
directory (`run_session_ids` and `transcript_dir` on `GET /review`). Until a
run has completed under the current daemon the session has neither, and the
block is omitted rather than half-written — an id with no directory is not a
path anyone can open, and a directory with no ids points at nothing.

## Searching the diff

The review diff carries the same find bar as the Git panel's, opened with the
magnifier in the file-navigation bar or with `⌘F` / `Ctrl+F` while the diff has
focus; `Esc` closes it. A bare query searches the patch text, and a leading `>`
switches to a fuzzy file jump. The matching rules — fzf `FuzzyMatchV1` path
ranking, smart case, wrapping `Enter` / `Shift+Enter` — are documented once, in
[git.md](git.md#searching-a-diff).

Two differences follow from this being a review:

- Stepping onto a match inside a **collapsed file expands it first**, the same
  way the comment navigator does.
- **Findings are not searched.** The comment navigator already walks those, and
  folding them in would make `3 / 40` count two different kinds of thing.

## Navigating comments

The diff view offers two granularities of navigation, because a review with
twenty comments spread over four files is painful to scroll by hand:

- **Toolbar prev/next** (top of the diff, always visible) steps **file to
  file**, skipping files with no comments. The counter reads
  `n / m files commented`, where `m` folds in unique out-of-diff paths so it
  matches every commented entity on screen.
- **Floating prev/next** (`review-comment-nav`, pinned bottom-right over the
  scroll) steps **comment to comment**, in render order: files top-to-bottom,
  within a file by the diff line each comment anchors to, then out-of-diff
  comments last. Jumping to a comment in a collapsed file expands that file
  first and moves the file rail's highlight with it. The widget only appears
  once the session has at least one anchored comment.
- **new | all** (the pair at the left of the floating widget) picks what
  the floating prev/next walks. **new**, the default, is only the findings
  this review session's runs reported, so a PR with a long GitHub thread
  history doesn't bury them. **all** adds the comments synced from GitHub
  on Load and Sync. The choice is remembered across sessions. It only
  narrows the navigator; every comment stays rendered in the diff.

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
exports both `LOOP_CHANNEL_ID` and `LOOP_API_URL`, and the CLI falls back to them,
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
| `--channel-id` | `$LOOP_CHANNEL_ID` | Channel whose review session to drive. Falls back to the container-injected `$LOOP_CHANNEL_ID`; required only if neither is set. |
| `--api-url` | `$LOOP_API_URL` then `http://localhost:8222` | Daemon URL. The agent container already exports `$LOOP_API_URL`. |
| `--pr` | (none) | PR number (`567`) or URL (`.../pull/567`) to **load** into the channel's review session (fetch PR + create its worktree) before running. Omitted → review whatever the channel already has loaded. |
| `--wait` | `false` | Block until the session reaches a terminal status (`ready` or `error`) and emit the JSON envelope to stdout. Without `--wait`, the command exits 0 immediately after the `202`. |
| `--timeout` | `60m` | Bound on the total `--wait` time. Enforced inside the HTTP client, not just between polls, so a hung response can't outlive the deadline. Transient transport errors (TCP reset, momentary daemon restart, proxy 502) back off and retry instead of failing the whole loop. Sits above the daemon-side review ceiling (50m) so the daemon flips first with a meaningful error rather than the CLI's generic timeout. |

The emitted JSON shape is `{"status":"ready","no_comments":bool,"comments":[...]}`, plus `"superseded":{"<deleted id>":"<kept id>"}` when the dedup pass after the run deleted any — the same payload used by the workflow body parser to populate `{{.Review.*}}` templates inside the seeded loops. Each comment carries the dedup pass's `verdict` and `verdict_reason` once it has one. The workflow parser leaves out the comments marked `false_positive` or `already_fixed`: the fix step doesn't get them, the same-as-previous check doesn't count them, and a round that leaves only those sets `NoComments`, so the loop stops.

`loop review dedup` runs the dedup pass on its own, over the whole session
rather than only the latest run's comments. It
takes the same `--channel-id`, `--api-url` and `--timeout` flags, blocks
until the pass is done, and prints the result, which lists every merged
group so a pass can be audited:

```json
{
  "removed": ["<id>", "..."],
  "clusters": [{"kept": "<id>", "removed": ["<id>"], "reason": "...", "note": "...", "note_added": true}],
  "related": [{"ids": ["<id>", "<id>"], "reason": "..."}],
  "moved": [{"id": "<id>", "from": 145, "to": 147}],
  "trimmed": [{"id": "<id>", "covered_by": "<id>", "reason": "..."}],
  "checked": 11,
  "errors": ["<id>: <msg>"]
}
```

`checked` counts the comments shown to the model. A comment whose delete
failed stays in the panel and is listed under `errors`. Neither case fails
the command. A run already in flight on the channel makes it fail with a
`409`.

Agents reach their own channel's session through MCP tools:
`get_review_comments` reads the PR and its comments (without the diff),
optionally only the unpushed agent comments or one file;
`dedup_review_findings` runs the dedup pass; `delete_review_comment`,
`update_review_comment`, `push_review_comment` and
`push_all_review_comments` manage single comments. An agent can't delete a
GitHub comment or act on another channel's session. See
[MCP server](mcpserver.md#review).

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

The default prompt is the `/code-review` slash command with the run's
effort (the panel's choice, else `claude_effort`) as its level, e.g.
`/code-review high` (see [Model and effort](#model-and-effort)). The
command reports through Claude Code's own **`ReportFindings`** tool. The daemon
intercepts that tool call on the agent's stream, so nothing has to round
-trip through HTTP. Findings need a repo-relative `file` and a 1-based
`line`: the tool's schema treats `line` as optional, but a finding
without one can't be anchored in the diff and is dropped, so the default
system prompt requires it. `summary` and `failure_scenario` are joined
into the comment body. `category` (a slug such as `correctness` or
`reuse`) is kept on the comment, lowercased: the panel shows it as a tag
on the comment's card and `get_review_comments` lists it. It isn't part
of the body, so it isn't pushed to GitHub.

The daemon doesn't check the line, so the PR context given to the
reviewer and to its finder subagents asks for it to be confirmed with a
numbered read of the file (the Read tool, `grep -n`, `nl -ba`) and pointed
at the statement the finding is about. `git diff` output carries no line
numbers, and an agent that counts from the hunk headers lands a line or
three off, on a blank line or a closing brace. The multi-round loop's
dedup pass re-checks anchors as a backstop.

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
- `category` — optional finding type slug, e.g. `correctness`, kept on
  the comment as with `ReportFindings`.

Malformed findings (empty path/body, non-positive line) are skipped, and
a finding whose id the session already holds is dropped. The id is a
stable content hash of path/line/body, so this catches an agent retrying
a report it already made, in the same call, over both channels, or in a
later run.

Nothing on the way in judges whether two differently worded findings are
the same. Each review run re-derives its findings rather than copying the
last run's text, so the same issue comes back reworded, anchored a few
lines off, or as a symptom of a cause another finding names. Telling those
apart is left to the [dedup pass](#lifecycle) that runs after every
review (or on demand through `POST .../review/dedup` and the
`dedup_review_findings` tool).

## See also

- [api.md](api.md) — full HTTP and event surface.
- [layouts.md](layouts.md) — how to add a Review pane.
- [git.md](git.md#searching-a-diff) — the find bar the review diff shares.
