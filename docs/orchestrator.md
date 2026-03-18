# Orchestrator & Message Processing

The orchestrator is the central coordinator of the Loop bot. It connects the chat platform bots, the agent runner (Docker containers), the scheduler, and the database. All message processing, command handling, permission checks, and agent execution flow through it.

## Architecture

The `Orchestrator` struct holds references to:

- `store` (`db.Store`) -- SQLite database for channels, messages, tasks, and permissions
- `bot` (`orchestrator.Bot`) -- The `BotRouter` that dispatches to platform-specific bots
- `runner` (`orchestrator.Runner`) -- Docker container runner for agent execution
- `scheduler` (`scheduler.Scheduler`) -- Cron/interval/once task scheduler
- `events` (`events.Broadcaster`) -- SSE/WebSocket event broadcaster for the Electron app
- `queue` (`*ChannelQueue`) -- Per-channel concurrency control
- `activeRuns` (`sync.Map`) -- Maps channel IDs to cancel functions for stop-button support
- `cfg` (`config.Config`) -- Application configuration

## Startup Flow

When `Start` is called:

1. Register message, interaction, channel delete, and channel join handlers on the bot.
2. Register slash commands on all platforms (`bot.RegisterCommands`).
3. Start the bot (opens connections to Discord/Slack/etc.).
4. Start the scheduler (loads tasks from DB, begins cron loop).

## Message Flow

The complete lifecycle of an incoming message follows this path:

```
Platform Event
    |
    v
HandleMessage (channel active check, auto-create, thread resolution)
    |
    v
Store message in DB + broadcast event
    |
    v
Trigger check (mention, reply, prefix, DM)
    |
    v
Permission check (config + DB merge)
    |
    v
processTriggeredMessage (queue, stop button, typing, agent run, deliver)
```

### Step 1: Channel Resolution

`HandleMessage` first checks if the channel is active in the database:

- **Active channel** -- Proceed to message storage.
- **Inactive channel** -- Attempt thread resolution via `resolveThread`. If the channel is a thread with an active parent, upsert the thread as a channel inheriting the parent's properties (`DirPath`, `SessionID`, `GuildID`, `Permissions`, `Platform`). If not a thread and the message has a trigger (mention, prefix, reply, DM), auto-create the channel. Otherwise, silently ignore.

### Step 2: Message Storage

Every message from a triggered channel is stored in the database via `store.InsertMessage`. The message ID is the platform's native ID (Discord snowflake, Slack timestamp) when available, or a generated `ask-{hex}` ID when the platform does not provide one.

If an event broadcaster is configured, a `message.created` event is broadcast with the message data so the Electron app can update its UI in real time.

### Step 3: Trigger Check

A message is "triggered" if any of these conditions are true:

- `IsBotMention` -- The message mentions the bot (platform-specific detection)
- `IsReplyToBot` -- The message is a reply to a bot message or is in a bot-owned thread
- `HasPrefix` -- The message starts with `!loop`
- `IsDM` -- The message is a direct message

If none are true, the message is stored but no agent run is initiated. This allows the bot to passively record conversation context in active channels.

### Step 4: Permission Check

Before processing a triggered message, the orchestrator checks whether the author has permission:

- **Bot self-mentions** are always allowed (e.g., from `create_thread` MCP tool posts).
- **Local platform** messages always bypass permission checks -- the user is running on their own machine.
- For other cases, `resolveRole` merges config-file permissions and database permissions to determine the author's role. If the resolved role is empty (no role), the message is silently ignored with a log entry.

See [Permission & RBAC System](permissions.md) for the full merge logic.

### Step 5: processTriggeredMessage

This is the core execution path:

```go
func (o *Orchestrator) processTriggeredMessage(ctx, msg) {
    1. queue.Acquire(channelID)           // Block until channel slot is free
    2. bot.SendStopButton(channelID)      // Send interactive stop button
    3. defer queue.Release(channelID)     // Release slot on exit
    4. defer activeRuns.Delete(channelID) // Clean up cancel func
    5. defer bot.RemoveStopButton(...)    // Remove stop button
    6. Start typing indicator goroutine   // Refreshes every 8 seconds
    7. prepareAgentRequest(msg)           // Fetch recent messages, build request
    8. executeAgentRun(msg, req)          // Run agent in container
    9. deliverResponse(msg, resp)         // Send response, store, mark processed
}
```

## Channel Queue

The `ChannelQueue` ensures that only one agent container runs per channel at a time. It uses a `map[string]chan struct{}` where each channel has a buffered channel of size 1.

- `Acquire(channelID)` sends to the channel (blocks if another run is in progress).
- `Release(channelID)` receives from the channel (unblocks the next waiting run).

This prevents race conditions where multiple messages in rapid succession could spawn concurrent agent runs for the same channel. Messages queue up and are processed sequentially.

## Agent Request Preparation

`prepareAgentRequest` builds the `agent.AgentRequest`:

1. **Recent messages** -- Fetch the last 50 messages from the database (`recentMessageLimit = 50`). These are reversed (oldest first) and formatted as `role: authorName: content` pairs, where `role` is "user" for human messages and "assistant" for bot messages.

2. **Channel data** -- Load the channel record for `SessionID` and `DirPath`.

3. **Prompt** -- Format as `authorName: content`.

4. **Session fork** -- If the channel is a thread (`ParentID != ""`) and the thread's `SessionID` matches the parent's `SessionID` (meaning this is the first message in the thread), set `ForkSession: true`. This creates an independent session for the thread while inheriting the parent's conversation context.

5. **Worktree parent** -- If the channel is a worktree thread (`Worktree: true`), look up the parent channel's `DirPath` and set `ParentDirPath` on the request. The runner uses this to mount the parent project directory so the container sees the main `.git` directory.

6. **Plan mode** -- If the incoming message has `Mode: "plan"`, set `PlanMode: true` on the request, which passes `--permission-mode plan` to Claude CLI instead of `--dangerously-skip-permissions`.

## Agent Execution

`executeAgentRun` manages the container lifecycle:

1. **Timeout** -- Create a context with `ContainerTimeout` (default 3600s / 1 hour).
2. **Cancel registration** -- Store the cancel function in `activeRuns` so stop button clicks can cancel the run.
3. **Streaming setup** (if `StreamingEnabled`):
   - Create a `streamTracker` that filters empty turns and tracks the last sent text for deduplication.
   - Set `OnTurn` callback to send intermediate responses as they arrive.
   - Set `OnToolUse` callback to broadcast tool usage events (tool name + summarized input).
   - Set `OnActivity` callback to broadcast model detection and subagent progress events.
4. **Status broadcast** -- Broadcast `agent.status: running` event.
5. **Run** -- Execute `runner.Run(ctx, req)`.
6. **Error handling**:
   - Context cancelled (stop button) -- Send "Run stopped." message.
   - Agent error -- Send error message to the channel.
   - Both cases broadcast `agent.status: error` event.

## Session Management

Claude Code sessions enable conversation continuity across multiple messages. The session ID is stored per-channel in the database.

### Resume

When a channel has a `SessionID`, the agent request includes it. The Docker runner passes `--resume <sessionID>` to Claude CLI, which continues the existing conversation.

### Fork

Thread sessions are forked from the parent's session on the first message. The `--resume <sessionID> --fork-session` flags create a new session that inherits the parent's context. After forking, the thread gets its own `SessionID` stored in the database.

### Compact on Too-Long

If an agent run fails with "Prompt is too long", the runner automatically:

1. Runs `/compact` against the current session to summarize and truncate the conversation.
2. Retries the original request with the compacted session ID.

If the initial run fails for other reasons and the request has a `SessionID`, the runner retries with just the latest message prompt (not the full message history rebuild).

## Streaming Support

When `StreamingEnabled` is true (the default), the runner follows container logs in real-time using `ContainerLogsFollow` instead of waiting for the container to exit.

