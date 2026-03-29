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

Delete a file.

**Query Parameters:**

| Param  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `path` | string | yes      | Relative path to the file |
| `root` | int    | no       | Root directory index (0 = primary, 1+ = extra directories) |

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
    "agent_id": "agent-0",
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
| `id`  | string | Agent ID (e.g. `"agent-0"`) |

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
  "from_agent_id": "agent-0",
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
  "from_agent_id": "agent-0",
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

The playground stores named HTML/CSS/JS items globally in `~/.loop/playground/{name}/` and broadcasts updates for live rendering in the desktop app's Playground panel.

### `PUT /api/playground?name=...`

Update a named playground. Stores files and broadcasts a `playground.update` event.

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Playground name (alphanumeric, hyphens, underscores, max 64 chars) |

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

List all playground names.

**Response (200):**
```json
{
  "items": ["snake-game", "dashboard", "chart-demo"]
}
```

Returns `{"items": []}` if no playgrounds exist.
