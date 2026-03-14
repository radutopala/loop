# Settings & Command Palette

> Related: [Desktop App](desktop-app.md) · [Configuration](configuration.md) · [Chat](chat.md)

## Settings Panel

The settings panel is accessible via **Cmd+,** (macOS) / **Ctrl+,** (Linux/Windows) or the gear icon in the sidebar footer.

### Global App Settings

Stored in `~/.loop/app.json`:

| Setting | Default | Description |
|---------|---------|-------------|
| `stopDaemonOnQuit` | `false` | Stop the Loop daemon when the app quits |
| `autoSaveOnBlur` | `true` | Auto-save editor files when the editor loses focus |

Both settings are toggled via pill-style switches in the settings panel.

### Daemon Info

Displays the current daemon status:

- **Running**: Yes/No indicator
- **Binary path**: Path to the `loop` binary
- **Restart button**: Restarts the daemon with loading state and retry logic (30 attempts at 500ms intervals)

After a successful restart, the app reloads channels to pick up any configuration changes.

### Global Config Editor

Displays the contents of `~/.loop/config.json` in a text editor:

- HJSON format (supports comments and trailing commas)
- **Save** button writes changes back to the file
- Validation errors shown inline
- Changes take effect after daemon restart

### Project Config Editor

Only shown when a channel with a `dir_path` is selected:

- Displays `.loop/config.json` relative to the project directory
- Same editor and save flow as global config
- Project config merges with global config (see [Configuration](configuration.md) for merge rules)
- Creates the file if it doesn't exist on first save

---

## Command Palette

Triggered via **Cmd+K** (macOS) / **Ctrl+K** (Linux/Windows). Provides unified search across channels, threads, and messages.

### Search Behavior

The palette combines three result types:

1. **Channels** — top-level channels matching the query
2. **Threads** — threads matching the query, shown under their parent channel
3. **Messages** — full-text message search (triggered after 300ms debounce)

### Fuzzy Matching

Channel and thread names are matched using fuzzy search — query characters must appear in order but not consecutively. For example, "cht" matches "Chat".

### Message Search

When the query doesn't match any channels or threads (or always, in addition):

- Calls `GET /api/messages/search?q=<query>&limit=20`
- Results show: truncated content, author name, channel name
- Clicking a message result navigates to the channel and scrolls to that message

### Keyboard Navigation

| Key | Action |
|-----|--------|
| **Up / Down** | Navigate results |
| **Enter / Tab** | Select highlighted result |
| **Esc** | Close palette |

The selected item auto-scrolls into view. The palette closes after selection.

### Result Display

| Type | Format |
|------|--------|
| Channel | Name + dir_path \| branch |
| Thread | Parent name › thread name |
| Message | Content (truncated) · author in channel |

### Integration with Chat

When a message search result is selected:

1. The app navigates to the message's channel
2. Sets `scrollToMessageId` in app state
3. `useMessages` fetches messages around that ID using the `around` pagination mode
4. `ChatView` scrolls to and highlights the target message with an indigo flash (2-second duration)
5. `scrollToMessageId` is cleared via `onScrollComplete` callback
