# MCP Server

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

### Always Available (12 tools)

#### Task Management

| Tool | Description |
|------|-------------|
| `schedule_task` | Create a scheduled task (cron, interval, or once). Supports `template_name` for deduplication and `auto_delete_sec` for thread cleanup. |
| `list_tasks` | List all scheduled tasks for the current channel |
| `show_task` | Show full task details by ID including complete prompt |
| `cancel_task` | Cancel a scheduled task by ID |
| `toggle_task` | Enable or disable a task by ID |
| `edit_task` | Edit a task's schedule, type, prompt, or auto_delete_sec |

#### Communication

| Tool | Description |
|------|-------------|
| `send_message` | Send a message to a channel or thread. Supports `@BotName` mentions (auto-converted to proper mentions). |
| `create_channel` | Create a new channel (bot auto-joins) |
| `create_thread` | Create a thread in the current channel. If message provided, triggers an agent immediately. |
| `delete_thread` | Delete a thread by ID |
| `search_channels` | Search channels and threads by optional query. Returns IDs, names, directory paths, parent IDs, and active status. |

#### Documentation

| Tool | Description |
|------|-------------|
| `get_readme` | Get Loop README with setup instructions, configuration, commands, and architecture |

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

### Memory Tools (when `--memory` enabled)

| Tool | Description |
|------|-------------|
| `search_memory` | Semantic search across memory files. Returns relevant chunks ranked by similarity. Optional `top_k` (default 5). |
| `index_memory` | Force re-index all memory files after edits |

## Construction

```go
server := mcpserver.New(channelID, apiURL, authorID, httpClient, logger,
    mcpserver.WithMemoryAPI(dirPath),       // optional: enables memory tools
    mcpserver.WithAgentTools("agent-0"),    // optional: enables inter-agent tools
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
- [Scheduling](scheduling.md) — Task types and templates
