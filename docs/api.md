# HTTP API Reference

Loop exposes a lightweight HTTP API for managing channels, threads, messages, tasks, files, memory, and real-time events. All endpoints are prefixed with `/api/`.

**Related docs:** [Terminal WebSocket](terminal.md) | [Events System](events.md) | [Memory System](memory.md)

## General

- **CORS:** All responses include `Access-Control-Allow-Origin: *`, `Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS`, and `Access-Control-Allow-Headers: Content-Type`. Preflight `OPTIONS` requests return `204 No Content`.
- **Content-Type:** JSON endpoints return `application/json`. File-reading endpoints return `text/plain; charset=utf-8`.
- **Error responses:** Plain text body with the appropriate HTTP status code.

### Common Error Codes

| Code | Meaning |
|------|---------|
| 400  | Bad request -- missing/invalid parameters or request body |
| 404  | Resource not found |
| 413  | Request entity too large (file operations) |
| 500  | Internal server error |
| 501  | Feature not configured (service dependency is nil) |
| 503  | Service unavailable (commands not configured) |

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
    "branch": "main",
    "commit": "abc1234",
    "worktree": false
  }
]
```

**Behavior notes:**
- When a channel has no `dir_path`, falls back to `~/.loop/{channel_id}/work`.
- `container_running` is determined by querying the Docker daemon for running containers.
- `agent_running` indicates whether an active Claude agent run exists for the channel.
- `branch` is resolved by running `git rev-parse --abbrev-ref HEAD` in the channel's directory.
- `commit` is the short commit hash from `git rev-parse --short HEAD`.
- `worktree` is true for threads created via `POST /api/worktrees`.

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

**Behavior notes:** Deletes child threads (channels with matching `parent_id`) before deleting the channel itself.

**Errors:** `404` if channel not found. `501` if channel deletion is not configured.

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

### `DELETE /api/threads/{id}`

Delete a thread.

**Path Parameters:**

| Param | Type   | Description |
|-------|--------|-------------|
| `id`  | string | Thread ID to delete |

**Response:** `204 No Content`

**Behavior notes:** Removes the thread's MCP config file, deletes from the chat platform (if a creator is configured), and removes from the database.

**Errors:** `501` if thread deletion is not configured.

---

## Messages

### `POST /api/messages`

Send a message to a channel. When an orchestrator is configured, routes through it asynchronously so Claude can respond.

**Request:**
```json
{
  "channel_id": "abc123",
  "content": "Hello, bot!",
  "mode": "plan"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Target channel or thread ID |
| `content`    | string | yes      | Message text |
| `mode`       | string | no       | Agent mode hint (e.g. `"plan"`) |

**Response:** `204 No Content`

**Behavior notes:**
- When an `IncomingMessageHandler` is set, the message is dispatched asynchronously with a detached context (the HTTP response returns immediately).
- When no handler is set, falls back to direct `PostMessage` via the configured message sender.

**Errors:** `400` if `channel_id` or `content` is empty. `501` if message sending is not configured and no handler is set.

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
      "created_at": "2026-01-01T00:00:00Z"
    }
  ],
  "next_cursor": 41
}
```

**Behavior notes:**
- **Cursor mode:** Fetches `limit+1` messages to determine if more exist. If so, `next_cursor` is set to the last returned message's ID. Messages are ordered oldest-first.
- **Around mode:** Uses a UNION ALL query (half before + half after the target message ID), ordered by `id ASC`. `next_cursor` is not set in around mode.

**Errors:** `400` if query params are invalid. `501` if message listing is not configured.

---

### `GET /api/messages/search`

Full-text search across all messages using case-insensitive `LIKE %query%`.

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
  "auto_delete_sec": 3600
}
```

| Field            | Type   | Required | Description |
|------------------|--------|----------|-------------|
| `channel_id`     | string | yes      | Channel to run the task in |
| `schedule`        | string | yes      | Cron expression, Go duration, or RFC3339 timestamp |
| `type`           | string | yes      | `cron`, `interval`, or `once` |
| `prompt`         | string | yes      | Prompt text for the agent |
| `template_name`  | string | no       | Template identifier for deduplication |
| `auto_delete_sec` | int   | no       | Auto-delete thread after N seconds |

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
    "auto_delete_sec": 3600
  }
]
```

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
  "auto_delete_sec": 7200
}
```

All fields are optional (use JSON `null` or omit). When `enabled` is provided, it is applied separately via `SetTaskEnabled`. Other fields are applied via `EditTask`.

**Response:** `200 OK` (empty body)

**Errors:** `400` if no fields provided or ID is invalid.

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

## Files

All file endpoints resolve the channel's `dir_path` from the database, falling back to `~/.loop/{channel_id}/work` when the channel has no explicit path.

### `GET /api/channels/{id}/files`

List directory contents for a channel's working directory.

**Query Parameters:**

| Param  | Type   | Default | Description |
|--------|--------|---------|-------------|
| `path` | string | `"."`   | Relative path within the channel's directory |

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

