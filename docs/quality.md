# Architectural Quality

The quality engine reduces a workspace to one continuous `quality_signal` in the 0–10000 band, computed as the geometric mean of five graph-level metrics. Scans run on demand from the desktop panel, the `loop quality scan` CLI, the `quality_scan` MCP tool, or — when enabled — automatically after each agent file edit.

Design is inspired by [sentrux](https://github.com/sentrux/sentrux) (Rust, MIT). Algorithms are clean-room Go re-implementations under `internal/quality/`.

**Related docs:** [Configuration](configuration.md) | [HTTP API](api.md) | [Events System](events.md) | [MCP Server](mcpserver.md) | [Commands](commands.md)

---

## What the signal measures

| Metric | Package | Captures | Signal direction |
|---|---|---|---|
| **Modularity** (Newman's Q) | `internal/quality/metrics` | Cohesion within packages vs. coupling across them. | Higher = healthier. |
| **Cycles** (Tarjan SCC) | `internal/quality/metrics` | Strongly-connected components in the import graph. | Fewer/smaller = healthier. |
| **Depth** (Lakos levelization) | `internal/quality/metrics` | Longest path through the dependency DAG. | Lower = healthier. |
| **Equality** (Gini) | `internal/quality/metrics` | Inequality of per-function complexity. | Lower (less concentration) = healthier. |
| **Redundancy** | `internal/quality/metrics` | Dead-code candidates and duplicate clones. | Lower = healthier. |

Each metric returns a `Score` in `[0, 1]`; `quality_signal` is `round(10000 * geo_mean(scores))`. See `internal/quality/metrics/signal.go`.

---

## Languages

The active grammar set is pinned in `internal/quality/grammars.go`:

- **Go** — `internal/quality/parser/queries/go.scm`
- **TypeScript** — `internal/quality/parser/queries/typescript.scm`
- **JavaScript** — `internal/quality/parser/queries/javascript.scm`

Files in other languages are enumerated and counted toward `quality.max_files` but are skipped at parse time and reported via the `parse_failed` counter. To add a language: append to `ActiveLanguages` in `grammars.go`, vendor the upstream tree-sitter `tags.scm` plus any quality-specific captures into `internal/quality/parser/queries/<lang>.scm`, and add fixtures under `internal/quality/parser/testdata/`.

The parser is [`github.com/odvcencio/gotreesitter`](https://github.com/odvcencio/gotreesitter) — a pure-Go tree-sitter runtime, MIT-licensed. No CGO; cross-compiles to every Go target supported by Loop.

---

## Configuration

Quality is driven by `quality.*` keys in `~/.loop/config.json` and per-project `.loop/config.json`. See [Configuration: Quality](configuration.md#quality).

```jsonc
"quality": {
  "max_files": 25000,                  // hard cap; over this returns RepoTooLarge
  "exclude_paths": ["docs/**"],        // appended to .gitignore + built-in defaults
  "rules": {
    "signal_floor":     { "enabled": true, "threshold": 5000 },
    "parse_fail":       { "enabled": true, "threshold": 0.01 },
    "no_import_cycles": { "enabled": true }
  }
}
```

Built-in path exclusions (always applied): `.git/`, `node_modules/`, `dist/`, `build/`, `target/`, `vendor/`, `*.min.js`, `*.generated.go` (see `internal/quality/graph/enumerate.go`). The repo's `.gitignore` is layered on top, then `quality.exclude_paths` is appended.

Config changes drop the engine's parser/graph cache for the affected channel but do **not** auto-trigger a rescan — the panel keeps rendering the previous snapshot until the next manual scan rebuilds with the new config.

---

## Snapshots

One row per `(channel_id, branch)` is persisted in the `quality_snapshots` table:

| Column | Type | Notes |
|---|---|---|
| `channel_id` | `TEXT` | Foreign key on `channels(id)`, `ON DELETE CASCADE`. |
| `branch_name` | `TEXT` | Composite primary key with `channel_id`. |
| `scanned_at` | `DATETIME` | UTC stamp from the engine clock. |
| `signal_value` | `INTEGER` | The 0–10000 quality_signal. |
| `geo_mean` | `REAL` | Pre-rounded geometric mean of metric scores. |
| `metric_breakdown_json` | `TEXT` | Per-metric score + raw value (JSON array). |
| `tile_data_json` | `TEXT` | Per-file deficit attribution for the treemap (JSON array). |

Manual "Scan now" and live-rescan both **upsert** for the current branch. Other branches' snapshots are preserved (switching branches doesn't lose data). Rows older than 7 days are pruned on engine start. See `internal/quality/snapshot/snapshot.go`.

When the panel asks for a snapshot whose branch differs from the current branch, the API returns the most recent row with `branch_mismatch: true` so the UI can render a banner.

---

## Rules

Three built-in rules ship enabled by default. Each emits structured citations consumable by the panel and any future `rules` MCP tool.

| Rule | Default | Tunable | Citations |
|---|---|---|---|
| `no_import_cycles` | `enabled: true` | enable/disable | One per SCC > 1 file. |
| `signal_floor` | `threshold: 5000` | enable/disable, threshold | The current signal value. |
| `parse_fail` | `threshold: 0.01` (1.0%) | enable/disable, threshold | Aggregate parse-fail count vs. scanned. |

Per-rule overrides go under `quality.rules.<name>.{enabled,threshold}` (project config and global config both honored). Rule pass/fail is **data**, not behavior: the CLI exits 0 regardless. CI gates on the JSON output:

```sh
loop quality scan --json | jq -e '.rules.failed | length == 0'
```

---

## Surfaces

### Desktop panel

`QualityPanel.tsx` (`app/src/components/panels/QualityPanel.tsx`). Available as a per-channel split-pane panel; opens via the `+` panel switcher or the chat-bar quality indicator.

Layout, top to bottom:

1. **Headline signal** — the 0–10000 number with red/amber/green band (red < 5000, amber 5000–7000, green > 7000).
2. **Metric cards** — one per metric (Modularity, Cycles, Depth, Equality, Dead Code) with current value.
3. **Treemap** — `@visx/hierarchy` binary treemap. Tile size = file LOC, tile color = per-file deficit (red = drag on signal, green = healthy). Clicking a tile selects it for the popover.
4. **Diagnostic popover** — opens when a tile is selected; shows path, LOC, top-reason metric, and per-metric deficit breakdown for that file.
5. **Failed rules** — citation cards for any failing rule.

Empty state (no snapshot ever scanned for this channel) renders a centered "Scan now" button plus a one-line explainer. Loading state during a scan dims the previous snapshot and shows `Scanning… <done>/<total>` driven by `quality.scan_progress` events (throttled to ~250 ms, with a guaranteed first and terminal tick).

The chat-bar **quality indicator** (`app/src/components/chat/QualityIndicator.tsx`) renders as a round dot in the input toolbar. Outline before the first scan; filled with the band color after. Clicking it adds a `quality` leaf to the channel's split-pane tree (lands on the empty-state if no snapshot exists).

### CLI

```sh
loop quality scan      [path]                    [--max-files <n>] [--json]
loop quality cycles    [path]                    [--max-files <n>] [--json]
loop quality whatif    [path] --file <muts.json> [--max-files <n>] [--json]
loop quality evolution [path] [--since-months N] [--max-commits N] [--json]
loop quality c4        [path]                    [--max-files <n>] [--json]
```

All subcommands run a one-shot scan of `[path]` (defaults to CWD). `--json` emits the same payload as the matching MCP tool / HTTP endpoint. Exit code 0 unless the engine itself crashes — rule pass/fail (for `scan`) is data in the output.

- **`scan`** — full scan; prints the signal + metric breakdown.
- **`cycles`** — lists Tarjan SCCs > 1 file.
- **`whatif`** — reads a JSON mutation list (`{op, path, new_module?, parts?}` or array) from `--file <path>` (use `-` for stdin) and prints the predicted Δ signal.
- **`evolution`** — `git log` over the configured window (default 12 months / 1000 commits) for coupling, churn, and bus-factor signals. Requires `git` on PATH.
- **`c4`** — emits a Mermaid `flowchart` clustered by Go package (file directory), with top-level segments wrapped in `subgraph` blocks.

Useful for CI gates and ad-hoc inspection without running the daemon.

### MCP tools

| Tool | Description |
|---|---|
| `quality_scan` | Trigger a scan for the current channel. Returns a status hint immediately; the full report ships via the `quality.scanned` event. |
| `quality_snapshot` | Read the persisted snapshot (current branch first, then most recent). Returns the signal, geo-mean, per-metric breakdown, and a branch-mismatch flag. |
| `quality_cycles` | List Tarjan SCCs > 1 file from the current cached graph. |
| `quality_metrics` | Per-metric raw values + scores from the latest snapshot. |
| `quality_diagnostics` | Top-N file deficit attribution (god files, hotspots) drawn from the snapshot's tile data. |
| `quality_rules` | Pass/fail status for the built-in rules with structured citations. |
| `quality_whatif` | Apply a list of mutations (`delete`, `move`, `split`) to a shadow graph and return the predicted Δ signal. The real graph is never touched. |
| `quality_evolution` | Git-history coupling pairs, churn hotspots, and bus-factor risk over the configured window (default 12 months / 1000 commits). |
| `quality_c4` | Mermaid `flowchart` clustered by Go package, top-level dirs wrapped in `subgraph` blocks. |
| `quality_bugfactor` | Files with the highest combined deficit + churn signal — best refactor candidates. |

All tools read `channelID` from the per-channel MCP server struct; they take no `WorkDir` argument.

### HTTP

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/channels/{id}/quality/scan` | Kick a scan asynchronously. Returns `202 Accepted` with `{"status": "started" \| "in_progress"}`. The full report follows over the events WebSocket. |
| `DELETE` | `/api/channels/{id}/quality/scan` | Cancel an in-flight scan for the channel. Emits `quality.scan_cancelled`. `204` if a scan was running, `404` otherwise. |
| `GET` | `/api/channels/{id}/quality/snapshot` | Fetch the persisted snapshot. `404` when no snapshot exists. |
| `GET` | `/api/channels/{id}/quality/cycles` | Tarjan SCCs > 1 file from the current cached graph. |
| `GET` | `/api/channels/{id}/quality/metrics` | Per-metric values + scores from the latest snapshot. |
| `GET` | `/api/channels/{id}/quality/diagnostics` | Top-N file deficit attribution. Optional `?limit=` (default 20). |
| `GET` | `/api/channels/{id}/quality/rules` | Pass/fail rule citations. |
| `POST` | `/api/channels/{id}/quality/whatif` | Apply a mutation list (request body) to a shadow graph and return the predicted Δ signal. |
| `GET` | `/api/channels/{id}/quality/evolution` | Git-history coupling, churn, bus-factor. Optional `?since_months=` and `?max_commits=`. |
| `GET` | `/api/channels/{id}/quality/c4` | Mermaid `flowchart` for the current graph. |
| `GET` | `/api/channels/{id}/quality/bugfactor` | Top refactor candidates by deficit + churn. Optional `?limit=`. |

There is no auth — endpoints are intended for local Electron only, like every other `/api/*` route.

### Events

All quality events use the standard 2-segment `noun.verb` format and are scoped to a `channel_id`.

| Event | Emitted | Payload |
|---|---|---|
| `quality.session_started` | When a scan starts. | `{dir_path, branch}` |
| `quality.scanned` | When a scan completes. | `QualityScanReport` (full payload — signal, metrics, tiles, rules). |
| `quality.rules_violated` | When a scan completes with at least one failing rule. | `{passed, failed}` rule lists. |
| `quality.session_ended` | When a scan returns (success or failure). | `{branch, ok, error?, repo_too_large?}` |
| `quality.scan_progress` | Throttled (~250 ms) during a scan, plus a guaranteed first and terminal tick. | `{done, total}` |
| `quality.scan_cancelled` | When `DELETE /quality/scan` cancels an in-flight scan. | `{branch}` |

See [Events System](events.md) for subscription details.

---

## Triggering scans

There is no live-rescan loop. Scans run on demand:

- **Panel** — the per-channel Quality panel exposes a "Scan now" button.
- **CLI** — `loop quality scan [--json]` from the workdir.
- **MCP** — the agent calls the `quality_scan` tool (per-channel).

---

## Repo-too-large guard

If, after applying built-in defaults + `.gitignore` + `quality.exclude_paths`, the remaining file count exceeds `quality.max_files` (default 25000), the engine refuses the scan and returns a structured `RepoTooLargeError`. The MCP tool, HTTP endpoint, and CLI surface this verbatim.

The cap is checked once at the start of file enumeration — no partial scan is produced. The panel's empty-state banner asks the user to add patterns to `quality.exclude_paths` or raise `quality.max_files`.

---

## Coalescing concurrent scans

The engine holds one in-flight cancel function per `channel_id`. A second `quality_scan` arriving while a scan is running returns `{status: "in_progress"}` immediately — no queue, no cancel-and-replace. The panel disables its "Scan now" button on `quality.session_started` and re-enables on `quality.scanned` / `quality.session_ended` / `quality.scan_cancelled`.

`DELETE /api/channels/{id}/quality/scan` invokes the cached cancel function — the engine returns `context.Canceled`, the daemon emits `quality.scan_cancelled`, and the partial state is discarded. Returns `404` if no scan is running.

---

## Package layout

```
internal/quality/
├── doc.go                  # package overview
├── grammars.go             # active gotreesitter grammar pin
├── engine/                 # entry point: Scan() orchestrates parser→graph→metrics→snapshot
├── parser/                 # tree-sitter wrapper + .scm queries (go/ts/js)
│   └── queries/
├── graph/                  # file enumeration, .gitignore, dependency graph + cache
├── metrics/                # 5 metrics + signal aggregation + tile attribution
├── rules/                  # 3 built-in rules + per-rule config
├── snapshot/               # SQL-backed persistence (sql_store.go)
├── whatif/                 # shadow-graph mutation simulator + predicted Δ signal
├── evolution/              # git-log coupling / churn / bus-factor analysis
└── c4/                     # Mermaid flowchart emitter (Component layer)
```

---

## Development

```sh
# Unit tests for the engine packages
go test ./internal/quality/...

# Local one-shot scan against the current repo
go run ./cmd/loop quality scan --root .
```

Coverage gate (`make coverage-check`) runs over the entire tree, including all of `internal/quality/...`. The parser package exposes a `parser.Parser` interface so unit tests run without invoking gotreesitter on every iteration.
