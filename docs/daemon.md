---
title: Daemon
---

# Daemon

Cross-platform daemon/service management for the Loop bot process.

**Package:** `internal/daemon`

## Supported Platforms

| Platform | Service Manager | Service Name | Config Location |
|----------|----------------|--------------|-----------------|
| macOS | launchd | `com.loop.agent` | `~/Library/LaunchAgents/com.loop.agent.plist` |
| Linux | systemd (user) | `loop` | `~/.config/systemd/user/loop.service` |
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
- **Linux** — writes systemd user unit, enables linger (`loginctl enable-linger`) for persistence across logouts. No root required.
- **Windows** — creates auto-start service via `sc.exe create`. Requires admin privileges.

### Stop

`Stop(sys System) error`

Uninstalls and stops the daemon:

- **macOS** — `launchctl bootout`, removes plist
- **Linux** — `systemctl --user disable --now`, removes unit file, disables linger
- **Windows** — `sc.exe stop` then `sc.exe delete`

Gracefully handles cases where service is already stopped or not installed.

### Status

`Status(sys System) (string, error)`

Returns one of:
- `"running"` — installed and active
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
}
```

Abstracts all OS operations for testability. `RealSystem` provides the production implementation.

## Proxy Forwarding

The daemon automatically captures proxy environment variables from the current environment and injects them into the service config. This ensures the daemon process inherits proxy settings even when started by the OS service manager.

Captured variables: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` (and lowercase variants).

## Related docs

- [Desktop App](desktop-app.md) — Electron daemon management UI
- [Configuration](configuration.md) — Global config reference
