# Real-Time Events System

The events system provides real-time delivery of server events to connected clients via WebSocket at `GET /api/ws`. Clients subscribe to specific channels and receive JSON-encoded events for message creation, agent status changes, tool usage, and channel lifecycle.

**Related docs:** [HTTP API](api.md) | [Terminal WebSocket](terminal.md) | [Memory System](memory.md)

---

## EventsHub Architecture

The `EventsHub` is the central event dispatcher. It maintains a set of WebSocket subscribers and broadcasts events to those whose channel filter matches.

```
Event source (orchestrator, bot, agent)
        │
        ▼
   EventsHub.Broadcast(Event)
        │
        ├──> subscriber 1 (channels: {chan_a, chan_b})  ──> match? ──> write
        ├──> subscriber 2 (channels: {})                ──> all     ──> write
        └──> subscriber 3 (channels: {chan_c})           ──> skip
```

### Thread Safety

The hub uses a `sync.RWMutex` (`mu`) to protect the subscriber set:

- **Registration/unregistration:** Acquires write lock (`Lock`).
- **Broadcast:** Acquires read lock (`RLock`) to snapshot the subscriber list, releases it, then iterates the snapshot to write to each connection.

Each connection has its own `sync.Mutex` (`writeMu`) that serializes writes to that specific WebSocket. This means:

- A concurrent `Unregister` may remove a subscriber that `Broadcast` is about to write to. This is safe because the connection's `writeMu` still guards the write.
- A failed write triggers `Unregister` for that client, which is idempotent (map delete).

---

## WebSocket Connection

### Endpoint

```
GET /api/ws
```

**Query Parameters:**

| Param      | Type   | Description |
|------------|--------|-------------|
| `channels` | string | Comma-separated channel IDs to subscribe to initially |

Example: `GET /api/ws?channels=chan_a,chan_b`

**Errors:** `501` if the events hub is not configured.

The WebSocket upgrader accepts all origins (`CheckOrigin` returns `true`).

---

## Subscription Model

### Initial Subscription

Clients can subscribe to channels in two ways:

1. **Query parameter:** Pass `?channels=id1,id2` on the WebSocket URL.
2. **Subscribe message:** Send a JSON message after connecting.

### Subscribe Message (Client to Server)

```json
{
  "type": "subscribe",
  "channels": ["chan_a", "chan_b", "chan_c"]
}
```

| Field      | Type     | Description |
|------------|----------|-------------|
| `type`     | string   | Must be `"subscribe"` |
| `channels` | string[] | Channel IDs to subscribe to; replaces the previous subscription |

**Behavior:**
- Sending a `subscribe` message **replaces** the current channel filter entirely (it does not merge).
- An empty `channels` array subscribes to **all** events (no filtering).
- The subscription update acquires the connection's `writeMu` to avoid races with concurrent broadcasts.

### Channel Filter Semantics

| `channels` state | Behavior |
|-------------------|----------|
| Empty map (or nil) | Receives events for **all** channels |
| Non-empty map | Receives events only for channels in the map |

---

## Event Types

All events share a common envelope:

```json
{
  "type": "message.created",
  "channel_id": "chan_a",
  "data": { ... },
  "timestamp": 1709337600000
}
```

| Field       | Type   | Description |
|-------------|--------|-------------|
| `type`      | string | Event type identifier |
| `channel_id`| string | Channel the event belongs to |
| `data`      | object | Type-specific payload |
| `timestamp` | int64  | Unix milliseconds when the event was broadcast |

---

### `message.created`

A new message was posted to a channel (by a user or the bot).

**Payload schema:**

```json
{
  "msg_id": "discord_msg_id_or_internal_id",
  "author_id": "user123",
  "author_name": "Alice",
  "content": "Hello, world!",
  "is_bot": false
}
```

| Field        | Type   | Description |
|--------------|--------|-------------|
| `msg_id`     | string | Platform-specific message ID |
| `author_id`  | string | Author's platform user ID |
| `author_name`| string | Author's display name |
| `content`    | string | Full message content |
| `is_bot`     | bool   | Whether the message was sent by the bot |

---

### `message.streaming`

Partial bot response during streaming. The client should update the in-progress message display.

