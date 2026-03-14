# Settings & Command Palette

This document covers the Settings panel and the Command Palette, the two main overlay interfaces in the Loop desktop app.

Related docs: [Desktop App](desktop-app.md) | [Sidebar](sidebar.md) | [Editor](editor.md)

---

## Settings Panel

The Settings panel (`src/components/Settings.tsx`) opens as a full-width overlay that replaces the workspace layout. It can be opened from:
- Sidebar footer "Settings" button
- macOS app menu "Settings..." item (`Cmd+,`)
- Keyboard shortcut `Cmd+,` / `Ctrl+,`

Press `Escape` to close.

### Sections

The settings panel contains these sections from top to bottom:

---

### Daemon

Displays the current daemon status and provides control actions.

#### Status Card

A dark card (`colors.bg` background, 8px border-radius) showing:

| Field | Display |
|-------|---------|
| **Status** | Green dot + "Running" (green text) or red dot + "Stopped" (red text) |
| **Binary** | Full path to the loop binary (word-break, right-aligned) |

#### Restart Button

A full-width button with a refresh icon:
- Normal: "Restart Daemon" with `colors.text`
- While restarting: "Restarting..." with spinning icon animation (`@keyframes spin`)
- Disabled while restarting

The restart flow:
1. Calls `window.loopAPI.restartDaemon()`
2. Main process runs `loop daemon:restart`
3. Polls health check for up to 15 seconds
4. Returns updated `DaemonInfo`
5. Triggers `onDaemonRestarted` callback (refreshes channels)

---

### App Settings

Two toggle rows with switch controls:

#### Stop Daemon on Quit

- **Label:** "Stop daemon when app quits"
- **Description:** "Uninstalls the daemon service on quit. It will be re-installed on next app launch."
- **Default:** `false`

When enabled, the `before-quit` handler runs `loop daemon:stop`.

#### Auto-Save Editor on Blur

- **Label:** "Auto-save editor on blur"
- **Description:** "Save open editor tabs when the window loses focus. Manual save with Cmd+S always works."
- **Default:** `true`

This setting affects the [Editor panel](editor.md) behavior.

### Toggle Switch UI

Each setting is a clickable row with:
- Label text (13px) and description (11px, dimmed) on the left
- A 36x20px toggle switch on the right
  - Off: `colors.border` background
  - On: `colors.active` (green) background
  - White 16px circle knob with CSS transition on `left` (2px or 18px)
  - 0.2s transition on background color and position

Settings are persisted to `~/.loop/app.json` via the `save-settings` IPC handler.

---

### Global Config

An editable view of the global Loop configuration file at `~/.loop/config.json`.

#### Read Mode

- File path displayed in monospace, dimmed text
- Content shown in a `<pre>` block with `colors.bg` background
- "Edit" button in the section header

#### Edit Mode

- Full-width textarea (200-400px height, resizable vertically)
- Monospace font, 12px, 1.5 line-height
- Syntax is HJSON (JSON with comments and trailing commas)
- Red border when there is a save error

**Keyboard shortcuts in edit mode:**

| Shortcut | Action |
|----------|--------|
| `Cmd+S` / `Ctrl+S` | Save the config |
| `Escape` | Cancel editing (stops propagation to prevent closing Settings) |
| `Tab` | Insert two spaces (no focus change) |

**Buttons:**
- "Cancel" -- reverts to read mode
- "Save" -- writes to disk via `save-config` IPC. Shows "Saving..." while in progress. Displays error message below textarea on failure.
- Hint text: "Cmd+S to save" (right-aligned, 10px)

---

### Project Config

Only shown when a specific project channel is selected (via the gear icon on a channel item in the sidebar).

- Displays the project-level config at `<dir>/.loop/config.json`
- Same editable UI as Global Config
- If the file does not exist, shows: "No .loop/config.json found -- click Edit to create one."
- Editing creates the `.loop/` directory and file if needed

---

### Header Bar

The settings panel includes a header bar matching the workspace layout header:

