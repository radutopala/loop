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

### `agent.status`

Agent lifecycle status change (started, completed, errored).

**Payload schema:**

```json
{
  "status": "completed",
  "error": "",
  "duration_ms": 12345,
  "num_turns": 3,
  "stop_reason": "end_turn",
  "model": "claude-sonnet-4-20250514"
}
```

| Field         | Type   | Description |
|---------------|--------|-------------|
| `status`      | string | Status value (e.g., `"started"`, `"completed"`, `"error"`) |
| `error`       | string | Error message (only when status is error) |
| `duration_ms` | int    | Total run duration in milliseconds |
| `num_turns`   | int    | Number of conversation turns |
| `stop_reason` | string | Why the agent stopped (e.g., `"end_turn"`, `"max_turns"`) |
| `model`       | string | Model used for the run |

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

| Method | Event Type | Data Type |
|--------|-----------|-----------|
| `BroadcastMessageCreated` | `message.created` | `MessageEventData` |
| `BroadcastMessageStreaming` | `message.streaming` | `MessageStreamingData` |
| `BroadcastAgentStatus` | `agent.status` | `AgentStatusEventData` |
| `BroadcastToolUse` | `tool.use` | `ToolUseEventData` |
| `BroadcastAgentActivity` | `agent.activity` | `AgentActivityEventData` |
| `BroadcastAskUser` | `agent.ask_user` | `AskUserQuestionEventData` |
| `BroadcastExitPlan` | `agent.exit_plan` | `ExitPlanModeEventData` |
| `BroadcastChannelCreated` | `channel.created` | `map[string]string{"channel_id": id}` |
| `BroadcastChannelDeleted` | `channel.deleted` | `nil` |

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
    BroadcastChannelCreated(parentChannelID, channelID string)
    BroadcastChannelDeleted(channelID string)
}
```

The `EventsHub` implements this interface, allowing it to be injected into any component that needs to emit events.
