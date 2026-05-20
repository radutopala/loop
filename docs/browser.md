---
title: Browser
---

# Browser

Manages Chrome browser instances for screencast streaming and MCP browser automation tools. Supports two modes: Docker (headless Chrome container per channel) and Host (user's local Chrome).

**Packages:** `internal/browser`, `internal/api` (handler), `internal/mcpbrowser` (MCP server)

## Architecture

```
Frontend (Electron)
  BrowserPanel ─── useBrowserWs
    │ WS: start/stop/screencast/input     │ HTTP: /api/browser/action
    ▼                                     ▼
┌──────────────────────────────────────────────────┐
│  browser_handler.go                              │
│  WS handler + action dispatcher                  │
│  Mode switching (Docker ↔ Host pill)             │
│  Idle monitoring                                 │
├──────────────────────────────────────────────────┤
│  BrowserProvider          CDPManager             │
│  (lifecycle only)         (CDP state)            │
│  ├─ DockerProvider        ├─ One browser WS conn │
│  │  Container CRUD        ├─ activeClient (tab)  │
│  │  Port mapping          ├─ Tab tracking        │
│  └─ HostProvider          ├─ Pane counting       │
│     DevToolsActivePort    └─ Notifications       │
├──────────────────────────────────────────────────┤
│  CDPClient / CDPSession                          │
│  Per-target chromedp context                     │
│  Navigate, screencast, input, tabs, JS, etc.     │
└──────────────────────────────────────────────────┘
                      │
                      ▼
                Chrome (CDP)
                Docker: headless container per channel
                Host: user's Chrome via DevToolsActivePort
```

## Two Modes

### Docker Mode (default)
- Headless Chrome in a dedicated container per channel (`loop-chrome-{channelID}`)
- Container created on first browser pane open, idle-stopped after timeout
- CDP endpoint: `ws://127.0.0.1:{hostPort}` (random mapped port)
- All tabs shown in UI (only agent-tracked tabs)

### Host Mode
- Connects to user's local Chrome via CDP
- Requires `chrome://inspect/#remote-debugging` enabled
- Discovery: reads `DevToolsActivePort` from Chrome's user data directory
- CDP endpoint: `ws://127.0.0.1:{port}/devtools/browser/{guid}`
- Only agent-created tabs shown (user's personal tabs hidden)
- Mode persisted per channel in localStorage

### Mode Switching
- Frontend pill toggle sends `POST /api/browser/mode`
- WS "start" message includes `mode` field (restores mode after daemon restart)
- Each mode has its own `CDPManager` (keyed `channelID|mode`)
- Switching preserves both tab sets

## BrowserProvider

Thin interface for Chrome lifecycle — 6 methods, no CDP state.

```go
type BrowserProvider interface {
    EnsureBrowser(ctx, channelID, containerID) error
    StopBrowser(ctx, channelID) error
    IsRunning(ctx, channelID) bool
    GetCDPEndpoint(channelID) string
    GetContainerID(channelID) (string, bool)
    IsHostMode() bool
}
```

### DockerProvider
- Creates/reuses Chrome containers with `docker create` + `docker start`
- Discovers mapped host port via `ContainerInspect`
- Reachability check via TCP dial (no `/json` HTTP API)

### HostProvider
- Discovers Chrome via `DevToolsActivePort` file
- Falls back to TCP dial on configured port
- `StopBrowser` clears session state only (does not kill Chrome)

## CDPManager

Manages a single browser-level CDP connection and per-tab contexts. One CDPManager per `(channelID, mode)` pair.

### Connection Model

```
Connect() ──► cdpFactory (NewCDPClient)
                  │
                  ▼
              One WebSocket to Chrome browser endpoint
                  │
GetOrCreate() ──► NewContextForTarget(targetID)
                  │ (Target.attachToTarget over same WS)
                  ▼
              Fresh CDPSession per tab (no cache)
```

- `Connect()` creates the initial connection via factory (with retries for Docker)
- `GetOrCreate(targetID)` creates child contexts from the initial connection via `NewContextForTarget` — no new WebSocket dial, no Chrome permission prompt
- No client cache — each tab switch creates a fresh context (cheap: just `Target.attachToTarget`)

### Key Methods

| Method | Description |
|--------|-------------|
| `Connect(ctx)` | Initial CDP connection with retries |
| `GetOrCreate(targetID)` | Fresh context for target via existing WS |
| `ActiveClient()` | Current tab's CDPSession |
| `SwitchActive(targetID)` | Set active target ID |
| `TrackTab/UntrackTab` | Tab order management |
| `NotifyTargetSwitch/TabAdded/TabRemoved` | WS handler notifications |
| `PaneConnected/PaneDisconnected` | Pane count for idle monitoring |

## CDPClient (CDPSession interface)

Wraps a chromedp context attached to a single page target.

### Construction

```go
// Initial connection (used by CDPManager.Connect):
client, err := NewCDPClient(ctx, wsURL, logger, opts...)

// Child context for different target (used by CDPManager.GetOrCreate):
child, err := client.NewContextForTarget(targetID)
// Uses Target.attachToTarget over same browser WS — no new dial
```

### Operations

| Category | Methods |
|----------|---------|
| Navigation | `Navigate`, `Reload`, `GoBack`, `GoForward`, `GetPageInfo` |
| Screencast | `StartScreencast`, `StopScreencast`, `ResetScreencast` |
| Screenshots | `Screenshot` |
| Input | `MouseClick`, `MouseMove`, `MouseScroll`, `MouseDown`, `MouseUp`, `KeyPress`, `TypeText` |
| Accessibility | `GetElementRefs`, `ClickRef`, `ScrollIntoView` |
| Tabs | `ListTabs`, `NewTab`, `CloseTab`, `SwitchTarget` |
| JavaScript | `EvaluateJS` |
| Capture | `EnableConsoleCapture`, `EnableNetworkCapture` |
| Window | `ResizeWindow` |

## MCP Browser Tools

### Proxy Mode (agent containers)
Agent containers run `loop mcp-browser` which proxies all tool calls via `POST /api/browser/action` to the host. The handler routes through CDPManager.

### Direct Mode (standalone)
`loop mcp-host-browser` connects directly to Chrome via CDPClient (no CDPManager). Auto-discovers Chrome's DevTools endpoint via the `DevToolsActivePort` file. Can be used as a standalone MCP server for any Claude Code session:

```json
{
  "mcpServers": {
    "browser": {
      "command": "loop",
      "args": ["mcp-host-browser"]
    }
  }
}
```

Requires `chrome://inspect/#remote-debugging` enabled in Chrome.

### Available Tools
`navigate`, `read_page`, `computer`, `form_input`, `screenshot`, `go_back`, `go_forward`, `reload`, `evaluate`, `list_tabs`, `new_tab`, `switch_tab`, `close_tab`, `page_info`, `get_page_text`, `find`, `read_console_messages`, `read_network_requests`, `resize_window`

## Idle Monitoring

`Server.RunBrowserIdleMonitor(ctx, timeout)` — single goroutine checks all CDPManagers. When a CDPManager has no connected panes and exceeds the timeout:
- CDPManager is closed (CDP connections cleaned up)
- For Docker mode: container is stopped and removed

## Last Tab Close

When the last tab is closed, a replacement `about:blank` tab is created **before** closing the old one (the active CDP client's context dies with the closed tab).

## Related Docs

- [Containers](containers.md) — Docker container lifecycle
- [Desktop App](desktop-app.md) — Electron architecture
