# Desktop App (Electron)

The Loop desktop app is an Electron application that wraps the React-based frontend and manages the Loop daemon lifecycle. It provides native OS integration including window management, deep linking, auto-updates, and system menus.

Related docs: [Sidebar](sidebar.md) | [Layouts](layouts.md) | [Settings](settings.md)

---

## Architecture

The app follows Electron's standard multi-process architecture:

| Process | Role | Entry Point |
|---------|------|-------------|
| **Main** | Window management, daemon lifecycle, IPC handlers, menus, auto-updater | `electron/main.ts` |
| **Preload** | Bridge between main and renderer via `contextBridge` | `electron/preload.cjs` |
| **Renderer** | React UI (Vite-bundled SPA) | `src/main.tsx` / `src/App.tsx` |

Context isolation is enabled (`contextIsolation: true`, `nodeIntegration: false`). The preload script exposes a `window.loopAPI` object that the renderer uses for all privileged operations.

---

## Window Management

Windows are created with `BrowserWindow` using these defaults:

| Property | Value |
|----------|-------|
| Default size | 1200 x 800 |
| Minimum size | 900 x 400 |
| Title bar style (macOS) | `hiddenInset` (traffic lights inset into content) |
| Title bar style (others) | `hidden` |
| Sandbox | `false` (required for preload) |

The title bar area uses `WebkitAppRegion: "drag"` on a 38px-tall region at the top. Interactive elements within the drag region use `WebkitAppRegion: "no-drag"` to remain clickable.

The window title updates dynamically based on the selected channel:
- No channel: `Loop`
- Channel selected: `<channel-name> - Loop`
- Thread selected: `<parent-name> > <thread-name> - Loop`

On macOS, the dock icon is set from `loop-macos.png` (rounded-rect background variant).

### Multi-Window

`Cmd+N` / `Ctrl+N` opens a new window via the File menu. The app enforces single-instance mode using `requestSingleInstanceLock()`. A second launch passes its URL arguments to the first instance rather than creating a new process.

---

## Deep Linking

The app registers the `loop://` protocol for deep linking to specific channels.

**URL format:** `loop://channel/<channel-id>`

### Registration

- **Production:** `app.setAsDefaultProtocolClient("loop")`
- **Development:** registers with `process.execPath` and the script path as arguments

### Navigation Flow

1. **macOS:** The `open-url` event fires when a `loop://` URL is opened. If the window is ready, it navigates immediately by setting `window.location.hash` via `executeJavaScript`. If the window is still loading, the channel ID is stored in `pendingChannelId` and applied after `did-finish-load`.

2. **Windows/Linux:** The `second-instance` event fires with `argv` containing the URL. The channel ID is extracted and navigation proceeds as above.

3. **Renderer side:** The renderer listens for `hashchange` events and also registers an `onNavigateChannel` callback via IPC. Hash-based navigation (`#<channel-id>`) keeps the URL and React state in sync.

---

## Daemon Management

The Loop daemon is a Go HTTP server that the Electron app manages automatically.

### Startup Sequence

1. **`ensureLoopConfig()`** -- If `~/.loop/config.json` does not exist, runs `loop onboard:global` to create it.
2. **`ensureDaemon()`** -- Runs `loop daemon:restart` to install/restart the daemon as a system service (launchd on macOS, systemd on Linux).
3. **Health check** -- Polls `GET /api/health` every 500ms for up to 15 seconds waiting for the daemon to become healthy.

### Binary Resolution

The binary is located in this order:
1. Bundled binary at `<resources>/bin/loop` (production builds)
2. Fallback flat layout at `<resources>/loop`
3. System `PATH` lookup via `which` / `where`

### Shutdown

If the "Stop daemon on quit" setting is enabled, the app runs `loop daemon:stop` during the `before-quit` event (15-second timeout).

---

## Auto-Update

Auto-update uses `electron-updater` and is only active in production builds (disabled when `VITE_DEV_SERVER_URL` is set).

### Configuration

- `autoDownload: false` -- downloads are user-initiated
- `autoInstallOnAppQuit: false` -- installs are user-initiated

### Check Interval

Updates are checked immediately on startup and then every **30 minutes** (`30 * 60 * 1000 ms`).

### Update States

| State | `UpdateStatus` fields | User action |
|-------|----------------------|-------------|
| Available | `available: true, downloading: false, downloaded: false` | Click to download |
| Downloading | `available: true, downloading: true, downloaded: false` | Wait (button disabled) |
| Downloaded | `available: true, downloading: false, downloaded: true` | Click to restart and install |
| Error | `error` set | Click to retry download |

### Install Flow

