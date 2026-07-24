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
| `list_tasks` | List all scheduled tasks for the current channel |
| `show_task` | Show full task details by ID including complete prompt |
| `cancel_task` | Cancel a scheduled task by ID |
| `toggle_task` | Enable or disable a task by ID |
| `edit_task` | Edit a task's schedule, type, prompt, workflow, bash script, or auto_delete_sec |

#### Communication

| Tool | Description |
|------|-------------|
| `send_message` | Send a message to a channel or thread. `channel_id` is optional — omit it to target the current channel/thread. Supports `@BotName` mentions (auto-converted to proper mentions). |
| `queue_message` | Queue a follow-up prompt for yourself in the current channel/thread/worktree. Enqueues a new turn behind any running/queued work (shows in the chat's queued-messages list); `interrupt=true` cancels the active run and jumps the queue to run next. `delay_seconds` (optional, non-negative) holds the prompt back until the delay elapses — it stays queued with a live countdown in the UI and a background poller drains it once due; a delay forces `interrupt` off. |
| `create_channel` | Create a new channel (bot auto-joins) |
| `create_thread` | Create a thread in the current channel. If message provided, triggers an agent immediately. |
| `create_worktree_thread` | Create a thread backed by a fresh git worktree (mirrors the UI `+wt` button). `branch` is the existing **base** to fork from (e.g. `main`); a new `worktree/<name>` branch is created and checked out off it. Optional `name` for the worktree directory; optional `message` triggers an agent immediately. |
| `rename_thread` | Rename a thread or channel's display name. Only updates the name — the directory and Claude sessions are preserved. |
| `rename_worktree_thread` | Rename a worktree thread to `new_name`: renames the worktree directory and its `worktree/<name>` branch, relocates the Claude session store, and updates the display name. Sessions are preserved; rejected with `409` if a run is active. |
| `fork_thread` | Fork a thread by ID (mirrors the sidebar `+fork` action): creates a sibling that continues the source's conversation on a forked Claude session, leaving the source untouched. Worktree threads also get a fresh worktree branched from the source's committed state. No agent is triggered — follow up with `send_message` to task the fork. |
| `delete_thread` | Delete a thread by ID |
| `search_channels` | Search channels and threads by optional query. Returns IDs, names, directory paths, parent IDs, and active status. |

#### Documentation

| Tool | Description |
|------|-------------|
| `get_readme` | Get Loop README with setup instructions, configuration, commands, and architecture |

#### Playground

| Tool | Description |
|------|-------------|
| `playground` | Manage playgrounds (action: create/update/delete). Create sets up the entry HTML, title, and description. Use `playground_file` to add JS, CSS, and other files. |
| `playground_file` | Manage files within a playground (action: create/update/read/delete/list). Write script.js, style.css, importmap.json, lib/utils.js, etc. Files served at relative URLs for ES module imports. |
| `playground_share` | Expose a playground publicly over a cloudflared quick tunnel, or stop (action: share/unshare). `share` returns a unique public URL; idempotent per playground. Requires `playground_share.enabled`. See [Playground: Public sharing](playground.md#public-sharing). |

#### Shortcuts

| Tool | Description |
|------|-------------|
| `prompt_shortcut` | Manage prompt shortcuts triggered via `#` in chat. Actions: `list`, `add`, `update`, `delete`. Scope: `global` (default, `~/.loop/config.json`) or `project` (project `.loop/config.json`). |
| `bash_shortcut` | Manage bash shortcuts triggered via `$` in the terminal shortcuts bar. Actions: `list`, `add`, `update`, `delete`. Scope: `global` or `project`. |

### Agent Tools (when `--agent-id` set)

Enabled for Swarm/Canvas terminal agents. Uses MCP Channels for push delivery.

| Tool | Description |
|------|-------------|
| `list_agents` | List active agents in the current channel with status and work summaries |
| `send_agent_message` | Send a push message to another agent by ID (delivered via `notifications/claude/channel`) |
| `update_agent_status` | Update this agent's display name and work summary (visible to other agents and frontend) |

When `--agent-id` is set, the server also:
- Declares `capabilities.experimental["claude/channel"]` in the MCP initialize response
- Sets instructions telling Claude how to use agent tools and respond to channel messages
- Starts a push receiver goroutine (WebSocket to `/api/ws/agent-channel`) that forwards messages as `notifications/claude/channel` JSON-RPC notifications to stdout

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
