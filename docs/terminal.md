# Terminal WebSocket System

The terminal system provides interactive PTY sessions inside Docker containers or directly on the host machine, accessible via a WebSocket connection at `GET /api/ws/terminal`.

**Related docs:** [HTTP API](api.md) | [Events System](events.md)

---

## WebSocket Protocol

The terminal WebSocket uses a bidirectional JSON-based control protocol layered on top of the standard WebSocket framing:

- **Client to server:** JSON text frames (control messages)
- **Server to client:** JSON text frames (status messages) and binary frames (terminal output)

The WebSocket upgrader accepts all origins (`CheckOrigin` returns `true`).

---

## Client-to-Server Messages

All messages are JSON objects with a `type` field that determines the operation.

### `create`

Create a new terminal session. Detaches any currently attached session first.

```json
{
  "type": "create",
  "container_id": "abc123def",
  "channel_id": "chan_001",
  "cmd": ["/bin/bash", "-l"],
  "target": "host",
  "rows": 24,
  "cols": 80
}
```

| Field          | Type     | Required | Description |
|----------------|----------|----------|-------------|
| `type`         | string   | yes      | Must be `"create"` |
| `container_id` | string   | no       | Docker container ID (for agent target) |
| `channel_id`   | string   | no       | Channel ID; used to resolve container via `ContainerFinder` |
| `cmd`          | string[] | no       | Command to execute; defaults to `/bin/sh` (agent) or user's shell (host) |
| `target`       | string   | no       | `"host"` or `"agent"` (default) |
| `rows`         | uint     | no       | Initial terminal rows; applied via resize after creation |
| `cols`         | uint     | no       | Initial terminal columns |

**Agent target behavior:**
- If `container_id` is empty and `channel_id` is provided, resolves the container via `ContainerFinder.FindContainerByChannel`.
- Looks up the channel's `dir_path` and `session_id` from the database.
- For threads, detects if the thread uses the parent's Claude session and sets a fork flag.
- When no explicit `cmd` is given and a `cmdBuilder` is configured, sends the interactive Claude command as shell input after session creation.
- Maximum of 64 command arguments; empty arguments are rejected.

**Host target behavior:**
- Resolves the working directory from the channel's `dir_path`. Falls back to parent channel's `dir_path` for threads, then to `~/.loop/{channel_id}/work`, and finally to `$HOME`.
- Default shell: `$SHELL` env var, then `/bin/zsh` if available, then `/bin/sh`. On Windows: `$COMSPEC`, then `powershell.exe`, then `cmd.exe`.
- Default shell arguments: `-l` (login shell) on Unix; none on Windows.

---

### `attach`

Re-attach to an existing session. Provides ring buffer history replay.

