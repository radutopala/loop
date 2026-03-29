# Workspace Layouts

The workspace layout system supports two layout types: **Split** (flexible split-pane interface) and **Canvas** (free-form draggable tiles). Layouts are named, switchable, and persisted per channel. Each layout tab stores its type (`"split"` or `"canvas"`).

Related docs: [Chat](chat.md) | [Editor](editor.md) | [Desktop App](desktop-app.md)

---

## Layout Types

### Split Layout

The default layout type. Panels are arranged in arbitrary horizontal and vertical splits using a recursive tree data structure (see [Tree Data Structure](#tree-data-structure) below).

### Canvas Layout

A free-form layout with draggable, resizable tiles on an infinite surface.

- **Background:** Dot grid pattern for spatial orientation.
- **Pan:** Scroll to pan the canvas viewport.
- **Zoom:** `Ctrl+Scroll` (or `Cmd+Scroll`) to zoom in/out.
- **Add tiles:** Double-click an empty area or use the `+` button in a tile header.
- **Tile controls:** Each tile header has `+` (add tile), maximize, and close buttons.
- **Bring to front:** Click a tile to raise it above others.
- **Singleton enforcement:** Chat, Editor, Memory, Diff, Docker Browser, and Host Browser are limited to one tile per canvas (Docker Browser and Host Browser are also mutually exclusive). Agent and Shell can have multiple tiles.
- **Auto-center:** Tiles are centered in the viewport on first load and after a layout reset.

### Creating Layouts

New layouts can be created as either Split or Canvas from the `+` button in the tab bar. The layout type is stored per tab and cannot be changed after creation.

---

## Tree Data Structure

Layouts are represented as a recursive tree of two node types:

### LeafNode

A terminal node that renders a single panel.

```typescript
interface LeafNode {
  type: "leaf";
  id: string;        // unique identifier (e.g., "chat", "editor", "agent-0", "shell-1")
  panel: PanelType;  // determines which component to render
  flex: number;       // relative size weight within parent split
}
```

### SplitNode

A container that splits its area between child nodes.

```typescript
interface SplitNode {
  type: "split";
  direction: "vertical" | "horizontal";  // vertical = top/bottom, horizontal = left/right
  children: PaneNode[];                   // 2+ children
  flex: number;                           // relative size weight within parent split
}
```

`PaneNode = LeafNode | SplitNode` -- the tree is recursive, allowing arbitrarily nested splits.

---

## Panel Types

| Panel | Type | ID Pattern | Singleton? | Description |
|-------|------|-----------|------------|-------------|
| Chat | `chat` | `"chat"` | Yes | Chat with the agent (see [chat.md](chat.md)) |
| Editor | `editor` | `"editor"` | Yes | File browser and code editor (see [editor.md](editor.md)) |
| Memory | `memory` | `"memory"` | Yes | Agent memory explorer |
| Diff | `diff` | `"diff"` | Yes | Git diff viewer |
| Docker Browser | `docker-browser` | `"docker-browser"` | Yes | Chrome browser inside the Docker container |
| Host Browser | `host-browser` | `"host-browser"` | Yes | Chrome browser on the host machine via CDP |
| Sessions | `sessions` | `"sessions"` | Yes | Browse and resume Claude sessions (channels only) |
| Playground | `playground` | `"playground-N"` | No | Live interactive code sandbox (HTML/CSS/JS) |
| Agent | `agent` | `"agent-N"` | No | Docker-isolated terminal session |
| Shell | `shell` | `"shell-N"` | No | Local machine shell session |

**Singleton panels** (chat, editor, memory, diff, docker-browser, host-browser, sessions) can appear at most once in a layout tree. The `canAddPanel()` function enforces this: if the panel type is in `SINGLETON_PANELS` and already present in the tree, the add operation is rejected.

**Channel-only panels**: The Sessions panel is only available for channels (not threads). The "Sessions" tab is hidden from the layout tab bar when viewing a thread.

**Multi-instance panels** (agent, shell, playground) can appear multiple times. Each instance gets a unique numbered ID (e.g., `agent-0`, `shell-1`, `playground-0`) from a per-channel counter.

**Exclusive panels** are singleton panels that are mutually exclusive -- only one from each exclusive group can exist in a layout at a time. If one is present, the others in the same group are greyed out and disabled in the split menu.

```typescript
const EXCLUSIVE_PANELS: PanelType[][] = [
  ["docker-browser", "host-browser"],
];
```

The `canAddPanel()` function checks both singleton and exclusive constraints.

---

## Named Layouts

Each channel has an ordered list of named layouts, displayed as a tab bar above the workspace content.

### Default Layouts

Default layouts are created for every new channel:

| Name | Structure | Notes |
|------|-----------|-------|
| **Chat** | Horizontal split: Chat (50%) + Diff (50%) | |
| **Editor** | Horizontal split: Editor (65%) + Diff (35%) | |
| **Memory** | Single leaf: Memory | |
| **Terminal** | Empty (user picks a panel on first use) | |
| **Diff** | Single leaf: Diff | |
| **Browser Chat** | Horizontal split: Chat (50%) + (Docker Browser + Diff stacked) | |
| **Sessions** | Single leaf: Sessions | Channels only — hidden for threads |
| **Swarm** | Three Agent panels in a split | |
| **Canvas** | Free-form canvas with draggable Agent, Diff, Memory tiles | |

The "Chat" layout is the initial active layout.

### Layout Operations

| Operation | How | Behavior |
|-----------|-----|----------|
| **Switch** | Click a layout tab | Saves current tree, loads the target layout's tree from storage. Terminal sessions are cleared. |
| **Create** | Click `+` button next to tabs | Creates a new Split or Canvas layout with auto-generated name (`Layout 1`, `Layout 2`, ...). Split layouts start empty -- user picks the first panel from the `EmptyLayoutPicker`. |
| **Rename** | Double-click the tab label | Opens an inline text input. Commit with Enter, cancel with Escape or blur. Rejects empty names and duplicates. |
| **Delete** | Click `x` on the tab | Shows a confirmation popover ("Delete? Yes / No") with an upward-pointing arrow. Kills any running terminal sessions. Cannot delete the last remaining layout. |
| **Reset current** | Click "Reset" dropdown > "Reset current" | Restores the active layout to its default tree (only available for default layout names). Kills running terminals. |
| **Restore defaults** | Click "Reset" dropdown > "Restore defaults" | Re-adds any previously deleted default layouts. Only shown when at least one default layout was removed. |

When a default layout is deleted, its name is tracked in a `removed` list so `ensureDefaultLayouts()` does not re-add it on next load. "Restore defaults" clears this list.

---

## Persistence

All layout state is stored in `localStorage` under the key `loop-workspace-layout`. The persistence logic lives in `app/src/layouts/persistence.ts`.

### Storage Format

```typescript
Record<channelId, {
  version: number;                   // migration version (current: 4)
  active: string;                    // name of the active layout
  layouts: Record<string, PaneNode>; // layout name -> tree (split layouts)
  order: string[];                   // tab display order
  removed?: string[];                // deleted default layout names
  types?: Record<string, "split" | "canvas">; // layout name -> type
}>
```

The tree is saved whenever it changes (on every split, remove, flex update, or drop operation). The active layout name is saved on switch.

### Versioned Migrations

Persisted data is migrated on load via a versioned migration system. Each channel's data stores a `version` field. On load, migrations run sequentially from the stored version up to `CURRENT_VERSION`.

```typescript
const migrations: Record<number, (data: ChannelData) => ChannelData> = {
  1: removeDefaultLayout,
  2: renameBrowserToBrowserChat,
  3: addTypesMap,
  4: migrateBrowserToDockerBrowser, // "browser" panel type -> "docker-browser"
};
```

New migrations increment `CURRENT_VERSION` and add an entry to the `migrations` map.

---

## Drag-to-Split

Panels can be rearranged by dragging their headers.

### Drag Source

Each pane has a `PaneLeafHeader` component (22px tall) with `draggable` set. On drag start:
1. The leaf ID is stored in the drag data transfer with MIME type `application/x-panel-drag`.
2. A custom event `layout-drag-start` is dispatched to activate drop zones across all panes.

### Drop Zones

The `DropZoneOverlay` component covers each pane's content area (below the header). It divides the area into 5 zones based on cursor position:

| Zone | Cursor Region | Result |
|------|--------------|--------|
| **Top** | Top 25% | Vertical split, new pane above |
| **Bottom** | Bottom 25% | Vertical split, new pane below |
| **Left** | Left 25% (middle 50% height) | Horizontal split, new pane left |
| **Right** | Right 25% (middle 50% height) | Horizontal split, new pane right |
| **Center** | Center area | **Swap** the two panes (no split) |

Active zones are highlighted with a blue translucent overlay (`rgba(96, 165, 250, 0.15)` fill, `rgba(96, 165, 250, 0.5)` border).

Dropping on the header of another pane also triggers a **center** (swap) drop.

---

## Divider Resizing

Split nodes render a 4px divider between each pair of children. The divider:
- Uses `cursor: row-resize` for vertical splits, `cursor: col-resize` for horizontal splits.
- Contains a centered 24px grip indicator (2px wide/tall, `colors.textDim` at 50% opacity).

### Resize Behavior

On mousedown:
1. The body cursor is set to the appropriate resize cursor.
2. `user-select: none` is applied to prevent text selection.
3. Mouse movement converts pixel deltas to flex deltas using `pixelsPerFlex = containerSize / allFlex`.
4. A **minimum flex of 0.15** is enforced on both sides (approximately 15% of the combined flex).
5. On mouseup, cursor and selection are restored.

---

## Tree Operations

All operations are immutable -- they return new tree objects.

| Function | Signature | Description |
|----------|-----------|-------------|
| `makeLeaf` | `(id, panel, flex?) -> LeafNode` | Create a new leaf node |
| `findLeafById` | `(node, id) -> LeafNode \| null` | Recursive search by leaf ID |
| `splitLeaf` | `(node, leafId, direction, newLeaf) -> PaneNode` | Split a leaf into a SplitNode containing the original + new leaf |
| `removeLeaf` | `(node, id) -> PaneNode \| null` | Remove a leaf; if parent split has one child left, promote it |
| `updateFlex` | `(node, parentPath, dividerIdx, flexA, flexB) -> PaneNode` | Update flex values for a divider's adjacent children |
| `swapLeavesInTree` | `(tree, idA, idB) -> PaneNode` | Swap two leaves' positions (preserving flex) |
| `moveLeaf` | `(tree, dragId, dropId, position) -> PaneNode` | Remove dragged leaf, then split the drop target to insert it |
| `collectLeaves` | `(node) -> LeafNode[]` | Flatten all leaves in the tree |
| `leafCount` | `(node) -> number` | Count total leaves |
| `canAddPanel` | `(tree, panel) -> boolean` | Check singleton and exclusive constraints |
| `hasAgentLeaf` | `(node) -> boolean` | Check if any leaf has `panel === "agent"` |

### Panel ID Allocation

Multi-instance panels (agent, shell) get IDs from a per-channel counter stored in `idCounters` (a `Map<string, number>`). The counter key is `layout-<channelId>`. When loading a layout from storage, `initIdCounter()` scans all leaf IDs to set the counter above the maximum existing numeric suffix.

Singleton panels always use their panel type as the ID (e.g., `"chat"`, `"editor"`).

---

## Singleton & Exclusive Enforcement

The `canAddPanel()` function prevents adding duplicate singleton panels and mutually exclusive panels:

```typescript
// In app/src/types/panels.ts
const SINGLETON_PANELS: PanelType[] = ["chat", "editor", "memory", "diff", "docker-browser", "host-browser", "sessions"];
const EXCLUSIVE_PANELS: PanelType[][] = [["docker-browser", "host-browser"]];

function canAddPanel(tree: PaneNode | null, panel: PanelType): boolean {
  if (!tree) return true;
  const existing = collectPanelTypes(tree);
  if (SINGLETON_PANELS.includes(panel) && existing.has(panel)) return false;
  for (const group of EXCLUSIVE_PANELS) {
    if (group.includes(panel) && group.some((p) => existing.has(p))) return false;
  }
  return true;
}
```

In the `PaneSplitMenu`, singleton panels that are already present and exclusive panels blocked by a sibling are rendered with `opacity: 0.35` and `disabled`.

---

## Tab Bar UI

The layout tab bar sits between the workspace header and the layout content area. It contains:

1. **"Layouts" label** -- uppercase, 10px, dimmed text
2. **Layout tabs** -- pill-shaped buttons (12px border-radius) with the layout name
   - Active tab: `colors.surface` background, `colors.textLight` border and text
   - Inactive tab: transparent background, `colors.textDim` text, hover shows `colors.hoverBg`
   - Each tab has an `x` button for deletion (only shown when more than 1 layout exists)
3. **`+` button** -- creates a new layout
4. **Kill button** -- red-bordered pill, only visible when agent state is "running". Kills all agent/shell sessions and the agent container.
5. **Reset button** -- opens a dropdown with "Restore defaults" and/or "Reset current"

---

## State Management in WorkspaceLayout

The `WorkspaceLayout` component manages:

| State | Type | Purpose |
|-------|------|---------|
| `layoutNames` | `string[]` | Ordered list of layout tab names |
| `activeName` | `string` | Currently active layout name |
| `tree` | `PaneNode \| null` | Current layout tree (null = empty layout) |
| `agentState` | `"running" \| "stopped" \| "none"` | Aggregate state of all terminal panes |
| `showLayoutMenu` | `boolean` | Whether the Reset dropdown is open |

### Chat State Hoisting

The `useChatState` hook is called in `WorkspaceLayout`, not in `ChatView`. This ensures the WebSocket connection and message state persist when the user switches between layouts. `ChatView` receives the `ChatState` as props and can mount/unmount freely without losing messages or connection state.

### Agent State Tracking

Each terminal pane (agent or shell) reports its `SessionStatus` via `onPaneStatus`. These are collected in `statusMapRef` (a Map of leaf ID to status). The aggregate `agentState` is computed as:
- `"none"` -- no terminal leaves exist
- `"running"` -- at least one terminal is not completed/failed
- `"stopped"` -- all terminals are completed or failed

The Kill button is only shown when `agentState === "running"`.

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Cmd+E` / `Ctrl+E` | Switch to the "Editor" layout |

This is handled in `App.tsx` via `layoutRef.current?.switchToLayout("Editor")`.