**Payload schema:**

```json
{
  "content": "Here is the partial response so far..."
}
```

| Field    | Type   | Description |
|----------|--------|-------------|
| `content`| string | Accumulated response text so far |

---

### `message.deleted`

A queued user message was removed from the channel's message queue via `DELETE /api/messages/{id}`. Emitted only when the delete actually removed a row (the query is scoped to `is_bot = 0 AND is_processed = 0`, so bot replies and already-processed history can never trigger it).

**Payload schema:**

```json
{
  "msg_id": "discord_msg_id_or_internal_id"
}
```

| Field    | Type   | Description |
|----------|--------|-------------|
| `msg_id` | string | Platform-specific message ID of the deleted message |

---

### `agent.status`

Agent lifecycle status change (running, completed, errored).

**Payload schema:**

```json
{
  "status": "completed",
  "run_id": "a1b2c3d4e5f67890",
  "duration_ms": 12345,
  "num_turns": 3,
  "stop_reason": "end_turn",
  "model": "claude-sonnet-4-20250514",
  "trigger_content": "hi",
  "thread_id": "thread_abc123"
}
```

| Field             | Type   | Description |
|-------------------|--------|-------------|
| `status`          | string | `"running"`, `"completed"`, or `"error"` |
| `run_id`          | string | Unique identifier for this agent run. The frontend uses this to distinguish concurrent runs on the same channel (e.g. a scheduled task completing should not clear the running indicator for a parallel chat agent). |
| `error`           | string | Error message (only when status is `"error"`) |
| `duration_ms`     | int    | Total run duration in milliseconds (on completion) |
| `num_turns`       | int    | Number of conversation turns (on completion) |
| `stop_reason`     | string | Why the agent stopped (e.g., `"end_turn"`, `"max_turns"`) |
| `model`           | string | Model used for the run |
| `trigger_content` | string | Content of the message that triggered the run (on `"running"` status) |
| `thread_id`       | string | Thread ID for scheduled task runs. Present on all status events (`running`, `error`, `completed`) when the task has an existing thread. The frontend uses this to route state (store entry, `isRunningMap`) to the thread instead of the parent channel, so the parent doesn't show a running indicator for thread work and the thread view shows the stop button and streaming content. |

---

### `tool.use`

The agent is invoking a tool.

**Payload schema:**

```json
{
  "tool_name": "Bash",
  "input": "ls -la"
}
```

| Field       | Type   | Description |
|-------------|--------|-------------|
| `tool_name` | string | Name of the tool being called |
| `input`     | string | Tool input (may be truncated for display) |

---

### `agent.activity`

Agent activity indicator for UI status displays. Covers model detection, subagent progress, and other activities.

**Payload schema:**

```json
{
  "activity": "model",
  "model": "claude-sonnet-4-20250514",
  "description": ""
}
```

| Field         | Type   | Description |
|---------------|--------|-------------|
| `activity`    | string | Activity type: `"model"`, `"subagent_started"`, `"subagent_progress"` |
| `model`       | string | Model name (when activity is `"model"`) |
| `description` | string | Human-readable description of the activity |

---

### `agent.ask_user`

Claude used the `AskUserQuestion` tool to ask structured questions. The desktop app renders these as interactive cards with clickable option buttons.

**Payload schema:**

```json
{
  "questions": [
    {
      "question": "Where should the file be created?",
      "header": "Location",
      "options": [
        { "label": "Root directory", "description": "Create in the project root" },
        { "label": "src/ folder", "description": "Create inside the src directory" }
      ],
      "multi_select": false
    }
  ]
}
```

| Field              | Type     | Description |
|--------------------|----------|-------------|
| `questions`        | array    | List of questions to present |
| `questions[].question` | string | The question text |
| `questions[].header`   | string | Short header/label for the question |
| `questions[].options`  | array  | Selectable options (label + description) |
| `questions[].multi_select` | bool | Whether multiple options can be selected |

The user's answers are sent as a regular message in the next turn (via `--resume`). An implicit "Other" free-text option is always available.

---

### `agent.exit_plan`

Claude used the `ExitPlanMode` tool to signal that a plan is ready for review. The desktop app renders this as a plan preview card with "Accept & Execute" and "Request Changes" buttons.

