# HTTP API Reference

Loop exposes a lightweight HTTP API for managing channels, threads, messages, tasks, tickets, files, memory, and real-time events. All endpoints are prefixed with `/api/`.

**Related docs:** [Terminal WebSocket](terminal.md) | [Events System](events.md) | [Memory System](memory.md) | [Kanban Panel](kanban.md)

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

**Behavior notes:** Removes the thread's MCP config file, deletes from the chat platform (if a creator is configured), and removes from the database. If the thread has an associated git worktree, the worktree and its branch are cleaned up automatically.

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
- The orchestrator's `processTriggeredMessage` has a race guard that aborts before any side effects (stop button, typing indicator, agent run) if the trigger message is no longer present in the channel's recent history — see [Orchestrator - Queued-message deletion race guard](orchestrator.md#queued-message-deletion-race-guard).

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
  ]
}
```

Sessions are sorted by modification time (newest first). `last_message` is extracted from the last assistant or user message in the JSONL file (last 32KB reverse-scanned). `current_session_id` is the session currently associated with the channel.

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
  "workflow_inputs": "{}"
}
```

All fields are optional (use JSON `null` or omit). When `enabled` is provided, it is applied separately via `SetTaskEnabled`. Other fields are applied via `EditTask`. Set `workflow_name` to convert a prompt task into a workflow task (or clear it with an empty string to revert).

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
| `root` | int    | no       | Root directory index (0 = primary, 1+ = extra directories) |

**Request body:** Raw file content (not JSON). Maximum **5 MB**.

**Response (200):**
```json
{"ok": true}
```

**Behavior notes:** Preserves original file permissions if the file already exists; defaults to `0644` for new files.

**Errors:** `400` if path is invalid. `413` if content exceeds 5 MB.

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
    {"path": "/project/.worktrees/wt1", "branch": "feature/bar", "thread_id": "thread-id-if-imported"}
  ]
}
```

**Behavior notes:**
- Branches checked out in other worktrees are excluded from the `branches` list (git won't allow switching to them).
- The main worktree is excluded from the `worktrees` list.
- `thread_id` is populated when the worktree has been imported as a thread (via `POST /api/worktrees` or `POST /api/worktrees/import`).

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

**Errors:** `400` if branch name is invalid.

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
- If `thread_id` is provided, also deletes the thread record from the database and broadcasts a `channel.deleted` event.
- If the git worktree removal fails (e.g. path already gone), returns `500`.

**Errors:** `400` if `channel_id` or `worktree_path` is missing, or if the channel is not found.

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
| `get_element_refs` | — | Get accessibility tree elements |
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
| `switch_tab` | `target_id` | Switch to a tab |
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

### `PUT /api/config/project`

Save project config for a channel.

**Query Parameters:**

| Param        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `channel_id` | string | yes      | Channel ID |

**Request:**
```json
{
  "raw": "{\n  \"claude_model\": \"claude-opus-4-6\"\n}"
}
```

**Response (200):**
```json
{"ok": true}
```

Creates the `.loop/` directory and config file if they don't exist.

**Errors:** `400` if `channel_id` is missing or HJSON is invalid. `404` if channel not found.

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

---

## Tickets

The ticket API manages filesystem-backed tickets stored in `.tickets/` within a project directory. Tickets are powered by the [`github.com/radutopala/ticket`](https://github.com/radutopala/ticket) library. See [Kanban Panel](kanban.md) for the frontend UI.

All ticket endpoints require a `dir` query parameter specifying the project directory path.

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

## Workflows

Declarative DAG-based workflow execution. See [Workflows](workflows.md) for architecture details.

### `GET /api/workflows`

List all available workflow definitions from the merged config.

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `dir_path` | string | Optional project directory for project-level config merge |
| `channel_id` | string | Optional channel ID — resolves `dir_path` and parent from DB for three-layer config merge (global → parent → worktree) |

**Response (200):**
```json
[
  {
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