### Callbacks

Three streaming callbacks are available:

| Callback | Trigger | Data |
|---|---|---|
| `OnTurn` | Each assistant text turn | The text content of the turn |
| `OnToolUse` | Each tool invocation | Tool name + summarized input (e.g., file path for Read/Edit, command for Bash) |
| `OnActivity` | Model detection, subagent events | Activity type + detail (model name, subagent description) |

### Deduplication

The `streamTracker` records the last streamed text. When the final response arrives (after the container exits), it is compared against the last streamed text. If they match, the final response is not sent again -- it was already delivered during streaming.

This prevents duplicate messages: without dedup, the user would see the last streaming turn and then the identical final response.

### Event Broadcasting

For the Electron app, streaming events are broadcast via the `EventsHub`:

- `message.created` -- New message (user or bot)
- `agent.status` -- Status changes (running, completed, error) with metadata (duration, turns, model)
- `tool_use` -- Tool invocations with name and input summary
- `agent.activity` -- Model detection and subagent progress

## Response Delivery

`deliverResponse` handles the final output:

1. **Broadcast completion** -- Send `agent.status: completed` with duration, turn count, stop reason, and model info.
2. **Update session** -- Store the new `SessionID` from the agent response.
3. **Send response** -- Unless it duplicates the last streamed turn, send the response via `bot.SendMessage` with a reply-to reference. Also store the bot message in the database and broadcast via EventsHub.
4. **Mark processed** -- Mark all recent messages as processed in the database. This prevents them from being included in future context windows unnecessarily.

## Thread Resolution

When a message arrives on an unregistered channel, `resolveThread` checks if it might be a thread:

1. Call `bot.GetChannelParentID(channelID)` -- Returns the parent channel ID if the channel is a thread, or empty string if not.
2. Check if the parent channel is active in the database.
3. If active, look up the parent channel record and upsert the thread as a new channel, inheriting:
   - `GuildID`
   - `DirPath`
   - `ParentID` (set to the parent channel ID)
   - `Platform`
   - `SessionID` (shared initially; forked on first agent run)
   - `Permissions` (inherited from parent)
   - `Active: true`

This allows threads to work automatically without requiring manual channel registration. The thread inherits its parent's working directory and permissions, and gets its own session on the first agent interaction.

## Channel Lifecycle

### Channel Join

When the bot is added to a channel (Slack `MemberJoinedChannel` event), `HandleChannelJoin` auto-creates the channel in the database with the platform type and resolved channel name.

### Channel Delete

`HandleChannelDelete` cleans up when a channel or thread is deleted:

- **Thread deletion** -- Remove the thread's MCP config file, delete the thread from the database.
- **Channel deletion** -- List all child threads, remove their MCP config files, delete child threads from the database, remove the channel's own MCP config file, delete the channel from the database.

MCP config cleanup is best-effort; failures are logged as warnings.

## Scheduled Task Execution

The `TaskExecutor` handles scheduled task runs. It follows a similar pattern to message processing but with key differences:

1. **No queue** -- Tasks run independently of the channel queue.
2. **Thread creation** -- On the first streaming turn, a thread is created for the task output with the name prefix `task #N (schedule)`. The prompt is truncated to 100 characters for the thread name.
3. **Ephemeral detection** -- If `AutoDeleteSec > 0`, the agent is instructed via system prompt that responses starting with `[EPHEMERAL]` indicate nothing meaningful to report. Ephemeral threads are renamed with a different emoji and auto-deleted after the configured delay.
4. **Permission user invites** -- All owner and member users from the channel's permissions are invited to the task thread.
5. **Channel event** -- A `channel.created` event is broadcast so the Electron sidebar refreshes.

## Related Documentation

- [Platform Support](platforms.md) -- Platform-specific message handling and bot behavior
- [Slash Commands & Interactions](commands.md) -- Command processing that feeds into `HandleInteraction`
- [Permission & RBAC System](permissions.md) -- Role resolution used by the permission check step
