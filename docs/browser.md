# Browser

Manages Chrome browser instances as sidecar Docker containers, providing Chrome DevTools Protocol (CDP) access for screencast streaming and MCP browser automation tools.

**Package:** `internal/browser`

## Architecture

Chrome runs in a dedicated Docker container (`loop-chrome-{channelID}`) on a shared network, accessible to both the agent's MCP browser tools and the desktop browser pane.

```
Desktop App  ←→  Browser Pane  ←→  WebSocket  ←→  Go Backend
                                                        ↓
                                                   CDPClient  ←→  Chrome Container (CDP port 9222)
                                                        ↑
                                              MCP Browser Server
```

## Manager

Manages Chrome sidecar containers (one per channel).

### Container Lifecycle

| Method | Description |
|--------|-------------|
| `EnsureBrowser(ctx, channelID, _)` | Creates or reuses Chrome container for channel |
| `StopBrowser(ctx, channelID)` | Stops and removes Chrome container |
| `IsRunning(ctx, channelID)` | Returns true if Chrome is running |
| `Cleanup(ctx)` | Stops all Chrome containers |
| `GetCDPEndpoint(channelID)` | Returns CDP WebSocket URL |
| `GetContainerID(channelID)` | Returns Docker container ID |

### Idle Monitoring

`RunIdleMonitor(ctx, timeout)` — periodically checks for idle browser sessions and stops them. A session is idle when no browser pane is connected and `lastUsedAt` exceeds the timeout.

- `TouchBrowser(channelID)` — updates last-used timestamp (called by MCP on each tool invocation)
- `PaneConnected(channelID)` / `PaneDisconnected(channelID)` — tracks active browser panes (prevents idle shutdown)

### Tab Management

| Method | Description |
|--------|-------------|
| `SetTargetID(channelID, targetID)` | Stores active page target |
| `GetTargetID(channelID)` | Returns active page target |
| `TrackTab(channelID, targetID)` | Adds tab to order |
| `UntrackTab(channelID, targetID)` | Removes tab from order |
| `NextTabID(channelID, closedTargetID)` | Returns tab to switch to after closing |
| `OrderTabs(channelID, tabs)` | Reorders tabs by insertion order |

### Tab Notification Channels

Non-blocking notifications (buffered channel size 1) for UI updates:

- `NotifyTargetSwitch` / `TargetSwitchCh` — active tab changed
- `NotifyTabAdded` / `TabAddedCh` — new tab opened
- `NotifyTabRemoved` / `TabRemovedCh` — tab closed

### CDP Client Cache

| Method | Description |
|--------|-------------|
| `SetCDPForTarget(channelID, targetID, cdp)` | Cache CDP client per target |
| `GetCDPForTarget(channelID, targetID)` | Get cached CDP client |
| `RemoveCDPForTarget(channelID, targetID)` | Remove and return cached client |
| `GetActiveCDP(channelID)` | Get CDP client for active target |

## CDPClient

Wraps a chromedp browser context for CDP operations.

### Construction

```go
client, err := NewCDPClient(ctx, wsURL, logger,
    WithTargetID(id),       // attach to specific target
    WithNewTarget(),        // create new page target
    WithAllocator(fn),      // override remote allocator
    WithRunFunc(fn),        // override chromedp.Run
    WithExec(fn),           // override cdp.Execute
)
```

### Navigation

| Method | Description |
|--------|-------------|
| `Navigate(ctx, url)` | Navigate to URL |
| `Reload(ctx)` | Reload current page |
| `GoBack(ctx)` | Navigate back in history |
| `GoForward(ctx)` | Navigate forward in history |
| `GetPageInfo(ctx)` | Returns URL and title |

### Screenshots & Screencast

| Method | Description |
|--------|-------------|
| `Screenshot(ctx)` | Capture full-page PNG screenshot |
| `StartScreencast(quality, maxW, maxH)` | Stream JPEG frames to channel |
| `StopScreencast()` | Stop streaming |
| `ResetScreencast()` | Reset state without CDP command |

### Input Dispatch

| Method | Description |
|--------|-------------|
| `MouseClick(ctx, x, y, button, count)` | Click at coordinates |
| `MouseMove(ctx, x, y)` | Move mouse |
| `MouseScroll(ctx, x, y, deltaX, deltaY)` | Scroll |
| `MouseDown(ctx, x, y, button)` | Mouse button down |
| `MouseUp(ctx, x, y, button)` | Mouse button up |
| `KeyPress(ctx, key)` | Key down + up |
| `TypeText(ctx, text)` | Type character by character |

### Accessibility & Elements

| Method | Description |
|--------|-------------|
| `GetElementRefs(ctx)` | Interactive elements from accessibility tree |
| `ClickRef(ctx, refs, refIndex)` | Click element by 1-based ref index |
| `ScrollIntoView(ctx, nodeID)` | Scroll element into view |

### Tabs

| Method | Description |
|--------|-------------|
| `ListTabs(ctx)` | All open tabs (via HTTP endpoint) |
| `NewTab(ctx, url)` | Open new tab |
| `SwitchTab(ctx, targetID)` | Switch to tab |
| `CloseTab(ctx, targetID)` | Close tab (via HTTP endpoint) |
| `SwitchTarget(targetID)` | Activate target via HTTP (non-blocking) |

### JavaScript & Console

| Method | Description |
|--------|-------------|
| `EvaluateJS(ctx, expression)` | Evaluate JS, return result as string |
| `EnableConsoleCapture(ctx, ch)` | Capture console messages to channel |
| `EnableNetworkCapture(ctx, ch)` | Capture network requests to channel |

## Data Types

### ElementRef
Interactive element with bounding box for precise interaction.

### TabInfo
Browser tab with `TargetID`, `URL`, `Title`, `Active` fields.

### PageInfo
Current page `URL` and `Title`.

### InputEvent
User input event: click, mousemove, scroll, keypress, typetext.

### ConsoleMessage
Browser console message with level, text, and timestamp.

### NetworkRequest
Captured network request with URL, method, status, type, and timestamp.

## Helper Functions

| Function | Description |
|----------|-------------|
| `ChromeHostname(channelID)` | Container hostname for a channel |
| `ChromeHTTPBaseURL(wsURL)` | Convert CDP WS URL to HTTP base |
| `ActivateTarget(wsURL, targetID)` | Activate target via HTTP |
| `CreatePageTarget(wsURL)` | Create new about:blank target |
| `ChromeBinaryPath()` | Chromium binary path inside containers |
| `CDPAddress()` | CDP listen address inside containers |

## Related docs

- [Containers](containers.md) — Docker container lifecycle
- [Terminal](terminal.md) — Terminal WebSocket protocol
- [Desktop App](desktop-app.md) — Electron architecture
