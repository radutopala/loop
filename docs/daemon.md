---
title: Daemon
---
Cross-platform daemon/service management for the Loop bot process.

**Package:** `internal/daemon`

## Supported Platforms

| Platform | Service Manager | Service Name | Config Location |
|----------|----------------|--------------|-----------------|
| macOS | launchd | `com.loop.agent` | `~/Library/LaunchAgents/com.loop.agent.plist` |
| Linux | systemd (user) | `loop` | `~/.config/systemd/user/loop.service` |
| Linux, no systemd user manager | detached process | — | `~/.loop/daemon.pid` |
| Windows | Windows SCM | `Loop` | Windows registry (via `sc.exe`) |

## Functions

### Start

`Start(sys System, logFile string) error`

Installs and starts the daemon service:

1. Resolves executable path (follows symlinks)
2. Creates config and log directories
3. Collects proxy environment variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` + lowercase variants)
4. Generates platform-specific service config
5. Registers and enables the service

**Platform details:**

- **macOS** — wraps binary with `/usr/bin/caffeinate -s` to prevent sleep. Uses `launchctl bootout` (cleanup) then `launchctl bootstrap` to load.
- **Linux** — writes systemd user unit, enables linger (`loginctl enable-linger`) for persistence across logouts. No root required. When the systemd user manager doesn't answer (`systemctl --user show-environment` fails — containers, sandboxes such as firejail, WSL without systemd), it instead runs `loop serve` in its own session with output appended to the log file, and records its pid in `~/.loop/daemon.pid`. That daemon doesn't restart on crash or survive a reboot.
- **Windows** — creates auto-start service via `sc.exe create`. Requires admin privileges.

### Stop

`Stop(sys System) error`

Uninstalls and stops the daemon:

- **macOS** — `launchctl bootout`, removes plist
- **Linux** — sends SIGTERM to the pid in `~/.loop/daemon.pid` (only if `/proc/<pid>/cmdline` is a `serve` process), waits up to 10s for it to exit and removes the pid file; then, if the systemd user manager answers, `systemctl --user disable --now`, removes unit file, disables linger
- **Windows** — `sc.exe stop` then `sc.exe delete`

Gracefully handles cases where service is already stopped or not installed.

### Status

`Status(sys System) (string, error)`

Returns one of:
- `"running"` — installed and active, or (Linux) the pid-file daemon is alive
- `"stopped"` — installed but not running
- `"not installed"` — no service config found

## System Interface

```go
type System interface {
    Executable() (string, error)
    UserHomeDir() (string, error)
    MkdirAll(path string, perm os.FileMode) error
    WriteFile(name string, data []byte, perm os.FileMode) error
    RemoveFile(name string) error
    RunCommand(name string, args ...string) ([]byte, error)
    Stat(name string) (os.FileInfo, error)
    GetUID() int
    EvalSymlinks(path string) (string, error)
    Getenv(key string) string
    ReadPIDFile(name string) ([]byte, error)
    ProcCmdline(pid int) ([]byte, error)
    StartDetached(name string, args []string, logFile string) (int, error)
    Terminate(pid int) error
    Sleep(d time.Duration)
}
```

Abstracts all OS operations for testability. `RealSystem` provides the production implementation.

## Proxy

`daemon:start` does not copy proxy variables into the service config. A captured value would pin whatever proxy the installing shell had, and if that proxy later stopped the daemon would keep dialling it, failing to reach the chat platforms at startup.

Set the proxy in config instead, under `proxies` (see [Configuration — Proxy](configuration.md#proxy)). The daemon's own connections (Discord, Slack and other outbound HTTP and websocket traffic) resolve it per connection, the same way containers and image builds do: config wins per variable, the daemon's environment fills the rest, and `proxies.no_proxy` adds to `NO_PROXY`. Localhost is never proxied. The `gh` and `git fetch` calls the daemon makes itself (the Git panel's pull request lookup, PR reviews) read their proxy only from the environment, so they get the same resolved proxy as `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` on each call. The config is re-read on every connection, so switching the proxy on or off needs no restart. A daemon run in the foreground with `loop serve` still picks up an exported proxy as a fallback.

## Related docs

- [Desktop App](desktop-app.md) — Electron daemon management UI
- [Configuration](configuration.md) — Global config reference