**Response (200):**
- **Text files:** `Content-Type: text/plain; charset=utf-8` with file contents as body.
- **Binary files:** Empty body with `X-File-Binary: true` header and `Content-Length` set.

**Behavior notes:**
- Binary detection checks the first 512 bytes for null bytes.
- Maximum file size is **5 MB** (5,242,880 bytes). Larger files return `413`.
- Path validation rejects absolute paths, `..` traversal, and symlink escapes.

**Errors:** `400` if path is invalid. `404` if file not found. `413` if file too large.

---

### `PUT /api/channels/{id}/file`

Write content to a file.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the file |

**Request body:** Raw file content (not JSON). Maximum **5 MB**.

**Response (200):**
```json
{"ok": true}
```

**Behavior notes:** Preserves original file permissions if the file already exists; defaults to `0644` for new files.

**Errors:** `400` if path is invalid. `413` if content exceeds 5 MB.

---

### `DELETE /api/channels/{id}/file`

Delete a file.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the file |

**Response (200):**
```json
{"ok": true}
```

**Errors:** `400` if path is invalid or target is a directory. `404` if file not found.

---

### Path Validation

All file operations validate the relative path against the channel's root directory:

1. Rejects absolute paths.
2. Rejects `..` traversal components.
3. Resolves symlinks and verifies the real path stays under the root directory.
4. For writes to nonexistent files, validates the parent directory instead.

---

## Diff

### `GET /api/channels/{id}/diff`

Get git diff information for a channel's working directory. Includes both tracked changes and untracked files.

**Response (200):**
```json
{
  "files": [
    {"path": "main.go", "additions": 5, "deletions": 2, "binary": false},
    {"path": "image.png", "additions": 0, "deletions": 0, "binary": true}
  ],
  "diff": "diff --git a/main.go b/main.go\n...",
  "total_additions": 5,
  "total_deletions": 2
}
```

**Behavior notes:**
- Runs `git diff --numstat` for file-level statistics and `git diff` for unified diff text.
- Runs `git ls-files --others --exclude-standard` to include untracked files.
- Untracked files generate synthetic diff patches (showing all lines as additions).
- Binary detection for untracked files checks the first 512 bytes for null bytes.
- If the directory is not a git repo, returns an empty `files` array.
- Files are sorted alphabetically by path.

**Errors:** `404` if channel not found.

---

## Branches & Worktrees

### `GET /api/channels/{id}/branches`

List local git branches and worktrees for a channel's directory.

**Response (200):**
```json
{
  "branches": ["main", "feature/foo"],
  "current": "main",
  "worktrees": [
    {"path": "/project/.worktrees/wt1", "branch": "feature/bar"}
  ]
}
```

**Behavior notes:**
- Branches checked out in other worktrees are excluded from the `branches` list (git won't allow switching to them).
- The main worktree is excluded from the `worktrees` list.

### `POST /api/channels/{id}/branches/switch`

Switch the git branch in a channel's working directory.

**Request:** `{"branch": "feature/foo"}`

**Response (200):** `{"ok": true}`

**Errors:** `400` if branch name is invalid or missing. `500` if git checkout fails (e.g. uncommitted changes).

### `POST /api/channels/{id}/branches/create`

Create and checkout a new branch.

**Request:** `{"name": "feature/new", "from": "main"}` (`from` is optional)

**Response (200):** `{"ok": true}`

### `POST /api/worktrees`

Create a git worktree as a new thread. The worktree gets its own branch (`worktree/<name>`) based on the selected branch, inherits the parent's session for `--fork-session`, and appears in the sidebar as a thread.

**Request:**
```json
{
  "channel_id": "parent-channel-id",
  "branch": "main",
  "name": "optional-name"
}
```

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

## WebSocket Endpoints

### `GET /api/ws`

Real-time events WebSocket. See [Events System](events.md) for the full protocol.

**Errors:** `501` if events hub is not configured.

---

### `GET /api/ws/terminal`

Interactive terminal WebSocket. See [Terminal WebSocket](terminal.md) for the full protocol.

**Errors:** `501` if terminal manager is not configured.

---

## Browser

### `POST /api/browser/ensure`

Start Chrome sidecar for a channel. Called by the `loop-browser` MCP server to lazily start Chrome on first tool use.

**Request:**
```json
{"channel_id": "ch-abc123"}
```

**Responses:**

| Code | Description |
|------|-------------|
| 200 | Chrome started or already running |
| 400 | Missing `channel_id` |
| 500 | Chrome start failed |
| 503 | Browser not configured (`browser_enabled: false`) |

---

### `POST /api/browser/touch`

Signal that a browser is still in use, preventing idle shutdown.

**Request:**
```json
{"channel_id": "ch-abc123"}
```

**Responses:**

| Code | Description |
|------|-------------|
| 200 | Touch recorded |
| 400 | Missing `channel_id` |
| 503 | Browser not configured |

---

### `GET /api/ws/browser`

WebSocket endpoint for browser screencast and control.

Returns 503 if browser is not configured. Once connected, supports JSON messages for start/stop/navigate/screencast/input/page_info/reload/back/forward. Binary messages carry JPEG screencast frames.