```json
{
  "type": "attach",
  "session_id": "a1b2c3d4"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `type`       | string | yes      | Must be `"attach"` |
| `session_id` | string | yes      | Session ID to attach to |

**Behavior:**
- Detaches any currently attached session first.
- Tries the agent manager first; if that fails or is nil, tries the host manager.
- On success, replays the ring buffer contents as binary output before streaming live output.

---

### `input`

Send user input to the active terminal session.

```json
{
  "type": "input",
  "data": "bHMgLWxhCg=="
}
```

| Field  | Type   | Required | Description |
|--------|--------|----------|-------------|
| `type` | string | yes      | Must be `"input"` |
| `data` | string | yes      | Base64-encoded input bytes |

**Errors:** Returns `no_session` if no active session; `invalid_input` if base64 decoding fails.

---

### `resize`

Resize the PTY of the active session.

```json
{
  "type": "resize",
  "rows": 40,
  "cols": 120
}
```

| Field  | Type | Required | Description |
|--------|------|----------|-------------|
| `type` | string | yes    | Must be `"resize"` |
| `rows` | uint | yes      | New terminal height |
| `cols` | uint | yes      | New terminal width |

**Errors:** Returns `no_session` if no active session; `missing_field` if rows or cols is 0.

---

### `stop`

Stop the active session and remove its container (agent sessions only). Used when shutting down a channel's terminal entirely.

```json
{
  "type": "stop",
  "session_id": "a1b2c3d4"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `type`       | string | yes      | Must be `"stop"` |
| `session_id` | string | no       | Can target a detached session; defaults to current active session |

**Behavior:**
- Calls `StopSession` on the appropriate manager to close the exec connection.
- For agent sessions, removes the container via `ContainerStopper.ContainerRemove` so the channel list API reflects the updated state.
- For host sessions, skips container removal.

---

### `close`

Close the exec session without removing the container. Used when closing an individual terminal pane while keeping the container alive.

```json
{
  "type": "close",
  "session_id": "a1b2c3d4"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `type`       | string | yes      | Must be `"close"` |
| `session_id` | string | no       | Defaults to current active session |

**Response:** Sends a `stopped` status message.

---

### `kill`

Remove the container for a channel without requiring an active terminal session. Used when the terminal panel is closed and no panes are open.

```json
{
  "type": "kill",
  "channel_id": "chan_001"
}
```

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `type`       | string | yes      | Must be `"kill"` |
| `channel_id` | string | yes      | Channel whose container should be removed |

**Behavior:**
- If there is an active agent session, stops it first.
- Resolves the channel to a container via `ContainerFinder` and removes it.
- If no container is found, still reports success.
- Suppresses "already in progress" errors from concurrent removal.

---

## Server-to-Client Messages

### Status Messages (JSON text frames)

```json
{
  "type": "created",
  "session_id": "a1b2c3d4",
  "message": "",
  "error_code": ""
}
```

| Type       | Description |
|------------|-------------|
| `created`  | Session created successfully; includes `session_id` |
| `attached` | Session attached successfully; includes `session_id` |
| `error`    | Operation failed; includes `message` and `error_code` |
| `closed`   | Session output stream ended (exec process exited or done channel closed) |
| `stopped`  | Session explicitly stopped via `stop`, `close`, or `kill` message |

### Terminal Output (binary frames)

Raw PTY output is sent as WebSocket binary frames. The client should render these bytes in a terminal emulator (e.g., xterm.js).

---

## Error Codes

| Code              | Description |
|-------------------|-------------|
| `invalid_json`    | Could not parse the control message as JSON |
| `no_session`      | Operation requires an active session but none is attached |
| `missing_field`   | A required field is missing (e.g., `session_id`, `rows`, `cols`, `channel_id`) |
| `invalid_input`   | Input data is invalid (e.g., bad base64, empty command argument, too many args) |
| `session_failed`  | Session operation failed (container not found, exec error, etc.) |
| `unknown_message` | Unrecognized message type |

---

## Session Lifecycle

```
create ──> readLoop starts ──> fan-out to attached clients
                                    │
                              WS disconnect
                                    │
                              detachCurrent()
                              (session survives)
                                    │
                              WS reconnect
                                    │
                              attach ──> replay ring buffer ──> resume streaming
                                    │
                              explicit close/stop
                                    │
                              StopSession() ──> exec connection closed
                                              ──> all client channels closed
                                              ──> session removed from registry
```

1. **Create:** A new Docker exec (or host PTY) is started with a TTY. The session is registered in the manager and a read loop goroutine begins.
2. **Read loop:** Reads from the exec connection in 4096-byte chunks. Each chunk is written to the ring buffer and fanned out to all attached client channels (buffered, capacity 64). Slow consumers have output dropped with a warning log.
3. **Attach:** Registers a new client channel on the session. The ring buffer contents are replayed immediately, followed by live output streaming.
4. **Detach on WS close:** When the WebSocket disconnects, the session is detached (not stopped). The exec process and ring buffer continue running so the session can be reattached later.
5. **Reattach:** A new WebSocket connection can attach to the same session ID. The ring buffer provides the scrollback history.
6. **Kill on explicit close:** Only `stop`, `close`, or `kill` messages terminate the exec process. `StopSession` closes the exec connection, closes all client channels, and removes the session from the manager.

---

## Docker Exec Sessions

Managed by `DockerExecClient` which wraps the Docker SDK exec API.

- **ExecCreate:** Creates an exec process inside a running container with `Tty: true`, stdin/stdout/stderr attached. Runs as the host user (`$USER` env var) matching the container's non-root `agent` user. Default command is `/bin/sh`.
- **ExecAttach:** Attaches to the exec process and returns an `io.ReadWriteCloser` wrapping the Docker `HijackedResponse`. Reads use the buffered reader; writes go to the underlying connection.
- **ExecResize:** Changes PTY dimensions via the Docker exec resize API.

The `hijackedConn` adapter wraps the Docker SDK's `HijackedResponse` as a standard `io.ReadWriteCloser`.

---

## Host PTY Sessions

Managed by `HostExecClient` which runs shell processes directly on the host machine.

### Unix (Darwin/Linux)

Uses `creack/pty` to start processes with a pseudo-terminal:

- **ExecCreate:** Prepares the command with `Setsid: true` (new process group). Validates the command via `exec.LookPath`.
- **ExecAttach:** Starts the process via `pty.Start` and returns a `hostPTYConn` wrapping the PTY file descriptor.
- **ExecResize:** Uses `pty.Setsize` to change the PTY window size.
- **Cleanup (hostPTYConn.Close):**
  1. Sends `SIGHUP` to the process group (`syscall.Kill(-pid, SIGHUP)`)
  2. Waits up to 3 seconds for the process to exit
  3. If still running, sends `SIGKILL` to the process group
  4. Closes the PTY file descriptor
  5. Uses `sync.Once` to ensure cleanup runs exactly once

### Windows

Uses `UserExistsError/conpty` for Windows ConPTY support:

- **ExecCreate:** Builds a command line string with proper quoting. Validates via `exec.LookPath`.
- **ExecAttach:** Starts the process via `conpty.Start` with working directory and environment options. Requires Windows 10 1809+.
- **ExecResize:** Uses `conpty.Resize(width, height)` (note: width before height).
- **Cleanup (hostConPTYConn.Close):** Calls `conpty.Close` with `sync.Once`.

---

## Ring Buffer

`RingBuffer` is a fixed-size circular byte buffer used for terminal scrollback. Default size is **1 MB** (1,048,576 bytes).

- **Thread-safe:** All operations are protected by a `sync.Mutex`.
- **Circular writes:** When full, new data overwrites the oldest bytes. The write position wraps around.
- **History replay:** `Bytes()` returns all buffered data in chronological order, handling the wrap-around case.
- **Methods:** `Write(p []byte)`, `Bytes() []byte`, `Len() int`, `Reset()`.

When data larger than the buffer is written, only the last `size` bytes are kept.

---

## Session Registry

Sessions are stored in a `map[string]*Session` within the `Manager`, keyed by randomly generated 8-character hex IDs (4 random bytes).

The `ManagerAdapter` bridges the internal `terminal.Manager` to the `api.TerminalManager` interface:

- `CreateSession` creates the session, immediately attaches a client channel, and returns all fields decomposed.
- `AttachSession` re-attaches to an existing session and returns the output channel, history, and done channel.
- `DetachSession`, `SendInput`, `Resize`, `StopSession` delegate directly.

---

## Agent vs Host Terminal Differences

| Aspect | Agent (Docker) | Host |
|--------|---------------|------|
| Backend | Docker exec API | `creack/pty` (Unix) / ConPTY (Windows) |
| Container ID | Docker container ID | Working directory path |
| Default shell | `/bin/sh` | `$SHELL` / `/bin/zsh` / `/bin/sh` |
| Default args | none | `-l` (login shell) |
| Container removal | Yes (on `stop`/`kill`) | No |
| User | `$USER` env var | Current process user |
| Cleanup | Exec connection close | SIGHUP -> 3s -> SIGKILL |
| Interactive cmd | Auto-injects Claude command | None |

---

## Container Ensurer

The `ContainerFinder` interface resolves a `channel_id` (and optional `dir_path`) to a running Docker container ID. This allows the terminal handler to create sessions by channel rather than requiring the client to know the container ID.

The `ContainerStopper` interface removes containers by ID, used during `stop` and `kill` operations.

---

## Idle Timeout

Sessions can be configured with an idle timeout via `Manager.SetIdleTimeout`. When set, a timer resets on each output read. If no output is received within the timeout duration, the session automatically closes by closing the exec connection.
