---
title: Orchestrator & Message Processing
---
The orchestrator is the central coordinator of the Loop bot. It connects the chat platform bots, the agent runner (Docker containers), the scheduler, and the database. All message processing, command handling, permission checks, and agent execution flow through it.

## Architecture

The `Orchestrator` struct holds references to:

- `store` (`db.Store`) -- SQLite database for channels, messages, tasks, and permissions
- `bot` (`orchestrator.Bot`) -- The `BotRouter` that dispatches to platform-specific bots
- `runner` (`orchestrator.Runner`) -- Docker container runner for agent execution
- `scheduler` (`scheduler.Scheduler`) -- Cron/interval/once task scheduler
- `events` (`events.Broadcaster`) -- SSE/WebSocket event broadcaster for the Electron app
- `channelLocks` (`sync.Map`) -- Per-channel `*sync.Mutex` that serialises the drain loop so only one agent run executes per channel at a time
- `activeRuns` (`sync.Map`) -- Maps channel IDs to cancel functions for stop-button support
- `activeRunMsgIDs` (`sync.Map`) -- Maps channel IDs to the `msg_id` of the row currently running; surfaced via `ActiveRunMessageID` for the interrupt path and FE diagnostics
- `cfg` (`config.Config`) -- Application configuration

## Startup Flow

When `Start` is called:

1. Register message, interaction, channel delete, and channel join handlers on the bot.
2. Register slash commands on all platforms (`bot.RegisterCommands`).
3. Start the bot (opens connections to Discord/Slack/etc.).
4. Start the scheduler (loads tasks from DB, begins cron loop).

After `Start` returns, `cmd/loop/serve.go` runs the **DB-queue resume sweep** before signalling readiness:

1. `store.ResetStaleRunningMessages` clears `is_running=1` rows left over from the prior daemon run (their containers are gone, so their agent runs cannot survive a restart). The sweep returns `(channel_id, msg_id)` pairs grouped per channel and the daemon broadcasts a `messages.processed` event per channel so any reconnected client clears the stale "processing" label.
2. `store.ListPendingChannels` returns every channel that still has `is_triggered=1 AND is_processed=0` rows; the daemon spawns a `go orch.ResumeChannel(ctx, ch)` per channel. `ResumeChannel` is `drainChannel(ctx, ch, nil)` — the same path `HandleMessage` uses, but with a `nil` incoming so the run is reconstructed from the row alone.

Rows that were running mid-restart end up marked processed with no agent response — the user sees the "processing" label disappear and can re-send if they wanted it. Queued rows resume in priority order.

## Message Flow

The complete lifecycle of an incoming message follows this path:

```
Platform Event
    |
    v
HandleMessage (channel active check, auto-create, thread resolution)
    |
    v
Trigger check (mention, reply, prefix, DM)
    |
    v
Permission check (config + DB merge)
    |
    v
Store message in DB (is_triggered=1 when allowed) + broadcast event
    |
    v
drainChannel (per-channel mutex, ClaimNextPending in priority order)
    |
    v
processClaimedMessage (stop button, typing, agent run, deliver, release)
```

### Step 1: Channel Resolution

`HandleMessage` first checks if the channel is active in the database:

- **Active channel** -- Proceed to message storage.
- **Inactive channel** -- Attempt thread resolution via `resolveThread`. If the channel is a thread with an active parent, upsert the thread as a channel inheriting the parent's properties (`DirPath`, `SessionID`, `GuildID`, `Permissions`, `Platform`). If not a thread and the message has a trigger (mention, prefix, reply, DM), auto-create the channel. Otherwise, silently ignore.

### Step 2: Trigger Check

A message is "triggered" if any of these conditions are true:

- `IsBotMention` -- The message mentions the bot (platform-specific detection)
- `IsReplyToBot` -- The message is a reply to a bot message or is in a bot-owned thread
- `HasPrefix` -- The message starts with `!loop`
- `IsDM` -- The message is a direct message

