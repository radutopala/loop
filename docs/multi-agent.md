---
title: Multi-Agent Workspaces
---
Loop supports running multiple Claude Code agents in parallel within the same channel, with real-time agent discovery and inter-agent messaging typed into each agent's terminal.

## Overview

Multiple agents share one Docker container per channel (each runs as a separate `docker exec`). They share the filesystem so file changes are visible across agents immediately. Inter-agent messages let agents coordinate work — "I'm done with the API, you can start the tests."

```
Channel "my-project" → 1 Docker container
  ├── chat:             batch run → claude (Chat pane, AgentID "chat")
  ├── docker-agent-0:   docker exec → bash → claude (Swarm/Canvas pane)
  ├── docker-agent-1:   docker exec → bash → claude (Swarm/Canvas pane)
  ├── docker-agent-2:   docker exec → bash → claude (Swarm/Canvas pane)
  └── docker-agent-3:   docker exec → bash → claude (Swarm/Canvas pane)
```

The terminal agents register, so they can discover and message each other. The chat agent (AgentID `"chat"`) only sends: it can list the terminal agents and message them, but it has no terminal to type messages into, so it isn't registered and the others neither list nor message it.

## Layouts

### Swarm (Static Split)

Chat sidebar (30%) + 2×2 agent grid (70%). Uses the existing split-pane layout system.

### Canvas (Free-form)

Draggable, resizable tiles on a free-form surface. Double-click empty area to add a new tile. Click a tile to bring it to front. Default preset: chat + 4 agent tiles.

Both layouts are available as tabs in the workspace header alongside Chat, Editor, Memory, Terminal, Diff, and Browser Chat.

## Agent Registry

The backend tracks active agents per channel in an in-memory registry (`internal/agentregistry/`).

### AgentInfo

```go
type AgentInfo struct {
    AgentID     string    // matches terminal pane ID, e.g. "docker-agent-0"
    ChannelID   string
    SessionID   string    // terminal session ID
    Name        string    // user-assigned or auto-generated
    Status      string    // "idle", "running", "completed", "error"
    WorkSummary string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

### Lifecycle

1. Frontend creates a terminal pane with `agent_id` in the WebSocket create message
2. Terminal handler registers the agent in the registry
3. `agent_instance.registered` event broadcast to frontend
4. Agent appears in `useAgentRegistry` hook with status dot in pane header
5. On shutdown, the MCP server calls `UnregisterAgent()` which sends `DELETE /api/agents/{id}` to the backend
6. On WebSocket close, agent is also unregistered as a fallback, and `agent_instance.unregistered` fires

Chat runs (batch runs via Discord/Slack/local messages, AgentID `"chat"`) don't register.

### Auto-Accept Prompts

Agent terminal sessions automatically accept Claude Code's workspace trust prompt. The terminal handler scans output for the trigger string and sends Enter when matched, up to a maximum of 3 prompts per session. This also works on reattach (scans history buffer).

### REST API

| Method | Path | Description |
|--------|------|-------------|
| `GET /api/agents?channel_id=X` | List agents for a channel |
| `PATCH /api/agents/{id}` | Update agent status/name/work summary |
| `DELETE /api/agents/{id}?channel_id=X` | Unregister agent (MCP server shutdown) |
| `POST /api/agents/{id}/message` | Type a message into the agent's terminal |

## Inter-Agent MCP Tools

When a terminal pane has an `agent_id`, the Loop MCP server inside the container enables three agent tools:

| Tool | Description |
|------|-------------|
| `list_agents` | List all active agents in the current channel with status and work summaries |
| `send_agent_message` | Send a message to another agent by ID, typed into its terminal |
| `update_agent_status` | Update this agent's display name and work summary |

### Instructions

The MCP server provides these instructions to Claude when agent tools are enabled:

```
You are agent "<id>" connected to Loop's inter-agent communication channel.
Your agent ID is marked with * in list_agents output.
Messages from other agents arrive typed into your prompt as "[from <agent id>] <message>".
...
```

## Message Delivery

```
Agent A calls send_agent_message tool
  → HTTP POST /api/agents/docker-agent-1/message
  → Backend looks up docker-agent-1's terminal session
  → Types "[from docker-agent-0] <message>" into it as one bracketed paste, then Enter
  → Claude B gets it as a prompt; while B is busy, Claude Code queues it
```

The message goes in as one paste, so a multi-line message stays one prompt, and ends with a newline so prompts queued while the agent is busy stay on lines of their own. Content holding a bracketed-paste marker is refused (`400`).

The call fails, and the tool reports it, when the target has no terminal (`409`) or its terminal session is gone (`409`; the stale session is forgotten).

## Frontend Events

| Event | Payload | Trigger |
|-------|---------|---------|
| `agent_instance.registered` | `{agent_id, channel_id, name}` | Terminal session with agent_id created |
| `agent_instance.unregistered` | `{agent_id, channel_id}` | Terminal session closed |
| `agent_instance.metadata` | `{agent_id, channel_id, name, status, work_summary}` | `PATCH /api/agents/{id}` called |

The `useAgentRegistry` hook subscribes to these events and maintains a `Map<string, AgentInfo>` for real-time UI updates. Pane headers show:
- Agent display name (instead of generic "Agent")
- Colored status dot: green = running, red = error, gray = idle
- Work summary tooltip on hover

## Configuration

No special configuration needed. The agent registry is initialized automatically during `serve` startup. Agent tools are enabled per-terminal-session when `agent_id` is provided.

The `--agent-id` flag on the `mcp` command controls whether agent tools are enabled:

```
loop mcp --channel-id ch-1 --api-url http://... --agent-id agent-0
```
