# Docker Container Lifecycle

Loop runs Claude Code agents inside Docker containers. This document covers how containers are created, configured, and managed throughout their lifecycle.

See also: [Configuration Reference](configuration.md) for global and project config fields that control container behavior.

---

## Container Naming

Containers are named using the pattern:

```
loop-{base}-{6-hex-chars}
```

- **base** is derived from the work directory's basename (`filepath.Base(dirPath)`) when a custom directory is set, or the channel ID otherwise.
- The base is sanitized: lowercased, non-alphanumeric characters replaced with hyphens, consecutive hyphens collapsed, leading/trailing hyphens trimmed, and truncated to 40 characters.
- A random 3-byte (6-hex-character) suffix ensures uniqueness.

Examples:
- Channel `abc123` with no custom dir: `loop-abc123-a1b2c3`
- Dir `/home/user/my-project`: `loop-my-project-d4e5f6`

---

## Container Creation

The `createAndStartContainer` method orchestrates the full creation pipeline:

1. **Resolve work directory** -- defaults to `~/.loop/{channelID}/work`, overridden by the channel's `dirPath` if set.
2. **Load project config** -- merges `{workDir}/.loop/config.json` with the global config (see [Configuration: Project Config](configuration.md#project-config)).
3. **Build environment variables** -- see [Environment Variables](#environment-variables).
4. **Build mounts** -- see [Mount Processing](#mount-processing).
5. **Write MCP config** -- see [MCP Config Generation](#mcp-config-generation).
6. **Create container** via Docker API.
7. **Copy files** into the container -- see [File Copying](#file-copying).
8. **Start container**.

All containers are labeled with:
- `app=loop-agent` -- identifies all Loop-managed containers.
- `loop-channel={channelID}` -- associates the container with a specific channel.

### Docker Host Configuration

Every container is created with:
- `ExtraHosts: ["host.docker.internal:host-gateway"]` -- enables containers to reach the host machine's API server.
- `Tty: false` -- avoids ANSI escape codes in output; logs are demultiplexed via `stdcopy.StdCopy`.

---

## Environment Variables

The following environment variables are set on every container:

| Variable | Value | Description |
|---|---|---|
| `CHANNEL_ID` | Channel ID | Identifies which channel this container serves |
| `API_URL` | `http://host.docker.internal{api_addr}` | Loop API URL reachable from inside the container |
| `HOME` | Host user's home directory | Container HOME matches host HOME for path consistency |
| `HOST_USER` | `$USER` from host | The host username |
| `TZ` | IANA timezone (e.g. `Europe/Bucharest`) | Detected from `$TZ`, `time.Now().Location()`, `/etc/timezone`, or `/etc/localtime` symlink; falls back to `UTC` |
| `PATH` | `$HOME/.local/bin:$HOME/bin:$HOME/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` | Standard PATH with user-local bin and Go directories |
| `CHOWN_PATHS` | Colon-separated paths | Named volume mount targets and copy-file targets that need ownership adjustment by the container entrypoint |
| `LOOP_CHANNEL_ID` | Channel ID | Set when the security gate or docker proxy is enabled. Routed to the bot prompt via `MultiManagerResolver.ByToken` on every in-container approval call |
| `LOOP_GATE_TOKEN` | 32-byte hex | Set when the security gate or docker proxy is enabled. Per-container bearer token authenticating HTTP callbacks to `POST /api/gate/container-approval` |
| `LOOP_GATE_ENABLED` | `1` | Set when the security gate is enabled. Entrypoint flips on this to `exec /usr/local/bin/loop syscallwrap -- "$@"` as root instead of plain `gosu "$AGENT_USER" "$@"` |
| `LOOP_GATE_POLICY_FILE` | `/etc/loop/gate-policy.json` | Set when the security gate is enabled. Path inside the container to the gate's policy JSON (bind-mounted read-only from `{policyDir}/{CID}/gate-policy.json` on the host). Read by `loop syscallwrap` parent before installing the filter |
| `LOOP_DOCKERPROXY_ENABLED` | `1` | Set when the Docker HTTP proxy is enabled. Entrypoint starts `loop dockerproxy &` as root before any privilege drop |
| `LOOP_DOCKERPROXY_POLICY_FILE` | `/etc/loop/proxy-policy.json` | Path inside the container to the proxy's policy JSON (bind-mounted read-only). Read by `loop dockerproxy` at startup |
| `LOOP_DOCKERPROXY_UPSTREAM` | `/var/run/docker.sock.host` | Path inside the container to the real daemon socket (bind-mounted `:ro` from the host's `/var/run/docker.sock`). The proxy listens on `/var/run/docker.sock` (tmpfs) and reverse-proxies to this |

### Authentication

Exactly one of the following is set, with OAuth taking precedence:

| Variable | Condition |
|---|---|
| `CLAUDE_CODE_OAUTH_TOKEN` | Set when `claude_code_oauth_token` is configured |
| `ANTHROPIC_API_KEY` | Set when `anthropic_api_key` is configured and no OAuth token exists |

### Proxy Forwarding

If any of `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy`, or `https_proxy` are set on the host, they are forwarded into the container with localhost addresses rewritten:

- `://localhost:` and `://127.0.0.1:` become `://host.docker.internal:`
- Bare port values like `:3128` become `http://host.docker.internal:3128`

When proxy variables are present, `NO_PROXY` and `no_proxy` are ensured to include `host.docker.internal` so that the container's calls to the Loop API bypass the proxy.

### Custom Environment Variables

Additional variables from the `envs` config map are appended. Values support `~` expansion (resolved to the host user's home directory). Values can be any JSON type; non-strings are stringified via `fmt.Sprintf("%v", ...)`.

---

## Mount Processing

Mounts are specified in the config as `host_path:container_path[:mode]` strings.

### Processing Rules

1. **Named volumes** (source has no slashes, doesn't start with `~` or `.`) are passed through to Docker without host path expansion or existence checks. The container path still gets `~` expansion. Named volume container paths are added to `CHOWN_PATHS`.

2. **Host path mounts** have both host and container paths expanded (`~` to home directory). If the host path does not exist, the mount is silently skipped.

3. **Git excludes auto-mount** -- if `git config --global --get core.excludesFile` returns a file that exists, it is bind-mounted read-only at the same path inside the container.

4. **Work directory** -- the resolved work directory is always appended as a bind mount (`workDir:workDir`), ensuring the project is available inside the container.

5. **Worktree parent mount** -- when `ParentDirPath` is set and the work directory is inside it (e.g. `/project/.worktrees/wt1` inside `/project`), the parent directory is mounted instead of the work directory. This ensures the container sees the main `.git` directory that git worktrees reference.

6. **Extra directories** -- when a channel has extra directories configured (`extra_dirs`), each extra directory is bind-mounted into the container at its original path. The `--add-dir` flag is passed to the Claude CLI for each extra directory, making all roots available to the agent.

### Named Volume Examples

```jsonc
"mounts": [
  "loop-npmcache:~/.npm",      // Docker named volume
  "loop-gocache:/go",           // Docker named volume
  "~/.ssh:~/.ssh:ro",           // Host path, read-only
  "~/.gitconfig:~/.gitconfig:ro" // Host path, read-only
]
```

---

## Security Gate

When the config's `gates.agentgate.enabled` flag is true (the default), the runner wires a per-container seccomp gate into the spawn pipeline. The seccomp filter and the `agentgate.Server` notify loop both live **inside the container** — no notify fd or host-side unix socket crosses the container boundary. See [Configuration: Security Gate](configuration.md#security-gate) for the rule model and [Gates: Runtime wiring](gates.md#runtime-wiring) for the full lifecycle.

### Per-Container Policy File

Before `ContainerCreate`, the runner calls `writeGatePolicyFile` which:

1. Creates a host directory `{policyDir}/{containerID}/`. The serve command computes `policyDir` as `{cfg.LoopDir}/run` (typically `~/.loop/run`) and passes it into the runner via `SetGatePolicy` and `SetDockerProxyDeps`. The directory lives under the user's home rather than `/run/loop` because macOS `/run` is on the read-only system volume; the same path works on Linux so there's one code path for both OSes.
2. Marshals the gate rule subset (`default_decision`, `path_rules`, `command_rules`, `file_rules`) to `{policyDir}/{containerID}/gate-policy.json` with mode `0640`. The on-disk wire format is snake_case throughout, matching `~/.loop/config.json`.
3. Adds a bind-mount `{hostPolicyPath}:/etc/loop/gate-policy.json:ro` and sets `LOOP_GATE_ENABLED=1`, `LOOP_GATE_POLICY_FILE=/etc/loop/gate-policy.json`, `LOOP_CHANNEL_ID={channelID}`, and `LOOP_GATE_TOKEN={32-hex}` on the container env. The bearer token is shared with the docker proxy layer when both are enabled.

The file is written before the container starts so the first in-container read always succeeds. On container removal, the runner calls `gateResolver.Remove(containerID)` which frees the Manager + token. Policy files are left on disk (overwritten next spawn with the same cid).

### Entrypoint Branch

`internal/container/image/entrypoint.sh` checks for `$LOOP_GATE_ENABLED` after user setup and, when present, execs the `loop syscallwrap` subcommand **as root** — no `gosu` wrapper:

```sh
if [ "$LOOP_GATE_ENABLED" = "1" ] && [ -x /usr/local/bin/loop ]; then
    exec /usr/local/bin/loop syscallwrap -- "$@"
fi
exec gosu "$AGENT_USER" "$@"
```

The `loop syscallwrap` parent process stays as uid 0 so the agent (running under `$AGENT_USER`) cannot signal it with `kill(2)` — different-uid `kill` is `EPERM`. Privilege drop to the agent uid happens in the **child** process (see below), so claude itself still runs unprivileged.

### Fork / Handshake

`loop syscallwrap` parent (root):

1. Loads the gate policy from `LOOP_GATE_POLICY_FILE`.
2. Builds an `httpapprover.Approver` against `$API_URL` + `$LOOP_GATE_TOKEN` — approval clicks round-trip to loop-server over HTTP.
3. Creates a `socketpair(AF_UNIX, SOCK_STREAM, 0)` and re-execs `/proc/self/exe` with `LOOP_SYSCALLWRAP_MODE=child`, `ExtraFiles=[child-end]`, `SysProcAttr.Credential={uid, gid}` (looked up from `$HOST_USER`), and `SysProcAttr.Pdeathsig=SIGKILL`.
4. Receives the handshake (channel id + notify fd via SCM_RIGHTS) on the parent-end of the socketpair, acks with a single `0x01` byte, and runs `agentgate.Server` against the notify fd until the child exits.

`loop syscallwrap` child (agent user):

1. `runtime.LockOSThread` pins the thread so the filter is installed on a predictable OS thread.
2. `prctl(PR_SET_PDEATHSIG, SIGKILL)` ensures the kernel SIGKILLs the child (and transitively claude) if the parent ever dies — belt-and-braces alongside the value the parent already set in `SysProcAttr`.
3. Installs the seccomp filter with `SECCOMP_SET_MODE_FILTER | SECCOMP_FILTER_FLAG_NEW_LISTENER | SECCOMP_FILTER_FLAG_TSYNC`.
4. Sends the notify fd + `$LOOP_CHANNEL_ID` over fd 3 via SCM_RIGHTS, reads the ack, and `syscall.Exec`s the target command (claude).

The filter is inherited by every descendant. An agent cannot disable the gate by unsetting its own env — the filter is already installed on its parent by the time claude starts.

---

## File Copying

Files listed in `copy_files` (default: `["~/.claude.json"]`) are copied into each container after creation but before startup. This gives each container its own independent copy rather than sharing via a bind mount, avoiding corruption from concurrent writes.

### Mechanism

Each file is:
1. Expanded (`~` to home directory).
2. Read from the host filesystem (non-existent files are silently skipped).
3. Packaged as a tar archive with a single entry.
4. Copied into the container at the file's parent directory via `CopyToContainer`.

### Mount Collision Filtering

Before copying, the list is filtered by `filterMountedCopyFiles`: any file whose expanded path matches a bind-mounted container path is removed. This prevents "device or resource busy" errors from attempting to `CopyToContainer` onto a bind-mounted path.

File copy target paths are added to `CHOWN_PATHS` so the container entrypoint can fix ownership.

---

## MCP Config Generation

Each container run writes a per-channel MCP configuration file at:

```
{workDir}/.loop/mcp-{channelID}.json
```

Using per-channel files avoids race conditions when a parent channel and its threads share the same work directory.

### Built-in "loop" Server

Unless the user has defined an MCP server named `"loop"`, a built-in entry is added:

```json
{
  "mcpServers": {
    "loop": {
      "command": "/usr/local/bin/loop",
      "args": ["mcp", "--channel-id", "<channelID>", "--api-url", "<apiURL>", "--log", "<workDir>/.loop/mcp.log"]
    }
  }
}
```

Additional flags are appended conditionally:
- `--author-id <authorID>` -- when an author ID is available.
- `--memory` -- when semantic memory is enabled in config.

### User-Defined Servers

MCP servers from the config's `mcp.servers` map are included. If a project config defines servers with the same name as global servers, the project version takes precedence (see [Configuration: Merge Rules](configuration.md#project-config-merge-rules)).

### Cleanup

The MCP config file is deleted from the host after the container run completes (in a deferred call).

---

## Session Retry Logic

When a container run fails with a "Prompt is too long" error and a session ID exists:

1. A **compact** request is sent first: the runner creates a new container with the `/compact` command using the existing session ID.
2. After compaction succeeds, the original request is **retried** with the compacted session ID.

For other failures with an existing session:
- The runner retries once, sending only the latest message as the prompt (not the full message history).
- If the retry also fails, the original error is returned.

---

## Container Registry

The `ContainerRegistry` is a centralized, in-memory registry that tracks all Docker containers (agent, shell, chrome) throughout their lifecycle. It serves as the single source of truth for container state and is the entry point for all container lookups.

### Lifecycle States

| Status | Description |
|--------|-------------|
| `running` | Container is active and running |
| `stopped` | Container has exited but hasn't been removed yet |
| `pending-removal` | Container is scheduled for removal after a delay |

### Container Types

| Type | Singleton | Description |
|------|-----------|-------------|
| `agent` | No | Claude Code agent containers — multiple per channel allowed |
| `shell` | Yes | Terminal access containers — one per channel |
| `chrome` | Yes | Chrome sidecar containers — one per channel |

Singleton types (shell, chrome) enforce at most one running instance per channel. Registering a duplicate returns the existing entry.

### Docker Labels

All Loop containers carry these labels for identification:

| Label | Value | Purpose |
|-------|-------|---------|
| `app` | `loop-agent` | Identifies all Loop-managed containers |
| `loop-type` | `agent`, `shell`, or `chrome` | Container type classification |
| `loop-channel` | Channel ID | Associates the container with a channel |

### Registration & Events

When a container is registered, updated, or removed, the registry broadcasts real-time events through the `ContainerBroadcaster` interface:

| Event | Trigger |
|-------|---------|
| `container.registered` | New container added to registry |
| `container.status_changed` | Container status transitions (e.g. running → stopped) |
| `container.removed` | Container unregistered from registry |

Events are delivered to all connected WebSocket clients (no channel scoping — containers are a global concern).

### Startup Restore

On server startup, `Restore()` populates the registry from Docker containers that survived a daemon restart. It queries Docker for containers with the `app=loop-agent` label and registers them without broadcasting events.

### Reconcile Loop

`RunReconcileLoop` runs periodically (every 30 seconds) and:

1. Queries Docker for live containers
2. Removes registry entries for containers that no longer exist in Docker (stale entries)
3. Syncs statuses for containers whose Docker state changed (e.g. running → stopped)
4. Schedules removal for containers that just stopped

### FindOrCreateShell

Terminal connections use `FindOrCreateShell` to get a shell container for a channel. It uses a per-channel mutex to prevent duplicate containers when multiple terminal panes connect simultaneously (double-checked locking pattern).

## Container Removal

### Scheduled Removal

`ScheduleRemove` marks a container as `pending-removal`, records the `remove_at` timestamp, and schedules actual Docker removal after a configurable delay (`container_keep_alive_sec`, default 300 seconds / 5 minutes). This keeps `docker logs` available for debugging shortly after a run finishes.

If a container is re-registered before the timer fires (e.g. restarted), the pending timer is cancelled automatically.

### Cleanup on Shutdown

`Cleanup()` lists all containers with the `app=loop-agent` label and force-removes them. This is called during graceful shutdown to clean up lingering containers.

---

## Chrome Sidecar Container

When `browser_enabled` is `true` (default), a Chrome container is started lazily on first browser tool use. Chrome runs in a dedicated Docker container with a port mapping (`127.0.0.1:0 → 9222`) so the host API server can connect via CDP (Chrome DevTools Protocol).

### Architecture

```
MCP (Docker) --HTTP POST--> Host API --CDPClient--> Chrome (:9222 → mapped port)
Browser Pane --WebSocket--> Host API --CDPClient--> Chrome
```

The host API server manages all CDP connections centrally. The MCP server inside agent containers is a stateless HTTP proxy — it calls `POST /api/browser/action` on the host API for every browser operation. The browser pane connects via WebSocket for screencast frames and input events, with control operations also routed through the HTTP API.

### Container Naming

Chrome containers follow the pattern `loop-chrome-{sanitized-channel-id}`. The same sanitization rules apply as for agent containers.

### Built-in "loop-browser" MCP Server

When browser is enabled, a `loop-browser` MCP server is registered in the container's MCP config:

```json
{
  "loop-browser": {
    "command": "/usr/local/bin/loop",
    "args": ["mcp-browser", "--log", "<workDir>/.loop/mcp-browser.log", "--api-url", "<apiURL>", "--channel-id", "<channelID>"]
  }
}
```

The MCP server proxies all 19 browser tools (navigate, screenshot, click, type, tabs, etc.) through `POST /api/browser/action` on the host API. Chrome is started lazily on first action. Screenshots can be returned as file paths (via a shared screenshots directory) instead of base64 for better performance.

### Idle Timeout

Chrome containers auto-stop after **5 minutes** of inactivity. Activity is tracked via:
- **MCP tools**: Each `POST /api/browser/action` call touches the idle timer.
- **Browser pane**: The desktop app's browser panel signals `PaneConnected`/`PaneDisconnected` — while a pane is connected, Chrome is never idle-stopped.

The idle monitor runs every minute and stops Chrome for sessions where `paneCount == 0` and the last activity exceeds the timeout.

### Resource Limits

Chrome sidecar containers run with:
- **Memory:** 512 MB
- **CPU:** 0.5 cores (50% of one core)

---

## Shell Containers

`CreateShellContainer` creates a long-lived container for terminal access. Instead of running the Claude CLI, these containers execute `sleep infinity` and persist until explicitly stopped.

Shell containers:
- Use the same `createAndStartContainer` pipeline (same env, mounts, MCP config).
- Are **not** auto-removed via `scheduleRemove`.
- Are used by the terminal system for interactive sessions (`docker exec`).

---

## Container Ensurer

`ChannelContainerEnsurer` provides a "find or create" pattern for terminal sessions:

1. Lists running containers matching the `loop-channel={channelID}` label.
2. If one exists, returns its ID.
3. If none exist, creates a new shell container via `CreateShellContainer`.

This ensures that terminal WebSocket connections always have a container to exec into, creating one on-demand if the channel has no running container.

---

## Image Building

`ImageBuild` uses the Docker CLI (`docker build`) rather than the Docker SDK to leverage BuildKit and avoid "configured logging driver does not support reading" errors.

The build:
- Passes `CLAUDE_VERSION` as a build arg, fetched from `https://storage.googleapis.com/claude-code-dist-.../latest` (falls back to a timestamp if the lookup fails).
- Injects `~/.gitconfig` as a build secret (if the file exists) so Git configuration is available during the build.

---

## Resource Limits

Containers are created with configurable resource limits:

| Config Field | Default | Docker Parameter |
|---|---|---|
| `container_memory_mb` | `1024` | `Resources.Memory` (in bytes) |
| `container_cpus` | `1.0` | `Resources.CPUQuota` / `Resources.CPUPeriod` |
| `container_timeout_sec` | `3600` | Context timeout on the `Run` call |

---

## Output Collection

Container output is collected in one of two modes:

### Streaming Mode

When `OnTurn` is set on the agent request (streaming enabled):
1. Container logs are followed in real-time via `ContainerLogsFollow`.
2. Each newline-delimited JSON event is parsed and dispatched:
   - `assistant` events trigger `OnTurn` (text) and `OnToolUse` (tool invocations).
   - `system` events with `task_started`/`task_progress` subtypes trigger `OnActivity`.
   - `result` events are captured as the final response.
3. After log streaming ends, the runner waits for the container to exit.

### Batch Mode

When `OnTurn` is nil:
1. The runner waits for the container to exit.
2. All logs are read at once via `ContainerLogs`.
3. The same JSON parsing extracts the final `result` event.

Both modes use a buffered scanner with a 1 MB max line size to handle large JSON events.