**Payload schema:**

```json
{
  "plan": "# My Plan\n\n## Steps\n1. Do this\n2. Do that",
  "planFilePath": "/path/to/plan.md"
}
```

| Field          | Type   | Description |
|----------------|--------|-------------|
| `plan`         | string | The full plan content (markdown) |
| `planFilePath` | string | Path where the plan file was written |

Clicking "Accept & Execute" switches mode from plan to agent and sends an approval message. Clicking "Request Changes" keeps plan mode and sends a revision request.

---

### `channel.created`

A new channel or thread was created. Sent to the **parent** channel so subscribers can update their channel/thread list.

**Payload schema:**

```json
{
  "channel_id": "new_thread_id"
}
```

| Field        | Type   | Description |
|--------------|--------|-------------|
| `channel_id` | string | ID of the newly created channel or thread |

---

### `playground.update`

A named playground was updated (by agent via MCP tool or API). This event is broadcast globally (no channel scoping).

**Payload schema:**
```json
{
  "name": "snake-game",
  "html": "<div id='app'>...</div>",
  "css": "body { margin: 0; }",
  "js": "console.log('hello')",
  "import_map": "{\"imports\":{}}",
  "description": "Added score counter"
}
```

The frontend `PlaygroundPanel` listens for this event. If the event's `name` matches the active playground, the iframe hot-reloads. If it's a new name, the panel auto-switches to it.

---

### `channel.deleted`

A channel was deleted.

**Payload schema:** `null` (no data field, or omitted).

The `channel_id` in the event envelope identifies which channel was deleted.

---

### `container.registered`

A new container was added to the registry (agent started, shell created, Chrome launched).

**Payload schema:**

```json
{
  "container_id": "abc123def456",
  "channel_id": "chan_a",
  "type": "agent",
  "status": "running",
  "container_name": "loop-my-project-a1b2c3"
}
```

| Field            | Type   | Description |
|------------------|--------|-------------|
| `container_id`   | string | Docker container ID |
| `channel_id`     | string | Channel the container belongs to |
| `type`           | string | Container type: `"agent"`, `"shell"`, or `"chrome"` |
| `status`         | string | Lifecycle status (always `"running"` on registration) |
| `container_name` | string | Docker container name |

**Scope:** Global (no channel filtering — broadcast to all subscribers).

---

### `container.status_changed`

A container's lifecycle status changed (e.g. running → stopped, stopped → pending-removal).

**Payload schema:**

```json
{
  "container_id": "abc123def456",
  "channel_id": "chan_a",
  "type": "agent",
  "status": "pending-removal",
  "container_name": "loop-my-project-a1b2c3",
  "remove_at": "2026-03-31T12:05:00Z"
}
```

| Field            | Type    | Description |
|------------------|---------|-------------|
| `container_id`   | string  | Docker container ID |
| `channel_id`     | string  | Channel the container belongs to |
| `type`           | string  | Container type |
| `status`         | string  | New status: `"running"`, `"stopped"`, or `"pending-removal"` |
| `container_name` | string  | Docker container name |
| `remove_at`      | string? | ISO 8601 timestamp when removal is scheduled (only for `pending-removal`) |

**Scope:** Global.

---

### `container.removed`

A container was unregistered from the registry (removed from Docker or reconciled away).

**Payload schema:**

```json
{
  "container_id": "abc123def456",
  "channel_id": "chan_a",
  "type": "agent",
  "status": "pending-removal",
  "container_name": "loop-my-project-a1b2c3"
}
```

| Field            | Type   | Description |
|------------------|--------|-------------|
| `container_id`   | string | Docker container ID |
| `channel_id`     | string | Channel the container belonged to |
| `type`           | string | Container type |
| `status`         | string | Status at time of removal |
| `container_name` | string | Docker container name |

**Scope:** Global.

---

### `ticket.created` / `ticket.updated` / `ticket.deleted`

