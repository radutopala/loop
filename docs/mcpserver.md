---
title: MCP Server
---
MCP (Model Context Protocol) server that runs inside agent containers, providing tools for task scheduling, communication, and memory search.

**Package:** `internal/mcpserver`

## Overview

The MCP server is started as a subprocess via `loop mcp` inside each agent container. It communicates over stdio transport and proxies all operations through the daemon's HTTP API.

```
Agent (Claude Code)  ←→  MCP Protocol (stdio)  ←→  loop mcp  ←→  HTTP API  ←→  Daemon
```

### CLI Flags

| Flag | Description |
|------|-------------|
| `--channel-id` | Channel or thread ID |
| `--dir` | Project working directory (for memory lookups) |
| `--api-url` | Daemon HTTP API URL |
| `--author-id` | User who triggered the request |
| `--platform` | Platform identifier |
| `--memory` | Enable memory search tools |
| `--agent-id` | Agent ID for inter-agent tools and MCP Channels |
| `--log` | Custom log file path |

## Registered Tools

### Always Available (19 tools)

#### Task Management

| Tool | Description |
|------|-------------|
| `schedule_task` | Create a scheduled task (cron, interval, once, or manual) running a prompt, a workflow (`workflow_name`), or a shell script (`bash_script`). Supports `template_name` for deduplication and `auto_delete_sec` for thread cleanup. |
| `list_tasks` | List all scheduled tasks for the current channel and its worktree threads; `threads: true` adds its plain threads |
| `show_task` | Show full task details by ID including complete prompt |
| `cancel_task` | Cancel a scheduled task by ID |
| `toggle_task` | Enable or disable a task by ID |
| `edit_task` | Edit a task's schedule, type, prompt, workflow, bash script, or auto_delete_sec |
| `move_task` | Move a task to another thread, worktree thread or the root channel of its project (default: the current channel), keeping its settings; its thread moves too |

#### Communication