If none are true, the message is still stored (so the bot can passively record conversation context) but with `is_triggered=0`, which keeps it out of the drain queue.

### Step 3: Permission Check

Before persisting `is_triggered=1`, the orchestrator checks whether the author has permission:

- **Bot self-mentions** are always allowed (e.g., from `create_thread` MCP tool posts).
- **Local platform** messages always bypass permission checks -- the user is running on their own machine.
- For other cases, `resolveRole` merges config-file permissions and database permissions to determine the author's role. If the resolved role is empty (no role), the row lands with `is_triggered=0` and the message is silently ignored with a log entry — denied messages stay as plain history and never enter the drain queue.

See [Permission & RBAC System](permissions.md) for the full merge logic.

### Step 4: Message Storage

Every message is stored in the database via `store.InsertMessage`. The message ID is the platform's native ID (Discord snowflake, Slack timestamp) when available, or a generated `ask-{hex}` ID when the platform does not provide one. The row carries `is_triggered` (gated by trigger + permission), `priority` (used to bump deny-with-prompt interrupts ahead of queued rows — see [Interrupting an active run](#interrupting-an-active-run)), and `mode` (`"plan"` for plan-mode runs) so a daemon restart can resume work without losing fields the in-flight `bot.IncomingMessage` would otherwise carry.

If an event broadcaster is configured, a `message.created` event is broadcast with the message data so the Electron app can update its UI in real time.

### Step 5: drainChannel

When a row lands with `is_triggered=1`, `HandleMessage` calls `drainChannel(channelID, incoming)` on the same goroutine. The drain is the only path that runs the agent — there is no in-memory queue and no per-message goroutine waiting on a slot.

```go
func (o *Orchestrator) drainChannel(ctx, channelID, incoming *bot.IncomingMessage) {
    lock := channelLocks.LoadOrStore(channelID, &sync.Mutex{})
    lock.Lock(); defer lock.Unlock()
    for {
        row, _ := store.ClaimNextPending(ctx, channelID)  // SELECT + UPDATE is_running=1
        if row == nil { return }
        processClaimedMessage(ctx, row, incoming)
        store.ReleaseRunningMessage(ctx, row.ID, true)    // is_running=0, is_processed=1
    }
}
```

`ClaimNextPending` runs inside a single SQLite write transaction and returns the next row matching `is_processed=0 AND is_triggered=1 AND is_running=0 AND kind='message'` for the channel, ordered by `priority DESC, id ASC`. The atomic SELECT + UPDATE serialises the claim against every other writer, so concurrent drains for the same channel can never hand the same row to two agents.

`processClaimedMessage` reconstructs a minimal `bot.IncomingMessage` from the row (`AuthorID`, `Content`, `Mode`, `MsgID`, `Priority`) and overlays the bot-side fields (`Platform`, `IsBotMention`, `IsReplyToBot`, `IsDM`, `AuthorRoles`, `GuildID`) from `incoming` when the msg_ids match. For priority-bumped or daemon-restart-resume rows the `incoming` value does not match (or is `nil`), and the run executes from the row alone. The remainder of the body matches the previous in-memory flow:

```go
func (o *Orchestrator) processClaimedMessage(ctx, row, incoming) {
    req, recent, channel, err := prepareAgentRequest(msg)
    if err != nil { return }                                 // ReleaseRunningMessage still fires
    stopMsgID, _ := bot.SendStopButton(channelID)
    defer activeRuns.Delete(channelID)
    defer activeRunMsgIDs.Delete(channelID)
    defer bot.RemoveStopButton(channelID, stopMsgID)
    activeRunMsgIDs.Store(channelID, msg.MessageID)
    go refreshTyping(typingCtx, channelID)
    resp, lastText, runID, err := executeAgentRun(ctx, msg, req, channel)
    if err != nil { markTriggerProcessed(msg, recent); return }
    deliverResponse(msg, resp, recent, lastText, runID)
}
```

