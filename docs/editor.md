# Code Editor Panel

The editor panel provides a full-featured code editor with a file tree sidebar, multi-tab interface, and syntax highlighting. It uses CodeMirror 6 with a Darcula (dark) theme and communicates with the Go backend API for all file operations.

Related docs: [Layouts](layouts.md) | [Desktop App](desktop-app.md)

---

## Architecture

The `EditorPanel` component (`src/components/EditorPanel.tsx`) combines:
- A resizable **file tree sidebar** on the left
- A **tab bar** for open files
- A **CodeMirror 6 editor** for the active file
- An optional **markdown preview** pane for `.md`/`.mdx` files

All file I/O goes through the backend REST API (`/api/channels/{id}/files`, `/api/channels/{id}/file`), not direct filesystem access. This ensures path traversal protection, Docker container support, and compatibility with browser development mode.

---

## CodeMirror 6 Configuration

### Theme

The editor uses a custom **Darcula** theme inspired by JetBrains IDEs:

- Font: JetBrains Mono (loaded via `@fontsource/jetbrains-mono`), 13px
- Background: `colors.sidebar` (`#171717`)
- Text: `#a9b7c6`
- Caret: `#bbbbbb`
- Selection: `#214283`
- Active line: `rgba(255,255,255,0.04)`
- Matching bracket: `#3b514d` background, `#ffef28` text
- Gutter: same background as editor, `#606366` text
- Active gutter line: `colors.selectedBg` background

The highlight style covers keywords (`#cc7832`), strings (`#6a8759`), numbers (`#6897bb`), comments (`#808080` italic), doc comments (`#629755` italic), function names (`#ffc66d`), types (`#ffc66d`), properties (`#9876aa`), and more.

### Extensions

| Extension | Purpose |
|-----------|---------|
| `lineNumbers()` | Line number gutter |
| `highlightActiveLine()` | Highlights the current line |
| `highlightActiveLineGutter()` | Highlights the gutter for the current line |
| `drawSelection()` | Custom selection rendering |
| `EditorView.lineWrapping` | Soft line wrapping |
| `bracketMatching()` | Matching bracket highlighting |
| `foldGutter()` | Code folding controls in gutter |
| `history()` | Undo/redo |
| `search({ top: true })` | Search panel at the top |
| Language extension | Syntax-specific highlighting (see below) |

---

## Language Support

Language extensions are selected based on file extension:

| Extensions | Language |
|------------|----------|
| `.js`, `.jsx`, `.mjs`, `.cjs` | JavaScript |
| `.ts`, `.tsx` | TypeScript (with JSX for `.tsx`) |
| `.go` | Go |
| `.py` | Python |
| `.json`, `.jsonl` | JSON |
| `.md`, `.mdx` | Markdown |
| `.css`, `.scss` | CSS |
| `.html`, `.htm`, `.svg` | HTML |
| `.yaml`, `.yml` | YAML |

Files with unrecognized extensions are opened without syntax highlighting.

---

## File Tree Sidebar

### Layout

The sidebar occupies the left side of the editor panel with:

| Property | Value |
|----------|-------|
| Default width | 280px |
| Minimum width | 120px |
| Maximum width | 500px |
| Persisted in | `localStorage` key `loop-editor-tree-width` |

### Header

The tree header contains:
- **"FILES" label** -- uppercase, dimmed
- **Refresh button** -- reloads the root and all expanded directories, and re-reads the currently open file from disk
- **New file button** (`+`) -- opens an inline input for creating a new file

### Directory Listing

Files are fetched from the API (`GET /api/channels/{id}/files?path=<dir>`). The root directory (`.`) is loaded on mount.

- **Directories** show a chevron icon that rotates on expand/collapse
- **Clicking a directory** toggles its expanded state and lazy-loads its contents if not yet fetched
- **Files** display with their name; clicking opens them in a tab
- The tree tracks expanded directories in a `Set<string>` and directory contents in a `Map<string, FileEntry[]>`

### Right-Click Context Menu

Right-clicking a file or directory opens a context menu with:

| Item | Available On | Action |
|------|-------------|--------|
| Copy relative path | File, Directory | Copies relative path to clipboard |
| Copy absolute path | File, Directory | Copies `dirPath + "/" + relativePath` to clipboard |
| New file here | Directory only | Opens inline input prefilled with directory path |
| Delete | File only | Deletes the file via API, closes its tab if open |

### Resize Handle

A 4px-wide handle on the right edge of the tree allows horizontal resizing:
- Cursor changes to `col-resize` on hover
- Drag updates width live (clamped to min/max)
- Final width is saved to `localStorage`
- `user-select: none` is applied during resize to prevent text selection

---

## Tab System

### Opening Tabs

- Clicking a file in the tree opens it in a new tab (or switches to it if already open)
- Tabs are displayed horizontally above the editor
- The active tab is highlighted

### Dirty Tracking