| Element | Description |
|---------|-------------|
| Toggle sidebar button | Same as workspace (chevron icon) |
| Search button (`Cmd+K`) | Opens the Command Palette |
| "SETTINGS" label | Uppercase, dimmed, 10px |
| Close button (X) | Closes the settings panel |

---

## Command Palette

The Command Palette (`src/components/CommandPalette.tsx`) is a spotlight-style search overlay for quick navigation to channels, threads, and messages.

### Opening

| Trigger | Notes |
|---------|-------|
| `Cmd+K` / `Ctrl+K` | Global keyboard shortcut (toggles open/close) |
| Search button in workspace header | Opens the palette |
| Search button in settings header | Opens the palette |

### UI Layout

- **Backdrop:** Fixed overlay covering the entire window, `rgba(0, 0, 0, 0.5)` background. Click to close.
- **Dialog:** 520px wide, max 400px tall, centered horizontally, 80px from top
  - `colors.surface` background, 8px border-radius, 1px border
  - Deep shadow: `0 16px 48px rgba(0, 0, 0, 0.4)`

### Search Input

- Full-width text input at the top of the dialog
- `colors.bg` background, 6px border-radius
- 14px sans-serif font
- Placeholder: "Search channels, threads, and messages..."
- Auto-focused on open

### State Reset

When the palette opens:
- Query is cleared
- Selection index reset to 0
- Message results cleared
- Search state reset

---

### Result Types

The palette displays three types of results:

#### Channels

| Field | Display |
|-------|---------|
| Icon | `#` hash symbol |
| Label | Channel name or `dir_path` |
| Detail | `dir_path | branch` (if branch exists) or just `dir_path` |

#### Threads

| Field | Display |
|-------|---------|
| Icon | Tree branch symbol |
| Label | Thread name |
| Detail | `<parent-name> > thread` |

#### Messages

| Field | Display |
|-------|---------|
| Icon | Speech bubble emoji |
| Label | Message content (truncated to 80 chars, single-line, monospace font) |
| Detail | `<author_name> in <channel_name>` |

Messages appear in a separate section with a "MESSAGES" header and a top border separator.

---

### Fuzzy Matching

Channel and thread filtering uses fuzzy matching: each character of the query must appear in order in the target text (case-insensitive), but not necessarily consecutively.

```typescript
function fuzzyMatch(query: string, text: string): boolean {
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  let qi = 0;
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++;
  }
  return qi === q.length;
}
```

Both the item label and detail text are checked.

---

### Message Search

Message search runs against the backend API (`GET /api/messages/search?q=...`).

| Property | Value |
|----------|-------|
| Debounce | 300ms |
| Minimum query length | 2 characters |
| Maximum results | 10 |
| Search method | `LIKE %query%` in the database |
| Sort order | `created_at DESC` (most recent first) |

While searching, "Searching messages..." is shown below the results.

---

### Keyboard Navigation

| Key | Action |
|-----|--------|
| `ArrowDown` | Move selection down |
| `ArrowUp` | Move selection up |
| `Enter` | Accept the selected item |
| `Tab` | Accept the selected item |
| `Escape` | Close the palette |

The selected item is highlighted with `colors.selectedBg` background. Mouse hover also updates the selection index. The selected item is scrolled into view with `scrollIntoView({ block: "nearest" })`.

---

### Selection Actions

| Result Type | Action on Select |
|-------------|-----------------|
| Channel | Navigate to the channel (set `selectedId`) |
| Thread | Navigate to the thread (set `selectedId`) |
| Message | Navigate to the message's channel and scroll to the message (via `scrollToMessageId`) |

After selection, the palette closes. For messages, the chat view:
1. Loads messages around the target using `around` pagination
2. Scrolls the target message into view (centered)
3. Highlights the message with an indigo flash (see [Chat - Highlighted Messages](chat.md#highlighted-messages))

---

### Combined Navigation List

Channel/thread items and message items are combined into a single list for keyboard navigation. The selection index spans both sections:
- Indices 0 to N-1: filtered channels and threads
- Indices N to N+M-1: message results

This allows seamless arrow-key navigation across both sections.