Errors or stops still mark the trigger row processed (via `markTriggerProcessed`) so the frontend doesn't keep showing it as "processing" while the next queued row starts. `drainChannel`'s loop keeps pulling until `ClaimNextPending` returns `nil`, then releases the channel lock — there is no idle goroutine waiting for work between drains.

## Per-channel Drain Serialization

The `channelLocks` `sync.Map` holds a `*sync.Mutex` per channel. `drainChannel` `Lock`s on entry and `Unlock`s when the loop drains the channel empty, so within a single channel only one agent run executes at a time. Across channels the drains are independent: a long agent run on channel A does not block channel B's `HandleMessage` from claiming and processing its own rows. The drain holds the in-memory lock only for the lifetime of the loop, not the row — once a row is released, the next `HandleMessage` call (or `ResumeChannel` from startup) can claim it.

There is no notify channel and no idle processor goroutine: each `HandleMessage` and `ResumeChannel` call attempts to drain on its own goroutine, the mutex collapses concurrent attempts into a single drain, and any rows inserted during a drain (e.g. a priority-bumped interrupt while an earlier row is running) are picked up by the same loop on its next iteration.

## Interrupting an active run

When the user clicks "Deny with prompt" on a gate approval (or the API receives `POST /api/messages` with `interrupt=true`), `messages_handler.go` cancels the active run via `runCanceller.CancelActiveRun(channelID)` and inserts the prompt with `priority = MaxQueuedPriority(channelID) + 1`. The interrupt row outranks any queued messages on the next `ClaimNextPending` (which orders by `priority DESC`) so the prompt runs ahead of them, but **no queued rows are deleted** — they keep their original `priority=0` and resume in FIFO order once the interrupt finishes. This replaces an earlier destructive design that dropped the queued rows entirely.

## Agent Request Preparation

`prepareAgentRequest` builds the `agent.AgentRequest`:

1. **Recent messages** -- Fetch the last 50 messages from the database (`recentMessageLimit = 50`). These are reversed (oldest first) and formatted as `role: authorName: content` pairs, where `role` is "user" for human messages and "assistant" for bot messages.

2. **Channel data** -- Load the channel record for `SessionID` and `DirPath`.

3. **Prompt** -- Format as `authorName: content`.

4. **Session fork** -- If the channel is a thread (`ParentID != ""`) and the thread's `SessionID` matches the parent's `SessionID` (meaning this is the first message in the thread), set `ForkSession: true`. This creates an independent session for the thread while inheriting the parent's conversation context.

5. **Worktree parent** -- If the channel is a worktree thread (`Worktree: true`), look up the parent channel's `DirPath` and set `ParentDirPath` on the request. The runner uses this to mount the parent project directory so the container sees the main `.git` directory.

6. **Plan mode** -- If the incoming message has `Mode: "plan"`, set `PlanMode: true` on the request. This appends a system prompt instructing the agent to call `EnterPlanMode` before doing anything else; the tool flips the session's permission context to `plan`, and Claude Code's per-turn attachment loop then injects the full plan-mode instructions (with a computed `planFilePath` and read-only restrictions) on subsequent turns.

## Agent Execution

`executeAgentRun` manages the container lifecycle:

1. **Timeout** -- Create a context with `ContainerTimeout` (default 3600s / 1 hour).
2. **Cancel registration** -- Store the cancel function in `activeRuns` so stop button clicks can cancel the run.
3. **Streaming setup** (if `StreamingEnabled`):
   - Create a `streamTracker` that filters empty turns and tracks the last sent text for deduplication.
   - Set `OnTurn` callback to send intermediate responses as they arrive.
   - Set `OnToolUse` callback to broadcast tool usage events (tool name + summarized input).
   - Set `OnActivity` callback to broadcast model detection and subagent progress events.