When the user clicks "Restart to update":
1. The daemon is restarted (`loop daemon:restart`) so it picks up the new bundled binary.
2. `autoUpdater.quitAndInstall(false, true)` quits the app and installs the update.

Update status is broadcast to all open windows via `webContents.send("update-status", ...)`.

---

## IPC Handlers

All IPC uses `ipcMain.handle` (invoke/handle pattern) for request-response communication.

| Channel | Direction | Purpose |
|---------|-----------|---------|
| `get-api-url` | Renderer -> Main | Returns the resolved API base URL from config |
| `get-settings` | Renderer -> Main | Returns `AppSettings` from `~/.loop/app.json` |
| `save-settings` | Renderer -> Main | Writes `AppSettings` to `~/.loop/app.json` |
| `get-daemon-info` | Renderer -> Main | Returns `{ running, binaryPath }` |
| `get-config` | Renderer -> Main | Returns global config content from `~/.loop/config.json` |
| `get-project-config` | Renderer -> Main | Returns project config from `<dir>/.loop/config.json` |
| `save-config` | Renderer -> Main | Writes config content to a file path |
| `restart-daemon` | Renderer -> Main | Runs `loop daemon:restart`, waits for health, returns info |
| `show-open-directory-dialog` | Renderer -> Main | Opens native directory picker dialog |
| `onboard-local` | Renderer -> Main | Runs `loop onboard:local` for a directory |
| `get-update-status` | Renderer -> Main | Returns current `UpdateStatus` |
| `download-update` | Renderer -> Main | Triggers `autoUpdater.downloadUpdate()` |
| `install-update` | Renderer -> Main | Restarts daemon, then `quitAndInstall` |

### Event Channels (Main -> Renderer)

| Channel | Purpose |
|---------|---------|
| `open-settings` | Sent from the macOS app menu "Settings..." item |
| `navigate-channel` | Sent when a deep link arrives |
| `update-status` | Broadcast when auto-updater state changes |

---

## Menu Structure

### macOS App Menu

| Menu | Items |
|------|-------|
| **Loop** | About, separator, **Settings...** (`Cmd+,`), separator, Services, separator, Hide/Hide Others/Unhide, separator, Quit |
| **File** | New Window (`Cmd+N`), separator, Close |
| **Edit** | Undo, Redo, separator, Cut, Copy, Paste, Select All |
| **View** | Reload, Force Reload, Toggle DevTools, separator, Reset/Zoom In/Zoom Out, separator, Toggle Fullscreen |
| **Window** | (system default window menu) |

On non-macOS platforms, the app-name menu is omitted, and File contains Quit instead of Close.

---

## Preload API

The preload script (`electron/preload.cjs`) exposes `window.loopAPI` with the following methods:

```typescript
interface LoopAPI {
  // Core
  getApiUrl: () => Promise<string>;
  onNavigateChannel: (callback: (channelId: string) => void) => void;

  // Directory operations
  showOpenDirectoryDialog?: () => Promise<string | null>;
  onboardLocal?: (dirPath: string) => Promise<{ ok: boolean; output?: string; error?: string }>;

  // Settings
  getSettings: () => Promise<AppSettings>;
  saveSettings: (settings: AppSettings) => Promise<void>;
  onOpenSettings: (callback: () => void) => void;

  // Daemon
  getDaemonInfo: () => Promise<DaemonInfo>;
  restartDaemon: () => Promise<DaemonInfo>;

  // Config
  getConfig: () => Promise<ConfigInfo>;
  getProjectConfig: (dirPath: string) => Promise<ConfigInfo>;
  saveConfig: (filePath: string, content: string) => Promise<{ ok: boolean; error?: string }>;

  // Auto-update
  getUpdateStatus?: () => Promise<UpdateStatus>;
  downloadUpdate?: () => Promise<void>;
  installUpdate?: () => Promise<void>;
  onUpdateStatus?: (callback: (status: UpdateStatus) => void) => void;
}
```

Some methods are marked optional (`?`) because they are only available in the Electron environment. The renderer handles `undefined` gracefully for browser-based development mode.

---

## Config Resolution

The API URL is resolved from `~/.loop/config.json` (HJSON format with comment stripping):

1. If `LOOP_API_URL` environment variable is set, use it directly.
2. Read `api_addr` from config (e.g., `":8222"` or `"localhost:8222"`).
3. If `api_addr` starts with `:`, prepend `localhost`.
4. Default: `http://localhost:8222`.

App-level settings are stored separately in `~/.loop/app.json`:

```json
{
  "stopDaemonOnQuit": false,
  "autoSaveOnBlur": true
}
```