| Tool | Description |
|------|-------------|
| `send_message` | Send a message to a channel or thread. `channel_id` is optional — omit it to target the current channel/thread. Supports `@BotName` mentions (auto-converted to proper mentions). |
| `queue_message` | Queue a follow-up prompt for yourself in the current channel/thread/worktree. Enqueues a new turn behind any running/queued work (shows in the chat's queued-messages list); `interrupt=true` cancels the active run and jumps the queue to run next. `delay_seconds` (optional, non-negative) holds the prompt back until the delay elapses — it stays queued with a live countdown in the UI and a background poller drains it once due; a delay forces `interrupt` off. A delayed prompt's result includes its `msg_id`, for `delete_queued_message`. |
| `list_queued_messages` | List the messages waiting in the current channel/thread/worktree's queue (`GET /api/channels/{id}/queued`): each one's `msg_id`, whether it's running, queued or delayed (with the UTC time it runs), and its first 120 characters. |
| `delete_queued_message` | Remove a waiting message from the current channel/thread/worktree's queue by `msg_id` (`DELETE /api/messages/{id}`), like the chat's Remove from queue. The message is looked up in the queue first: one already running, or one not in the agent's own channel, is refused. Denied in read-only runs (learn and explain). |
| `monitor` | Wait for something in the background without holding the channel ([`POST /api/channels/{id}/monitors`](api.md#post-apichannelsidmonitors)): `command` runs with `/bin/sh -c` in a fresh agent container while the agent's turn ends, and when it exits or times out (`timeout_sec`, default 3600, at most 86400) a `[monitor <id>]` message with its status and output tail wakes the agent. That message queues behind a run in progress; with `interrupt` it cancels that run instead. Claude Code's own `Monitor` tool is denied in one-shot runs, since it would keep the run, and the channel, busy until the watch ends; a [live session](chat.md#live-sessions) allows it, since its process outlives the turn. At most 10 per channel. Own channel only; denied in read-only runs. |
| `list_monitors` | List the current channel's running monitors: id, description, start time (UTC), timeout and command. |
| `cancel_monitor` | Stop one of the current channel's monitors by `monitor_id`, without a wake-up message. Denied in read-only runs. |
| `list_sessions` | List the Claude Code sessions in a channel's project dir (`GET /api/channels/{id}/sessions`), newest first: each one's `session_id`, last write time (UTC) and the first 120 characters of its last message. The channel's current session is marked `*`, one a channel or thread already holds `[thread]`. `channel_id` is optional — defaults to the current channel/thread. |
| `resume_session` | Make another of the project's sessions the channel's (`PUT /api/channels/{id}/session`), like the [Sessions panel's](sessions.md#resuming) resume here, so its next message resumes that conversation. `channel_id` is optional — defaults to the current channel/thread. The agent's own channel has a run in progress, so its switch takes effect when that run ends (after the run saves its own session). A session a thread also holds is forked on the next run. Denied in read-only runs (learn and explain). |
| `create_channel` | Create a new channel (bot auto-joins) |
| `create_thread` | Create a thread in the current channel. If message provided, triggers an agent immediately. |
| `create_worktree_thread` | Create a thread backed by a fresh git worktree (mirrors the UI `+wt` button). `branch` is the existing **base** to fork from (e.g. `main`); a new `worktree/<name>` branch is created and checked out off it. Optional `name` for the worktree directory; optional `message` triggers an agent immediately. |
| `rename_thread` | Rename a thread, worktree thread or channel's display name. Only updates the name — the directory, git branch and Claude sessions are preserved. |
| `set_thread_description` | Set a thread or worktree thread's description (at most 500 characters), shown in the sidebar's hover popup. `thread_id` defaults to the agent's own thread; an empty description clears it. |
| `set_ticket_url` | Link a channel, thread or worktree thread to its ticket: the absolute http(s) URL of a Jira issue, GitHub issue or PR, or any other tracker's page. Shown in the sidebar's hover popup, and opened from the row's **Open Ticket** menu item. `channel_id` defaults to the agent's own channel or thread; an empty `ticket_url` clears it. |
| `fork_thread` | Fork a thread by ID (mirrors the sidebar `+fork` action): creates a sibling that continues the source's conversation on a forked Claude session, leaving the source untouched. Worktree threads also get a fresh worktree branched from the source's committed state. No agent is triggered — follow up with `send_message` to task the fork. |
| `delete_thread` | Delete a thread by ID |
| `search_channels` | Search channels and threads by optional query. Returns IDs, names, directory paths, parent IDs, and active status. |
| `channel_status` | Show a channel's tree (`GET /api/channels`): the channel, its threads and worktree threads and theirs, indented, each with the lines of its [sidebar hover popup](sidebar.md#row-info): description, ticket, path, branch (and a worktree's base), its PR/MR's number, checks and URL (`GET /api/channels/{id}/pr/status`, for the rows on a branch), why its `ci`, `cnf` and `cmt` pills are lit, commit, distance from its base and upstream, model/effort overrides, and whether its agent is running or it's locked. `channel_id` is optional — defaults to the tree the current channel/thread is in, marked `this one`. |

#### Review

| Tool | Description |
|------|-------------|
| `run_review` | Start a PR review as the Review panel's Run button does (`POST /api/channels/{id}/review/start`): `pr_number`, optional `model` and `effort` (the channel's own by default), `mode` (`review` or `review-fix`), `max_iterations` (1–10, default the project's `review.max_iterations`, 3 unless set), `fork` (`chat`, `fresh` or a session id to fork; the Review panel's choice by default, `chat` for a newly loaded PR), `scope` (`auto`, `full` or `incremental`, see [Incremental re-review](review.md#incremental-re-review); the Review panel's choice by default) and `channel_id` (this channel by default; another channel or thread's id runs the review there, so reviews can run in several threads at once). Loads the PR unless already loaded, starts `review-loop` or `review-fix-loop` and returns the run id. Refused while that channel has a review loading or running. Denied in read-only runs. |
| `report_review_findings` | Add findings to the channel's [PR review session](review.md) (`POST /api/channels/{id}/review/comments`): each one's repo-relative `path`, 1-based `line`, `side` (`RIGHT`, the default, or `LEFT`) and `body`. Duplicates are skipped. Denied in read-only runs. |
| `get_review_comments` | Read the channel's review session (`GET /api/channels/{id}/review?diff=false`): the PR number, title and URL, head SHA, status, the PR [outcome](review.md#outcome) with the comments that decide it, the commit it was approved at when it was, and every comment as `[id] path:line SIDE`, its source (`agent`, or `github by <author>` with the GitHub comment id), whether an agent comment was pushed (with its GitHub id), `outdated` and `resolved`, its `category`, `verdict` and `impact` when it has them, a GitHub thread's assessment (`thread addressed` or `thread open` with its impact), then the body, the verdict's or assessment's reason, the paths of the [transcripts](review.md#every-form-carries-the-transcripts) of the agent passes that reported or changed it, and the GitHub replies in its thread, each with its author and GitHub id. The count leaves replies out. `unpushed_only` keeps the agent comments not yet pushed; `path` keeps one file. The PR diff is left out. |
| `dedup_review_findings` | Run the [dedup pass](review.md) over the channel's review session (`POST /api/channels/{id}/review/dedup`), as `loop review:dedup` does: removes agent comments that repeat another comment (from the PR too when they were pushed; GitHub comments are never deleted), trims an unpushed one that bundles an issue another comment covers, re-anchors misplaced ones, notes related ones and records a verdict (`real`, `false_positive` or `already_fixed`, with a reason) on the agent comments it keeps, then lists what it did. Takes minutes; refused while a review run is in progress. Denied in read-only runs. |
| `delete_review_comment` | Delete an agent comment by `comment_id` (`DELETE /api/channels/{id}/review/comments/{cid}`), from the PR too when it was pushed. GitHub comments are refused. Denied in read-only runs. |
| `update_review_comment` | Replace an unpushed agent comment's `body` (`PATCH /api/channels/{id}/review/comments/{cid}`), e.g. to fold in what a deleted duplicate added, and/or record a `verdict` (`real`, `false_positive` or `already_fixed`) with a `reason`, replacing the dedup pass's, and with `real` its `impact` (`blocking` or `nice_to_have`); Check all and the verification step record their verdicts this way. Give `body`, `verdict` or both. A pushed comment takes only a verdict; GitHub comments can't change. Denied in read-only runs other than the verification step's. |
| `assess_review_thread` | Record whether an unresolved GitHub thread a person started still stands, by its first comment's `comment_id` (`POST .../review/comments/{cid}/assess`): `status` `addressed` (a later commit or a reply settles it) or `open` with its `impact` (`blocking` or `nice_to_have`), and a one-sentence `reason`. The [outcome](review.md#outcome) counts open threads; an agent comment takes a verdict instead. Denied in read-only runs other than the verification step's. |
| `push_review_comment` | Post one agent comment to the PR as the configured gh user (`POST .../review/comments/{cid}/push`). Denied in read-only runs. |
| `push_all_review_comments` | Post every unpushed agent comment to the PR (`POST .../review/push-all`), skipping those the dedup pass marked `false_positive` or `already_fixed`, and report how many were pushed, skipped and which failed. Denied in read-only runs. |

The review tools act on the agent's own channel only and take no `channel_id`. The daemon holds the routes that change comments to the token's own channel as well (see [Agent scope](api.md#agent-scope)).

#### Documentation

| Tool | Description |
|------|-------------|
| `get_readme` | Get Loop README with setup instructions, configuration, commands, and architecture |

#### Playground

| Tool | Description |
|------|-------------|
| `ui_state` | The desktop app's windows: each one's open channel, layout tab and panes (with agent terminals' status and whether they're busy), and the side view open beside the chat. Windows showing a channel outside the agent's project have no state. |
| `ui_run` | Run steps in a desktop app window, in order until one fails: open a channel, switch tabs, add, replace, remove or maximize panes, open a file, scroll the chat to a message (`show_message`), open or close the Learn, Explain or Shell side view and read it (`open_side`, pane `side`), type into an agent terminal, read its output or wait for it, or queue a message in a chat pane and wait for its reply. Without a leading `select_channel`, it opens the agent's own channel first; any channel it opens must be in the agent's project. Terminal steps work only on agent terminals (`docker-agent`, `docker-shell`) and chat panes (`chat`, `chat-side`) and the side view, never host shells. See [API: UI Bridge](api.md#ui-bridge). |
| `ui_walkthrough` | Walk the user through code or text in the editor: each step highlights lines of a file in the project, with a Markdown explanation on a card over them. The user steps through with Prev / Next (Alt+← / Alt+→), or it plays every `autoplay_sec` seconds (1 to 600) until the last step, a Pause, or the user stepping or editing. The card's Ask button sends the step's lines to the chat, as does Ask on a selection. Kept until the channel is deleted; reopen with `ui_run`'s `open_walkthrough`. See [API: Walkthroughs](api.md#walkthroughs). |
| `playground` | Manage playgrounds (action: create/update/delete). Create sets up the entry HTML, title, and description. Use `playground_file` to add JS, CSS, and other files. |
| `playground_file` | Manage files within a playground (action: create/update/read/delete/list). Write script.js, style.css, importmap.json, lib/utils.js, etc. Files served at relative URLs for ES module imports. |
| `playground_share` | Expose a playground publicly over a cloudflared quick tunnel, or stop (action: share/unshare). `share` returns a unique public URL; idempotent per playground. Requires `playground_share.enabled`. See [Playground: Public sharing](playground.md#public-sharing). |

#### Chat Components

| Tool | Description |
|------|-------------|
| `chat_component` | Show a component inline in the desktop chat (action: templates/show). `templates` returns each template with a guide to filling it; `show` takes `template`, `title`, `html` and optional `css`/`js`, and posts the composed document as an agent message in the current channel. Built-in templates: `math`, `canvas`, `react`; more under `chat_components` in config. Rejected on Slack and Discord. See [Chat: Components](chat.md#components). |

#### Shortcuts

| Tool | Description |
|------|-------------|
| `prompt_shortcut` | Manage prompt shortcuts triggered via `#` in chat. Actions: `list`, `add`, `update`, `delete`. Scope: `global` (`~/.loop/config.json`) or `project` (project `.loop/config.json`). |
| `bash_shortcut` | Manage bash shortcuts triggered via `$` in the terminal shortcuts bar. Actions: `list`, `add`, `update`, `delete`. Scope: `global` or `project`. |

#### Shortcut scopes

`list` is cross-scope: it returns global and project shortcuts merged, with each
entry labelled by the `scope` it is defined in (project entries shadow global
ones of the same name).

`add`, `update` and `delete` operate on **one** config file and therefore
require an explicit `scope` — there is no default. A shortcut written to one
scope is invisible to the other, so a `delete` that omitted the scope used to
silently search the global config and report a bare "shortcut not found" for a
shortcut that `list` had just displayed. Run `list` first and pass back the
scope it reports. A not-found error now names the scope and config file that
were searched.

Names are stored verbatim, including spaces — `git pull` is a valid shortcut
name and is matched exactly, never slugified.

### Agent Tools (when `--agent-id` set)

Enabled for Swarm/Canvas terminal agents. Messages are typed into the target agent's terminal.

| Tool | Description |
|------|-------------|
| `list_agents` | List the agents in the current channel, its chats and terminal agents, with status and work summaries |
| `send_agent_message` | Send a message to another agent by ID as `[from <agent id>] <message>`: typed into a terminal agent's terminal, or queued as a chat's next message; fails when a terminal agent has no terminal. Only reaches the agents of its own channel; another channel or thread takes `send_message` |
| `update_agent_status` | Update this agent's display name and work summary (visible to other agents and frontend) |

When `--agent-id` is set, the server also sets instructions telling Claude how to use the agent tools and how messages from other agents arrive.

With `--agent-id chat`, the id chat runs get, the server has `list_agents` and `send_agent_message` but not `update_agent_status`, and it doesn't register: the daemon lists the channel's chats itself, and queues the messages sent to them (see [Multi-Agent Workspaces](multi-agent.md)). Its `list_agents` marks the chat that asked, from the API's `self`.

### Learn Tools (learn agent only)

Registered only when `--agent-id` is `learn`, the id the orchestrator gives a [learn pass](orchestrator.md#learn-pass). A learn pass works alone, so that id gets none of the agent tools above: no `list_agents`, `send_agent_message` or `update_agent_status`, and it isn't registered in the agent registry.

| Tool | Description |
|------|-------------|
| `propose_learnings` | File the learn pass's proposals for the user to apply or dismiss in the Learn view; nothing is applied until they do. Takes `proposals`, at most 5 items of `{kind, title, rationale?, payload}`, where `kind` is `prompt_shortcut`, `bash_shortcut`, `scheduled_task`, `gate_rule`, `mount`, `rename`, `description` or `ticket_url`, plus an optional `replaces`, the id of an earlier proposal the new one supersedes. `withdraw`, at most 20 items of `{id, reason}`, withdraws earlier proposals the run shows are stale; only pending or failed ones of the channel can be. Either list may be empty, not both. Posts to [`POST /api/channels/{id}/learn/proposals`](api.md#post-apichannelsidlearnproposals) for the server's channel, which must be a learn thread; if any proposal is invalid or any withdrawal refused, nothing changes and the error names the one to fix. |

A learn run can't call the Loop tools that change state (shortcuts, tasks, threads, messages, workflows, playgrounds and so on): they're denied by the learn run's `--disallowedTools`, not left unregistered.

### Workflow Tools (always available)

| Tool | Description |
|------|-------------|
| `run_workflow` | Start a workflow run by name. Pass `inputs` for required/optional workflow inputs. Uses `list_workflows` to discover available workflows. |
| `get_workflow_run` | Get the status and node outputs of a workflow run by its run ID |
| `list_workflows` | List all available workflow definitions with names, descriptions, and input schemas |
| `list_workflow_runs` | List recent workflow runs, optionally filtered by `channel_id` |
| `cancel_workflow_run` | Cancel a running workflow by its run ID |
| `resume_workflow_run` | Resume a paused workflow (e.g. after an approval node) with an optional response |
| `save_workflow` | Create or update a workflow definition in global or project config. Pass `action` (`add`/`update`) and the full workflow JSON |
| `delete_workflow` | Delete a workflow definition by name from global or project config |
| `delete_workflow_run` | Delete a workflow run by ID (cancels first if active) |
| `retry_workflow_run` | Retry a completed/failed/cancelled workflow run by ID |

### Memory Tools (when `--memory` enabled)

| Tool | Description |
|------|-------------|
| `search_memory` | Semantic search across memory files. Returns relevant chunks ranked by similarity. Optional `top_k` (default 5). |
| `index_memory` | Force re-index all memory files after edits |

### Quality Tools

| Tool | Description |
|------|-------------|
| `quality_scan` | Trigger an architectural quality scan for the current channel. Returns a status hint immediately; the full report ships via the `quality.scanned` event. |
| `quality_snapshot` | Read the persisted snapshot (current branch first, then most recent). Returns `quality_signal`, geo-mean, per-metric breakdown, and a branch-mismatch flag. |
| `quality_complexity` | Per-function complexity hotspots from the cached graph (cyclomatic, cognitive, max nesting, params, LOC). Optional `limit` (default 50, max 100) and `offset` (default 0) page through the worst-first list. Requires a prior scan. |
| `quality_clones` | Clone clusters from the cached graph (SimHash near-duplicate detection). Optional `limit` (default 25, max 50) and `offset` (default 0) page through clusters by total LOC desc. Requires a prior scan. |

These four tools read `channelID` from the per-channel server struct and take no `WorkDir` argument. See [Quality](quality.md) for the engine and metric semantics, including the `T/raw` complexity curve and the SimHash + Hamming-distance clone clustering.

## Construction

```go
server := mcpserver.New(channelID, apiURL, authorID, httpClient, logger,
    mcpserver.WithMemoryAPI(dirPath),       // optional: enables memory tools
    mcpserver.WithAgentTools("docker-agent-0"), // optional: enables inter-agent tools
    mcpserver.WithWorkflowAPI(),            // enables workflow tools (always added)
)
server.Run(ctx, transport)
server.UnregisterAgent() // graceful cleanup after Run returns
```

### Graceful Shutdown

After `Run()` returns, the caller invokes `UnregisterAgent()` which sends `DELETE /api/agents/{id}?channel_id=X` to the backend. This removes the agent from the registry so other agents no longer see it in `list_agents` results. If no `--agent-id` was set, `UnregisterAgent()` is a no-op.

## Design

- All tool handlers call daemon HTTP endpoints — the MCP server is a thin proxy
- `doAPICall[T]()` — generic HTTP request wrapper with JSON unmarshaling
- `doAPICallNoBody()` — for DELETE/POST with 204 No Content
- Error responses set `IsError: true` in MCP result
- Per-channel MCP config files (`mcp-{channelID}.json`) avoid races when parent/thread share workDir

## Related docs

- [API](api.md) — HTTP API reference (includes agent registry endpoints)
- [Multi-Agent](multi-agent.md) — Agent registry, MCP Channels, Swarm & Canvas layouts
- [Containers](containers.md) — Container MCP config setup
- [Memory](memory.md) — Semantic search and Ollama embeddings
- [Quality](quality.md) — Architectural quality engine
- [Scheduling](scheduling.md) — Task types and templates
- [Workflows](workflows.md) — DAG-based workflow engine