4. **Run ID** -- Generate a unique `run_id` (random hex) for this run. The `run_id` is included in all `agent.status` broadcasts so the frontend can distinguish concurrent runs on the same channel (e.g. a chat agent and a scheduled task).
5. **Status broadcast** -- Broadcast `agent.status: running` event with the `run_id`.
6. **Run** -- Execute `runner.Run(ctx, req)`.
7. **Error handling**:
   - Context cancelled (stop button) -- Send "Run stopped." message.
   - Agent error -- Send error message to the channel.
   - Both cases broadcast `agent.status: error` event with the same `run_id`.

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

- `message.created` -- New message (user or bot). Bot replies and intermediate agent-event rows (`thinking`, `tool_use`, `tool_result`, `compacting`) carry a `trigger_msg_id` field pointing back at the user message whose run produced them; the FE uses it to group events under the correct user row on reload, surviving out-of-order processing of priority-bumped runs
- `message.deleted` -- A queued user message was removed from the queue (via `DELETE /api/messages/{id}`)
- `messages.processed` -- One or more user messages were marked processed; the FE clears their `processing`/`queued` labels. Emitted by `deliverResponse`, by `markTriggerProcessed` on run errors/stops, and by the daemon startup sweep when `ResetStaleRunningMessages` clears in-flight rows from a prior run
- `agent.status` -- Status changes (running, completed, error) with metadata (duration, turns, model, run_id, `msg_id` of the triggering row so the FE can label the correct chat bubble as "processing" even when a priority-bumped row is processed out of chronological order, and `trigger_content` on the `running` transition so the FE can render the floating `TriggerQuote` banner when the user has scrolled the triggering row off-screen)
- `tool.use` -- Tool invocations with name and input summary
- `agent.activity` -- Model detection and subagent progress
- `agent.ask_user` -- Structured questions from AskUserQuestion tool
- `agent.exit_plan` -- Plan ready for review from ExitPlanMode tool
- `agent.todos` -- Todo list updates from TodoWrite tool

## Response Delivery

`deliverResponse` handles the final output:

1. **Broadcast completion** -- Send `agent.status: completed` with run_id, duration, turn count, stop reason, and model info.
2. **Update session** -- Store the new `SessionID` from the agent response.
3. **Send response** -- Unless it duplicates the last streamed turn, send the response via `bot.SendMessage` with a reply-to reference. Also store the bot message in the database (stamped with `trigger_msg_id = msg.MessageID` so the FE can group the reply under its triggering user message) and broadcast via EventsHub.
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

1. **No drain queue** -- Tasks bypass `drainChannel` entirely and call `runner.Run` directly. They do not go through `ClaimNextPending` and do not contend with chat messages for the per-channel mutex.
2. **Thread creation** -- On the first streaming turn, a thread is created for the task output with the name prefix `task #N (schedule)`. The prompt is truncated to 100 characters for the thread name.
3. **Ephemeral detection** -- If `AutoDeleteSec > 0`, the agent is instructed via system prompt that responses starting with `[EPHEMERAL]` indicate nothing meaningful to report. Ephemeral threads are renamed with a different emoji and auto-deleted after the configured delay.
4. **Permission user invites** -- All owner and member users from the channel's permissions are invited to the task thread.
5. **Channel event** -- A `channel.created` event is broadcast so the Electron sidebar refreshes.
6. **Stop button support** -- The executor registers `runCancel` in the orchestrator's shared `activeRuns` map before calling `runner.Run`, and defers cleanup on return. This allows the `/loop stop` command and the Electron stop button to cancel a running task. On the local platform, subsequent runs (where the thread already exists) register under the thread's channel ID so the stop button in the thread view targets the correct container. Discord/Slack runs register under the parent channel ID.
7. **Thread-routed status events** -- For subsequent runs on the local platform, `agent.status` events include `thread_id` in the payload. The frontend routes the running/completed/error state to the thread's store entry (not the parent), so the parent channel doesn't show a running indicator for thread work.

## Related Documentation

- [Platform Support](platforms.md) -- Platform-specific message handling and bot behavior
- [Slash Commands & Interactions](commands.md) -- Command processing that feeds into `HandleInteraction`
- [Permission & RBAC System](permissions.md) -- Role resolution used by the permission check step
