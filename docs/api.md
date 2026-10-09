---
title: HTTP API Reference
---
Loop exposes a lightweight HTTP API for managing channels, threads, messages, tasks, tickets, files, memory, and real-time events. All endpoints are prefixed with `/api/`.

**Related docs:** [Terminal WebSocket](terminal.md) | [Events System](events.md) | [Memory System](memory.md) | [Kanban Panel](kanban.md)

## General

- **Authentication:** Every route except the few listed under [Authentication](#authentication) needs a token. See that section.
- **CORS:** All responses include `Access-Control-Allow-Origin: *`, `Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS`, and `Access-Control-Allow-Headers: Content-Type, Authorization`. Preflight `OPTIONS` requests return `204 No Content`. CORS grants nothing on its own: without a token a page on another origin only gets 401s.
- **Content-Type:** JSON endpoints return `application/json`. File-reading endpoints return `text/plain; charset=utf-8`.
- **Error responses:** Plain text body with the appropriate HTTP status code.

### Common Error Codes

| Code | Meaning |
|------|---------|
| 400  | Bad request -- missing/invalid parameters or request body |
| 401  | Missing or invalid API token |
| 403  | The token is valid but may not make this request (an agent token on an owner-only route, or naming another project) |
| 404  | Resource not found |
| 413  | Request entity too large (file operations) |
| 500  | Internal server error |
| 501  | Feature not configured (service dependency is nil) |
| 503  | Service unavailable (commands not configured) |

---

## Authentication

Every API caller is either the **owner** or an **agent**. Nothing is trusted for where it comes from: no cookies, no origin checks, and `localhost` gets no pass.

### Tokens

| Caller | Token | Where it lives |
|---|---|---|
| Owner: the desktop app, the `loop` CLI, host tools | Owner token, 32 random bytes in hex | `api-token` in Loop's directory under the OS user config dir (`~/Library/Application Support/loop/` on macOS, `~/.config/loop/` on Linux). Dir `0700`, file `0600`. `loop serve` creates it on first start |
| Agent: the clients inside one agent container (the MCP server, mcp-browser, `loop review`, the agent-channel WebSocket) | Agent token, issued per container | `/run/loop/api-token` in the container, readable by the agent user only. Never in the container's environment, so `docker inspect` doesn't show it. Revoked when the container goes |

The owner token's directory is never a channel dir and never mounted into a container, whatever the config says.

Send the token as a header:

```
Authorization: Bearer <token>
```

Browsers can't set headers on a WebSocket, so WebSocket clients send it as a subprotocol instead: `Sec-WebSocket-Protocol: loop, loop.token.<token>`. The server answers with `loop`.

**Rotating.** `loop api:rotate-token`, **Developer → Rotate API Token…** in the desktop app, or `POST /api/auth/rotate` with the owner token writes a new owner token and the daemon switches to it at once. The CLI reads the file on every request and the desktop app reads it again on its next 401, so both carry on without a restart. A browser tab opened with `loop app:url` needs a fresh URL. Agent tokens are unaffected.

**Browser mode.** A UI running in a plain browser tab, without the desktop app's preload, has no file to read. `loop app:url` prints the UI's URL with the token in the fragment (`#loop_token=...`); the UI moves it to the tab's `sessionStorage` and drops it from the address bar. Fragments aren't sent to the server, but anyone with the URL has full access, so treat it as a secret.

### Agent scope

An agent token works only on the routes in-container clients call (the table is `agentRoutes` in `internal/api/agent_scope.go`): messages, threads, tasks, shortcuts, memory, playground, workflows, learn proposals, review, quality, the UI bridge (`GET /api/ui/state`, `POST /api/ui/commands`), the browser action and the agent-channel WebSocket. Every other route answers 403, among them config, terminals, gates, images, token rotation and content links.

On the routes it may call, an agent is held to its own project. Every channel, thread, task, workflow run and `dir_path` a request names, in the path, the query or the JSON body, must be the agent's own channel or dir, or share its project root (after symlinks). A request that names anything else gets 403. The exception is `POST /api/messages` (the `send_message` tool): its `channel_id` may be any channel, so one agent can hand work or news to another project's channel. Agents may also only change `project` bash shortcuts, and can't start workflows while `workflow_bash_local` runs workflow bash on the host. The routes that change review comments (delete, edit, push one, push all) are held to the agent's own channel, not its project: they act on the PR as the user. An agent may not delete a GitHub comment.

On the UI bridge, an agent's command must start with `select_channel` to a channel of its project, and every `select_channel` in it must stay in the project. `GET /api/ui/state` leaves out the `state` of windows showing a channel outside it. The UI WebSocket stays owner-only.

### Public routes

| Route | Why |
|---|---|
| `GET /api/health` | Liveness checks before a token is at hand |
| `POST /api/gate/container-approval` | Checks its own per-container gate token, which only root processes in the container can read |
| `GET /c/{cap}/{path...}` | Content links, below |

### `POST /api/auth/rotate`

Owner only. Writes a new owner token and switches to it. Returns `204 No Content`.

### `POST /api/content-caps`

Owner only. Iframes, images, video and `<base href>` can't send an `Authorization` header, so the UI loads them through a short-lived signed link instead. Returns a base URL that serves one channel root's files or one playground.

**Request body:**

| Field | Type | Description |
|---|---|---|
| `kind` | string | `raw` (a channel root's files) or `playground` |
| `channel_id` | string | `raw`: the channel (required). `playground`: set for a project playground, empty for a global one |
| `root` | int | `raw`: the channel root index |
| `name` | string | `playground`: its name (required) |

**Response (200):**
```json
{"base_url": "/c/<cap>/", "expires_in_sec": 3600}
```

The link is signed with a key that lives only in the running daemon, so links stop working when it restarts. The UI mints a new one well before expiry.

### `GET /c/{cap}/{path...}`

Serves `path` under what the link names, like `GET /api/channels/{id}/raw/{root}/{path...}` and the playground routes do, with the same path checks: a path that leaves the root, symlinks included, gets 400. A bad or expired link gets `403 link expired or invalid`. Playground pages are sent with `Content-Security-Policy: sandbox allow-scripts allow-forms allow-modals allow-popups allow-downloads`, which gives them an opaque origin: agent-written JavaScript can't read the UI's storage even when both are served from the same origin.

---

## Health

### `GET /api/health`

Returns server health status.

**Response (200):**
```json
{"status": "ok"}
```

---

## Channels

### `GET /api/channels`

List all channels with optional filtering. Enriches each channel with container running status, agent running status, and current git branch.

**Query Parameters:**

| Param      | Type   | Description |
|------------|--------|-------------|
| `query`    | string | Filter channels by name (case-insensitive substring match) |
| `platform` | string | Filter by platform (`discord`, `slack`, `local`) |

**Response (200):**
```json
[
  {
    "channel_id": "abc123",
    "name": "my-project-a1b2",
    "dir_path": "/home/user/projects/my-project",
    "parent_id": "",
    "active": true,
    "container_running": true,
    "agent_running": false,
    "review_running": false,
    "branch": "main",
    "commit": "abc1234",
    "worktree": false,
    "locked": false,
    "last_activity_at": "2026-03-25T14:30:00Z"
  }
]
```

**Behavior notes:**
- When a channel has no `dir_path`, falls back to `~/.loop/{channel_id}/work`.
- `container_running` is determined by querying the Docker daemon for running containers.
- `agent_running` indicates whether an active Claude agent run exists for the channel. A run is registered before its `agent.status` "running" event is broadcast, so a response to a request made after that event reflects the run.
- `review_running` is true while a review run or a dedup pass runs on the channel's [review session](#review) (its status is `reviewing`). Review sessions live in the daemon's memory, so this comes from the review store, not the database. The sidebar shows the channel as running and patches the flag live from `review.status` events.
- `branch` is resolved by running `git rev-parse --abbrev-ref HEAD` in the channel's directory.
- `commit` is the short commit hash from `git rev-parse --short HEAD`, and `subject` its subject line.
- `upstream` is the branch's tracking branch (e.g. `origin/main`), with `ahead` / `behind` counting the commits between them. Omitted when there's none.
- `sync_base` is set on a worktree thread whose base branch still resolves, with `base_ahead` / `base_behind` counting the commits between the checkout and it.
- `worktree` is true for threads created via `POST /api/worktrees`.
- `root_dir_path` is set on rows inside a worktree chain — the worktree thread itself, a thread under it (e.g. a scheduled task's), or a worktree cut from another worktree — and holds the `dir_path` of the non-worktree checkout the chain was cut from. Omitted everywhere else. The Kanban panel uses it for its Local/Root board switch.
- `model_override` / `effort_override` are the model and effort picked for the channel (see [`PATCH /api/channels/{id}/agent-config`](#patch-apichannelsidagent-config)). Omitted when it inherits the config's.
- `last_activity_at` is when the channel's newest message was written, or when its review session last changed if that's later, since a review writes no messages. Omitted when it has neither, or when the message lookup fails and there's no review session (the list is still returned). The sidebar's Recent section sorts by it.
- `description` is what the channel or thread is for, set via [`POST /api/channels/{id}/description`](#post-apichannelsiddescription). Omitted when empty.
- `ticket_url` is the URL of the channel or thread's ticket, set via [`POST /api/channels/{id}/ticket`](#post-apichannelsidticket). Omitted when unset.
- `trust_pending` is `true` when the project config's host-reaching fields changed since you last trusted them, so they don't apply yet (see [`GET /api/config/project/trust`](#get-apiconfigprojecttrust)). For a worktree chain it's the root checkout's config. Omitted otherwise, and when the config can't be read.
- `task_id` is set on a thread a scheduled task created for its output: the id of that task. Omitted on every other channel and thread, including ones that host tasks. The sidebar's hide-task-threads toggle filters on it.
- `locked` is true when the channel/thread is guarded against accidental deletion (toggle via [`PATCH /api/channels/{id}/lock`](#patch-apichannelsidlock)). `DELETE /api/channels/{id}` and `DELETE /api/threads/{id}` return `409 Conflict` while a row is locked.
- Hidden learn and explain threads (`kind: "learn"` or `"explain"`, see [Learn](#learn) and [Explain](#explain)) are left out of the list.

**Errors:** `501` if channel listing is not configured.

---

### `POST /api/channels`

Ensure a channel exists for the given directory path. If a channel already maps to the directory on the specified platform, its ID is returned. Otherwise, a new channel is created on the chat platform and stored in the database.

**Request:**
```json
{
  "dir_path": "/home/user/projects/my-project",
  "platform": "discord"
}
```

| Field      | Type   | Required | Description |
|------------|--------|----------|-------------|
| `dir_path` | string | yes      | Absolute path to project directory |
| `platform` | string | no       | Target platform (`discord`, `slack`, `local`) |

**Response (200):**
```json
{"channel_id": "abc123"}
```

**Behavior notes:**
- Channel name is derived from `filepath.Base(dir_path)`, sanitized to lowercase alphanumeric/hyphens/underscores, with a random hex suffix.
- On Discord/Slack platforms, invites the bot owner and sets the channel topic to the directory path.

**Errors:** `400` if `dir_path` is empty. `501` if channel creation is not configured.

---

### `POST /api/channels/create`

Create a new channel with an explicit name.

**Request:**
```json
{
  "name": "my-channel",
  "author_id": "user123",
  "channel_id": "source_channel_for_platform_lookup",
  "platform": "local"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `name`       | string | yes      | Channel display name |
| `author_id`  | string | no       | User to invite to the new channel |
| `channel_id` | string | no       | Source channel for platform/guild inference |
| `platform`   | string | no       | Target platform |

**Response (201):**
```json
{"channel_id": "abc123"}
```

**Errors:** `400` if `name` is empty. `501` if channel creation is not configured.

---

### `POST /api/channels/ensure-all`

Ensure a channel exists for the given directory on all configured platforms.

**Request:**
```json
{"dir_path": "/home/user/projects/my-project"}
```

**Response (200):**
```json
[
  {"platform": "discord", "channel_id": "abc123", "created": false},
  {"platform": "slack", "channel_id": "C0123456", "created": true}
]
```

**Errors:** `400` if `dir_path` is empty. `501` if channel creation is not configured.

---

### `DELETE /api/channels/{id}`

Delete a channel and all its child threads.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel ID to delete |

**Response:** `204 No Content`

**Behavior notes:** Deletes child threads (channels with matching `parent_id`) before deleting the channel itself. Removes the MCP config files of the channel, its threads and their hidden learn and explain threads (`.loop/mcp-<id>.json`, and `.loop/mcp-<id>-learn.json` / `.loop/mcp-<id>-explain.json` for a hidden thread, each in its own dir), unless `keep_mcp_configs` is set for the channel's project. Cancels a learn pass or explanation still running or queued, deletes the hidden threads' forked session files, and removes the channel's containers.

**Errors:** `404` if channel not found. `409` if the channel (or any of its threads) is locked. `501` if channel deletion is not configured.

---

### `PATCH /api/channels/{id}/lock`

Toggle the locked flag on a channel or thread. Locking guards against accidental UI deletes — an unlock is required before the corresponding `DELETE /api/channels/{id}` or `DELETE /api/threads/{id}` will succeed (they return `409 Conflict` otherwise).

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Request:**
```json
{"locked": true}
```

| Field    | Type | Required | Description |
|----------|------|----------|-------------|
| `locked` | bool | yes      | New locked state |

**Response:** `204 No Content`

**Behavior notes:** Broadcasts a `channel.locked` event with the new state so other clients update their sidebar/menus.

**Errors:** `404` if channel not found. `501` if channel locking is not configured.

---

### `POST /api/channels/{id}/rename`

Rename a channel, thread or worktree thread's display name. Only the name changes — the directory path, git branch and Claude sessions are untouched.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Request:**
```json
{"name": "New Name"}
```

| Field  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `name` | string | yes      | New display name |

**Response:** `200 OK`
```json
{"channel_id": "abc123", "name": "New Name"}
```

**Behavior notes:** Broadcasts a `channel.updated` event carrying the new `name` so other clients refresh their sidebar live.

**Errors:** `400` if `name` is empty. `404` if channel not found.

---
### `POST /api/channels/{id}/description`

Set a channel, thread or worktree thread's description, shown in the sidebar's row info popup. An empty description clears it.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Request:**
```json
{"description": "Fixes the login redirect"}
```

| Field         | Type   | Required | Description |
|---------------|--------|----------|-------------|
| `description` | string | no       | The description, trimmed; at most 500 characters. Empty or missing clears it. |

**Response:** `200 OK`
```json
{"channel_id": "abc123", "description": "Fixes the login redirect"}
```

**Behavior notes:** Allowed on locked rows. Broadcasts a `channel.updated` event carrying only the new `description`, so other clients refresh their sidebar live.

**Errors:** `400` if the body isn't valid JSON or the description is longer than 500 characters. `404` if channel not found. `501` if not configured.

---
### `POST /api/channels/{id}/ticket`

Link a channel, thread or worktree thread to its ticket in any tracker (Jira, GitHub, Linear, …), shown in the sidebar's row info popup. An empty `ticket_url` clears it.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Request:**
```json
{"ticket_url": "https://example.atlassian.net/browse/PROJ-123"}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `ticket_url` | string | no       | The ticket's URL, trimmed: absolute, `http` or `https`, with a host, at most 2048 characters. Empty or missing clears it. |

**Response:** `200 OK`
```json
{"channel_id": "abc123", "ticket_url": "https://example.atlassian.net/browse/PROJ-123"}
```

**Behavior notes:** Allowed on locked rows. Broadcasts a `channel.updated` event carrying only the new `ticket_url`, so other clients refresh their sidebar live.

**Errors:** `400` if the body isn't valid JSON, or the URL isn't an absolute http(s) URL or is longer than 2048 characters. `404` if channel not found. `501` if not configured.

---

## Threads

### `POST /api/threads`

Create a new thread under a parent channel. If the channel ID points to a thread, resolves to its parent channel automatically.

**Request:**
```json
{
  "channel_id": "parent_channel_id",
  "name": "Thread title",
  "author_id": "user123",
  "message": "Initial message for the thread"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Parent channel ID |
| `name`       | string | yes      | Thread display name |
| `author_id`  | string | no       | Thread creator's user ID |
| `message`    | string | no       | Initial message content |

**Response (201):**
```json
{"thread_id": "thread_abc123"}
```

**Behavior notes:**
- When an `IncomingMessageHandler` (orchestrator) is configured, the initial message is **not** stored via `CreateThread`. Instead, `HandleThreadCreated` is called asynchronously to store it as a user message and trigger the agent.
- Broadcasts a `channel.created` event to the parent channel via the EventsHub.
- Thread inherits the parent's `dir_path`, `session_id`, `permissions`, `guild_id`, and `platform`.

**Errors:** `400` if `channel_id` or `name` is empty. `501` if thread creation is not configured.

---

### `POST /api/threads/{id}/fork`

Forks a thread: creates a sibling thread that continues the source thread's conversation. The new thread copies the source's Claude session id (marked fork-pending) and imports its history for display; the orchestrator runs the fork's first message with `--fork-session`, so the two threads diverge instead of clobbering each other — the SOURCE thread keeps its session untouched.

For **worktree threads**, the fork additionally creates a new git worktree branched from whatever the source worktree has checked out: its current branch, whatever it's named, or its commit when HEAD is detached. It starts from the committed state; uncommitted changes stay behind. The new thread's `base_branch` is set to that branch (or commit) so its diff shows only the fork's own delta. The source's transcript is copied from the source worktree's own Claude project dir (a worktree thread's transcripts live there, not under the parent channel's) into the new worktree's, and its history is imported from there. When the source has no session, or its transcript is gone, the fork starts with no session rather than the parent channel's.

**Response (201):**
```json
{ "thread_id": "abc123", "worktree_path": "/path/to/.worktrees/wt-1a2b" }
```

`worktree_path` is present only for worktree forks. **Errors:** `400` when the id is not a thread or is a hidden learn thread (`thread not found`); `500` on git/store failures.

---

### `POST /api/channels/{id}/messages/{msgId}/fork`

Forks a channel's or thread's conversation at one of its messages: like [`POST /api/threads/{id}/fork`](#post-apithreadsidfork), but the new thread's session is cut at that message. `msgId` is the message's `msg_id`. The fork of a top-level channel is a new thread under it, and the fork of a thread is a sibling thread.

- **Agent reply:** the reply must record where it sits in its session's transcript; such messages carry `"forkable": true` in the message list, the timeline and `message.created` events. The new thread holds the reply's session with `fork_pending` and the reply's transcript uuid. Its first run resumes with `--fork-session --resume-session-at=<uuid>`, so it keeps the reply and drops everything after it. See [Orchestrator: Fork at a turn](orchestrator.md#fork-at-a-turn).
- **User message:** Claude Code doesn't report a prompt's uuid, so Loop finds it when the message's run ends: it walks the transcript back through `parentUuid` from the run's first reply that records a uuid to the prompt, and records the prompt's uuid on the message. The fork cuts at the entry before the prompt. A message from before Loop recorded prompts is located the same way at fork time, from its first reply that records a uuid. When the prompt started its session, the new thread starts with no session. Either way, the response's `prompt` is the message's text, for the composer to offer again.

The history up to the cut is imported into the new thread for display. Worktree threads also get a new worktree, as in the thread fork.

**Response (201):**
```json
{ "thread_id": "abc123", "prompt": "the user message, for a fork at one" }
```

`worktree_path` is present for worktree forks, and `prompt` only for a fork at a user message.

**Errors:**
- `404`: the channel doesn't exist or is a hidden learn or explain thread, or it has no such message.
- `409`: the message can't be forked at. This covers a reply with no transcript position, a user message with no such reply, and a transcript that is missing or doesn't contain the reply.
- `500`: git or store failures.
- `501`: not configured.

---

### `DELETE /api/threads/{id}`

Delete a thread.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Thread ID to delete |

**Response:** `204 No Content`

**Behavior notes:** Deletes from the chat platform (if a creator is configured) and removes from the database, then cancels the runs of its hidden learn and explain threads, deletes their forked session files and removes the MCP config files of the thread and those threads, unless `keep_mcp_configs` is set for the parent channel's project. If the thread has an associated git worktree, the worktree and its branch are cleaned up automatically.

**Errors:** `409` if the thread is locked (toggle via [`PATCH /api/channels/{id}/lock`](#patch-apichannelsidlock)). `501` if thread deletion is not configured.

---

## Messages

### `POST /api/messages`

Send a message to a channel. When an orchestrator is configured, routes through it asynchronously so Claude can respond.

**Request:**
```json
{
  "channel_id": "abc123",
  "content": "Hello, bot!",
  "mode": "plan",
  "interrupt": false,
  "delay_seconds": 0
}
```

| Field           | Type   | Required | Description |
|-----------------|--------|----------|-------------|
| `channel_id`    | string | yes      | Target channel or thread ID |
| `content`       | string | yes      | Message text |
| `mode`          | string | no       | Agent mode hint (e.g. `"plan"`) |
| `interrupt`     | bool   | no       | When `true`, cancels the active run on the channel and inserts this message with `priority = MaxQueuedPriority(channel_id) + 1` so it claims next ahead of any queued rows. Existing queued messages are preserved (not deleted). Used by the chat UI's "Deny with prompt" gate flow. |
| `delay_seconds` | int    | no       | When `> 0`, holds the message back for that many seconds. The row is inserted with `not_before = now + delay_seconds` (unix seconds); `ClaimNextPending` skips it until then and a background poller drains the channel once it comes due. Takes precedence over `interrupt` (an interrupt would be pointless if the message runs later). Set by the [`queue_message`](mcpserver.md) MCP tool. |

**Response:** `204 No Content`, or with `delay_seconds > 0`, `200 OK` with the queued message's id, so the caller can remove it with [`DELETE /api/messages/{id}`](#delete-apimessagesid) before it runs:

```json
{"msg_id": "ask-3f2a…"}
```

**Behavior notes:**
- When an `IncomingMessageHandler` is set, the message is dispatched asynchronously with a detached context (the HTTP response returns immediately).
- When no handler is set, falls back to direct `PostMessage` via the configured message sender.
- `interrupt=true` requires both a `RunCanceller` and a `Store` on the server; the orchestrator wires both during startup.
- `delay_seconds > 0` routes through `HandleIncomingMessageDelayed` with a `msg_id` picked by the handler, which stamps `not_before` on the inserted row. Because the row is not yet due, the immediate drain claims nothing; the orchestrator's delay poller re-drains the channel once `not_before` passes (this also recovers pending delays across a daemon restart, since the drain is event-driven).

**Errors:** `400` if `channel_id` or `content` is empty. `501` if message sending is not configured and no handler is set.

---

### `DELETE /api/messages/{id}`

Remove a waiting user message from a channel's queue before the orchestrator dispatches it.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | `msg_id` of the message to delete (platform-specific message ID) |

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel owning the message (`msg_id` is unique per channel, not globally) |

**Response:** `204 No Content`

**Behavior notes:**
- Only deletes rows where `is_bot = 0 AND is_processed = 0` — bot replies and already-processed history can never be removed through this endpoint.
- On success, broadcasts a [`message.deleted`](events.md#messagedeleted) WebSocket event so connected clients remove the message from their local state.
- A queued row deleted while another run is in progress simply never gets claimed: `ClaimNextPending` only sees `is_processed=0 AND is_triggered=1 AND is_running=0` rows, so the atomic claim transaction can never hand the deleted row to an agent.

**Errors:** `400` if `channel_id` is missing. `404` if no matching deletable row exists (message missing, already processed, or is a bot message). `500` on database error. `501` if message deletion is not configured.

---

### `GET /api/channels/{id}/sessions`

List Claude Code session JSONL files for a channel's project directory.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel ID  |

**Response:**

```json
{
  "current_session_id": "4482da1c-831c-...",
  "sessions": [
    {
      "session_id": "4482da1c-831c-...",
      "last_modified": "2026-03-25T14:30:00Z",
      "last_message": "I've updated the configuration file..."
    }
  ],
  "imported_session_ids": ["4482da1c-831c-..."]
}
```

Sessions are sorted by modification time (newest first). `last_message` is extracted from the last assistant or user message in the JSONL file (last 32KB reverse-scanned). `current_session_id` is the session currently associated with the channel. `imported_session_ids` lists the session ids already held by a channel or thread.

[Learn passes'](chat.md#learn-from-a-run) sessions aren't importable and are left out: every pass forks a new session file into the channel's project dir. A file is taken for one when its id is a learn thread's own session (not the reviewed run's, which a learn thread holds with `fork_pending` until its pass forks it), or when the first prompt it logs (the first `queue-operation` enqueue in its first 64KB, which Claude writes at the top of a `--print` session, before the history a fork copies in) is a learn pass's trigger message. [Explanations'](chat.md#explain-a-turn) sessions are left out the same way.

---

### `PUT /api/channels/{id}/session`

Switch the channel to another of its project's Claude sessions, so its next run resumes that conversation. The Sessions panel's **resume here** button and the `resume_session` MCP tool call it.

**Request:**

```json
{ "session_id": "4482da1c-831c-..." }
```

- The session must be a transcript in the channel's project dir, as listed by [`GET /api/channels/{id}/sessions`](#get-apichannelsidsessions).
- The chat's messages are left as they are.
- A session another channel or thread also holds is stored with `fork_pending`, so the next run starts with `--fork-session` instead of writing into the other's conversation.
- While a chat run is in progress in the channel, the switch is held (in memory) and applied when the run ends, after the run saves its own session id.

**Response:**

```json
{ "deferred": false }
```

`deferred` is true when the switch waits for the run in progress.

**Errors:** `400` for a missing or malformed `session_id`, or a channel with no project dir. `404` if the channel or the session isn't found. `500` on database error. `501` if session switching is not configured.

---

### `GET /api/channels/{id}/agent-config`

Return the channel's per-channel model/effort overrides plus the effective config defaults they fall back to (global → project → worktree merge for the channel's dir), so a UI can label "default" concretely.

**Response (200):**
```json
{
  "model": "claude-opus-4-8",
  "effort": "high",
  "default_model": "claude-sonnet-5-5",
  "default_effort": "",
  "models": ["claude-opus-5-5", "claude-opus-5-5[1m]", "claude-opus-5", "..."]
}
```

`model` / `effort` are empty when the channel inherits from config. `models` lists the model ids to offer — the same list as the `claude_model` config options; a `[1m]` id runs the model with its 1M-token context window.

**Errors:** `404` if the channel doesn't exist. `501` if the store is not configured.

---

### `PATCH /api/channels/{id}/agent-config`

Set the channel's model/effort overrides. Empty strings clear an override (inherit from config). Takes effect on the channel's **next** agent run — chat and scheduled runs alike — with no restart.

**Request:**
```json
{"model": "claude-opus-4-8", "effort": "high"}
```

`effort` must be one of `low`, `medium`, `high`, `xhigh`, `max`, or empty. `model` is free text (any Claude model id).

**Response:** `204 No Content`.

**Behavior notes:** Broadcasts a [`channel.agent_config`](events.md#channelagent_config) event with the new overrides so the sidebar updates live.

**Errors:** `400` on invalid effort. `404` if the channel doesn't exist. `501` if the store is not configured.

---

### `GET /api/channels/{id}/container-stats`

Live CPU/memory usage for the channel's running containers, digested from the Docker stats endpoint with the docker-cli formulas (CPU% from cpu/system deltas × online CPUs; memory usage minus reclaimable page cache). The desktop app polls this every ~3s to render the readout in the chat and docker-agent pane headers.

**Response (200):**
```json
[
  {
    "container_id": "0a1b2c…",
    "type": "agent",
    "cpu_percent": 12.5,
    "mem_usage": 402653184,
    "mem_limit": 4294967296
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Registry container type: `agent` (chat runs), `shell` (terminal panes), `chrome`, … |
| `cpu_percent` | number | Percent of one CPU; can exceed 100 on multi-core containers |
| `mem_usage` / `mem_limit` | number | Bytes; usage excludes reclaimable page cache |

**Behavior notes:**
- Containers whose stats fetch fails (teardown race) are silently skipped.
- Returns `[]` when the channel has no running containers or the daemon lacks a stats source — never an error.
- The non-streaming Docker stats call takes ~1s server-side (the daemon primes the CPU delta), so responses are not instant.

---

### `GET /api/channels/{id}/audit`

List the agent-gate audit files accumulated for a channel. The files are rotating JSONL (`agentgate-YYYY-MM-DD.jsonl`) written by `FileAuditor` inside the container and kept on the host under `{policyDir}/<channel>/audit/`. See [Security Gate: Known gaps](gates.md#known-gaps) for the record schema and the `verbose` flag.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel ID  |

**Query Parameters:**

| Param    | Type | Default | Max | Description |
|----------|------|---------|-----|-------------|
| `offset` | int  | 0       | --  | Skip the first N files (files are returned newest-first by date) |
| `limit`  | int  | 50      | 500 | Number of files to return |

**Response (200):**
```json
{
  "files": [
    {
      "date": "2026-04-24",
      "size": 12456,
      "last_modified": "2026-04-24T18:02:11Z"
    }
  ],
  "total": 1
}
```

**Behavior notes:**
- `date` is parsed out of the filename and validated against `YYYY-MM-DD`; anything else is skipped.
- Files are sorted newest-first by date for infinite-scroll pagination.
- When the audit directory does not yet exist (container never spawned or gate disabled), the endpoint returns `{"files": []}` with `200` rather than `404`.

**Errors:** `501` if the audit-dir resolver is not configured.

---

### `DELETE /api/channels/{id}/audit/{date}`

Remove one audit file from disk.

**Path Parameters:**

| Param  | Type   | Description |
|--------|--------|-------------|
| `id`   | string | Channel ID |
| `date` | string | `YYYY-MM-DD` |

**Response:** `204 No Content` on success.

**Errors:** `400` if `date` is not `YYYY-MM-DD`. `500` if the unlink fails. `501` if the audit-dir resolver is not configured.

---

### `GET /api/channels/{id}/messages`

List messages for a channel. Supports two modes: **cursor-based pagination** (default) and **around mode**.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Query Parameters (cursor mode):**

| Param    | Type  | Default | Max | Description |
|----------|-------|---------|-----|-------------|
| `cursor` | int64 | 0       | --  | Fetch messages older than this message ID |
| `limit`  | int   | 50      | 200 | Number of messages to return |

**Query Parameters (around mode):**

| Param    | Type  | Description |
|----------|-------|-------------|
| `around` | int64 | Center message ID; returns messages surrounding it |
| `limit`  | int   | Total messages to return (split evenly before/after) |

**Response (200):**
```json
{
  "messages": [
    {
      "id": 42,
      "channel_id": "abc123",
      "msg_id": "discord_msg_id",
      "author_id": "user123",
      "author_name": "Alice",
      "content": "Hello!",
      "is_bot": false,
      "trigger_msg_id": "",
      "created_at": "2026-01-01T00:00:00Z"
    }
  ],
  "next_cursor": 41
}
```

`trigger_msg_id` is the `msg_id` of the user message whose agent run produced this row. Empty (and omitted in JSON via `omitempty`) for user messages and pre-feature bot rows.

`forkable` is `true` on a message that records its place in the session's transcript (an agent reply, a user message whose run has ended, or a message a fork or resume imported), so the conversation can be [forked at it](#post-apichannelsidmessagesmsgidfork). It is omitted otherwise.

**Behavior notes:**
- **Cursor mode:** Fetches `limit+1` messages to determine if more exist. If so, `next_cursor` is set to the last returned message's ID. Messages are ordered oldest-first.
- **Around mode:** Uses a UNION ALL query (half before + half after the target message ID), ordered by `id ASC`. `next_cursor` is not set in around mode.
- This endpoint returns chat-only rows (`kind = 'message'`); agent thinking and tool events are not included. Use [`/api/channels/{id}/timeline`](#get-apichannelsidtimeline) for an interleaved view.

**Errors:** `400` if query params are invalid. `501` if message listing is not configured.

---

### `GET /api/channels/{id}/queued`

Return the canonical queue of unprocessed user messages for a channel — every row with `kind = 'message'`, `is_bot = 0`, `is_processed = 0`, ordered by `priority DESC, id ASC` (the exact order the orchestrator drains in). The chat UI calls this on channel mount and after every event that could change the queue (`message.created`, `message.deleted`, `message.updated`, `messages.processed`, `agent.status`) so the "queued"/"processing" labels and the queued-messages popup stay correct even when older pages of chat history are not loaded in the renderer.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Response (200):**
```json
{
  "messages": [
    {
      "id": 51,
      "channel_id": "abc123",
      "msg_id": "msg-bumped",
      "author_id": "user123",
      "author_name": "Alice",
      "content": "do this first instead",
      "is_bot": false,
      "is_processed": false,
      "priority": 1,
      "created_at": "2026-01-01T00:00:30Z"
    },
    {
      "id": 49,
      "channel_id": "abc123",
      "msg_id": "msg-older",
      "author_id": "user123",
      "author_name": "Alice",
      "content": "do this second",
      "is_bot": false,
      "is_processed": false,
      "created_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

The in-flight message is **included** in the response, marked `"is_running": true` (omitted on the others). Clients use the [`agent.status`](events.md#agentstatus) event's `msg_id` to distinguish "processing" from "queued", and `is_running` until that event arrives. A scheduled task running in the thread claims no row, so every message sent meanwhile stays "queued" until it finishes. Higher `priority` values sort first; `priority` is omitted when zero. Rows queued with a delay carry `not_before` (unix seconds); the chat UI renders a live countdown chip until it elapses. `not_before` is omitted when zero (immediate). A row being edited in some window carries `edit_hold_until` (unix seconds) until the edit is saved, cancelled, or its lease lapses; it and everything behind it won't start meanwhile (see [`POST /api/channels/{id}/queued/{msg_id}/hold`](#post-apichannelsidqueuedmsg_idhold)).

**Errors:** `501` if message listing is not configured. `500` on database error.

---

### `POST /api/channels/{id}/queued/{msg_id}/steer`

Promote one queued message to the front of the channel's queue and cancel the active run, so the agent takes that message next instead of when the current turn ends. This is the per-row form of `POST /api/messages` with `interrupt=true`, and makes the same trade: the cancelled run's session is resumed on the next turn, so the work so far is redirected rather than discarded. Queued rows are preserved.

**Path Parameters:**

| Param    | Type   | Description |
|----------|--------|-------------|
| `id`     | string | Channel or thread ID |
| `msg_id` | string | `msg_id` of the queued row to steer |

**Request Body:** none.

**Response:** `204 No Content`.

Ordering matters and is handled server-side: the row is promoted **before** the run is cancelled, or the dying run's drain loop could claim an older queued row in the window between the two. A row already claimed by a run (`is_running = 1`) is never promoted — the response is `404` and the active run is left alone. A row that was queued with a delay has its `not_before` pulled back to `1`: already in the past, so `ClaimNextPending` accepts it, but still non-zero so the delay poller keeps seeing it and wakes an otherwise idle channel.

**Errors:** `404` if no matching queued row exists (already processed, already running, wrong channel, or never existed). `501` if the store is not configured. `500` on database error.

---

### `POST /api/channels/{id}/queued/{msg_id}/hold`

Hold a queued user message so it can be edited: until the hold expires or is released, the drain won't start it — nor anything queued behind it, so queue order is kept (see [Edit holds](orchestrator.md#edit-holds)). Calling it again renews the hold; the chat app renews every 2 minutes while the edit is open.

**Path Parameters:**

| Param    | Type   | Description |
|----------|--------|-------------|
| `id`     | string | Channel or thread ID |
| `msg_id` | string | `msg_id` of the queued row |

**Request Body:** none.

**Response (200):**
```json
{ "hold_until": 1790000000 }
```

`hold_until` is unix seconds, 5 minutes from now.

**Errors:** `409` if the row is no longer waiting — it already started, finished, was deleted, or never existed (bot rows and non-message rows never match). `501` if the store is not configured. `500` on database error.

---

### `DELETE /api/channels/{id}/queued/{msg_id}/hold`

Release an edit hold without changing the message — the cancel path. When a hold was actually cleared, the channel's drain is kicked so the row (and anything behind it) can start.

**Response:** `204 No Content`, also when there was nothing to release.

**Errors:** `501` if the store is not configured. `500` on database error.

---

### `PUT /api/channels/{id}/queued/{msg_id}`

Replace a queued user message's content and clear its edit hold in one write, then kick the channel's drain. Broadcasts [`message.updated`](events.md#messageupdated) so other windows show the new text.

**Request Body:**
```json
{ "content": "the edited prompt" }
```

**Response:** `204 No Content`.

The write only matches a row that is still waiting (`is_running = 0 AND is_processed = 0`), and it serializes with the orchestrator's claim on SQLite's single writer. If a run claimed the row first — typically after the hold lapsed — the response is `409` and the edit is not applied; the run uses the original text.

**Errors:** `400` on an invalid body or blank `content`. `409` if the row is no longer waiting. `501` if the store is not configured. `500` on database error.

---

### `GET /api/channels/{id}/timeline`

List the channel's interleaved timeline — chat messages plus persisted agent events (thinking blocks, tool calls, tool results) — in canonical chain order. Each row carries a `kind` discriminator and a per-channel `chain_position`.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel or thread ID |

**Query Parameters:**

| Param             | Type  | Default | Max | Description |
|-------------------|-------|---------|-----|-------------|
| `cursor_position` | int64 | 0       | --  | Fetch rows older than this `chain_position`. Pair with `cursor_id` for the second-key tiebreaker. `0` is a valid sentinel that pages into legacy rows. |
| `cursor_id`       | int64 | 0       | --  | Tiebreaker `id` for rows that share `cursor_position` (in particular legacy rows where `chain_position = 0`). |
| `limit`           | int   | 50      | 200 | Number of rows to return |

**Response (200):**
```json
{
  "items": [
    {
      "kind": "message",
      "position": 12,
      "id": 101,
      "data": {
        "id": 101,
        "channel_id": "abc123",
        "msg_id": "user_msg_uuid",
        "author_id": "user123",
        "author_name": "Alice",
        "content": "Refactor the auth middleware",
        "is_bot": false,
        "is_processed": true,
        "created_at": "2026-04-30T08:00:00Z"
      }
    },
    {
      "kind": "thinking",
      "position": 13,
      "id": 102,
      "text": "Let me check how the existing tests cover this path...",
      "truncated": false,
      "trigger_msg_id": "user_msg_uuid"
    },
    {
      "kind": "tool_use",
      "position": 14,
      "id": 103,
      "tool_use_id": "toolu_017fNc...",
      "tool_name": "Read",
      "tool_input": "{\"file_path\":\"/work/internal/api/auth.go\"}",
      "trigger_msg_id": "user_msg_uuid"
    },
    {
      "kind": "tool_result",
      "position": 15,
      "id": 104,
      "tool_use_id": "toolu_017fNc...",
      "text": "package api\n\n// ...\n",
      "is_error": false,
      "truncated": true,
      "trigger_msg_id": "user_msg_uuid"
    }
  ],
  "next_cursor": { "position": 12, "id": 101 }
}
```

| Field                    | Type     | Description |
|--------------------------|----------|-------------|
| `items[].kind`           | string   | One of `"message"`, `"thinking"`, `"tool_use"`, `"tool_result"` |
| `items[].position`       | int64    | Per-channel monotonic chain position. `0` for legacy rows that pre-date the timeline feature. |
| `items[].id`             | int64    | Row id; used as a stable tiebreaker for cursor pagination |
| `items[].data`           | object   | Present when `kind == "message"`; same shape as `/messages` rows (`id`, `channel_id`, `msg_id`, `author_id`, `author_name`, `content`, `is_bot`, `is_processed`, `trigger_msg_id`, `created_at`) |
| `items[].text`           | string   | Present when `kind ∈ {"thinking", "tool_result"}`. Capped at 8 KiB inline; the full content was truncated server-side at the same cap when the run wrote it. |
| `items[].truncated`      | bool     | `true` when the row's content was truncated to fit the inline cap |
| `items[].tool_use_id`    | string   | Pairs `tool_use` rows with their matching `tool_result` row (and matching live `tool.use` / `tool.result` events) |
| `items[].tool_name`      | string   | Present on `tool_use` rows |
| `items[].tool_input`     | string   | Present on `tool_use` rows; serialised tool input (truncated to 8 KiB inline) |
| `items[].is_error`       | bool     | Present on `tool_result` rows; `true` when the tool failed |
| `items[].trigger_msg_id` | string   | The `msg_id` of the user message whose agent run produced this row. Present on bot replies and agent events (`thinking`, `tool_use`, `tool_result`, `compacting`). Empty/omitted on user messages and pre-feature rows. The FE uses it to group events under their triggering message, surviving out-of-order processing of priority-bumped messages. |
| `next_cursor`            | object\|null | `{position, id}` to fetch the next page; `null` when there are no more rows |

**Behavior notes:**
- Rows are ordered by `(chain_position DESC, id DESC)` so the response runs newest → oldest.
- The endpoint fetches `limit + 1` rows to determine whether a next page exists. If so, `next_cursor` is set to the `(chain_position, id)` of the row past the cap.
- **Legacy rows** (chat from before this feature shipped) all carry `chain_position = 0` and are ordered by `id` within that bucket. Cursor pagination handles the boundary between backfilled rows (`chain_position > 0`) and legacy rows transparently.
- Thinking, tool-use, and tool-result content are stored inline on the message row and **truncated at 8 KiB** when the docker stream writes them. The `truncated` flag tells the UI when a block was clipped.
- Live `agent.thinking` / `tool.use` / `tool.result` SSE events fire alongside row inserts, so the desktop app can render them in real time and refetch the head of the timeline on run completion.

**Errors:** `400` if cursor params are negative or non-numeric. `501` if the timeline service is not configured.

---

### `GET /api/messages/search`

Full-text search across all messages using case-insensitive `LIKE %query%`. Messages in hidden learn and explain threads are left out.

**Query Parameters:**

| Param   | Type   | Default | Max | Required | Description |
|---------|--------|---------|-----|----------|-------------|
| `q`     | string | --      | --  | yes      | Search query |
| `limit` | int    | 20      | 50  | no       | Max results |

**Response (200):**
```json
[
  {
    "id": 42,
    "channel_id": "abc123",
    "author_name": "Alice",
    "content": "matching message",
    "is_bot": false,
    "created_at": "2026-01-01T00:00:00Z"
  }
]
```

**Errors:** `400` if `q` is empty. `501` if message search is not configured.

---

### `GET /api/channels/{id}/messages/search`

Finds the channel's messages whose content contains `q`, case-insensitively. `%` and `_` in `q` match literally. Tool calls, tool results and thinking rows are not searched. Backs the chat's find bar.

**Query Parameters:**

| Param   | Type   | Default | Max  | Required | Description |
|---------|--------|---------|------|----------|-------------|
| `q`     | string | --      | --   | yes      | Text to find (trimmed) |
| `limit` | int    | 500     | 1000 | no       | Max results |

**Response (200):** the matching message ids, newest first.
```json
{ "ids": [812, 640, 57] }
```

**Errors:** `400` if `q` is empty or `limit` is invalid. `501` if message search is not configured.

---

## Learn

A channel's learn switch, its hidden learn thread, and the proposals learn passes file. See [Chat: Learn from a run](chat.md#learn-from-a-run). The channel endpoints below return `404` when `{id}` is itself a learn thread.

### `GET /api/channels/{id}/learn`

Return the channel's learn switch, the config default it falls back to (global → project → worktree merge for the channel's dir), and its learn thread.

**Response (200):**
```json
{
  "available": true,
  "learn": "on",
  "default_learn": false,
  "enabled": true,
  "learn_channel_id": "learn-a1b2c3d4e5f6",
  "running": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| `available` | bool | `false` for Slack and Discord channels and scheduled tasks' threads, which never learn |
| `learn` | string | The channel's setting: `"on"`, `"off"`, or empty to inherit `default_learn` |
| `default_learn` | bool | `learn.enabled` from the merged config; `false` when the config fails to load |
| `enabled` | bool | The effective switch; always `false` when not `available` |
| `learn_channel_id` | string | The hidden learn thread; empty until the channel's first learn pass |
| `running` | bool | A learn pass is running in the learn thread. A pass still waiting for its turn, or your own reply running there, doesn't count. |

**Errors:** `404` if the channel doesn't exist or is a learn thread. `500` on a store error. `501` if the store is not configured.

---

### `PUT /api/channels/{id}/learn`

Set the channel's learn switch. It takes effect from the channel's next run.

**Request:**
```json
{"learn": "off"}
```

`learn` must be `"on"`, `"off"`, or empty to inherit the config.

**Response:** `204 No Content`.

**Behavior notes:** Broadcasts a [`channel.learn`](events.md#channellearn) event.

**Errors:** `400` if the body isn't valid JSON, `learn` is another value, or the channel is a Slack or Discord one (`learn runs only in desktop app channels`) or a scheduled task's thread (`learn doesn't run in task threads`). `404` if the channel doesn't exist or is a learn thread. `501` if the store is not configured.

---

### `GET /api/channels/{id}/learn/proposals`

List the proposals filed for the channel, newest first, in every status.

**Response (200):**
```json
{
  "proposals": [
    {
      "id": 12,
      "channel_id": "abc123",
      "learn_channel_id": "learn-a1b2c3d4e5f6",
      "message_id": "loop-msg-17",
      "kind": "bash_shortcut",
      "title": "Add a make lint bash shortcut",
      "rationale": "make lint was typed three times in this run.",
      "payload": "{\"name\":\"lint\",\"description\":\"Run the linter\",\"command\":\"make lint\"}",
      "status": "pending",
      "created_at": "2026-03-25T14:30:00Z",
      "updated_at": "2026-03-25T14:30:00Z"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `message_id` | string | The turn (its last bot message's `msg_id`) whose learn pass filed it; omitted when unknown |
| `kind` | string | `prompt_shortcut`, `bash_shortcut`, `scheduled_task`, `gate_rule`, `mount`, `rename`, `description` or `ticket_url` |
| `payload` | string | The kind's payload as a JSON string (see [`POST`](#post-apichannelsidlearnproposals)) |
| `status` | string | `pending`, `applying`, `applied`, `dismissed`, `failed` or `withdrawn` |
| `error` | string | Why the last apply failed; omitted when empty |
| `withdrawn_reason` | string | Why a later learn pass withdrew it; set only when `status` is `withdrawn` |

`proposals` is `[]` when there are none.

**Errors:** `404` if the channel doesn't exist or is a learn thread. `500` on a store error. `501` if the store is not configured.

---

### `POST /api/channels/{id}/learn/proposals`

File a learn pass's proposals and withdraw earlier ones it found stale. This is what the learn agent's [`propose_learnings`](mcpserver.md#learn-tools-learn-agent-only) MCP tool calls. Here `{id}` is the **learn thread**; the proposals are stored for the channel it learns from, as `pending`.

**Request:**
```json
{
  "proposals": [
    {
      "kind": "gate_rule",
      "title": "Ask before git push",
      "rationale": "The agent pushed without asking.",
      "payload": {"type": "command", "rule": {"commands": ["git"], "args_patterns": ["^push( .*)?$"], "decision": "approve", "message": "git push"}},
      "replaces": 9
    }
  ],
  "withdraw": [
    {"id": 7, "reason": "The run removed the lint target this shortcut calls."}
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `proposals` | array | New proposals, at most 5 |
| `proposals[].replaces` | int | Optional id of an earlier proposal this one supersedes; it's withdrawn with the reason `replaced by a newer proposal` |
| `withdraw` | array | Earlier proposals to withdraw, at most 20, each `{id, reason}`; `reason` is required, at most 300 characters, trimmed |

At least one of `proposals` and `withdraw` must be non-empty: a call may only withdraw. Only the channel's own `pending` or `failed` proposals can be withdrawn, never one `applying`, `applied`, `dismissed` or already `withdrawn`. A withdrawn proposal keeps its row, with `status: "withdrawn"` and the reason in `withdrawn_reason`; a `failed` one's `error` is cleared.

At most 5 proposals per call. Each needs a `kind`, a `title` (at most 200 characters) and a `payload` object; `rationale` is optional (at most 1000 characters). Title and rationale are trimmed. Payloads by kind (unknown fields are rejected):

| Kind | Payload |
|------|---------|
| `prompt_shortcut` | `{"name", "description"?, "prompt"}`. `name` at most 100 characters. |
| `bash_shortcut` | `{"name", "description"?, "command"}`. `name` at most 100 characters. |
| `scheduled_task` | `{"type", "schedule", "prompt"?, "bash_script"?, "auto_delete_sec"?}`. `type` is `cron`, `interval` or `once`; exactly one of `prompt` / `bash_script`; `auto_delete_sec` not negative. |
| `gate_rule` | `{"type", "rule"}`. `type` is `path`, `command` or `file`; `rule` is an agentgate rule of that list, and must compile and name what it matches: a command rule needs `commands` or `args_patterns`, a file rule `paths`, a path rule `pattern`. A rule without one would match every call and, as project rules match first, override the global rules. |
| `mount` | `{"mount"}`, `host_path:container_path[:ro\|rw]`. |
| `rename` | `{"name"}`, at most 100 characters. |
| `description` | `{"description"}`, at most 500 characters. |
| `ticket_url` | `{"ticket_url"}`, an absolute http(s) URL, at most 2048 characters. |

The payload is stored in canonical JSON. New proposals get the `message_id` of the learn thread's running pass, else its newest done one (a user's reply in the learn thread files against the turn that pass reviewed); none when it has neither. The withdrawals and the inserts run in one transaction: either every proposal is valid and every withdrawal allowed, and all are stored, or nothing changes. A withdrawal takes the proposal the same way apply claims it (a conditional update on its status), so one the user is applying or dismissing at that moment can't be withdrawn, and one withdrawn first can't be applied.

**Response (201):** `{"proposals": [...], "withdrawn": [...]}`, the stored proposals and the withdrawn ones as in the `GET` above. `proposals` is `[]` for a withdraw-only call; `withdrawn` is omitted when none were.

**Behavior notes:** Broadcasts a [`learn.proposals`](events.md#learnproposals) event for the learning channel.

**Errors:** `400` if the body isn't valid JSON, both `proposals` and `withdraw` are empty (`proposals or withdraw is required`), `proposals` has more than 5 items or `withdraw` more than 20, a proposal is invalid (the message names it, e.g. `proposal 2: title is required`), a withdrawal has no reason (`withdraw 1: reason is required`), or a proposal is withdrawn twice in the call (`withdraw 2: proposal 7 is withdrawn twice`, also when a `replaces` repeats one). `404` if `{id}` isn't a learn thread. `409` if a proposal to withdraw isn't the channel's (`proposal 7 not found in this channel`) or isn't open (`proposal 7 is applied; only pending or failed proposals can be withdrawn`). `500` on a store error. `501` if the store is not configured.

---

### `GET /api/channels/{id}/learn/passes`

List the channel's learn passes, newest first, in every status. Each reviews one turn, the one ending with bot message `message_id`.

**Response (200):**
```json
{
  "passes": [
    {
      "id": 5,
      "channel_id": "abc123",
      "message_id": "loop-msg-17",
      "learn_channel_id": "learn-a1b2c3d4e5f6",
      "status": "done",
      "created_at": "2026-03-25T14:30:00Z",
      "updated_at": "2026-03-25T14:31:10Z",
      "message_row_id": 412
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `message_id` | string | The reviewed turn's last bot message (`msg_id`) |
| `learn_channel_id` | string | The channel's hidden learn thread, where the pass runs |
| `status` | string | `queued`, `running`, `done` or `failed`. Passes from before passes queued one after another may also be `superseded`: a newer pass replaced it before it started. |
| `error` | string | Why the pass failed; omitted when empty |
| `message_row_id` | int | The bot message's row id, for linking to it; omitted when the message is gone |

`passes` is `[]` when there are none. A pass is recorded only when its turn has a bot reply. Passes left `queued` or `running` when Loop stopped are marked `failed` at the next start, unless their trigger still waits to run.

**Behavior notes:** Each status change is broadcast as a [`learn.pass`](events.md#learnpass) event.

**Errors:** `404` if the channel doesn't exist or is a learn thread. `500` on a store error. `501` if the store is not configured.

---

### `POST /api/channels/{id}/learn/passes`

Start a learn pass over the turn that ended with bot message `message_id`, on demand. It runs whether or not the channel's Learn switch is on, and whatever the turn's length (`learn.min_turns`). A pass over the turn already `queued` or `running` is returned as it is; otherwise a new one is queued, also when the turn's earlier pass is `done` or `failed` (or `superseded`, on an old row).

**Request:**
```json
{"message_id": "loop-msg-17"}
```

**Response (200):** the pass, shaped as in the list (without `message_row_id`).

**Behavior notes:** A new pass creates the channel's hidden learn thread on first use and is broadcast as a [`learn.pass`](events.md#learnpass) event, as is each later status change. It forks the session where the turn ended, so it sees the turn as it was even when later ones followed (a turn from before Loop recorded where turns end forks the channel's current session instead), and its trigger message quotes the turn's prompt and final reply. Like an automatic pass, it queues behind the passes already in the learn thread and runs after them; nothing is replaced.

**Errors:** `400` if the body isn't valid JSON, `message_id` is missing, the channel isn't one learn passes run for (a Slack or Discord channel, or a task thread), or the message isn't a bot message of a turn in the channel. `404` if the channel doesn't exist, is hidden, or was deleted meanwhile. `409` if neither the turn nor the channel has a session to fork. `500` on a store error. `501` if on-demand learn passes are not configured.

---

### `POST /api/learn/proposals/{id}/apply`

Apply a `pending` or `failed` proposal. It moves to `applying` first, so a double click can't apply it twice. One left in `applying` for over a minute (its outcome was lost: the status save failed, or Loop stopped mid-apply) can be applied or dismissed again. See [Configuration: Where learn proposals are written](configuration.md#where-learn-proposals-are-written) for what each kind changes.

**Response (200):** the proposal, with `status` `applied`, or `failed` and an `error`. A failed apply is recorded on the proposal, not returned as an HTTP error, so it can be retried.

**Behavior notes:** Broadcasts a [`learn.proposal_updated`](events.md#learnproposal_updated) event. A rename, description or ticket URL also broadcasts `channel.updated`, a scheduled task `task.created`. Config-kind applies to the same `.loop/config.json` (e.g. several proposals applied at once) are serialized, from the duplicate check to the write, so none is lost or added twice.

**Errors:** `400` if `{id}` isn't an integer. `404` if the proposal doesn't exist. `409` if it's already `applied`, `dismissed` or `withdrawn` (a learn pass may withdraw it while the request is in flight), or has been `applying` for under a minute (`proposal is already applied`). `500` on a store error. `501` if the store is not configured.

---

### `GET /api/learn/proposals/{id}/preview`

What applying a proposal would change in the project config, without changing it. The server works out the same edit apply makes, with the same duplicate checks, against the file as it is now. Owner-only.

**Response (200):**
```json
{
  "path": "/home/user/project/.loop/config.json",
  "diff": "--- /home/user/project/.loop/config.json\n+++ /home/user/project/.loop/config.json\n@@ -1,3 +1,6 @@\n {\n-  \"envs\": {}\n+  \"envs\": {},\n+  \"mounts\": [\n+    \"~/.aws:~/.aws:ro\"\n+  ]\n }\n"
}
```

| Field | Description |
|---|---|
| `path` | The `.loop/config.json` the proposal edits (see [Where learn proposals are written](configuration.md#where-learn-proposals-are-written)) |
| `diff` | The edit as a unified diff, from `/dev/null` when the file would be created. Omitted when the change is already there (a gate rule the config has) |
| `error` | Why applying it would fail (a shortcut or mount that already exists, a config that doesn't parse), as apply would record it |

All fields are omitted for kinds that edit no file (`scheduled_task`, `rename`, `description`, `ticket_url`).

**Errors:** `400` if `{id}` isn't an integer. `404` if the proposal doesn't exist. `500` on a store error. `501` if the store is not configured.

---

### `POST /api/learn/proposals/{id}/dismiss`

Dismiss a `pending` or `failed` proposal, or one stuck in `applying` as above.

**Response (200):** the proposal, with `status: "dismissed"`.

**Behavior notes:** Broadcasts a [`learn.proposal_updated`](events.md#learnproposal_updated) event.

**Errors:** Same as apply.

---

## Explain

A channel's explain switch and the explanations of its turns. See [Chat: Explain a turn](chat.md#explain-a-turn). These endpoints return `404` when `{id}` is a hidden learn or explain thread, and `501` if the store is not configured.

### `GET /api/channels/{id}/explain`

Return the channel's explain switch and the config default it falls back to (global → project → worktree merge for the channel's dir).

**Response (200):**
```json
{"available": true, "explain": "", "default_explain": false, "enabled": false}
```

| Field | Type | Description |
|-------|------|-------------|
| `available` | bool | `false` for Slack and Discord channels and scheduled tasks' threads, which are never explained |
| `explain` | string | The channel's setting: `"on"`, `"off"`, or empty to inherit `default_explain` |
| `default_explain` | bool | `explain.enabled` from the merged config; `false` when the config fails to load |
| `enabled` | bool | The effective switch; always `false` when not `available` |

**Errors:** `404` if the channel doesn't exist or is hidden. `500` on a store error.

---

### `PUT /api/channels/{id}/explain`

Set the channel's explain switch: with it on, every turn that completes is explained. It takes effect from the channel's next run.

**Request:**
```json
{"explain": "on"}
```

`explain` must be `"on"`, `"off"`, or empty to inherit the config.

**Response:** `204 No Content`.

**Behavior notes:** Broadcasts a [`channel.explain`](events.md#channelexplain) event.

**Errors:** `400` if the body isn't valid JSON, `explain` is another value, or the channel is never explained (`explain runs only in desktop app channels`, `explain doesn't run in task threads`). `404` if the channel doesn't exist or is hidden. `500` on a store error.

---

### `GET /api/channels/{id}/explanations`

List the channel's explanations, newest first, in every status.

**Response (200):**
```json
[
  {
    "id": 3,
    "channel_id": "abc123",
    "message_id": "loop-msg-17",
    "explain_channel_id": "explain-a1b2c3d4e5f6",
    "status": "done",
    "content": "### Summary\n\nAdded a lint target...",
    "created_at": "2026-03-25T14:30:00Z",
    "updated_at": "2026-03-25T14:31:10Z",
    "message_row_id": 412,
    "prompt": "add a lint target",
    "reply": "Added `make lint`..."
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `message_id` | string | The explained turn's last bot message (`msg_id`) |
| `explain_channel_id` | string | The channel's hidden explain thread, where the run goes |
| `status` | string | `queued`, `running`, `done` or `failed` |
| `content` | string | The explanation in markdown; empty until `done` |
| `error` | string | Why the run failed; omitted when empty |
| `message_row_id` | int | The bot message's row id, for linking to it; omitted when the message is gone |
| `prompt` / `reply` | string | The first 300 characters of the turn's prompt and final reply; omitted when gone |

The list is `[]` when there are none.

**Errors:** `404` if the channel doesn't exist or is hidden. `500` on a store error.

---

### `POST /api/channels/{id}/explanations`

Explain the turn that ended with bot message `message_id`. Without `force`, an existing explanation is returned as it is; with `force` (Re-explain) a done or failed one is queued again, replacing its content. An explanation already queued or running is returned as it is either way, unless it has been so for over an hour (its run was lost).

**Request:**
```json
{"message_id": "loop-msg-17", "force": false}
```

**Response (200):** the explanation, shaped as in the list.

**Behavior notes:** A newly queued explanation creates the channel's hidden explain thread on first use, queues a run there that forks the channel's current session, and broadcasts an [`explain.updated`](events.md#explainupdated) event, as does each later status change.

**Errors:** `400` if the body isn't valid JSON, `message_id` is missing, the channel is never explained, or the message isn't a bot message of a turn in the channel. `404` if the channel doesn't exist, is hidden, or was deleted meanwhile. `409` if the channel has no session to fork yet. `500` on a store error. `501` if explanations are not configured.

---

## Tasks

### `POST /api/tasks`

Create a new scheduled task.

**Request:**
```json
{
  "channel_id": "abc123",
  "schedule": "0 9 * * *",
  "type": "cron",
  "prompt": "Summarize today's PRs",
  "template_name": "daily-summary",
  "auto_delete_sec": 3600,
  "worktree": true,
  "origin_branch": "main",
  "update_before_run": true
}
```

| Field              | Type   | Required | Description |
|--------------------|--------|----------|-------------|
| `channel_id`       | string | yes      | Channel to run the task in |
| `schedule`         | string | yes      | Cron expression, Go duration, or RFC3339 timestamp |
| `type`             | string | yes      | `cron`, `interval`, or `once` |
| `prompt`           | string | no       | Prompt text for the agent (required unless `workflow_name` is set) |
| `template_name`    | string | no       | Template identifier for deduplication |
| `auto_delete_sec`  | int    | no       | Auto-delete thread after N seconds |
| `worktree`         | bool   | no       | Run the agent in an isolated git worktree |
| `origin_branch`    | string | no       | Base branch for worktree tasks. Auto-detected on first run if omitted. |
| `update_before_run`| bool   | no       | Prepend git fetch/rebase instructions to the prompt before each run |
| `workflow_name`    | string | no       | Name of a workflow to run on schedule (mutually exclusive with `prompt`) |
| `workflow_inputs`  | string | no       | JSON object of inputs to pass to the workflow |
| `bash_script`      | string | no       | Shell script to run in the channel's agent container on schedule (mutually exclusive with `prompt` and `workflow_name`); output is posted to the channel |

**Response (201):**
```json
{"id": 1}
```

---

### `GET /api/tasks`

List tasks for a channel.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID |

**Response (200):**
```json
[
  {
    "id": 1,
    "channel_id": "abc123",
    "schedule": "0 9 * * *",
    "type": "cron",
    "prompt": "Summarize today's PRs",
    "enabled": true,
    "next_run_at": "2026-01-02T09:00:00Z",
    "template_name": "daily-summary",
    "auto_delete_sec": 3600,
    "worktree": true,
    "origin_branch": "main",
    "update_before_run": true,
    "running": false,
    "thread_id": "thread_abc123",
    "workflow_name": "",
    "workflow_inputs": ""
  }
]
```

The `running` field indicates whether the task is currently being executed. It is set atomically when execution begins and cleared when it finishes. When `workflow_name` is set, the task triggers a workflow run instead of an agent prompt.

**Errors:** `400` if `channel_id` is missing.

---

### `GET /api/tasks/{id}`

Get a single task by ID.

**Path Parameters:**

| Param | Type  | Description |
|-------|-------|-------------|
| `id`  | int64 | Task ID |

**Response (200):** Single task object (same schema as list items).

**Errors:** `400` if ID is invalid. `404` if task not found.

---

### `DELETE /api/tasks/{id}`

Delete a scheduled task.

**Response:** `204 No Content`

**Errors:** `400` if ID is invalid.

---

### `PATCH /api/tasks/{id}`

Update one or more fields of a scheduled task. At least one field must be provided.

**Request:**
```json
{
  "enabled": false,
  "schedule": "0 10 * * *",
  "type": "cron",
  "prompt": "Updated prompt",
  "auto_delete_sec": 7200,
  "worktree": true,
  "origin_branch": "develop",
  "update_before_run": true,
  "workflow_name": "validate",
  "workflow_inputs": "{}",
  "bash_script": "df -h | tail -1"
}
```

All fields are optional (use JSON `null` or omit). When `enabled` is provided, it is applied separately via `SetTaskEnabled`. Other fields are applied via `EditTask`. Set `workflow_name` to convert a prompt task into a workflow task, or `bash_script` to convert it into a [bash task](scheduling.md#scheduled-bash-scripts) (clear either with an empty string to revert).

**Response:** `200 OK` (empty body)

**Errors:** `400` if no fields provided or ID is invalid.

---

### `POST /api/tasks/{id}/run`

Trigger an immediate execution of a task ("Run Now"). The task runs asynchronously in the background; the endpoint returns immediately.

**Path Parameters:**

| Param | Type  | Description |
|-------|-------|-------------|
| `id`  | int64 | Task ID |

**Response:** `202 Accepted` (empty body)

**Errors:** `400` if ID is invalid. `404` if task not found. `409 Conflict` if the task is already running.

---

### `POST /api/tasks/{id}/move`

Move a task under another channel, thread or worktree thread of the same project, for example from a worktree thread to the root channel or back. The target must be under the same root channel as the task's current owner, since its worktree, origin branch, templates and workflows belong to that project's repo. Only the task's owner changes: its schedule, prompt, `worktree`, `origin_branch` and other settings stay as they are. A thread deeper than one level resolves to its depth-1 parent, as on create.

On the local platform the task's thread moves along with it. A thread with its own worktree (a `worktree: true` task) keeps its directory. A thread that shares its parent's directory switches to the new parent's, and its session transcript is copied there so the next run resumes it. Other platforms own their threads, so the task starts a fresh thread on its next run.

**Path Parameters:**

| Param | Type  | Description |
|-------|-------|-------------|
| `id`  | int64 | Task ID |

**Request Body:**

```json
{"channel_id": "wt-thread-1"}
```

**Response:** `200 OK` (empty body). Moving a task to the channel it's already under is a no-op.

**Errors:** `400` if the ID or body is invalid, `channel_id` is missing, the target is the task's own thread, or the target is in another project. `404` if the task, the target channel or the task's current channel isn't found. `409 Conflict` if the task is running.

---

### `GET /api/tasks/{id}/runs`

List recent run logs for a task (up to 50, newest first).

**Path Parameters:**

| Param | Type  | Description |
|-------|-------|-------------|
| `id`  | int64 | Task ID |

**Response (200):**
```json
[
  {
    "id": 10,
    "task_id": 1,
    "status": "success",
    "response_text": "Completed successfully",
    "error_text": "",
    "started_at": "2026-01-02T09:00:00Z",
    "finished_at": "2026-01-02T09:01:30Z"
  }
]
```

The `status` field is one of `"running"`, `"success"`, or `"failed"`.

**Errors:** `400` if ID is invalid.

---

## Commands

### `POST /api/commands`

Execute a slash command. The command is parsed and dispatched asynchronously through the interaction handler.

**Request:**
```json
{
  "channel_id": "abc123",
  "author_id": "user123",
  "command": "schedule type=cron schedule='0 9 * * *' prompt='Daily standup'"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel context |
| `author_id`  | string | no       | Defaults to `"local-user"` |
| `command`    | string | yes      | Command string |

**Supported commands:**

| Command          | Arguments | Description |
|------------------|-----------|-------------|
| `tasks`          | --        | List tasks |
| `status`         | --        | Show status |
| `readme`         | --        | Show README |
| `template-list`  | --        | List templates |
| `iamtheowner`    | --        | Claim ownership |
| `task`           | `task_id` | Show task |
| `cancel`         | `task_id` | Cancel task |
| `toggle`         | `task_id` | Toggle task |
| `stop`           | `[channel_id]` | Stop agent |
| `template-add`   | `name`    | Add template |
| `schedule`       | `type=... schedule=... prompt=...` | Schedule task |
| `edit`           | `task_id [key=value ...]` | Edit task |
| `allow_user`     | `target_id [role]` | Grant access |
| `deny_user`      | `target_id` | Revoke access |

**Response:** `204 No Content`

**Behavior notes:**
- The command string supports quoted arguments (single or double quotes).
- Key-value pairs use `key=value` syntax.
- Unknown commands return `400` with `"unknown command"`.

**Errors:** `400` if `channel_id` or `command` is empty, or command is unknown. `503` if commands not configured.

---

## Extra Directories

Extra directories are configured in the project config (`.loop/config.json`) via the `extra_dirs` field. They are automatically loaded when listing roots or resolving file paths.

### `GET /api/channels/{id}/roots`

List all root directories for a channel: the primary `dir_path` followed by any extra directories from the project config.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Channel ID  |

**Response (200):**
```json
{
  "roots": [
    "/home/user/projects/my-project",
    "/home/user/projects/shared-lib",
    "/home/user/projects/proto"
  ]
}
```

The first entry is always the primary `dir_path`. Subsequent entries are the extra directories in the order they were set.

**Errors:** `404` if channel not found.

---

## Files

All file endpoints resolve the channel's `dir_path` from the database, falling back to `~/.loop/{channel_id}/work` when the channel has no explicit path. When a channel has extra directories, file endpoints accept an optional `root` query parameter to select which root directory to operate on.

### `GET /api/channels/{id}/files`

List directory contents for a channel's working directory.

**Query Parameters:**

| Param  | Type   | Default | Description |
|--------|--------|---------|-------------|
| `path` | string | `"."`   | Relative path within the channel's directory |
| `root` | int    | `0`     | Root directory index (0 = primary `dir_path`, 1+ = extra directories) |

**Response (200):**
```json
{
  "entries": [
    {"name": "src", "type": "dir"},
    {"name": "main.go", "type": "file", "size": 1234}
  ]
}
```

**Behavior notes:** Entries are sorted with directories first, then alphabetically by name (case-insensitive).

**Errors:** `400` if path validation fails (absolute path, `..` traversal, symlink escape).

---

### `GET /api/channels/{id}/file`

Read a file's contents.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the file |
| `root` | int    | no       | Root directory index (0 = primary, 1+ = extra directories) |
| `ref`  | string | no       | A commit hash (4–64 hex digits). Reads the file as it was in that commit (`git show <ref>:./<path>`) instead of from disk. |

**Response (200):**
- **Text files:** `Content-Type: text/plain; charset=utf-8` with file contents as body.
- **Image files** (`.png`, `.jpg`/`.jpeg`, `.gif`, `.webp`): `Content-Type` set to the matching `image/*` MIME with the raw bytes as the body. No `X-File-Binary` header — the desktop app uses this endpoint directly as `<img src>` so the browser handles caching and decoding.
- **Video and PDF files** (`.mp4`, `.webm`, `.mov`, `.pdf`): streamed from disk with `Content-Type` set to the matching `video/*` or `application/pdf` MIME and `Accept-Ranges: bytes`, so `Range` requests return `206 Partial Content`. The desktop app plays videos with `<video src>` and renders PDFs with its pdf.js viewer.
- **Other binary files:** Empty body with `X-File-Binary: true` header and `Content-Length` set.

**Behavior notes:**
- The image, video and PDF branches are matched on extension and run before null-byte binary detection.
- Binary detection checks the first 512 bytes for null bytes.
- Maximum file size is **5 MB** (5,242,880 bytes). Larger files return `413`. Videos and PDFs are exempt: they're streamed, never buffered whole.
- Path validation rejects absolute paths, `..` traversal, and symlink escapes.
- With `ref`, the file only has to exist in that commit — it may since have been deleted — so only the lexical checks apply; the response is text or `X-File-Binary`, never the image/video/PDF branches. The path is resolved relative to the channel's directory, as on disk.

**Errors:** `400` if path or `ref` is invalid. `404` if file not found (at `ref`, when given). `413` if file too large.

---

### `PUT /api/channels/{id}/file`

Write content to a file.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the file |
| `root` | int    | no       | Root directory index (0 = primary, 1+ = extra directories) |

**Request body:** Raw file content (not JSON). Maximum **5 MB**.

**Response (200):**
```json
{"ok": true}
```

**Behavior notes:**
- Preserves original file permissions if the file already exists; defaults to `0644` for new files.
- A symlink whose target resolves inside the root is followed: the write lands on the target and the link stays. A link that resolves outside the root, or points at nothing, is refused rather than written through.

**Errors:** `400` if path is invalid, a symlink leaves the root, or a symlink is dangling. `413` if content exceeds 5 MB.

---

### `DELETE /api/channels/{id}/file`

Delete a file or directory.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the file or directory |
| `root` | int    | no       | Root directory index (0 = primary, 1+ = extra directories) |

**Response (200):**
```json
{"ok": true}
```

**Behavior notes:** If the target is a directory, it is removed recursively (`RemoveAll`) with path traversal protection. If the target is a file, it is removed with `Remove`.

**Errors:** `400` if path is invalid. `404` if not found.

---

### `GET /api/channels/{id}/raw/{root}/{path...}`

Serve a file's raw bytes by path. The editor's HTML preview uses this as the `<base href>` of the rendered page, so relative stylesheets, scripts and images resolve against the file's directory. The root index is a path segment rather than a query parameter because relative URL resolution drops the query string.

**Path Parameters:**

| Param  | Type   | Description |
|--------|--------|-------------|
| `root` | int    | Root directory index (0 = primary, 1+ = extra directories) |
| `path` | string | Relative path to the file |

**Response (200):** The file's bytes with `Content-Type` from its extension (`application/octet-stream` when unknown). `Range` requests return `206 Partial Content`.

**Behavior notes:**
- Responses carry `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox allow-scripts`, so a page opened directly from this route runs in an opaque origin and can't call the API with the app's origin.
- Path validation matches the other file routes: absolute paths, `..` traversal and symlink escapes are rejected.

**Errors:** `400` if the root index or path is invalid, or the path is a directory. `404` if file not found.

---

### `POST /api/channels/{id}/dir`

Create a directory (including nested intermediate directories).

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the directory to create |
| `root` | int    | no       | Root directory index (0 = primary, 1+ = extra directories) |

**Response (200):**
```json
{"ok": true}
```

**Behavior notes:** Uses `MkdirAll` to create the directory and any missing intermediate parents. Path validation walks up to the first existing ancestor to verify the path stays under the root directory.

**Errors:** `400` if path is invalid.

---

### `POST /api/channels/{id}/files/exists`

Batched existence check for path candidates discovered in chat text or tool input. Used by the desktop app's clickable file-link UX (see [Chat: File Links](chat.md#file-links)).

**Request body:**
```json
{"paths": ["app/src/main.tsx", "/Users/me/dev/loop/README.md", "missing/file.go"]}
```

**Response (200):**
```json
{
  "results": [
    {"path": "app/src/main.tsx",                      "exists": true,  "root_index": 0, "rel_path": "app/src/main.tsx"},
    {"path": "/Users/me/dev/loop/README.md",          "exists": true,  "root_index": 0, "rel_path": "README.md"},
    {"path": "missing/file.go",                       "exists": false}
  ]
}
```

**Behavior notes:**
- Each candidate is resolved against the channel's primary `dir_path` and any `extra_dirs` from project config, in order. The first root that contains the path wins; the `root_index` in the response refers to that root (compatible with the `root` query parameter on other file endpoints).
- Relative paths are tried under each root via the same path-validation rules as the read/write endpoints (no absolute, no `..` traversal, symlink-aware containment check).
- Absolute paths are stat'd directly, then matched against each root's resolved prefix to derive `rel_path`. Paths outside every root return `exists: false`.
- Directories return `exists: false` — only regular files are reported as existing, since the link UX opens files in the editor.
- The batch is capped at **200 paths per request**; extras are silently dropped. Request body is limited to 64 KiB.

**Errors:** `400` if the request body is malformed or the channel's directory cannot be resolved.

---

### `GET /api/channels/{id}/files/search`

Fuzzy search for files across the channel's primary `dir_path` and any `extra_dirs`. Used by the chat composer's `@` file picker.

**Query Parameters:**

| Param   | Type   | Description |
|---------|--------|-------------|
| `q`     | string | Fuzzy query — every rune must appear in the relative path in order, case-insensitively. Empty `q` returns the first N entries. |
| `limit` | int    | Max results to return. Default `30`, max `100`. |

**Response (200):**
```json
{
  "results": [
    {"root_index": 0, "rel_path": "app/src/main.tsx",    "name": "main.tsx"},
    {"root_index": 0, "rel_path": "internal/api/server.go", "name": "server.go"}
  ]
}
```

**Behavior notes:**
- Walks all configured roots (primary `dir_path` first, then `extra_dirs`). `root_index` indicates which root the match came from (compatible with the `root` query parameter on file read/write endpoints).
- Always skips `.git`, `node_modules`, `vendor`, `.next`, `dist`, `build`, `__pycache__` subtrees.
- Honors the top-level `.gitignore` in each root (basename and full-relpath patterns; negation patterns and nested `.gitignore` files are ignored).
- Walk stops once `limit` matches have accumulated across all roots.

**Errors:** `400` if the channel's directory cannot be resolved.

---

### `POST /api/channels/{id}/paste-image`

Persist an image pasted into the chat input. The desktop app calls this from `ChatInput`'s `onPaste` handler whenever the clipboard contains an image file (see [Chat: Paste Images](chat.md#paste-images)).

**Request body:**
```json
{
  "data": "<base64-encoded image bytes>",
  "media_type": "image/png"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `data`       | string | yes      | Standard base64 (`+/=` alphabet) of the raw image bytes. |
| `media_type` | string | yes      | One of `image/png`, `image/jpeg`, `image/gif`, `image/webp`. |

**Response (200):**
```json
{"path": "/Users/me/dev/project/.loop/pastes/paste-20260518-112528-66a7b575.png"}
```

The returned `path` is **absolute**, so the agent's built-in `Read` tool (which requires absolute paths) can pick the file up directly. The renderer inserts the path at the caret position in the chat input.

**Behavior notes:**
- The file is written under `<workspace>/.loop/pastes/`; the directory is created with `MkdirAll` on first paste. `.loop/` should be gitignored.
- Filename format: `paste-<YYYYMMDD>-<HHMMSS>-<8-hex>.<ext>` (UTC timestamp, 4-byte random suffix). Collisions within the same second are avoided by the random suffix.
- Extension is derived from `media_type` — `.jpg` for `image/jpeg`, `.png`/`.gif`/`.webp` otherwise.
- Request body is limited to **5 MB** of raw image bytes after base64 decoding. Larger payloads return `413`.

**Errors:** `400` for malformed JSON, missing `data`, unsupported `media_type`, or invalid base64. `404` if the channel does not exist. `413` if the image exceeds 5 MB. `501` if file operations are not configured. `500` if the filesystem write fails.

---

### Path Validation

All file operations validate the relative path against the channel's root directory:

1. Rejects absolute paths.
2. Rejects `..` traversal components.
3. Resolves symlinks and verifies the real path stays under the root directory.
4. For writes to nonexistent files, validates the parent directory instead.
5. For directory creation, walks up to the first existing ancestor (allows creating nested directories).

---

## Diff

### `GET /api/channels/{id}/diff`

Get git diff information for a channel's working directory. Includes both tracked changes and untracked files, and tags each entry with the bucket it came from (staged / unstaged / untracked / conflict).

**Query Parameters:**

| Param    | Type   | Description |
|----------|--------|-------------|
| `source` | string | When provided with `target`, switches to branch-to-branch diff mode (`git diff source..target`). `status` is omitted in this mode. |
| `target` | string | Branch / ref name for branch-to-branch diff mode. |
| `commit` | string | A commit hash (4–64 hex digits). Switches to single-commit mode: the changes that commit introduced. Takes precedence over `source`/`target`. |
| `root`   | int    | Root directory index (0 = primary `dir_path`, 1+ = extra directories from project config). Defaults to 0. |

**Response (200) — uncommitted mode:**
```json
{
  "files": [
    {"path": "main.go",   "additions": 5, "deletions": 2, "binary": false, "status": "staged"},
    {"path": "main.go",   "additions": 1, "deletions": 0, "binary": false, "status": "unstaged"},
    {"path": "new.txt",   "additions": 4, "deletions": 0, "binary": false, "status": "untracked"},
    {"path": "image.png", "additions": 0, "deletions": 0, "binary": true,  "status": "untracked"}
  ],
  "diff": "diff --git a/main.go b/main.go\n...",
  "staged_diff": "diff --git a/main.go b/main.go\n...",
  "unstaged_diff": "diff --git a/main.go b/main.go\n...",
  "untracked_diff": "diff --git a/new.txt b/new.txt\n...",
  "conflict_diff": "",
  "total_additions": 10,
  "total_deletions": 2
}
```

**File entry fields:**

| Field       | Type   | Description |
|-------------|--------|-------------|
| `path`      | string | File path relative to the repo root |
| `old_path`  | string | Original path (set when the file was renamed; omitted otherwise) |
| `additions` | int    | Lines added in this entry |
| `deletions` | int    | Lines deleted in this entry |
| `binary`    | bool   | True for binary files |
| `status`    | string | `"staged"`, `"unstaged"`, `"untracked"`, or `"conflict"`. Omitted in branch-to-branch diff mode. A partially-staged file appears twice — once as `staged` and once as `unstaged` — in that order. |

**Behavior notes:**
- Uncommitted mode (default): runs `git diff --cached` (staged), `git diff` (unstaged), and `git ls-files --others --exclude-standard` (untracked). Conflicts are surfaced via `git diff --diff-filter=U`.
- The frontend parses `staged_diff` / `unstaged_diff` / `untracked_diff` / `conflict_diff` independently so the per-bucket `status` tag survives partial staging (a path that appears in both staged and unstaged buckets would otherwise collide in a single parsed-by-path lookup).
- Branch-to-branch mode (`?source=branchA&target=branchB`): a single `git diff` is returned in `diff`; the per-status fields and the `status` tag on each file are omitted.
- Single-commit mode (`?commit=<sha>`): runs `git show --diff-merges=first-parent`, so a merge commit is diffed against its first parent and a root commit against the empty tree. The response has the branch-to-branch shape. Anything other than a hex hash returns `400`; a hash that doesn't resolve to a commit returns `404`.
- Untracked files generate synthetic diff patches (all lines as additions). Binary detection checks the first 512 bytes for null bytes.
- If the directory is not a git repo, returns an empty `files` array.
- Files are sorted by (path, status priority) — `conflict` < `staged` < `unstaged` < `untracked`.
- The `root` parameter mirrors the same field on file endpoints: `0` (default) targets the channel's primary `dir_path`; `1+` indexes into `extra_dirs` from the project's `.loop/config.json`. Both uncommitted and branch-to-branch modes honor `root`.

**Errors:** `404` if channel not found. `400` if `root` is non-numeric, negative, or out of range.

---

## Pull Request

### `GET /api/channels/{id}/pr`

Look up the open GitHub pull request whose head branch matches the channel's current branch. Shells out to `gh pr view` against the channel's working directory.

**Response (200, PR found):**
```json
{
  "present": true,
  "pr": {
    "number": 24,
    "url": "https://github.com/owner/repo/pull/24",
    "base_ref": "main",
    "head_ref": "feat/git-panel-pr-aware",
    "state": "OPEN",
    "title": "feat(git-panel): default diff base to PR target and live-update branch",
    "is_draft": false
  }
}
```

**Response (200, no PR):**
```json
{"present": false}
```

**Behavior notes:**
- The lookup uses `gh pr view --json number,url,baseRefName,headRefName,state,title,isDraft` and falls back to a `gh pr list` query when `pr view` reports no match.
- The `gh` account is picked by the `github.gh_user` config key (mergeable per-project via `.loop/config.json`); empty falls back to whichever account `gh` currently has active.
- Environmental failures (`gh` not installed, no GitHub remote, network) degrade to `present: false` rather than a 5xx so the UI hides the PR chip silently.

**Errors:** `404` if channel not found.

---

## Branches & Worktrees

### `GET /api/channels/{id}/branches`

List local git branches and worktrees for a channel's directory.

**Query Parameters:**

| Param  | Type | Default | Description |
|--------|------|---------|-------------|
| `root` | int  | 0       | Root directory index (0 = primary `dir_path`, 1+ = extra directories from project config) |

**Response (200):**
```json
{
  "branches": ["main", "feature/foo"],
  "current": "main",
  "worktrees": [
    {"path": "/project/.worktrees/wt1", "branch": "feature/bar", "thread_id": "thread-id-if-imported"}
  ]
}
```

**Behavior notes:**
- Branches checked out in other worktrees are excluded from the `branches` list (git won't allow switching to them).
- The main worktree is excluded from the `worktrees` list.
- `thread_id` is populated when the worktree has been imported as a thread (via `POST /api/worktrees` or `POST /api/worktrees/import`).
- The `root` parameter mirrors the same field on `/api/channels/{id}/diff` and `/api/channels/{id}/commits`: `0` (default) targets the channel's primary `dir_path`; `1+` indexes into `extra_dirs` from the project's `.loop/config.json`. Each extra root is treated as an independent repo.

**Errors:** `400` if `root` is non-numeric, negative, or out of range.

### `POST /api/channels/{id}/branches/switch`

Switch the git branch in a channel's working directory.

**Request:** `{"branch": "feature/foo"}`

**Response (200):** `{"ok": true}`

**Errors:** `400` if branch name is invalid or missing. `500` if git checkout fails (e.g. uncommitted changes).

### `POST /api/channels/{id}/branches/create`

Create and checkout a new branch.

**Request:** `{"name": "feature/new", "from": "main"}` (`from` is optional)

**Response (200):** `{"ok": true}`

### `GET /api/channels/{id}/commits`

List commit history for a channel's git repository.

**Query Parameters:**

| Param    | Type   | Default | Description |
|----------|--------|---------|-------------|
| `branch` | string | HEAD    | Branch name to list commits from |
| `limit`  | int    | 50      | Maximum number of commits to return (max 200) |
| `skip`   | int    | 0       | Number of commits to skip (for pagination) |
| `root`   | int    | 0       | Root directory index (0 = primary `dir_path`, 1+ = extra directories from project config) |

**Response (200):**
```json
{
  "commits": [
    {
      "hash": "abc123def456...",
      "short": "abc123d",
      "subject": "feat: add new feature",
      "author": "John Doe",
      "date": "2026-03-30 14:22:01 +0000"
    }
  ]
}
```

**Behavior notes:**
- Commits are returned in reverse chronological order (newest first).
- On empty repositories (no commits yet), returns `{"commits": []}` instead of an error.
- Branch names are validated against a safe character set (alphanumeric, slashes, hyphens, dots, underscores).
- The `skip` parameter enables lazy pagination — the frontend loads pages of 50 commits and fetches more on scroll.
- The `root` parameter mirrors the same field on `/api/channels/{id}/diff` and other file endpoints: `0` (default) targets the channel's primary `dir_path`; `1+` indexes into `extra_dirs` from the project's `.loop/config.json`.

**Errors:** `400` if branch name is invalid, or if `root` is non-numeric / negative / out of range.

### `POST /api/worktrees`

Create a git worktree as a new thread. The worktree gets its own branch (`worktree/<name>`) based on the selected branch, inherits the parent's session for `--fork-session`, and appears in the sidebar as a thread.

**Request:**
```json
{
  "channel_id": "parent-channel-id",
  "branch": "main",
  "name": "optional-name",
  "author_id": "optional-user-id",
  "message": "optional first prompt"
}
```

If `message` is provided, the bot posts it as a self-mention into the new thread to trigger a runner immediately with that prompt — same auto-trigger semantics as `POST /api/threads`.

**Response (201):**
```json
{
  "thread_id": "new-thread-id",
  "worktree_path": "/project/.worktrees/wt-abc123"
}
```

**Behavior notes:**
- Creates `git worktree add -b worktree/<name> <path> <branch>` so any branch can be used as base, including the currently checked out one.
- Copies the parent's Claude session file to the worktree's project dir (`~/.claude/projects/<encoded-path>/`) so `--resume --fork-session` works on the first message.
- The thread's `DirPath` points to the worktree directory; `Worktree` flag is set to true.
- Container mounts include the parent project directory so git worktree references resolve correctly.

### `POST /api/worktrees/import`

Import an existing git worktree as a thread. Unlike `POST /api/worktrees` which creates a new worktree, this associates an already-existing worktree directory with a thread.

**Request:**
```json
{
  "channel_id": "parent-channel-id",
  "worktree_path": "/project/.worktrees/existing-wt"
}
```

**Response (201):**
```json
{
  "thread_id": "new-thread-id",
  "worktree_path": "/project/.worktrees/existing-wt"
}
```

**Behavior notes:**
- Validates that `worktree_path` is a real git worktree (checked against `git worktree list --porcelain`).
- Idempotent: if a thread already exists for the worktree path, returns it with `200` instead of creating a duplicate.
- Copies the parent's Claude session file to the worktree's project dir for `--fork-session` support.
- Thread name is derived from the worktree directory name and branch: `<dirname> (<branch>)`.

---

### `DELETE /api/worktrees`

Remove a git worktree from disk and optionally delete its associated thread.

**Request Body:**

| Field           | Type   | Required | Description |
|-----------------|--------|----------|-------------|
| `channel_id`    | string | yes      | Parent channel ID that owns the worktree |
| `worktree_path` | string | yes      | Absolute path to the worktree directory |
| `thread_id`     | string | no       | Thread ID to delete (if the worktree was imported as a thread) |

**Response:** `204 No Content` on success.

**Behavior notes:**
- Runs `git worktree remove --force` on the worktree path, then `git worktree prune`.
- If `thread_id` is provided, also deletes the thread record from the database and broadcasts a `channel.deleted` event. As with `DELETE /api/threads/{id}`, the thread's hidden learn and explain threads go with it: their running pass or explanation is cancelled, the queued ones are dropped, and their forked session files are deleted.
- If the git worktree removal fails (e.g. path already gone), returns `500`.

**Errors:** `400` if `channel_id` or `worktree_path` is missing, or if the channel is not found.

---

## Quality

See [Quality](quality.md) for the engine's full architecture, metric definitions, and rule semantics.

### `POST /api/channels/{id}/quality/scan`

Kick a quality scan for the channel. The request returns immediately; the full report ships over the WebSocket as a `quality.scanned` event.

**Response (202 Accepted):**
```json
{ "status": "started" }
```

When a scan is already in flight for this channel, returns `{"status": "in_progress"}` without queueing or replacing — the engine coalesces concurrent triggers per channel.

**Errors:** `501` if the quality scanner is not configured.

---

### `GET /api/channels/{id}/quality/snapshot`

Fetch the persisted quality snapshot. Returns the row for the channel's current branch first; on miss falls back to the most recent snapshot on any branch with `branch_mismatch: true` so the panel can render a banner.

**Response (200):**
```json
{
  "dir_path": "/work",
  "branch": "main",
  "current_branch": "main",
  "branch_mismatch": false,
  "signal": 6532,
  "geo_mean": 0.6532,
  "scanned_at": "2026-04-30T17:01:23Z",
  "metrics": [
    { "name": "modularity", "score": 0.71, "raw": 0.42 }
  ],
  "tiles": [
    { "path": "internal/foo/bar.go", "loc": 312, "deficit": 0.18, "metric_deficits": { "depth": 0.12 }, "top_reason": "depth" }
  ]
}
```

**Errors:** `404` if no snapshot has ever been recorded for the channel. `501` if the snapshot reader is not configured.

---

### `GET /api/channels/{id}/quality/complexity`

Per-function complexity hotspots from the cached graph (cyclomatic, cognitive, max nesting, parameter count, LOC). Recomputes the metric using the channel's effective `quality.complexity` thresholds — same numbers the engine produced during scan. The function list is sorted worst-first; per-dimension scores follow a saturating `T/raw` curve past threshold, so badly-saturated functions stay ranked against each other.

**Query Parameters:**

| Param | Type | Required | Description |
|---|---|---|---|
| `limit` | int | no | Max functions per page (default 50, max 100). |
| `offset` | int | no | Start offset into the worst-first list (default 0). |

**Response (200):**
```json
{
  "score": 0.903,
  "raw": 338,
  "total_functions": 8388,
  "over_threshold": 338,
  "histogram": {
    "cyclomatic": { "ok": 8201, "warn": 138, "crit": 48 },
    "cognitive":  { "ok": 8171, "warn": 141, "crit": 75 },
    "nesting":    { "ok": 8370, "warn":  17, "crit":  0 },
    "params":     { "ok": 8347, "warn":  30, "crit": 10 },
    "loc":        { "ok": 8219, "warn": 135, "crit": 33 }
  },
  "functions": [
    {
      "path": "internal/orchestrator/executor.go",
      "name": "ExecuteTask",
      "start_line": 91,
      "cyclomatic": 105,
      "cognitive":  243,
      "max_nesting": 4,
      "param_count": 2,
      "loc": 485,
      "score": 0.062
    }
  ],
  "offset": 0,
  "limit": 50,
  "returned": 1
}
```

`raw` is the count of over-threshold functions (mirrors `over_threshold`). Histogram buckets are `ok` (≤ T), `warn` (T..2T), `crit` (> 2T). `score` is the LOC-weighted mean of per-function scores.

**Errors:** `400` on invalid `limit` / `offset`. `404` if no graph is cached for the channel (run a scan first). `501` if the quality engine is not configured.

---

### `GET /api/channels/{id}/quality/clones`

Clone clusters from the cached graph. SimHash fingerprints over normalised function-body shingles are bucketed by Hamming distance — see `quality.clones.max_distance`. Clusters with one member are dropped. The list is sorted by total LOC descending.

**Query Parameters:**

| Param | Type | Required | Description |
|---|---|---|---|
| `limit` | int | no | Max clusters per page (default 25, max 50). |
| `offset` | int | no | Start offset (default 0). |

**Response (200):**
```json
{
  "score": 0.842,
  "raw": 412,
  "duplicated_loc": 412,
  "total_loc": 33559,
  "cluster_count": 27,
  "clusters": [
    {
      "members": [
        { "path": "internal/api/foo_handler.go", "name": "handleFoo", "start_line": 91, "end_line": 142, "loc": 52 },
        { "path": "internal/api/bar_handler.go", "name": "handleBar", "start_line": 91, "end_line": 142, "loc": 52 }
      ],
      "loc": 104,
      "max_distance": 1
    }
  ],
  "offset": 0,
  "limit": 25,
  "returned": 1
}
```

`raw` is `duplicated_loc`, the LOC counted as duplicate (every member's LOC except one representative per cluster). `score` is `1 - duplicated_loc/total_loc`.

**Errors:** `400` on invalid `limit` / `offset`. `404` if no graph is cached for the channel. `501` if the quality engine is not configured.

---

## Review

See [review.md](review.md) for the full lifecycle. Endpoints below return
`501 review service not configured` if the daemon was started without
`gh` available or without a worktree provider wired in.

### `GET /api/channels/{id}/review/prs`

List the open pull requests in the repo backing the channel's working
directory. The FE renders these as a picker so the user can click a row to
auto-load instead of pasting a PR number or URL.

Response: `{"prs": [{"number": 42, "url": "...", "base_ref": "main", "head_ref": "feat-x", "state": "OPEN", "title": "Add X", "is_draft": false}, ...]}` — capped at 100.

**Errors:** `400` if the channel has no `dir_path`. `404` if the channel does
not exist. `500` on `gh` failure. `503` (`gh CLI not installed`) when the
`gh` binary is missing. `501` if the review service is not configured.

### `POST /api/channels/{id}/review/load`

Load a PR's diff into a local worktree under the channel's `dir_path`.
Body: `{"pr_number": 42}`. Replaces any existing session for the channel.

Response: `{"present": true, "session": { ... }}` — the full session, mirroring `review.Session` in `internal/review/session.go`.

**Errors:** `400` on invalid `pr_number` or missing `dir_path`. `404` if the PR does not exist. `500` on `gh`/git failure.

### `GET /api/channels/{id}/review`

Return the channel's review session, or `{"present": false}` if none.
`?diff=false` leaves out `raw_diff`, by far the largest field, for callers
that only want the comments.

### `DELETE /api/channels/{id}/review`

Remove the channel's session and delete the on-disk worktree, stopping a
run in progress. Broadcasts `review.status` with `idle` when there was a
session. Idempotent — `204` whether or not one exists.

### `POST /api/channels/{id}/review/run`

Start an agent review pass. Returns `202 {"status":"started"}` and the
run continues in the background, streaming `review.comment` and
`review.status` events over the WebSocket.

A concurrent call while a run is in flight returns `202 {"status":"in_progress"}` without restarting.

Once the agent is done, and before the session turns `ready`, the daemon
runs the [dedup pass](#post-apichannelsidreviewdedup) over the comments
the run added, checking each against every other comment in the session.
The session's `superseded` field then maps each comment it deleted to the
comment it was folded into; a new run clears it. A run that added no
agent comment skips the pass, and a failed pass is only logged.

If the session is configured to fork (see below), the chosen session's
transcript is copied into the worktree's Claude project dir before the
agent starts, and the run is launched with `--resume <id> --fork-session`.

**Errors:** `404` if no session. `409` if the session is not in `ready`
status. `400` if the configured fork cannot be resolved or staged (no chat
session yet, unknown session id, transcript missing on disk). `501` if the
review agent is not wired.

### `POST /api/channels/{id}/review/dedup`

Fold duplicate findings. Every comment in the session, across files, goes
to a read-only agent run (no Bash, no edits, strict MCP config) when at
least one is the agent's, even a lone one, since it still needs a verdict.
The model clusters them by root
cause, including a cause and its symptom (even when the symptom has a
narrower fix of its own), or the same issue anchored in two files, and picks the most severe and specific one to keep. The daemon
deletes the others the same way `DELETE /review/comments/{cid}` does, which
includes removing them from the PR if they were pushed. Each deletion is
broadcast as `review.comment_removed`. The model's note on what the dropped
comments added is appended to the kept comment when that is an unpushed
agent finding, and broadcast as `review.comment_updated`.
Only agent findings are ever deleted: a GitHub comment can be the one kept,
but is never dropped. The run is synchronous, and while it runs the session
holds the channel's review-run slot and shows status `reviewing`. It is in
the agent token scope, so an agent can call it from its container through
`loop review:dedup`. Every review run already folds the comments it added
(see [`POST .../review/run`](#post-apichannelsidreviewrun)); this regroups
the whole session.

Response:

```json
{
  "removed": ["<id>"],
  "clusters": [{"kept": "<id>", "removed": ["<id>"], "reason": "...", "note": "...", "note_added": true}],
  "related": [{"ids": ["<id>", "<id>"], "reason": "..."}],
  "moved": [{"id": "<id>", "from": 145, "to": 147}],
  "trimmed": [{"id": "<id>", "covered_by": "<id>", "reason": "..."}],
  "verdicts": [{"id": "<id>", "verdict": "false_positive", "reason": "..."}],
  "checked": 11,
  "errors": ["<id>: <msg>"]
}
```

`clusters` lists each merged group with the ids actually removed; a group
whose deletes all failed is left out. `note_added` is false when the kept
comment is a GitHub or pushed one, which is never edited. `related` groups
findings about the same code path that need separate fixes; nothing is
deleted for them. `moved` lists the unpushed agent findings the model
re-anchored to the statement they are about, at most 20 lines from where
they were; each move is broadcast as `review.comment_updated`. `trimmed`
lists the unpushed agent findings that bundled several issues and were
rewritten to the ones no other comment covers; `covered_by` is the comment
that reports the part cut out. Each is broadcast as `review.comment_updated`. `verdicts`
lists the kept agent findings the model checked against the code: `real`,
`false_positive` or `already_fixed`, with a one-sentence `reason`. A move or
a verdict counts only when the pass opened that comment's file in the PR
worktree with the Read tool (the parent checkout's copy doesn't count); the
others are dropped. Each verdict is
stored on the comment as `verdict` and `verdict_reason` (pushed findings
included; nothing is deleted for a verdict) and broadcast as
`review.comment_updated`. `checked` is the number of comments shown to the model
(`0` when there is nothing to fold, in which case no agent runs). `errors`
lists the deletions that failed; those comments stay.

**Errors:** `404` if no session. `409` if the session has no worktree, is
not `ready`, or a review run is in flight. `400` if the channel has no
`dir_path`. `403` if review is disabled for the project. `500` if the
refresh from GitHub fails, the agent run fails, or its reply has no
parseable JSON. `501` if the review service or agent is not wired.

### `POST /api/channels/{id}/review/stop`

Stop the review run in flight, or a [dedup pass](#post-apichannelsidreviewdedup),
which holds the same slot. Returns `202 {"status":"stopping"}` at once. The
agent is stopped, and the session goes back to `ready` once it is gone,
keeping the findings reported so far; `review.status` says when. A stopped
run is no error. A `POST .../review/run` before then still answers
`202 {"status":"in_progress"}`, and a stopped dedup pass answers its own
caller `500`.

**Errors:** `404` if no session. `409` if no review run or dedup pass is in
flight. `501` if the review service is not wired.

### `PUT /api/channels/{id}/review/fork`

Choose which Claude session the *next* review run forks from. Body:
`{"mode": "", "session_id": ""}` where `mode` is one of:

| `mode` | Behaviour |
|---|---|
| `""` | Fresh session; the reviewer sees only the diff. |
| `"current"` | Fork whatever session the channel's chat is on when the run starts — the default for a newly loaded PR. With no chat session yet, the run starts fresh. |
| `"custom"` | Fork `session_id`. |

It is always a fork, never a resume — review turns never land in the chat
session the user is still talking to. `session_id` is stored only for
`custom` and cleared on any other mode.

The choice lives on the review session (not on the run request) because
the desktop app's Run button dispatches a workflow whose `loop review:run`
step has nowhere to carry per-run options. It is in-memory, so it resets
when the daemon restarts.

Response: `{"present": true, "session": { ... }}` — the updated session.

**Errors:** `400` on invalid JSON, an unknown `mode`, or `custom` without a
`session_id`. `404` if the channel has no review session. `501` if the
review service is not configured.

### `PUT /api/channels/{id}/review/agent`

Choose the model and reasoning effort the *next* review run uses. Body:
`{"model": "", "effort": ""}`. `model` is any Claude model id, passed to
the CLI verbatim; `effort` is one of `low`, `medium`, `high`, `xhigh`,
`max`. Empty inherits the config's `claude_model` / `claude_effort`.
Values are trimmed. Stored on the in-memory review session like the fork
choice, and reported back as the session's `model` / `effort`.

Response: `{"present": true, "session": { ... }}` — the updated session.

**Errors:** `400` on invalid JSON or an unknown `effort`. `404` if the
channel has no review session. `501` if the review service is not
configured.

### `DELETE /api/channels/{id}/review/comments/{cid}`

Delete one comment from the session, and from the PR when it has a GitHub
copy (a pushed agent comment, or a GitHub comment the configured gh user
wrote). Broadcasts `review.comment_removed`. Response: `204`.

**Errors:** `403` for a GitHub comment by someone else, for any GitHub
comment with an agent token, or when review is disabled. `404` if session or
comment id is unknown. `500` on `gh` failure; the comment is kept. `501` if
the review service is not configured.

### `PATCH /api/channels/{id}/review/comments/{cid}`

Replace a comment's body, e.g. to fold in what a duplicate adds. Body:
`{"body": "..."}`. Only an unpushed agent comment can change. Broadcasts
`review.comment_updated`.

Response: the updated comment.

**Errors:** `400` on invalid JSON or an empty body. `404` if session or
comment id is unknown. `409` for a pushed or GitHub comment. `501` if the
review service is not configured.

### `POST /api/channels/{id}/review/comments/{cid}/push`

Push one comment to the PR via `gh api /repos/{owner}/{repo}/pulls/{N}/comments`. Flips `pushed=true` on the in-memory session on success.

Response: `{"pushed": true}` (or `{"pushed": true, "already": true}` if already pushed).

**Errors:** `404` if session or comment id is unknown. `500` on `gh` failure.

### `POST /api/channels/{id}/review/push-all`

Push every unpushed comment in the session. Errors are accumulated rather
than short-circuiting — one bad comment does not block the rest.

Response: `{"pushed": N, "failed": M, "errors": ["id: msg", ...]}`.

---

## Memory

See [Memory System](memory.md) for the full architecture.

### `POST /api/memory/search`

Semantic search across indexed memory files using cosine similarity.

**Request:**
```json
{
  "query": "how does the scheduler work",
  "top_k": 5,
  "dir_path": "/home/user/projects/my-project",
  "channel_id": "abc123"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `query`      | string | yes      | Natural language search query |
| `top_k`      | int    | no       | Number of results (default: 5) |
| `dir_path`   | string | no*      | Project directory for scoping |
| `channel_id` | string | no*      | Alternative to `dir_path` (looked up from DB) |

\* At least one of `dir_path` or `channel_id` is required.

**Response (200):**
```json
{
  "results": [
    {
      "file_path": "/home/user/memory/architecture.md",
      "content": "## Scheduler\nThe scheduler runs...",
      "score": 0.87,
      "chunk_index": 1
    }
  ]
}
```

**Errors:** `400` if `query` is empty or neither `dir_path` nor `channel_id` provided. `501` if memory indexer not configured.

---

### `POST /api/memory/index`

Force re-index all memory files for a project directory.

**Request:**
```json
{
  "dir_path": "/home/user/projects/my-project",
  "channel_id": "abc123"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `dir_path`   | string | no*      | Project directory containing memory files |
| `channel_id` | string | no*      | Alternative to `dir_path` |

**Response (200):**
```json
{"count": 3}
```

The `count` is the number of files that were (re-)indexed.

**Errors:** `400` if neither `dir_path` nor `channel_id` provided. `501` if memory indexer not configured.

---

### `GET /api/memory/files`

List distinct indexed memory file paths for a project.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `dir_path`   | string | no*      | Project directory |
| `channel_id` | string | no*      | Alternative to `dir_path` |

**Response (200):**
```json
{
  "files": [
    {"file_path": "/home/user/memory/notes.md", "dir_path": "/home/user/projects/my-project"}
  ]
}
```

**Behavior notes:** Only returns files that still exist on disk (`os.Stat` check).

**Errors:** `400` if resolution fails. `501` if store not configured.

---

### `GET /api/memory/file`

Read a memory file's raw content.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Absolute path to a `.md` file |

**Response (200):** `Content-Type: text/plain; charset=utf-8` with file contents.

**Errors:** `400` if path is empty, not absolute, or not a `.md` file. `404` if file not found.

---

## Readme

### `GET /api/readme`

Get the Loop project README content.

**Response (200):** `Content-Type: text/plain; charset=utf-8` with the compiled-in README text.

---

## UI Bridge

Drives the desktop app: each app window connects to `/api/ws/ui`, reports what it shows, and runs the steps a command sends it. The CLI wraps it as `loop ui:run` and `loop ui:state`; agents use the `ui_state` and `ui_run` MCP tools.

### `GET /api/ui/state`

The connected windows and what each shows.

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `after` | integer | no | Waits (up to 30s) for a version past this one, then answers with the state as it is |

**Response:**
```json
{
  "version": 12,
  "clients": [
    {
      "client_id": "w-1",
      "focused": true,
      "connected_at": "2026-10-09T10:00:00Z",
      "focused_at": "2026-10-09T10:01:00Z",
      "state": {
        "focused": true,
        "channel_id": "ch-1",
        "tab": "Default",
        "tabs": ["Default", "Agents"],
        "canvas": false,
        "panes": [
          {"id": "chat", "panel": "chat"},
          {"id": "docker-agent-0", "panel": "docker-agent", "open_mode": "fresh", "status": "running", "busy": true}
        ],
        "maximized": "docker-agent-0"
      }
    }
  ]
}
```

`busy` is an agent terminal that printed in the last 1.5s, e.g. the Claude TUI at work. `status` is `connecting`, `running`, `completed` or `failed`.

**Errors:** `400` invalid `after`.

---

### `POST /api/ui/commands`

Runs steps in a window, in order, until one fails.

**Request:**
```json
{
  "client_id": "w-1",
  "timeout": "2m",
  "steps": [
    {"op": "select_channel", "channel_id": "ch-1"},
    {"op": "add_pane", "panel": "docker-agent", "open_mode": "fresh"},
    {"op": "send_input", "pane": "docker-agent", "text": "run the tests", "submit": true},
    {"op": "wait_for", "pane": "docker-agent", "match": "PASS|FAIL", "lines": 200}
  ]
}
```

`client_id` picks the window; without it, the one the user last focused. `timeout` is a Go duration (default `1m`, at most `10m`).

**Steps:** a `pane` is a pane id or a panel type (its first pane).

| Op | Fields | Does |
|----|--------|------|
| `select_channel` | `channel_id` | Opens the channel |
| `set_tab` | `tab` | Switches the layout tab |
| `create_tab` | `tab?` | Opens a new, empty split tab named `tab` (default the next `Layout N`); the result has its `tab` |
| `rename_tab` | `tab`, `name` | Renames the tab; the result has its new `tab` |
| `remove_tab` | `tab` | Removes the tab, closing its terminals if it's the open one; the last tab stays |
| `replace_pane` | `pane`, `panel`, `open_mode?`, `item?`, `scope?` | Puts a new pane in the pane's place |
| `add_pane` | `panel`, `next_to?`, `direction?`, `side?`, `open_mode?`, `item?`, `scope?` | Adds a pane beside `next_to`, or, without it, along that edge of the tab. `direction` is `horizontal` (a column, the default) or `vertical` (a row); `side` is `after` (right or below, the default) or `before` (left or above). Next to a pane in a row of columns (or a column of rows), the pane joins it with an equal share instead of splitting that pane in two |
| `remove_pane` | `pane` | Closes the pane |
| `maximize_pane` | `pane` | Makes the pane fill the tab |
| `restore_pane` | | Puts a maximized pane back |
| `open_file` | `path`, `line?` | Opens a file of the channel's roots in the editor |
| `send_input` | `pane`, `text`, `submit?` | Types into an agent terminal |
| `read_output` | `pane`, `lines?` | The terminal's last `lines` (default 50, at most 2000) |
| `wait_for` | `pane`, `match?`, `quiet_ms?`, `lines?` | Waits until the last `lines` match the regular expression `match` (multiline), or, without it, until the terminal is quiet for `quiet_ms` (default 2000). After a `send_input` to that pane in the same command, only output since counts. A session that ended counts as done |

`open_mode` is for `docker-agent` panes. `item` names the playground of a `playground` pane, and `scope` (`global` or `project`) picks it when both scopes have one by that name.

Terminal steps (`send_input`, `read_output`, `wait_for`) work only on `docker-agent` and `docker-shell` panes, which run in containers. A host shell is refused, by the daemon and again by the window.

**Response:**
```json
{
  "client_id": "w-1",
  "results": [
    {"op": "select_channel", "ok": true},
    {"op": "add_pane", "ok": true, "pane": "docker-agent-1"},
    {"op": "send_input", "ok": true, "pane": "docker-agent-1"},
    {"op": "wait_for", "ok": true, "pane": "docker-agent-1", "output": "...\nPASS"}
  ]
}
```

A failed step has `"ok": false` and an `error`; the steps after it don't run.

**Errors:** `400` empty steps, a step without an op, a bad timeout, or a terminal step on a host shell; `403` an agent's command outside its project; `404` no window, or no window `client_id`; `502` the window dropped; `504` the window didn't answer in time.

---

## WebSocket Endpoints

### `GET /api/ws`

Real-time events WebSocket. See [Events System](events.md) for the full protocol.

**Errors:** `501` if events hub is not configured.

---

### `GET /api/ws/terminal`

Interactive terminal WebSocket. See [Terminal WebSocket](terminal.md) for the full protocol.

**Errors:** `501` if terminal manager is not configured.

---

### `GET /api/ws/ui`

The app windows' connection to the [UI bridge](#ui-bridge): a window sends `hello`, then `state` whenever what it shows changes, and answers each `command` with a `result` message holding its `results`. Owner only.

---

## Browser

### `POST /api/browser/action`

Unified endpoint for all browser operations. Used by both the `loop-browser` MCP server (inside agent containers) and the desktop browser panel frontend.

**Request:**
```json
{
  "channel_id": "ch-abc123",
  "action": "navigate",
  "params": {"url": "https://example.com"}
}
```

**Actions:**

| Action | Params | Description |
|--------|--------|-------------|
| `navigate` | `url` | Navigate to a URL |
| `reload` | — | Reload the current page |
| `go_back` | — | Navigate back in history |
| `go_forward` | — | Navigate forward in history |
| `get_page_info` | — | Get current URL and title |
| `get_element_refs` | — | Get the page's interactive elements |
| `mouse_click` | `x`, `y`, `button`, `click_count` | Click at coordinates |
| `mouse_move` | `x`, `y` | Move mouse |
| `mouse_scroll` | `x`, `y`, `delta_x`, `delta_y` | Scroll |
| `mouse_down` | `x`, `y`, `button` | Mouse button down |
| `mouse_up` | `x`, `y`, `button` | Mouse button up |
| `key_press` | `key` | Press a key |
| `type_text` | `text` | Type text |
| `click_ref` | `refs`, `ref_index` | Click element by ref |
| `screenshot` | — | Capture screenshot |
| `evaluate_js` | `expression` | Evaluate JavaScript |
| `list_tabs` | — | List all open tabs |
| `new_tab` | `url` | Open a new tab |
| `switch_tab` | `target_id` | Make a tab the active one and bring it to the front |
| `close_tab` | `target_id` | Close a tab |
| `resize_window` | `width`, `height` | Resize viewport |
| `scroll_into_view` | `backend_node_id` | Scroll element into view |
| `read_console` | `pattern`, `only_errors`, `clear`, `limit` | Read console messages |
| `read_network` | `pattern`, `clear`, `limit` | Read network requests |

**Responses:**

| Code | Description |
|------|-------------|
| 200 | JSON response with `result`, `error`, `image`, `element_refs`, `tabs`, `page_info`, or `screenshot_path` |
| 400 | Missing `channel_id` or invalid JSON |
| 503 | Browser not configured (`browser_enabled: false`) |

The endpoint handles Chrome lifecycle internally: lazily starts Chrome on first action, touches the idle timer on every action, and manages CDP connections.

---

### `GET /api/ws/browser`

WebSocket endpoint for browser screencast streaming and input.

Returns 503 if browser is not configured. The WS handles four message types:

| Message | Direction | Description |
|---------|-----------|-------------|
| `start` | Client → Server | Initialize CDP connection and screencast for a channel |
| `stop` | Client → Server | Stop the browser session |
| `screencast` | Client → Server | Start screencast frame streaming (with `width`/`height`) |
| `input` | Client → Server | Mouse/keyboard input events |
| Binary frames | Server → Client | JPEG screencast frames |
| `started` | Server → Client | CDP connected, ready |
| `stopped` | Server → Client | Session stopped |
| `tabs` | Server → Client | Tab list update |
| `tab_switched` | Server → Client | Active tab changed |
| `tab_created` | Server → Client | New tab opened |
| `tab_closed` | Server → Client | Tab closed |
| `error` | Server → Client | Error message |

Control operations (navigate, tabs, reload, etc.) go through `POST /api/browser/action`, not the WebSocket.


---

## Containers

### `GET /api/containers`

List all tracked containers across all channels. Returns containers sorted with running containers first (newest first), then non-running containers (newest first).

**Response:** `200 OK`

```json
[
  {
    "container_id": "abc123def456",
    "channel_id": "chan_a",
    "type": "agent",
    "status": "running",
    "container_name": "loop-my-project-a1b2c3",
    "created_at": "2026-03-31T10:00:00Z",
    "updated_at": "2026-03-31T10:00:00Z"
  },
  {
    "container_id": "def789ghi012",
    "channel_id": "chan_b",
    "type": "chrome",
    "status": "pending-removal",
    "container_name": "loop-chrome-chan-b",
    "created_at": "2026-03-31T09:50:00Z",
    "updated_at": "2026-03-31T10:01:00Z",
    "remove_at": "2026-03-31T10:06:00Z"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `container_id` | string | Docker container ID |
| `channel_id` | string | Channel the container belongs to |
| `type` | string | `"agent"`, `"shell"`, or `"chrome"` |
| `status` | string | `"running"`, `"stopped"`, or `"pending-removal"` |
| `container_name` | string | Docker container name |
| `created_at` | string | ISO 8601 creation timestamp |
| `updated_at` | string | ISO 8601 last status change timestamp |
| `remove_at` | string? | ISO 8601 scheduled removal time (only for `pending-removal`) |

**Errors:** `503` if the container registry is not configured.

---

## Agent Registry

### `GET /api/agents`

List active agents for a channel.

**Query Parameters:**

| Param | Required | Description |
|-------|----------|-------------|
| `channel_id` | Yes | Channel ID to list agents for |

**Response:** JSON array of `AgentInfo` objects.

```json
[
  {
    "agent_id": "docker-agent-0",
    "channel_id": "ch-1",
    "session_id": "sid-abc",
    "name": "Worker",
    "status": "running",
    "work_summary": "implementing auth module",
    "created_at": "2026-03-25T10:00:00Z",
    "updated_at": "2026-03-25T10:05:00Z"
  }
]
```

| Status | Description |
|--------|-------------|
| 200 | JSON array (empty `[]` if no agents) |
| 400 | Missing `channel_id` |
| 503 | Agent registry not configured |

---

### `PATCH /api/agents/{id}`

Update an agent's status, name, or work summary.

**Request Body:**

```json
{
  "channel_id": "ch-1",
  "status": "running",
  "work_summary": "indexing files",
  "name": "Worker"
}
```

All fields except `channel_id` are optional — only non-empty values are applied.

| Status | Description |
|--------|-------------|
| 200 | Updated `AgentInfo` JSON |
| 400 | Missing `channel_id` or invalid JSON |
| 404 | Agent not found |
| 503 | Agent registry not configured |

---

### `DELETE /api/agents/{id}`

Unregister an agent from the registry. Called by the MCP server on graceful shutdown.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Agent ID (e.g. `"docker-agent-0"`) |

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID the agent belongs to |

**Response:** `204 No Content`

**Behavior notes:** Broadcasts an `agent_instance.unregistered` event to the frontend via the EventsHub.

**Errors:** `400` if `agent_id` or `channel_id` is missing. `503` if agent registry not configured.

---

### `POST /api/agents/{id}/message`

Send a push message to an agent's mailbox.

**Request Body:**

```json
{
  "channel_id": "ch-1",
  "from_agent_id": "docker-agent-0",
  "content": "I finished the API, you can start tests"
}
```

| Status | Description |
|--------|-------------|
| 204 | Message delivered |
| 400 | Missing `channel_id` or `content` |
| 404 | Target agent not found |
| 503 | Agent registry not configured |

Messages are non-blocking — dropped if the target's mailbox (buffer size 64) is full.

---

### `GET /api/ws/agent-channel`

WebSocket endpoint for MCP servers to receive pushed messages.

**Query Parameters:**

| Param | Required | Description |
|-------|----------|-------------|
| `agent_id` | Yes | Agent ID to subscribe for |
| `channel_id` | Yes | Channel ID |

Messages are forwarded as JSON:

```json
{
  "from_agent_id": "docker-agent-0",
  "content": "task completed",
  "timestamp": "2026-03-25T10:05:00Z"
}
```

The WebSocket closes when the agent is unregistered (terminal session closed).

---

## Configuration

Config endpoints expose a schema-driven API for reading and writing Loop configuration. Both global (`~/.loop/config.json`) and per-project (`{workDir}/.loop/config.json`) configs are supported. The schema endpoint powers the Settings form UI.

### `GET /api/config/schema`

Returns the JSON Schema describing all config fields, their types, defaults, and descriptions. Used by the frontend to render typed form controls.

**Response (200):**
```json
{
  "type": "object",
  "properties": {
    "platforms": {
      "type": "array",
      "items": {"type": "string", "enum": ["local", "discord", "slack"]},
      "description": "Platforms to enable"
    },
    "claude_model": {
      "type": "string",
      "enum": ["", "claude-opus-4-6", "claude-sonnet-4-6"],
      "description": "Claude model to use"
    }
  }
}
```

The schema includes metadata for rendering (e.g. `enum` for dropdowns, `format: "password"` for secret fields).

---

### `GET /api/config`

Returns the global config as both parsed JSON and raw HJSON text.

**Response (200):**
```json
{
  "config": { "platforms": ["local"], "claude_model": "" },
  "raw": "{\n  \"platforms\": [\"local\"]\n}"
}
```

| Field    | Type   | Description |
|----------|--------|-------------|
| `config` | object | Parsed config values |
| `raw`    | string | Raw HJSON file contents (for the JSON editor view) |

---

### `PUT /api/config`

Save global config. Accepts raw HJSON text.

**Request:**
```json
{
  "raw": "{\n  \"platforms\": [\"local\"],\n  \"claude_model\": \"claude-opus-4-6\"\n}"
}
```

| Field | Type   | Required | Description |
|-------|--------|----------|-------------|
| `raw` | string | yes      | HJSON config text to write to `~/.loop/config.json` |

**Response (200):**
```json
{"ok": true}
```

**Errors:** `400` if the HJSON is invalid.

---

### `GET /api/config/project`

Returns the project config for a channel.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID to look up the project directory |

**Response (200):**
```json
{
  "config": { "claude_model": "claude-opus-4-6" },
  "raw": "{\n  \"claude_model\": \"claude-opus-4-6\"\n}"
}
```

Same shape as `GET /api/config`. If no project config file exists, `config` is an empty object and `raw` is `""`.

**Errors:** `400` if `channel_id` is missing. `404` if channel not found.

---

### `GET /api/shortcuts`

Returns prompt shortcuts with resolved prompt text. When a `channel_id` is provided, project-level shortcuts are merged on top of global ones (project overrides global by name).

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | no       | Channel ID to merge project-level shortcuts |

**Response (200):**
```json
[
  {
    "name": "coverage",
    "description": "Run coverage check",
    "prompt": "Run make coverage-check and report results"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Shortcut identifier |
| `description` | string | Human-readable description |
| `prompt` | string | Resolved prompt text (inline or loaded from file) |

Shortcuts with unresolvable prompts (e.g. missing file) are silently skipped.

---

### `POST /api/shortcuts`

Add, update, or delete a prompt shortcut in the global or project config file.

**Request Body:**

| Field         | Type   | Required | Description |
|---------------|--------|----------|-------------|
| `action`      | string | yes      | `add`, `update`, or `delete` |
| `name`        | string | yes      | Shortcut name |
| `scope`       | string | no       | `global` (default) or `project` |
| `channel_id`  | string | conditional | Required when scope is `project` |
| `description` | string | no       | Human-readable description (add/update) |
| `prompt`      | string | conditional | Inline prompt text (required for add/update unless `prompt_path` is set) |
| `prompt_path` | string | conditional | Path to prompt file relative to `shortcuts/` dir (mutually exclusive with `prompt`) |

**Response:** `204 No Content` on success.

**Errors:**
- `400` — missing name, invalid action, missing prompt, mutually exclusive fields, or missing channel_id for project scope
- `404` — shortcut not found (update/delete)
- `409` — duplicate name (add)

---

### `GET /api/bash-shortcuts`

Returns bash shortcuts with resolved command text. When a `channel_id` is provided, project-level shortcuts are merged on top of global ones (project overrides global by name).

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | no       | Channel ID to merge project-level shortcuts |

**Response (200):**
```json
[
  {
    "name": "make lint",
    "description": "Run the linter",
    "command": "make lint"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Shortcut identifier |
| `description` | string | Human-readable description |
| `command` | string | Resolved command text (inline or loaded from file) |

Shortcuts with unresolvable commands (e.g. missing file) are silently skipped.

---

### `POST /api/bash-shortcuts`

Add, update, or delete a bash shortcut in the global or project config file.

**Request Body:**

| Field          | Type   | Required | Description |
|----------------|--------|----------|-------------|
| `action`       | string | yes      | `add`, `update`, or `delete` |
| `name`         | string | yes      | Shortcut name |
| `scope`        | string | no       | `global` (default) or `project` |
| `channel_id`   | string | conditional | Required when scope is `project` |
| `description`  | string | no       | Human-readable description (add/update) |
| `command`      | string | conditional | Inline command text (required for add/update unless `command_path` is set) |
| `command_path` | string | conditional | Path to script file relative to `bash-shortcuts/` dir (mutually exclusive with `command`) |

**Response:** `204 No Content` on success.

**Errors:**
- `400` — missing name, invalid action, missing command, mutually exclusive fields, or missing channel_id for project scope
- `404` — shortcut not found (update/delete)
- `409` — duplicate name (add)

---

### `PUT /api/config/project`

Save project config for a channel. Owner-only.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID |

**Request:**
```json
{
  "content": "{\n  \"claude_model\": \"claude-opus-4-6\"\n}"
}
```

**Response:** `204 No Content`

Creates the `.loop/` directory and config file if they don't exist. A project config that was trusted stays trusted (see [`GET /api/config/project/trust`](#get-apiconfigprojecttrust)).

**Errors:** `400` if `channel_id` is missing, the channel isn't found, or the HJSON is invalid.

---

### `GET /api/config/project/trust`

Whether the project config's host-reaching fields apply as written (see [Configuration: Project Config Trust](configuration.md#project-config-trust)). Owner-only.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID; worktree channels use their root project's config |

**Response (200):**
```json
{
  "trusted": false,
  "current": "{\n  \"mounts\": [\n    \"~/data:/data\"\n  ]\n}",
  "approved": "{}",
  "diff": "--- .loop/config.json (last trusted)\n+++ .loop/config.json (now)\n@@ -1 +1,5 @@\n-{}\n+{\n+  \"mounts\": [\n+    \"~/data:/data\"\n+  ]\n+}\n",
  "hash": "4f1c…"
}
```

| Field | Description |
|---|---|
| `trusted` | The fields apply as written |
| `current` | The fields as the file has them now, as indented JSON |
| `approved` | The version last trusted, which applies while `trusted` is false; `""` if never trusted |
| `diff` | `approved` → `current` as a unified diff, from `/dev/null` if never trusted; `""` while `trusted` |
| `hash` | Identifies `current`; pass it to `POST` to trust exactly this version |

**Errors:** `400` if `channel_id` is missing or the channel isn't found. `500` if the project config doesn't parse. `501` if trust isn't configured.

---

### `POST /api/config/project/trust`

Trust the project config as reviewed. Owner-only.

**Query Parameters:** `channel_id` as above.

**Request:**
```json
{"hash": "4f1c…"}
```

**Response:** `204 No Content`

**Errors:** `400` if `channel_id` or `hash` is missing. `409` if the config changed since the status with that `hash`: fetch the status again. `501` if trust isn't configured.

---

### `GET /api/config/history`

The global config's revisions, newest first. Owner-only.

Loop records a revision of `~/.loop/config.json` and of each channel's project `.loop/config.json` whenever the content changes: on its own writes (Settings, learn, shortcut, workflow and built-in edits, restores), and within a minute of an edit made outside Loop. Each file keeps its newest 200 revisions.

**Response (200):**
```json
{
  "path": "/home/user/.loop/config.json",
  "revisions": [
    {"id": 12, "source": "settings", "created_at": "2026-10-09T10:00:00Z", "added": 2, "removed": 1},
    {"id": 3, "source": "initial", "created_at": "2026-10-01T09:00:00Z", "added": 40, "removed": 0}
  ]
}
```

| Field | Description |
|---|---|
| `source` | What wrote the content: `settings`, `learn`, `shortcuts`, `workflows`, `builtins`, `restore:<id>`, `external` for an edit made outside Loop, or `initial` for the content Loop first saw |
| `added`, `removed` | Lines changed from the revision before it; the oldest revision counts every line as added |

**Errors:** `500` if the loop directory isn't configured. `501` if config history isn't configured.

---

### `GET /api/config/project/history`

The project config's revisions, as for [`GET /api/config/history`](#get-apiconfighistory). Owner-only.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID; worktree channels use their root project's config |

**Errors:** `400` if `channel_id` is missing or the channel isn't found. `501` if config history isn't configured.

---

### `GET /api/config/history/{id}`

One revision with its content and the diff from the revision before it. Owner-only.

**Query Parameters:**

| Param     | Type    | Required | Description |
|-----------|---------|----------|-------------|
| `against` | integer | no       | A revision of the same file to diff from instead, older or newer |

**Response (200):**
```json
{
  "id": 12,
  "path": "/home/user/.loop/config.json",
  "content": "{\n  \"claude_model\": \"sonnet\"\n}\n",
  "hash": "9b2e…",
  "source": "settings",
  "created_at": "2026-10-09T10:00:00Z",
  "diff": "--- /home/user/.loop/config.json\n+++ /home/user/.loop/config.json\n@@ …"
}
```

`diff` is from `/dev/null` for the oldest revision, and from the `against` revision when it's given.

**Errors:** `400` for an invalid id or `against`, or an `against` revision of another file. `404` if either revision doesn't exist. `501` if config history isn't configured.

---

### `POST /api/config/history/{id}/restore`

Write a revision's content back to its config file. Owner-only. The restore is recorded as a revision with source `restore:<id>`, and a project config that was trusted stays trusted, as with [`PUT /api/config/project`](#put-apiconfigproject).

**Response:** `204 No Content`

**Errors:** `400` for an invalid id. `404` if there's no such revision. `501` if config history isn't configured.

---

### `POST /api/builtins/restore`

Re-seed any missing built-in workflows or prompt shortcuts under `~/.loop/`. Idempotent — entries the user has kept (or modified) are left untouched, so this restores deletions rather than resetting to defaults. Backs the "Restore built-ins" bar in the Settings panel.

**Request Body:**

| Field  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `kind` | string | yes      | `"workflows"` or `"shortcuts"` |

Canonical names per kind:

| Kind | Names |
|------|-------|
| `workflows` | `review-loop`, `review-fix-loop` |
| `shortcuts` | `builtin code review`, `builtin simplify` |

**Response (200):**
```json
{
  "kind": "workflows",
  "added": ["review-fix-loop"],
  "skipped": ["review-loop"]
}
```

`added` lists names that were missing and have now been written back. `skipped` lists names that were already present. Empty arrays are emitted as `[]` (never `null`).

**Errors:** `400` if `kind` is not `"workflows"` or `"shortcuts"`. `500` on filesystem errors.

---

## Playground

The playground stores named HTML/CSS/JS items and broadcasts updates for live rendering in the desktop app's Playground panel. Items can be stored globally (`~/.loop/playground/{name}/`) or per-project (`.loop/playground/{name}/` in the channel's working directory).

All playground endpoints accept optional `scope` and `channel_id` query parameters to target project-scoped items. Without these, operations default to global scope.

### `PUT /api/playground?name=...`

Update a named playground. Stores files and broadcasts a `playground.update` event.

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Playground name (alphanumeric, hyphens, underscores, max 64 chars) |
| `scope` | string | no | `"global"` (default) or `"project"` |
| `channel_id` | string | no | Required when `scope=project` — identifies the project directory |

**Request:**
```json
{
  "html": "<div id='app'></div>",
  "css": "body { margin: 0; background: #111; }",
  "js": "import confetti from 'canvas-confetti'; confetti();",
  "import_map": "{\"imports\":{\"canvas-confetti\":\"https://esm.sh/canvas-confetti\"}}",
  "description": "Added confetti effect"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `html` | string | no | HTML body content (no `<html>`/`<head>`/`<body>` tags) |
| `css` | string | no | CSS styles |
| `js` | string | no | JavaScript ES module code |
| `import_map` | string | no | JSON import map for bare module specifiers |
| `description` | string | no | Brief description (saved as README.md) |

**Response:** `200 OK`

**Errors:** `400` if name is invalid or missing. `500` on file write errors.

### `GET /api/playground?name=...`

Get a named playground's content.

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Playground name |

**Response (200):**
```json
{
  "name": "snake-game",
  "html": "<div id='app'></div>",
  "css": "body { margin: 0; }",
  "js": "console.log('hi')",
  "import_map": "{\"imports\":{}}",
  "description": "Initial setup"
}
```

**Errors:** `400` if name is invalid. `404` if playground not found.

### `GET /api/playground/state?name=...`

A playground's state: a JSON object its page keeps through `window.loop.state`, stored as `state.json` in the playground's dir. A playground without one answers `{}`.

**Errors:** `400` invalid name; `404` no such playground.

---

### `PATCH /api/playground/state?name=...`

Merges the body, a JSON object, into the state; a `null` value removes its key. Answers with the new state and sends a `playground.update` event with `"kind": "state"`, which the playground's panels pass to their page.

**Request:**
```json
{"score": 12, "draft": null}
```

**Errors:** `400` the body isn't a JSON object; `404` no such playground; `413` the state would grow past 1 MiB.

---

### `GET /api/playground/export?name=...`

Export a playground as a standalone HTML file with embedded CSS, JS, and import map.

**Response (200):** `text/html` with `Content-Disposition: attachment; filename="playground-{name}.html"`.

**Errors:** `400` if name is invalid.

### `GET /api/playground/items`

List all playground names from both global and project scopes.

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `channel_id` | string | no | If provided, also includes project-scoped items from the channel's directory |

**Response (200):**
```json
{
  "items": [
    {"name": "snake-game", "scope": "global"},
    {"name": "my-viz", "scope": "project"}
  ]
}
```

Returns `{"items": []}` if no playgrounds exist. Items include a `scope` field indicating whether they are `"global"` or `"project"`.

### `GET /api/playground/serve/{name}`

Serve a global playground as a standalone HTML page (used as iframe `src`).

### `GET /api/playground/serve-project/{channel_id}/{name}/`

Serve a project-scoped playground as a standalone HTML page. Uses path-based routing instead of query parameters so that relative sub-resource URLs (style.css, script.js) resolve correctly via the `<base>` tag.

### `PUT /api/playground/share?name=...`

Expose a playground publicly over a [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/) quick tunnel. Accepts `scope`/`channel_id` like the other playground endpoints. Returns `{ "url": "https://<host>/p/<token>", "token": "<32-hex>" }`. **Idempotent** per resolved playground dir — re-sharing returns the same token. Requires `playground_share.enabled` (else `403`). See [Playground: Public sharing](playground.md#public-sharing).

### `DELETE /api/playground/share?name=...`

Stop sharing a playground; `204` on success. Resolves the dir, so any channel/thread mapping to the same playground can unshare it. The tunnel is torn down when the last share is removed.

### `GET /api/playground/share`

With no `name`: list every active share — `{ "shares": [{ name, scope, channel_id, url }] }`. With `?name=...` (+ optional `scope`/`channel_id`): return the status of that one playground — `{ "shared": bool, "url": string }`, resolved by dir so the answer is identical for every channel/thread mapping to the same playground.

---

## Tickets

The ticket API manages filesystem-backed tickets stored in `.tickets/` within a project directory. Tickets are powered by the [`github.com/radutopala/ticket`](https://github.com/radutopala/ticket) library. See [Kanban Panel](kanban.md) for the frontend UI.

All ticket endpoints require a `dir` query parameter specifying the directory path. The store opened is always `dir/.tickets/` — the same one the `tk` CLI reads in that directory — so a worktree passed as `dir` gets the worktree's own store, not the store of the checkout it was cut from.

### `GET /api/tickets`

List tickets for a project directory.

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `dir` | string | **(required)** Project directory path |
| `status` | string | Filter by status (`open`, `in_progress`, `closed`) |
| `tag` | string | Filter by tag |
| `assignee` | string | Filter by assignee |
| `type` | string | Filter by type (`task`, `bug`, `feature`, `epic`, `chore`) |
| `sort` | string | Sort field (default: `priority`) |
| `reverse` | bool | Reverse sort order |

**Response (200):**
```json
[
  {
    "id": "tic-a1b2c3d4",
    "title": "Fix login bug",
    "description": "Users can't log in with SSO",
    "status": "open",
    "type": "bug",
    "priority": 1,
    "assignee": "",
    "tags": ["auth", "urgent"],
    "deps": [],
    "parent": "",
    "external_ref": "JIRA-1234",
    "design": "",
    "acceptance": "SSO login works for all providers",
    "notes": [
      { "timestamp": "2026-04-11T09:30:00Z", "content": "Reproduced with the Okta provider only" }
    ],
    "created": "2026-04-10T10:00:00Z",
    "updated": "2026-04-10T10:00:00Z"
  }
]
```

---

### `POST /api/tickets`

Create a new ticket.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `dir` | string | yes | Project directory path |
| `title` | string | yes | Ticket title |
| `description` | string | no | Markdown description |
| `type` | string | no | `task` (default), `bug`, `feature`, `epic`, `chore` |
| `priority` | int | no | 0–4 (default: 2) |
| `assignee` | string | no | Assignee name |
| `tags` | string[] | no | Tags |
| `parent` | string | no | Parent ticket ID |
| `external_ref` | string | no | External issue reference |
| `design` | string | no | Design notes |
| `acceptance` | string | no | Acceptance criteria |

**Response (201):** The created ticket object.

---

### `GET /api/tickets/{id}`

Get a single ticket by ID (supports short ID prefix matching).

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `dir` | string | **(required)** Project directory path |

**Response (200):** The ticket object.

**Errors:** `404` if no ticket matches the ID.

---

### `PATCH /api/tickets/{id}`

Update ticket fields. Only provided fields are modified.

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `dir` | string | **(required)** Project directory path |
| `status` | string | New status |
| `title` | string | New title |
| `description` | string | New description |
| `type` | string | New type |
| `priority` | int | New priority (0–4) |
| `assignee` | string | New assignee |
| `tags` | string[] | Replace tags |
| `deps` | string[] | Replace dependency list |
| `parent` | string | New parent ticket ID |
| `external_ref` | string | New external reference |
| `design` | string | New design notes |
| `acceptance` | string | New acceptance criteria |

**Response:** `204 No Content` on success.

**Errors:** `404` if ticket not found; `400` for invalid status/type/priority.

Broadcasts `ticket.updated` WebSocket event.

---

### `DELETE /api/tickets/{id}`

Delete a ticket.

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `dir` | string | **(required)** Project directory path |

**Response:** `204 No Content` on success.

Broadcasts `ticket.deleted` WebSocket event.

---

### `POST /api/tickets/{id}/notes`

Append a timestamped note to a ticket — the same thing `tk add-note` does. Notes are append-only: no endpoint edits or removes one, and `PATCH` leaves them untouched. The timestamp is the server's current time in UTC.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `dir` | string | yes | Project directory path |
| `content` | string | yes | Note text (Markdown); leading and trailing whitespace is trimmed |

**Response (201):** The updated ticket object, with the new note last in `notes`.

**Errors:** `400` for a missing `dir` or blank `content`; `404` if ticket not found.

Broadcasts `ticket.updated` WebSocket event.

---

### `POST /api/tickets/{id}/assign`

Assign a worktree to a ticket. This performs an atomic multi-step operation:

1. Claims the ticket (`open` → `in_progress`) with file locking
2. Detects the current branch of the parent project
3. Creates a git worktree on branch `tk-<ticket-id>`
4. Creates a thread named after the ticket title
5. Sets the ticket's assignee to the thread name
6. Optionally auto-starts an agent with the ticket description

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `dir` | string | yes | Project directory path |
| `channel_id` | string | yes | Parent channel ID |

**Response (200):**
```json
{
  "thread_id": "thread-abc123",
  "worktree_path": "/path/to/worktrees/tk-a1b2c3d4"
}
```

**Errors:** `409` if the ticket is not in `open` status (already claimed).

## Gate Approvals

When the security gate is enabled, agent containers that trip an `approve` rule block waiting for a human decision. The gate broadcasts `gate.approval_requested` on WebSocket; the UI resolves it back with this endpoint. See [Security Gate](configuration.md#security-gate) for the rule model.

### `GET /api/gate/approvals`

Snapshot every pending approval the daemon currently knows about, aggregated across all live agent containers. The renderer calls this on every WebSocket `onOpen` (page reload, network blip, daemon restart) to reconcile its `gateApprovals` map and the electron dock-bouncer's pending set against the source of truth — any `req_id` the client thought was pending but is missing from the snapshot is treated as resolved, and any snapshot entry the client did not know about is added.

**Response (200):**

```json
{
  "approvals": [
    {
      "req_id": "gate-req-8f1c...",
      "container_id": "ab12cd34ef56",
      "channel_id": "C0123456",
      "kind": "docker-http",
      "target": "POST /containers/abc123/exec",
      "source": "chat",
      "message": "agent wants to exec into a container",
      "details": { "cmd": "bash, -c, whoami", "user": "root" }
    }
  ]
}
```

| Field          | Type   | Description |
|----------------|--------|-------------|
| `req_id`       | string | Correlation id, same one carried on `gate.approval_requested` and accepted by [`POST /api/gate/approvals/{id}`](#post-apigateapprovalsid) |
| `container_id` | string | Docker container id that owns the request — useful for cross-referencing with audit logs |
| `channel_id`   | string | Channel the approval is prompting on |
| `kind`         | string | Same set as the event payload (`"connect"`, `"execve"`, `"file"`, `"docker-http"`, `"docker-body"`) |
| `target`       | string | Human-readable target |
| `source`       | string | Origin within the container — `"chat"` for the entrypoint agent, `"terminal:<leafId>"` for a specific terminal pane. Omitted when unknown |
| `message`      | string | Rule's `message` field (omitted when empty) |
| `details`      | object | Structured body summary (omitted when empty); same shape as `gate.approval_requested.details` |

Returns an empty `approvals` array when nothing is pending. Returns `501` if the gate approval resolver is not configured (gate disabled).

### `POST /api/gate/approvals/{id}`

Resolve a pending gate approval by request id. The `id` is the `req_id` from the `gate.approval_requested` event.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `decision` | string | yes | One of `"once"`, `"session"`, or `"deny"`. `once` lets the current syscall through; `session` caches the allow for the container lifetime; `deny` rejects the syscall |
| `author_id` | string | no | Clicking user's id. Falls back to `local.DefaultAuthorID` when empty — Discord/Slack handlers pass the platform user id; the local desktop omits it |

```json
{ "decision": "once" }
```

**Response:** `204` No Content on success. The gate broadcasts `gate.approval_resolved` with the decision and actor so the UI can dismiss the card.

**Errors:** `400` if the path id is missing, the body isn't valid JSON, or `decision` is empty. `404` if no pending request matches the id (already resolved, expired, or never existed). `501` if the gate approval resolver is not configured.

### `POST /api/gate/container-approval`

Inbound call from the in-container docker proxy (`loop dockerproxy`) or seccomp-gate parent (`loop syscallwrap`) when a rule matches `approve`. The server looks up the owning per-container `Manager` by the bearer token, renders an approval prompt on the associated chat channel, and blocks until the user clicks. Not intended to be called by UI clients — the click resolve path is [`POST /api/gate/approvals/{id}`](#post-apigateapprovalsid).

**Headers:**

| Header | Value |
|---|---|
| `Authorization` | `Bearer <gate token>` — the 32-byte-hex bearer the runner minted for this container, read from `/run/loop/gate-token` (root-only) inside it. Compared in constant time |
| `Content-Type` | `application/json` |

**Request Body:**

| Field | Type | Description |
|---|---|---|
| `kind` | string | `"docker-http"` / `"docker-body"` for the proxy; gate-trap categories (`"connect"`, `"execve"`, `"file"`) for the seccomp path |
| `target` | string | Human-readable target of the operation (`"GET /containers/json"`, `"/etc/passwd"`, etc.) |
| `message` | string | Rule's `message` field — shown to the user verbatim |
| `cache_key` | string | Key the `Manager` uses when the user picks "Allow for session" |

**Response (200):**

```json
{ "decision": "allow", "actor": "u-42", "reason": "cache-hit" }
```

| Field | Type | Description |
|---|---|---|
| `decision` | string | `"allow"` or `"deny"` |
| `actor` | string | Clicking user's id (empty on local desktop) |
| `reason` | string | Free-form tag (e.g. `"cache-hit"`, `"rate-limited"`) |

**Errors:** `401` if the `Authorization` header is missing, malformed, or the token doesn't match any live container. `400` if the body isn't valid JSON. `503` if `cfg.Gates.Agentgate.Enabled` and `cfg.Gates.DockerProxy.Enabled` are both false (no `ContainerApprovalRouter` is constructed — neither enforcement layer is running, so no legitimate caller should hit this endpoint).

## Workflows

Declarative DAG-based workflow execution. See [Workflows](workflows.md) for architecture details.

### `GET /api/workflows`

List all available workflow definitions from the merged config.

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `dir_path` | string | Optional project directory for project-level config merge |
| `channel_id` | string | Optional channel ID — resolves `dir_path` and parent from DB for three-layer config merge (global → parent → worktree) |

**Response (200):** Each definition is tagged with its config `scope` (`"global"` or `"project"`) so the panel can group them. With no channel/dir every entry is global; with a channel, names present only in the project's config are `"project"`.
```json
[
  {
    "scope": "global",
    "name": "code-review",
    "description": "Review branch changes",
    "inputs": {},
    "nodes": [
      { "id": "diff", "type": "bash", "script": "git diff main...HEAD" },
      { "id": "review", "type": "prompt", "depends_on": ["diff"], "prompt": "Review:\n\n{{.NodeOutputs.diff}}" }
    ]
  }
]
```

**Errors:** `501` if the workflow engine is not configured.

### `POST /api/workflows`

Add, update, or delete a workflow definition in the global or project config file.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action` | string | Yes | `"add"`, `"update"`, or `"delete"` |
| `scope` | string | No | `"global"` (default) or `"project"` |
| `channel_id` | string | For project scope | Channel ID to resolve project directory |
| `workflow` | object | For add/update | Full workflow definition (`name`, `description`, `nodes`, `inputs`) |
| `name` | string | For delete | Workflow name to delete |

**Response:** `204 No Content`

**Errors:** `400` invalid request, `404` workflow not found (update/delete), `409` duplicate name (add).

### `POST /api/workflows/runs`

Start a new workflow run.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `workflow_name` | string | yes | Name of the workflow to run |
| `channel_id` | string | no | Channel context for prompt nodes |
| `dir_path` | string | no | Project directory for bash/prompt nodes |
| `inputs` | object | no | Input values keyed by input name |

**Response (201):**
```json
{
  "run_id": "wfr-a1b2c3d4e5f67890"
}
```

**Errors:** `400` if `workflow_name` is missing or request body is invalid JSON. `500` on engine errors (workflow not found, missing required inputs, etc.). `501` if the workflow engine is not configured.

### `GET /api/workflows/runs`

List workflow runs.

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `channel_id` | string | Optional filter by channel |
| `limit` | int | Max results per page (default 50, capped at 1000) |
| `offset` | int | Number of rows to skip for pagination (default 0; non-positive values are treated as 0) |

When no `channel_id` is provided, each run is enriched with `channel_name` and `channel_worktree` resolved by walking up the parent chain to the nearest named ancestor — the global Workflows panel uses this to label unnamed threads. The list view paginates via infinite scroll (see [Workflows](workflows.md)).

**Response (200):**
```json
[
  {
    "id": "wfr-a1b2c3d4e5f67890",
    "workflow_name": "code-review",
    "channel_id": "",
    "status": "completed",
    "started_at": "2026-04-11T10:00:00Z",
    "finished_at": "2026-04-11T10:02:30Z",
    "channel_name": "dm",
    "channel_worktree": false
  }
]
```

**Errors:** `501` if the workflow engine is not configured.

### `GET /api/workflows/runs/{id}`

Get a workflow run with all node statuses and outputs.

**Response (200):**
```json
{
  "run": {
    "id": "wfr-a1b2c3d4e5f67890",
    "workflow_name": "code-review",
    "status": "completed",
    "inputs": "{\"issue_url\":\"https://...\"}",
    "workflow_def": "{\"name\":\"code-review\",\"nodes\":[...]}",
    "started_at": "2026-04-11T10:00:00Z",
    "finished_at": "2026-04-11T10:02:30Z"
  },
  "node_runs": [
    {
      "run_id": "wfr-a1b2c3d4e5f67890",
      "node_id": "diff",
      "status": "success",
      "input": "git diff main...HEAD",
      "session_id": "",
      "output": "+added line\n-removed line",
      "attempt": 1,
      "started_at": "2026-04-11T10:00:00Z",
      "finished_at": "2026-04-11T10:00:05Z",
      "last_heartbeat_at": "2026-04-11T10:00:04Z"
    }
  ]
}
```

**Errors:** `404` if the run does not exist. `501` if the workflow engine is not configured.

### `POST /api/workflows/runs/{id}/resume`

Resume a paused workflow run (e.g. after an approval node). The response text becomes the approval node's output.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `response` | string | no | Response text for the approval node (defaults to `"approved"` if empty) |

```json
{ "response": "approved" }
```

**Response:** `204` No Content on success.

**Errors:** `400` if request body is invalid JSON. `500` if no pending approval exists for the run. `501` if the workflow engine is not configured.

### `POST /api/workflows/runs/{id}/cancel`

Cancel a running workflow. Cancels the context for all active nodes.

**Response:** `204` No Content on success.

**Errors:** `500` on engine errors. `501` if the workflow engine is not configured.

### `POST /api/workflows/runs/{id}/retry`

Retry a completed, failed, or cancelled workflow run. Creates a new run with the same workflow definition and inputs.

**Response (201):**
```json
{
  "run_id": "wfr-b2c3d4e5f6a78901"
}
```

**Errors:** `500` on engine errors (run not found, run still active, workflow definition not found, etc.). `501` if the workflow engine is not configured.

### `DELETE /api/workflows/runs/{id}`

Delete a workflow run. If the run is active (running or paused), it is cancelled first.

**Response:** `204` No Content on success.

**Errors:** `500` on engine errors. `501` if the workflow engine is not configured.
