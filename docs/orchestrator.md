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
- `delayPollInterval` (`time.Duration`) / `delayStop` (`chan struct{}`) -- Drive the delay poller (see [Delayed messages](#delayed-messages)); the poller ticks every `DelayPollInterval` (1s) and is stopped on `Stop`
- `cfg` (`config.Config`) -- Application configuration

## Startup Flow

When `Start` is called:

1. Register message, interaction, channel delete, and channel join handlers on the bot.
2. Register slash commands on all platforms (`bot.RegisterCommands`).
3. Start the bot (opens connections to Discord/Slack/etc.).
4. Start the scheduler (loads tasks from DB, begins cron loop).
5. Start the delay poller (`startDelayPoller`) — a 1s ticker that re-drains channels whose delayed messages have come due (see [Delayed messages](#delayed-messages)). `Stop` closes `delayStop` (once) to shut it down.

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

Every message is stored in the database via `store.InsertMessage`. The message ID is the platform's native ID (Discord snowflake, Slack timestamp) when available, or a generated `ask-{hex}` ID when the platform does not provide one. The row carries `is_triggered` (gated by trigger + permission), `priority` (used to bump deny-with-prompt interrupts ahead of queued rows — see [Interrupting an active run](#interrupting-an-active-run)), `mode` (`"plan"` for plan-mode runs), and `not_before` (unix seconds; `0` = immediate, `> 0` holds the row back — see [Delayed messages](#delayed-messages)) so a daemon restart can resume work without losing fields the in-flight `bot.IncomingMessage` would otherwise carry.

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

`ClaimNextPending` runs inside a single SQLite write transaction and returns the next row matching `is_processed=0 AND is_triggered=1 AND is_running=0 AND kind='message'` for the channel, ordered by `priority DESC, id ASC`. It additionally requires `(not_before = 0 OR not_before <= now)`, so a [delayed row](#delayed-messages) is invisible to the claim until its time arrives. The atomic SELECT + UPDATE serialises the claim against every other writer, so concurrent drains for the same channel can never hand the same row to two agents.

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
    maybeLearn(ctx, channel, msg, resp)                     // see Learn pass
    maybeExplain(ctx, channel, msg)                         // see Explanations
}
```

Errors or stops still mark the trigger row processed (via `markTriggerProcessed`) so the frontend doesn't keep showing it as "processing" while the next queued row starts. `drainChannel`'s loop keeps pulling until `ClaimNextPending` returns `nil`, then releases the channel lock — there is no idle goroutine waiting for work between drains.

## Per-channel Drain Serialization

The `channelLocks` `sync.Map` holds a `*sync.Mutex` per channel. The `TaskExecutor` shares it, so a scheduled task and the chat queue of the thread it writes to never run at once (see [Scheduled Task Execution](#scheduled-task-execution)). `drainChannel` `Lock`s on entry and `Unlock`s when the loop drains the channel empty, so within a single channel only one agent run executes at a time. Across channels the drains are independent: a long agent run on channel A does not block channel B's `HandleMessage` from claiming and processing its own rows. The drain holds the in-memory lock only for the lifetime of the loop, not the row — once a row is released, the next `HandleMessage` call (or `ResumeChannel` from startup) can claim it.

There is no notify channel and no idle processor goroutine: each `HandleMessage` and `ResumeChannel` call attempts to drain on its own goroutine, the mutex collapses concurrent attempts into a single drain, and any rows inserted during a drain (e.g. a priority-bumped interrupt while an earlier row is running) are picked up by the same loop on its next iteration.

## Interrupting an active run

When the user clicks "Deny with prompt" on a gate approval (or the API receives `POST /api/messages` with `interrupt=true`), `messages_handler.go` cancels the active run via `runCanceller.CancelActiveRun(channelID)` and inserts the prompt with `priority = MaxQueuedPriority(channelID) + 1`. The interrupt row outranks any queued messages on the next `ClaimNextPending` (which orders by `priority DESC`) so the prompt runs ahead of them, but **no queued rows are deleted** — they keep their original `priority=0` and resume in FIFO order once the interrupt finishes. This replaces an earlier destructive design that dropped the queued rows entirely.

## Delayed messages

The [`queue_message`](mcpserver.md) MCP tool can hold a prompt back with `delay_seconds`. `POST /api/messages` with `delay_seconds > 0` routes through `HandleIncomingMessageDelayed`, which stamps `not_before = now + delay_seconds` (unix seconds) on the inserted row. Because `ClaimNextPending` skips rows whose `not_before` is still in the future, the immediate drain that `HandleMessage` kicks off claims nothing for that row.

Since the drain is purely event-driven (there is no idle processor goroutine — see [Per-channel Drain Serialization](#per-channel-drain-serialization)), nothing would ever re-attempt the claim once the delay elapses. The **delay poller** closes that gap:

- `startDelayPoller` runs a `time.Ticker` at `DelayPollInterval` (1s). On each tick it calls `store.ChannelsWithDueDelayedMessages`, which returns the distinct channels that have a `kind='message'`, unprocessed, triggered, not-running row with `0 < not_before <= now` — or with a lapsed [edit hold](#edit-holds) (`0 < edit_hold_until <= now`).
- For each such channel it calls `drainAsync(channelID, nil)` — the same drain path as a fresh message, but reconstructed from the row alone. The now-due row is claimable, so it runs.
- `Stop` closes `delayStop` (guarded by a `sync.Once`) to terminate the ticker goroutine. A non-positive `delayPollInterval` disables the poller (tests set it to `0`).

Because eligibility lives entirely in the row's `not_before` column and the poller re-derives due channels from the DB, pending delays survive a **daemon restart**: after the resume sweep, the poller picks up any still-due rows on its next tick without special-casing restart recovery.

## Edit holds

The chat lets the user edit a queued message in place (see [Queued Messages Popup](chat.md#queued-messages-popup)). While the edit is open the row must not start, or the agent would run the old text. Opening the edit stamps `edit_hold_until` (unix seconds) on the row through `POST /api/channels/{id}/queued/{msg_id}/hold`, and `ClaimNextPending` treats a held row as a **barrier**: it picks the top eligible row by `(priority DESC, id ASC)` exactly as before, then claims it only if `edit_hold_until <= now`. A held top row therefore stops the whole channel queue rather than letting the row behind it jump ahead — queue order survives the edit. Rows ahead of the held one keep running.

The hold, the save (`PUT …/queued/{msg_id}`, which writes the content and clears the hold in one statement), and the claim are all single `UPDATE`s on SQLite's one writer connection, and the hold and save only match rows that are still `is_running = 0 AND is_processed = 0`. So exactly one of "edit" and "claim" wins:

- **Edit first** — the row is held; the drain passes over it until save or cancel clears the hold, and the API then kicks the drain via `ResumeChannel`.
- **Claim first** — the hold or save matches no row and the API answers `409`. The app ends the edit, keeps the typed text in the composer, and suggests sending it as a new message.

The hold is a lease (5 minutes, renewed by the app every 2 minutes), so an app that closes or sleeps mid-edit can't stall the channel for good. When it lapses nothing clears the column, but the claim now accepts the row, and the delay poller above also watches for lapsed holds so an idle channel wakes up and drains.

## Agent Request Preparation

`prepareAgentRequest` builds the `agent.AgentRequest`:

1. **Recent messages** -- Fetch the last 50 messages from the database (`recentMessageLimit = 50`). These are reversed (oldest first) and formatted as `role: authorName: content` pairs, where `role` is "user" for human messages and "assistant" for bot messages.

2. **Channel data** -- Load the channel record for `SessionID` and `DirPath`.

3. **Prompt** -- Format as `authorName: content`.

4. **Session fork** -- If the channel is a thread (`ParentID != ""`) and the thread's `SessionID` matches the parent's `SessionID` (meaning this is the first message in the thread), set `ForkSession: true`. This creates an independent session for the thread while inheriting the parent's conversation context.

5. **Worktree parent** -- If the channel is a worktree thread (`Worktree: true`), look up the parent channel's `DirPath` and set `ParentDirPath` on the request. The runner uses this to mount the parent project directory so the container sees the main `.git` directory.

6. **Plan mode** -- If the incoming message has `Mode: "plan"`, set `PlanMode: true` on the request. This appends a system prompt instructing the agent to call `EnterPlanMode` before doing anything else; the tool flips the session's permission context to `plan`, and Claude Code's per-turn attachment loop then injects the full plan-mode instructions (with a computed `planFilePath` and read-only restrictions) on subsequent turns.

7. **Learn thread** -- If the channel is a hidden learn thread (`Kind: "learn"`), the request becomes a learn run (see [Learn pass](#learn-pass)).

8. **Explain thread** -- If the channel is a hidden explain thread (`Kind: "explain"`), the request becomes an explain run (see [Explanations](#explanations)).

## Agent Execution

`executeAgentRun` manages the container lifecycle:

1. **Timeout** -- Create a context with `ContainerTimeout` (default 3600s / 1 hour).
2. **Cancel registration** -- Store the cancel function in `activeRuns` so stop button clicks can cancel the run.
3. **Streaming setup**:
   - Create a `streamTracker` that filters empty turns and tracks the last sent text for deduplication.
   - Set `OnTurn` callback to send intermediate responses as they arrive.
   - Set `OnToolUse` callback to broadcast tool usage events (tool name + summarized input).
   - Set `OnActivity` callback to broadcast model detection and subagent progress events.
4. **Run ID** -- Generate a unique `run_id` (random hex) for this run. The `run_id` is included in all `agent.status` broadcasts so the frontend can distinguish concurrent runs on the same channel (e.g. a chat agent and a scheduled task).
5. **Status broadcast** -- Broadcast `agent.status: running` event with the `run_id`.
6. **Run** -- Execute `runner.Run(ctx, req)`.
7. **Error handling**:
   - Context cancelled (stop button) -- Send "Run stopped." message.
   - Agent error -- Post "⚠️ The run failed:" with the error in a code block. It is sent to the platform *and* stored as a bot message (like the session-limit notice), because the desktop app's bot sends nothing and the `agent.status` error only feeds its notifications — a sent-only message left a failed run silent in the app. When the error says Docker ran out of disk space (`no space left on device` / `ENOSPC`, from the Docker API or the agent's output), the message is instead: "Docker is out of disk space (no space left on device). Free space in Docker — e.g. `docker builder prune -a` and removing unused images/containers — then send again."
   - Both cases broadcast `agent.status: error` event with the same `run_id`.

A run that ends without a result event still says why: the error names the container's exit code (137 as "killed — out of memory or out of disk space") and carries the last lines of its non-JSON output, which is where stderr lands. See [Run failures](containers.md#run-failures).

## Session Management

Claude Code sessions enable conversation continuity across multiple messages. The session ID is stored per-channel in the database.

### Resume

When a channel has a `SessionID`, the agent request includes it. The Docker runner passes `--resume <sessionID>` to Claude CLI, which continues the existing conversation.

### Fork

Thread sessions are forked from the parent's session on the first message. The `--resume <sessionID> --fork-session` flags create a new session that inherits the parent's context. After forking, the thread gets its own `SessionID` stored in the database. A learn pass or explanation can cut its fork where the turn it reviews ended (see [Fork at a turn](#fork-at-a-turn)).

### Pruned transcripts

Claude Code deletes old transcripts while Loop keeps pinning the channel's
session id, so a stored id can outlive the file it names. Before resuming, the
runner checks that the transcript exists and silently starts a fresh session
when it provably doesn't — otherwise `--resume` would fail every turn from then
on. See [Pruned transcripts](sessions.md#pruned-transcripts).

### Compact on Too-Long

If an agent run fails with "Prompt is too long", the runner automatically:

1. Runs `/compact` against the current session to summarize and truncate the conversation.
2. Retries the original request with the compacted session ID.

If the initial run fails for other reasons and the request has a `SessionID`, the runner retries with just the latest message prompt (not the full message history rebuild).

## Streaming Support

The runner follows container logs in real-time using `ContainerLogsFollow` instead of waiting for the container to exit.

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

## Learn pass

After `deliverResponse`, `processClaimedMessage` calls `maybeLearn`, which may queue a **learn pass**: a review of the run that just finished, in a hidden thread, that files proposals for the user to apply. Runs that errored or were stopped return before this, and scheduled tasks don't go through it. See [Chat: Learn from a run](chat.md#learn-from-a-run) for the UI and [Configuration: Learn](configuration.md#learn) for the config.

1. **Config** -- Merge the channel's config the way its runs do: global → root checkout → worktree for a worktree chain (the root checkout being the nearest non-worktree ancestor's `DirPath`), global → the channel's own `DirPath` otherwise. A project config that fails to load falls back to the global config. Config proposals still land in the root checkout's `.loop/config.json`.
2. **Skip reasons** -- The channel was loaded before the run, so outside a learn thread it's read again first: `channel gone` when it was deleted meanwhile (or the lookup fails). The checks below use that fresh row, so a Learn switch turned on or off during the run applies to it. `learnSkipReason` returns the first that applies, logged at debug level:
   - `learn thread` -- the channel is itself a learn thread;
   - `not a desktop channel` -- the channel's platform isn't `local` (a Slack or Discord channel), whose proposals only the desktop app could show;
   - `task thread` -- the channel is a scheduled task's thread (`TaskID != 0`);
   - `parked on a plan or question` -- the channel is parked on an ExitPlanMode or AskUserQuestion card;
   - `learn off` -- the channel's `learn_override` is `off`, or empty and `learn.enabled` is false;
   - `N turns, below min_turns M` -- the run took fewer than `learn.min_turns` turns.

   Then `no session` when the response has no session id.
3. **Learn thread** -- `ensureHiddenThread` returns the channel's learn thread, creating it on first use: a local-platform thread with id `learn-<hex>`, name `learn: <channel name>`, the parent's `GuildID` and `DirPath`, `ParentID` set to the channel, and `Kind: "learn"`. `InsertHiddenThread` inserts it only while the parent still exists (one statement), so a channel deleted in between is skipped as `channel gone` instead of leaving an orphan learn thread. Learn threads are left out of `GET /api/channels` and are deleted, with their messages, quality snapshots and the channel's proposals and learn passes, when the channel is deleted; `DELETE /api/threads/{id}` and `DELETE /api/worktrees` (with a `thread_id`) also cancel the learn thread's running pass and delete its fork (see [Forks](#forks)).
4. **Record** -- `recordLearnPass` stores a `learn_passes` row as `queued` for the turn's last bot message (`LastBotMessage`), with a new trigger id, and broadcasts it as [`learn.pass`](events.md#learnpass). A turn with no reply (or a failed insert) isn't recorded; its pass still runs.
5. **Announce** -- Broadcast a global [`learn.started`](events.md#learnstarted) event for the channel, carrying the learn thread's id.
6. **Trigger** -- `HandleMessage` on the learn thread with author id `loop-learn` (name `loop`), the recorded trigger id, `HasPrefix: true` and the local platform. The content is `The run in "<channel>" just finished. Review it and propose what Loop should learn from it.`, followed by the run's last prompt as a blockquote. The trigger waits on the learn thread's own queue behind its earlier passes and the user's replies, like any queued message, so the channel's queue isn't held up and nothing is replaced or dropped: every pass runs, one at a time. A `loop-learn` message for a channel that isn't active is dropped: it's never auto-created as a plain channel, where the pass would run without the learn restrictions.

When `prepareAgentRequest` builds the learn thread's request, `applyLearnRequest` turns it into a learn run:

- **Session** -- a pass (a `loop-learn` trigger) forks the session of the turn it reviews, cut where that turn ended (see [Fork at a turn](#fork-at-a-turn)), so it sees the turn as it was even when later turns followed. A pass that isn't recorded, or whose turn doesn't record where it ended, forks the parent's whole current session instead. With no session at all the run fails with `learn.ErrNoSession`. A user's reply in the learn thread resumes the learn thread's own session, the latest pass's fork.
- **Agent id** `learn`, which gives the run its own MCP config with the [`propose_learnings`](mcpserver.md#learn-tools-learn-agent-only) tool and none of the inter-agent tools.
- **Model / effort** -- `learn.model` / `learn.effort`, else the parent channel's overrides, else the config's.
- **System prompt** -- the built-in learn instructions (each proposal kind and its payload, at most 5 proposals, propose never act), then the current state so it doesn't propose duplicates: the channel's name, description, ticket URL and whether it's a worktree thread, the project config path, the merged prompt and bash shortcuts, the channel's scheduled tasks, task templates, agentgate rules and mounts, and the channel's earlier proposals: those still waiting on the user (pending, applying or failed, each with its id and status), the 20 most recently dismissed and the 20 most recently withdrawn (with the reason), so a later pass doesn't file them again. A dismissed or withdrawn rename, description or ticket URL only rules out that value: a later run pointing to a different one can propose it. The instructions also say when to withdraw a waiting proposal: when this run shows it's wrong or obsolete (a wrong shortcut command, a name the work has moved past, a mount no longer needed), or a new proposal supersedes it (`replaces`), but not just because the user hasn't acted on it yet, and never one applying. `learn.prompt` is appended last.
- **Tools** -- `ReadOnly` passes `--tools Read,Grep,Glob,TodoWrite,ToolSearch`, the whole built-in tool set of the pass: no `Bash`, `Edit`, `Write`, `NotebookEdit`, `WebFetch`, `WebSearch`, `Agent`, `Skill`, `EnterWorktree`, `RemoteTrigger`, `PushNotification`, `SendMessage`, `Cron*` or any built-in a later Claude Code adds. `--tools` doesn't reach MCP tools, so it also adds, on top of `claude_batch_disallowed_tools`, `--disallowedTools` for every Loop MCP tool that changes state (shortcuts, tasks, threads and channels, messages, agent messages and `update_agent_status`, workflows, playgrounds, chat components, `index_memory`, `quality_scan`, which saves a snapshot, and `report_review_findings`). Loop's read-only MCP tools (`list_*`, `show_task`, `get_*`, `search_*`, the `quality_*` reports including `quality_snapshot`, and `quality_whatif`, which only simulates) stay available.
- **MCP servers** -- the pass's MCP config (`.loop/mcp-<learn thread>-learn.json`) holds only the Loop server: no user MCP servers, no browser. `--strict-mcp-config` stops Claude from also loading the project's `.mcp.json` or user-level servers. The file goes with the learn thread when its parent is deleted.

A learn thread only ever runs this way. When its parent can't be loaded (lookup error or gone), `prepareAgentRequest` fails the run rather than letting it run as a chat with the full tool set; the row is marked processed and the drain moves on.

`processClaimedMessage` calls `learnPassStarted` before a `loop-learn` run, which finds the pass's row by its trigger id and marks it `running`, and `learnPassDone` after it: `done`, or `failed` with the error. Each change is broadcast as `learn.pass`. `GET /api/channels/{id}/learn` reports `running` while a pass's row is `running` (`LearnPassRunning`): a pass still queued, or the user's reply running there, doesn't count. Proposals the learn thread files (`POST /api/channels/{id}/learn/proposals`) carry the `message_id` of its running pass, else its newest done one, so a user's later reply in the learn thread files against the turn that pass reviewed. At startup, after `ResetStaleRunningMessages`, `FailInterruptedLearnPasses` fails the passes left `queued` or `running` whose trigger no longer waits unprocessed in the learn thread; those still waiting run as the thread drains, as explanations do. Rows from before passes queued this way may still say `superseded`: a newer run's pass replaced that one before it started.

The learn run's `agent.status` events carry `trigger: "learn"` (`runTrigger` matches the `loop-learn` author), so the desktop app doesn't mark it unread, notify or bounce the dock. A message the user sends in the learn thread runs the same way (same agent id, prompt and denials) but with `trigger: "learn-reply"`, and never starts a learn pass of its own.

**On-demand passes** -- `LearnTurn(ctx, ch, messageID)`, called by [`POST /api/channels/{id}/learn/passes`](api.md#post-apichannelsidlearnpasses), starts a pass over one turn the user picked, whether or not the Learn switch is on and whatever the turn's length:

1. **Checks** -- the channel must be one explanations run for (`explain.Unavailable`: a desktop channel, not a task thread or hidden thread; else `learn.ErrUnavailable`), `messageID` a bot message of a turn in it (`GetChatMessage`, with a trigger id; else `learn.ErrNotATurn`), and there must be a session to fork: the turn's own, recorded on its bot message, or the channel's (else `learn.ErrNoSession`).
2. **Existing** -- `ActiveLearnPass` returns the turn's pass already `queued` or `running`, which is returned as it is. A turn whose earlier pass is done or failed (or, on an old row, superseded) gets a new one.
3. **Record** -- the turn's prompt is loaded (a lookup error is logged and the prompt left out), the learn thread ensured, and a `learn_passes` row inserted as `queued` for `messageID` and broadcast as `learn.pass`, as `recordLearnPass` does. A failed insert fails the request.
4. **Queue and start** -- as for an automatic pass (steps 5-6 above): its trigger queues behind the learn thread's earlier passes, and its run forks the turn's session cut where the turn ended, falling back to the channel's current session. The trigger is `learn.TurnTriggerMessage`: the same first line as an automatic pass's (so `learn.IsTrigger` still knows it), then that the user asked for this turn to be reviewed, quoting its prompt and final reply (each cut at 2000 characters).

Each pass forks into a new session file in the channel's Claude project dir; the learn thread keeps only its latest (see [Forks](#forks)). `GET /api/channels/{id}/sessions` leaves them out (see [API](api.md#get-apichannelsidsessions)), and a terminal pane's crash relaunch resumes the pane's own session by id rather than `claude --continue`, which would pick the newest file, often a learn pass's.

## Explanations

An explanation is a write-up of one chat turn, run in the channel's hidden explain thread. See [Chat: Explain a turn](chat.md#explain-a-turn) for the UI and [Configuration: Explain](configuration.md#explain) for the config.

`Explain(ctx, ch, messageID, force)` starts one, called by [`POST /api/channels/{id}/explanations`](api.md#post-apichannelsidexplanations) and by `maybeExplain`:

1. **Checks** -- `explain.Unavailable` refuses Slack and Discord channels, task threads and hidden threads (`ErrUnavailable`). Without `force`, an existing explanation of the turn is returned as it is. The message must be a bot message of the channel with a `trigger_msg_id` (`ErrNotATurn`), and there must be a session to fork: the turn's own, recorded on its bot message, or the channel's (`ErrNoSession`).
2. **Explain thread** -- `ensureHiddenThread` (shared with the learn pass) returns the channel's explain thread, creating it on first use: id `explain-<hex>`, name `explain: <channel>`, `Kind: "explain"`.
3. **Queue** -- `QueueExplanation` upserts the `explanations` row for `(channel_id, message_id)` as `queued`, with a new trigger id. A row already queued or running is left alone and returned, unless it hasn't moved for an hour (its trigger was lost). A re-explain keeps the row's id and clears its content.
4. **Trigger** -- For a newly queued row, broadcast [`explain.updated`](events.md#explainupdated) and `HandleMessage` on the explain thread with author id `loop-explain` and that trigger id. The content names the channel and quotes the turn's prompt and final reply. Runs on the explain thread take turns on its queue, so one explanation of a channel runs at a time and the channel's own queue isn't held up.

When `prepareAgentRequest` builds the explain thread's request, `applyExplainRequest` turns it into an explain run: it forks the explained turn's session cut where the turn ended (see [Fork at a turn](#fork-at-a-turn)), else the parent's whole current session, and fails with `ErrNoSession` when there's neither; agent id `explain` (its own MCP config with only the Loop server), `ReadOnly` (the same tool set and denials as a learn pass), `explain.model` / `explain.effort` else the parent's overrides, and the built-in explain prompt with `explain.prompt` appended. An explain thread runs nothing else: a message from another author, or one whose parent is gone, fails the run.

`processClaimedMessage` calls `explainRunStarted` before the run, which marks the row `running`, and `explainRunDone` after it: `done` with the final reply as `content`, or `failed` with the error (an empty reply counts as failed). Each change is broadcast as `explain.updated`. Explain runs carry `trigger: "explain"`, so they don't mark anything unread, notify or bounce the dock.

**Automatic explanations** -- After a run completes, `maybeExplain` rereads the channel and explains the turn's last bot message (`LastBotMessage`) when `explainSkipReason` finds nothing against it: the channel is available, the run isn't parked on a plan or question card, the Explain switch is on (`explain_override`, else `explain.enabled`), and there's a session.

**Startup** -- After `ResetStaleRunningMessages`, `FailInterruptedExplanations` marks `failed` every queued or running explanation whose trigger no longer waits unprocessed in its explain thread. Those still waiting run as the thread drains.

Explain runs' session files are left out of `GET /api/channels/{id}/sessions`, like learn passes' (`explain.IsTrigger`), and each is deleted once its run ends (see [Forks](#forks)).

## Fork at a turn

Each bot text turn stored from a run records where it ended in the session: the `messages` columns `session_id` and `transcript_uuid` come from the stream-json assistant event's top-level `session_id` and `uuid` (a subagent's events, with a `parent_tool_use_id`, record nothing). A final reply that repeats no streamed turn has no event of its own and records nothing, nor do rows stored before the columns existed.

`forkAtTurn` points a learn pass's or explanation's request at its reviewed turn: when the turn's bot message records both, `SessionID` is that session and `ResumeAt` that uuid, with `ForkSession`; otherwise the parent's current session, uncut. The runner passes `--resume-session-at=<uuid>` after `--fork-session` only for a batch run that resumes a session with forking on, so the fork ends at that entry and the turns after it aren't in it. When Claude exits with `No message found with message.uuid` (the entry isn't in the session, e.g. after a compaction), the runner logs a warning and retries once with `ResumeAt` cleared, forking the whole session. Every other retry that stops forking (a missing transcript, a compaction retry, the backoff retry) clears `ResumeAt` too.

## Forks

Every learn pass and explanation forks a new session file, `<id>.jsonl` and an optional `<id>/` directory, in the channel's Claude project dir (`~/.claude/projects/<encoded dir>/`). `sessionFiles` deletes both, with its home dir and remove funcs injected. `dropFork` guards each delete: only a hidden thread's session, never one another channel row points to (`SessionInUse`), never the parent's session a legacy learn thread still holds with `fork_pending`, and only on the thread's drain, with no run in flight. A failed delete is only logged; the files are left behind.

- **Explain** -- `afterHiddenRun` deletes each explain run's fork once the run ends, done or failed.
- **Learn** -- the learn thread keeps its latest fork for the user's replies. When a pass stores a new one, the fork it replaced is deleted; a failed run's fork, never stored, is deleted at once. A reply that resumed the thread's fork makes none.
- **Deleted thread** -- `StopHiddenThread`, called for each hidden thread deleted with its channel or thread, cancels its run and then deletes its fork on the thread's drain. A fork made by a run that was still going when the thread was deleted is deleted when that run ends.

A failed run whose streamed turns never reported a session (it stopped before any text) leaves its fork behind, since its id is unknown.

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

1. **No drain queue, same lock** -- Tasks bypass `drainChannel` and `ClaimNextPending` and call `runner.Run` directly, but they hold the per-channel mutex of the thread they write to (shared through `SetChannelLocks`). A task resuming its thread waits for a chat run already draining it, and reads the thread's session only once it holds the lock, so it resumes where that run left off. A message sent to the thread while the task runs, by you or by the task's own `queue_message`, is queued and runs after the task, instead of starting a second run on the same session. A first run takes the lock of the thread it creates as soon as it's created. The parent channel's lock is never taken, so chat in the channel is unaffected.
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