- Any edit marks the tab as dirty (tracked in `dirtyTabs` Set)
- Dirty tabs show a dot indicator next to the filename
- The `dirtyContentRef` Map caches unsaved content in memory so it survives tab switches without triggering auto-save

### Closing Tabs

- Each tab has a close button
- Closing the active tab auto-selects the adjacent tab
- If auto-save is enabled, the file is saved before closing
- Dirty content cache is cleared for the closed tab

### Persistence

Tab state (open tabs and selected tab) is persisted in `localStorage`:
- Key: `loop-editor-tabs` (standalone) or `loop-layout-editor-tabs` (embedded in layout)
- Format: `Record<channelId, { tabs: string[]; selected: string | null }>`
- Saved on every tab open, close, or switch

---

## Auto-Save on Blur

Auto-save is controlled by the `autoSaveOnBlur` setting (default: `true`, configurable in [Settings](settings.md)).

### Behavior

| Trigger | Action |
|---------|--------|
| Window blur | If auto-save enabled, saves all dirty tabs |
| Tab switch | If auto-save enabled, saves the current tab; otherwise, caches content in `dirtyContentRef` |
| Tab close | If auto-save enabled, saves before closing |
| Component unmount | If auto-save enabled, saves all dirty tabs |

### The `autoSaveOnBlurRef` Pattern

The auto-save setting can change at any time. To avoid stale closures in cleanup functions and callbacks, the setting is stored in a `useRef` (`autoSaveOnBlurRef`) that is updated on every render. Cleanup functions and event handlers read from the ref instead of the state value.

### Window Focus Reload

When the window regains focus, the editor reloads the currently open file from disk. If the content differs from the editor state (indicating an external edit), the editor is updated and the dirty flag is cleared.

---

## Dirty Content Cache

The `dirtyContentRef` is a `Map<string, string>` that stores unsaved editor content in memory.

**Purpose:** When switching tabs, if a file has been edited but not saved (and auto-save is disabled), the unsaved content must be preserved. Since CodeMirror creates a new editor state on tab switch, the dirty content is snapshot into this map before switching away and restored when switching back.

**Lifecycle:**
1. On tab switch away: if dirty, snapshot `view.state.doc.toString()` into the map
2. On tab switch to: if the path exists in the map, use cached content instead of fetching from disk
3. On save: remove the path from the map
4. On tab close: remove the path from the map

---

## File Operations

All operations go through the Go backend API for security.

| Operation | API | Notes |
|-----------|-----|-------|
| List directory | `GET /api/channels/{id}/files?path=<dir>` | Returns `FileEntry[]` with name, type, size |
| Read file | `GET /api/channels/{id}/file?path=<path>` | Returns text content; `X-File-Binary: true` header for binary files |
| Save file | `PUT /api/channels/{id}/file?path=<path>` | Body is raw file content. Appends trailing newline if missing. |
| Delete file | `DELETE /api/channels/{id}/file?path=<path>` | Removes file from disk |
| Create file | `PUT` with empty body | Same as save with empty content |

### Binary File Detection

The backend checks the first 512 bytes for null bytes. If detected, the response includes `X-File-Binary: true` header and the editor shows a "Binary file" message instead of attempting to render.

### Maximum File Size

The backend enforces a **5MB** maximum file size (`maxFileSize = 5 * 1024 * 1024`). Files exceeding this limit return an error. The editor displays file sizes formatted as B, KB, or MB.

### Path Validation

The Go backend's `validateFilePath()` function rejects:
- Empty paths
- Absolute paths (starting with `/`)
- Path traversal (`..` segments)
- Symlinks that escape the project root directory

---

## Markdown Preview

For `.md` and `.mdx` files, the editor shows a split view with:
- CodeMirror editor on the left
- Rendered HTML preview on the right (using the `marked` library)

The preview updates with a 300ms debounce after each edit. A toggle button lets the user show/hide the preview pane.

---

## Keyboard Shortcuts

| Shortcut | Context | Action |
|----------|---------|--------|
| `Cmd+S` / `Ctrl+S` | Editor focused or window-level | Save the active file immediately |
| `Cmd+F` / `Ctrl+F` | Editor focused | Open the search panel |
| `Cmd+H` / `Ctrl+H` | Editor focused (via search keymap) | Open search and replace |
| `Cmd+G` / `Ctrl+G` | Search panel open | Find next match |
| Fold/unfold shortcuts | Editor focused (via fold keymap) | Collapse/expand code blocks |
| `Tab` | Editor focused | Insert indentation (`indentWithTab`) |
| `Cmd+Z` / `Ctrl+Z` | Editor focused | Undo |
| `Cmd+Shift+Z` / `Ctrl+Y` | Editor focused | Redo |

### Editor Context Menu

Right-clicking inside the CodeMirror editor area opens a custom context menu with:

| Item | Condition | Action |
|------|-----------|--------|
| Copy | Text selected | Copy selection to clipboard |
| Cut | Text selected | Copy and remove selection |
| Select All | Always | Select entire document |
| Find... | Always | Open the search panel |
