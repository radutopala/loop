# Multi-Agent Workspaces

Loop supports running multiple Claude Code agents in parallel within the same channel, with real-time agent discovery and push-based inter-agent messaging via MCP Channels.

## Overview

Multiple agents share one Docker container per channel (each runs as a separate `docker exec`). They share the filesystem so file changes are visible across agents immediately. MCP Channels enable agents to coordinate work — "I'm done with the API, you can start the tests."

```
Channel "my-project" → 1 Docker container
  ├── chat:    batch run → claude (Chat pane, AgentID "chat")
  ├── agent-0: docker exec → bash → claude (Swarm/Canvas pane)
  ├── agent-1: docker exec → bash → claude (Swarm/Canvas pane)
  ├── agent-2: docker exec → bash → claude (Swarm/Canvas pane)
  └── agent-3: docker exec → bash → claude (Swarm/Canvas pane)
```

The chat agent registers as AgentID `"chat"` so all 5 agents (1 chat + 4 terminal) can discover each other and communicate via MCP Channels.

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
    AgentID     string    // matches terminal pane ID, e.g. "agent-0"
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

For chat agents (batch runs via Discord/Slack/local messages), the container runner registers with AgentID `"chat"` at the start of `runOnce()` and unregisters on return.

### Auto-Accept Prompts

Agent terminal sessions automatically accept Claude Code's workspace trust and `--dangerously-load-development-channels` confirmation prompts. The terminal handler scans output for the trigger string and sends Enter when matched, up to a maximum of 3 prompts per session. This also works on reattach (scans history buffer).

### REST API

| Method | Path | Description |
|--------|------|-------------|
| `GET /api/agents?channel_id=X` | List agents for a channel |
| `PATCH /api/agents/{id}` | Update agent status/name/work summary |
| `DELETE /api/agents/{id}?channel_id=X` | Unregister agent (MCP server shutdown) |
| `POST /api/agents/{id}/message` | Send message to agent's mailbox |
| `GET /api/ws/agent-channel?agent_id=X&channel_id=Y` | WebSocket for push message delivery |

## Inter-Agent MCP Tools

When a terminal pane has an `agent_id`, the Loop MCP server inside the container enables three agent tools:

| Tool | Description |
|------|-------------|
| `list_agents` | List all active agents in the current channel with status and work summaries |
| `send_agent_message` | Send a push message to another agent by ID |
| `update_agent_status` | Update this agent's display name and work summary |

### Instructions

The MCP server provides these instructions to Claude when agent tools are enabled:

```
You are connected to Loop's inter-agent communication channel.
Messages from other agents arrive as <channel source="loop" from_agent="...">.
Use the `list_agents` tool to discover other running agents in this channel.
Use the `send_agent_message` tool to send a message to another agent by ID.
Use the `update_agent_status` tool to set your name and work summary.
When you receive a channel message, respond helpfully — you are collaborating with the sender.
```

## MCP Channels (Push Notifications)

Inter-agent messaging uses Claude Code's MCP Channels protocol for real-time push delivery.

### Capability Declaration

The MCP server declares `capabilities.experimental["claude/channel"]` during initialization so Claude Code processes channel notifications.

### Message Flow

```
Agent A calls send_agent_message tool
  → HTTP POST /api/agents/agent-1/message
  → Backend pushes to agent-1's mailbox channel
  → Agent B's MCP server reads from WebSocket (startPushReceiver)
  → channelTransport.WriteNotification() writes to stdout:
    {"jsonrpc":"2.0","method":"notifications/claude/channel",
     "params":{"content":"...","meta":{"from_agent":"agent-0"}}}
  → Claude B sees: <channel source="loop" from_agent="agent-0">message</channel>
```

### Push Receiver Resilience

The `startPushReceiver` goroutine automatically reconnects when the WebSocket connection drops (e.g. when the agent is unregistered/re-registered during a frontend tab switch). On dial failure it retries after 2 seconds; on read error it reconnects after 1 second.

### Claude CLI Flag

The `--dangerously-load-development-channels server:loop` flag is added to Claude CLI args when an `agent_id` is set. This enables Claude Code to process `notifications/claude/channel` JSON-RPC notifications from the MCP server.

Both chat agents (batch runs) and terminal agents get this flag when they have an `agent_id`.

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

The `--agent-id` flag on the `mcp` command controls whether agent tools and channel transport are enabled:

```
loop mcp --channel-id ch-1 --api-url http://... --agent-id agent-0
```
