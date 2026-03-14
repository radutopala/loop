# Split Pane Workspace Layouts

The workspace layout system provides a flexible split-pane interface where users can arrange multiple panels (chat, editor, terminal, etc.) in arbitrary horizontal and vertical splits. Layouts are named, switchable, and persisted per channel.

Related docs: [Chat](chat.md) | [Editor](editor.md) | [Desktop App](desktop-app.md)

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
| Agent | `agent` | `"agent-N"` | No | Docker-isolated terminal session |
| Shell | `shell` | `"shell-N"` | No | Local machine shell session |

**Singleton panels** (chat, editor, memory, diff) can appear at most once in a layout tree. The `canAddPanel()` function enforces this: if the panel type is in `SINGLETON_PANELS` and already present in the tree, the add operation is rejected.

**Multi-instance panels** (agent, shell) can appear multiple times. Each instance gets a unique numbered ID (e.g., `agent-0`, `shell-1`) from a per-channel counter.

---

## Named Layouts

Each channel has an ordered list of named layouts, displayed as a tab bar above the workspace content.

### Default Layouts

Five default layouts are created for every new channel:

| Name | Structure |
|------|-----------|
| **Chat** | Horizontal split: Chat (50%) + Diff (50%) |
| **Editor** | Horizontal split: Editor (65%) + Diff (35%) |
| **Memory** | Single leaf: Memory |
| **Terminal** | Empty (user picks a panel on first use) |
| **Diff** | Single leaf: Diff |

The "Chat" layout is the initial active layout.

### Layout Operations

| Operation | How | Behavior |
|-----------|-----|----------|
| **Switch** | Click a layout tab | Saves current tree, loads the target layout's tree from storage. Terminal sessions are cleared. |
| **Create** | Click `+` button next to tabs | Creates a new layout with auto-generated name (`Layout 1`, `Layout 2`, ...). Starts empty -- user picks the first panel from the `EmptyLayoutPicker`. |
| **Rename** | Double-click the tab label | Opens an inline text input. Commit with Enter, cancel with Escape or blur. Rejects empty names and duplicates. |
| **Delete** | Click `x` on the tab | Shows a confirmation popover ("Delete? Yes / No") with an upward-pointing arrow. Kills any running terminal sessions. Cannot delete the last remaining layout. |
| **Reset current** | Click "Reset" dropdown > "Reset current" | Restores the active layout to its default tree (only available for default layout names). Kills running terminals. |
| **Restore defaults** | Click "Reset" dropdown > "Restore defaults" | Re-adds any previously deleted default layouts. Only shown when at least one default layout was removed. |

When a default layout is deleted, its name is tracked in a `removed` list so `ensureDefaultLayouts()` does not re-add it on next load. "Restore defaults" clears this list.

---

## Persistence

All layout state is stored in `localStorage` under the key `loop-workspace-layout`.

### Storage Format

```typescript
Record<channelId, {
  active: string;                    // name of the active layout
  layouts: Record<string, PaneNode>; // layout name -> tree
  order: string[];                   // tab display order
  removed?: string[];                // deleted default layout names
}>
```

The tree is saved whenever it changes (on every split, remove, flex update, or drop operation). The active layout name is saved on switch.

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
| `canAddPanel` | `(tree, panel) -> boolean` | Check singleton constraint |
| `hasAgentLeaf` | `(node) -> boolean` | Check if any leaf has `panel === "agent"` |

### Panel ID Allocation

Multi-instance panels (agent, shell) get IDs from a per-channel counter stored in `idCounters` (a `Map<string, number>`). The counter key is `layout-<channelId>`. When loading a layout from storage, `initIdCounter()` scans all leaf IDs to set the counter above the maximum existing numeric suffix.

Singleton panels always use their panel type as the ID (e.g., `"chat"`, `"editor"`).

---

## Singleton Enforcement

The `canAddPanel()` function prevents adding duplicate singleton panels:

```typescript
const SINGLETON_PANELS: PanelType[] = ["chat", "editor", "memory", "diff"];

function canAddPanel(tree: PaneNode | null, panel: PanelType): boolean {
  if (!tree) return true;
  if (SINGLETON_PANELS.includes(panel)) {
    return !collectPanelTypes(tree).has(panel);
  }
  return true; // multi-instance: always allowed
}
```

In the `PaneSplitMenu`, singleton panels that are already present are rendered with `opacity: 0.35` and `disabled`.

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