Ticket lifecycle events from the [Tickets API](api.md#tickets). The Kanban panel subscribes to these to refresh the board.

**Payload schema:**

```json
{
  "ticket_id": "tic-a1b2c3d4"
}
```

| Field       | Type   | Description |
|-------------|--------|-------------|
| `ticket_id` | string | The ticket ID that was affected |

**Scope:** Global (no channel filtering).

---

### `workflow.run_started` / `workflow.run_completed` / `workflow.run_paused`

Workflow run lifecycle events. Broadcast when a workflow begins execution, when it reaches a terminal state (completed, failed, or cancelled), or when an approval node pauses the run for human input.

**Payload schema:**

```json
{
  "run_id": "wfr-a1b2c3d4e5f67890",
  "workflow_name": "code-review",
  "channel_id": "chan_a",
  "status": "paused",
  "paused_node_id": "approve",
  "error": ""
}
```

| Field            | Type   | Description |
|------------------|--------|-------------|
| `run_id`         | string | Workflow run ID |
| `workflow_name`  | string | Name of the workflow definition |
| `channel_id`     | string | Channel context (may be empty) |
| `status`         | string | `"running"`, `"paused"`, `"completed"`, `"failed"`, or `"cancelled"` |
| `paused_node_id` | string | Node ID that caused the pause (only on `"paused"` status) |
| `error`          | string | Error message (only on `"failed"` status) |

**Scope:** Global.

---

### `workflow.node_started` / `workflow.node_completed`

Individual node lifecycle events within a workflow run.

**Payload schema:**

```json
{
  "run_id": "wfr-a1b2c3d4e5f67890",
  "node_id": "diff",
  "status": "success",
  "output": "+added line"
}
```

| Field     | Type   | Description |
|-----------|--------|-------------|
| `run_id`  | string | Parent workflow run ID |
| `node_id` | string | Node identifier within the workflow |
| `status`  | string | `"running"`, `"success"`, `"failed"`, or `"skipped"` |
| `output`  | string | Node output text (truncated to 1000 chars, only on completion) |

**Scope:** Global.

---

## Broadcast Flow

1. **Event source** calls a typed broadcast method (e.g., `BroadcastMessageCreated`).
2. The method constructs an `Event` struct with the appropriate type and data, then calls `Broadcast`.
3. `Broadcast` sets the `timestamp` to `time.Now().UnixMilli()` and marshals the event to JSON.
4. Under `RLock`, a snapshot of all current subscribers is taken.
5. The lock is released.
6. For each subscriber in the snapshot:
   a. Acquire the subscriber's `writeMu`.
   b. Check if the subscriber's channel filter matches the event's `channel_id`.
   c. If matched (or filter is empty), write the JSON as a WebSocket text message.
   d. Release `writeMu`.
   e. If the write fails, log the error and unregister the subscriber.

### Broadcast Methods

| Method | Event Type | Data Type | Scope |
|--------|-----------|-----------|-------|
| `BroadcastMessageCreated` | `message.created` | `MessageEventData` | Channel |
| `BroadcastMessageStreaming` | `message.streaming` | `MessageStreamingData` | Channel |
| `BroadcastMessagesProcessed` | `messages.processed` | `MessagesProcessedData` | Channel |
| `BroadcastMessageDeleted` | `message.deleted` | `MessageDeletedData` | Channel |
| `BroadcastAgentStatus` | `agent.status` | `AgentStatusEventData` | Channel (global when `ThreadID` is set) |
| `BroadcastToolUse` | `tool.use` | `ToolUseEventData` | Channel |
| `BroadcastAgentActivity` | `agent.activity` | `AgentActivityEventData` | Channel |
| `BroadcastAskUser` | `agent.ask_user` | `AskUserQuestionEventData` | Channel |
| `BroadcastExitPlan` | `agent.exit_plan` | `ExitPlanModeEventData` | Channel |
| `BroadcastTodoWrite` | `agent.todos` | `TodoWriteEventData` | Channel |
| `BroadcastChannelCreated` | `channel.created` | `map[string]string{"channel_id": id}` | Channel |
| `BroadcastChannelDeleted` | `channel.deleted` | `nil` | Channel |
| `BroadcastAgentInstanceRegistered` | `agent_instance.registered` | `AgentInstanceEventData` | Channel |
| `BroadcastAgentInstanceUnregistered` | `agent_instance.unregistered` | `AgentInstanceEventData` | Channel |
| `BroadcastAgentInstanceMetadata` | `agent_instance.metadata` | `AgentInstanceEventData` | Channel |
| `BroadcastContainerRegistered` | `container.registered` | `ContainerEventData` | Global |
| `BroadcastContainerStatusChanged` | `container.status_changed` | `ContainerEventData` | Global |
| `BroadcastContainerRemoved` | `container.removed` | `ContainerEventData` | Global |
| `BroadcastImageBuildStatus` | `image.build_status` | `ImageBuildStatusData` | Global |
| `BroadcastImageUpdateAvailable` | `image.update_available` | `ImageUpdateAvailableData` | Global |
| `BroadcastTaskCreated` | `task.created` | `TaskEventData` | Global |
| `BroadcastTaskUpdated` | `task.updated` | `TaskEventData` | Global |
| `BroadcastTaskDeleted` | `task.deleted` | `TaskEventData` | Global |
| `BroadcastTaskRunCompleted` | `task.run_completed` | `TaskRunEventData` | Global |
| `BroadcastTicketEvent` | `ticket.created` / `ticket.updated` / `ticket.deleted` | `map[string]any` | Global |
| `BroadcastWorkflowRunStarted` | `workflow.run_started` | `WorkflowRunEventData` | Global |
| `BroadcastWorkflowRunCompleted` | `workflow.run_completed` | `WorkflowRunEventData` | Global |
| `BroadcastWorkflowRunPaused` | `workflow.run_paused` | `WorkflowRunEventData` | Global |
| `BroadcastWorkflowNodeStarted` | `workflow.node_started` | `WorkflowNodeEventData` | Global |
| `BroadcastWorkflowNodeCompleted` | `workflow.node_completed` | `WorkflowNodeEventData` | Global |

---

## Client Reconnection

The events system does not maintain client state between connections. When a client reconnects:

- It must re-subscribe to channels (via query parameter or subscribe message).
- There is no event replay or missed-event recovery.
- The client should refresh its local state (e.g., re-fetch messages) after reconnecting.

---

## Broadcaster Interface

External components (orchestrator, agent, bot) interact with the events system through the `events.Broadcaster` interface:

```go
type Broadcaster interface {
    BroadcastMessageCreated(channelID string, data MessageEventData)
    BroadcastMessageStreaming(channelID string, data MessageStreamingData)
    BroadcastAgentStatus(channelID string, data AgentStatusEventData)
    BroadcastToolUse(channelID string, data ToolUseEventData)
    BroadcastAgentActivity(channelID string, data AgentActivityEventData)
    BroadcastAskUser(channelID string, data AskUserQuestionEventData)
    BroadcastExitPlan(channelID string, data ExitPlanModeEventData)
    BroadcastTodoWrite(channelID string, data TodoWriteEventData)
    BroadcastMessagesProcessed(channelID string, data MessagesProcessedData)
    BroadcastChannelCreated(parentChannelID, channelID string)
    BroadcastChannelDeleted(channelID string)
    BroadcastAgentInstanceRegistered(channelID string, data AgentInstanceEventData)
    BroadcastAgentInstanceUnregistered(channelID string, data AgentInstanceEventData)
    BroadcastAgentInstanceMetadata(channelID string, data AgentInstanceEventData)
    BroadcastImageBuildStatus(data ImageBuildStatusData)
    BroadcastImageUpdateAvailable(data ImageUpdateAvailableData)
    BroadcastWorkflowRunStarted(data WorkflowRunEventData)
    BroadcastWorkflowRunCompleted(data WorkflowRunEventData)
    BroadcastWorkflowRunPaused(data WorkflowRunEventData)
    BroadcastWorkflowNodeStarted(data WorkflowNodeEventData)
    BroadcastWorkflowNodeCompleted(data WorkflowNodeEventData)
}
```

The `EventsHub` also implements `ContainerBroadcaster` for container lifecycle events:

```go
type ContainerBroadcaster interface {
    BroadcastContainerRegistered(data ContainerEventData)
    BroadcastContainerRemoved(data ContainerEventData)
    BroadcastContainerStatusChanged(data ContainerEventData)
}
```

Container events are broadcast globally (empty channel ID) so all connected clients receive them regardless of their channel subscription.
