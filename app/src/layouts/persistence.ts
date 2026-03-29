import type { PaneNode } from "../types/panels";
import type { LayoutRoot } from "../canvas/types";

const LAYOUT_KEY = "loop-workspace-layout";

/** Default layout names — these are the "fixed" buttons in the header. */
export const DEFAULT_LAYOUT_NAMES = ["Chat", "Editor", "Memory", "Terminal", "Diff", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground"] as const;

export type LayoutType = "split" | "canvas";

export interface ChannelLayouts {
  active: string;
  layouts: Record<string, LayoutRoot>;
  /** Layout type per tab — "split" (default) or "canvas". */
  types: Record<string, LayoutType>;
  /** Ordered layout names for tab display. */
  order: string[];
  /** Default layout names that were explicitly removed by the user. */
  removed?: string[];
  /** Migration version — incremented after each migration runs. */
  version?: number;
}

/** Default layout type per tab name. */
export const DEFAULT_LAYOUT_TYPES: Record<string, LayoutType> = {
  Canvas: "canvas",
};

// ---------------------------------------------------------------------------
// Versioned migrations
// ---------------------------------------------------------------------------

/** Current schema version. Bump when adding a new migration. */
const CURRENT_VERSION = 7;

/**
 * Each migration transforms a ChannelLayouts from version N-1 to N.
 * They run in order, once per channel, and the version is bumped after each.
 */
const migrations: Record<number, (ch: ChannelLayouts) => void> = {
  // v1: Remove stale "Default" layout from very old format.
  1: (ch) => {
    if (ch.layouts["Default"]) {
      delete ch.layouts["Default"];
      ch.order = ch.order.filter((n) => n !== "Default");
      if (ch.active === "Default") ch.active = "Chat";
    }
  },

  // v2: Rename "Browser" layout tab to "Browser Chat".
  2: (ch) => {
    if (ch.layouts["Browser"] || ch.order.includes("Browser")) {
      if (ch.layouts["Browser"]) {
        if (!ch.layouts["Browser Chat"]) {
          ch.layouts["Browser Chat"] = ch.layouts["Browser"];
        }
        delete ch.layouts["Browser"];
      }
      ch.order = ch.order.map((n) => (n === "Browser" ? "Browser Chat" : n));
      if (ch.active === "Browser") ch.active = "Browser Chat";
    }
    if (ch.removed) {
      ch.removed = ch.removed.map((n: string) => (n === "Browser" ? "Browser Chat" : n));
    }
  },

  // v3: Add layout types map and fix corrupted canvas layouts.
  3: (ch) => {
    if (!ch.types) {
      ch.types = { ...DEFAULT_LAYOUT_TYPES };
    }
    const defaults = createDefaultLayouts();
    for (const [name, lt] of Object.entries(ch.types)) {
      if (lt === "canvas" && ch.layouts[name] && !Array.isArray((ch.layouts[name] as any).tiles)) {
        ch.layouts[name] = defaults.layouts[name] ?? { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] };
      }
    }
  },

  // v4: Replace panel:"browser" with "docker-browser" in all layouts.
  4: (ch) => {
    for (const layout of Object.values(ch.layouts)) {
      migrateBrowserPanel(layout);
    }
  },

  // v5: Add "Sessions" default layout (ensureDefaultLayouts handles insertion).
  5: () => {},

  // v6: Update Editor layout to include host shell and chat pane.
  6: (ch) => {
    const defaults = createDefaultLayouts();
    if (defaults.layouts["Editor"]) {
      ch.layouts["Editor"] = defaults.layouts["Editor"];
    }
  },
  // v7: Add "Playground" default layout.
  7: () => {
    // ensureDefaultLayouts handles insertion — no explicit migration needed.
  },
};

/** Run all pending migrations on a channel's layouts. Returns true if any ran. */
function runMigrations(ch: ChannelLayouts): boolean {
  const from = ch.version ?? 0;
  if (from >= CURRENT_VERSION) return false;

  for (let v = from + 1; v <= CURRENT_VERSION; v++) {
    migrations[v]?.(ch);
  }
  ch.version = CURRENT_VERSION;
  return true;
}

// ---------------------------------------------------------------------------
// Storage helpers
// ---------------------------------------------------------------------------

function loadAll(): Record<string, ChannelLayouts> {
  try {
    const stored = localStorage.getItem(LAYOUT_KEY);
    if (stored) {
      const parsed = JSON.parse(stored);
      if (typeof parsed === "object" && parsed !== null) {
        // Clean up old formats — ensureDefaultLayouts will recreate properly
        for (const key of Object.keys(parsed)) {
          const val = parsed[key];
          if (val && typeof val === "object" && "type" in val) {
            // Old single-tree format — discard, ensureDefaultLayouts will create fresh defaults
            delete parsed[key];
          } else if (val && typeof val === "object" && val.layouts && !val.order) {
            // Missing order field — derive from layouts keys
            val.order = Object.keys(val.layouts);
          }
        }
        return parsed;
      }
    }
  } catch { /* ignore */ }
  return {};
}

function saveAll(all: Record<string, ChannelLayouts>): void {
  try {
    localStorage.setItem(LAYOUT_KEY, JSON.stringify(all));
  } catch { /* ignore */ }
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export function loadChannelLayouts(channelId: string): ChannelLayouts | null {
  const all = loadAll();
  return all[channelId] ?? null;
}

export function loadLayout(channelId: string): LayoutRoot | null {
  const ch = loadChannelLayouts(channelId);
  if (!ch) return null;
  return ch.layouts[ch.active] ?? null;
}

export function loadActiveLayoutName(channelId: string): string | null {
  const ch = loadChannelLayouts(channelId);
  return ch?.active ?? null;
}

export function saveLayout(channelId: string, layoutName: string, tree: PaneNode): void {
  const all = loadAll();
  const ch = all[channelId] ?? { active: layoutName, layouts: {}, types: {}, order: [], version: CURRENT_VERSION };
  ch.layouts[layoutName] = tree;
  ch.active = layoutName;
  if (!ch.order.includes(layoutName)) ch.order.push(layoutName);
  all[channelId] = ch;
  saveAll(all);
}

export function saveLayoutType(channelId: string, layoutName: string, lt: LayoutType): void {
  const all = loadAll();
  const ch = all[channelId] ?? { active: layoutName, layouts: {}, types: {}, order: [], version: CURRENT_VERSION };
  ch.types[layoutName] = lt;
  ch.active = layoutName;
  if (!ch.order.includes(layoutName)) ch.order.push(layoutName);
  all[channelId] = ch;
  saveAll(all);
}

export function saveActiveLayout(channelId: string, layoutName: string): void {
  const all = loadAll();
  const ch = all[channelId];
  if (ch) {
    ch.active = layoutName;
    saveAll(all);
  }
}

export function deleteLayout(channelId: string, layoutName: string): void {
  const all = loadAll();
  const ch = all[channelId];
  if (!ch) return;
  delete ch.layouts[layoutName];
  ch.order = ch.order.filter((n) => n !== layoutName);
  // Track removed defaults so ensureDefaultLayouts doesn't re-add them.
  if ((DEFAULT_LAYOUT_NAMES as readonly string[]).includes(layoutName)) {
    ch.removed = ch.removed ?? [];
    if (!ch.removed.includes(layoutName)) ch.removed.push(layoutName);
  }
  if (ch.order.length === 0) {
    delete all[channelId];
  } else if (ch.active === layoutName) {
    ch.active = ch.order[0]!;
  }
  saveAll(all);
}

export function renameLayout(channelId: string, oldName: string, newName: string): void {
  if (oldName === newName) return;
  const all = loadAll();
  const ch = all[channelId];
  if (!ch || !ch.layouts[oldName]) return;
  ch.layouts[newName] = ch.layouts[oldName];
  delete ch.layouts[oldName];
  ch.order = ch.order.map((n) => (n === oldName ? newName : n));
  if (ch.active === oldName) ch.active = newName;
  saveAll(all);
}

export function clearLayout(channelId: string, layoutName: string): void {
  const all = loadAll();
  const ch = all[channelId];
  if (!ch) return;
  delete ch.layouts[layoutName];
  // Keep the slot in order so the tab stays but is empty
  saveAll(all);
}

export function getLayoutNames(channelId: string): string[] {
  const ch = loadChannelLayouts(channelId);
  return ch?.order ?? [];
}

export function createDefaultLayouts(): ChannelLayouts {
  return {
    active: "Chat",
    order: ["Chat", "Editor", "Memory", "Terminal", "Diff", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground"],
    types: { Canvas: "canvas" },
    version: CURRENT_VERSION,
    layouts: {
      Chat: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "chat", panel: "chat", flex: 50 }, { type: "leaf", id: "diff", panel: "diff", flex: 50 }] },
      Editor: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "split", direction: "vertical", flex: 65, children: [{ type: "leaf", id: "editor", panel: "editor", flex: 70 }, { type: "leaf", id: "shell-0", panel: "shell", flex: 30 }] }, { type: "leaf", id: "chat", panel: "chat", flex: 35 }] },
      Memory: { type: "leaf", id: "memory", panel: "memory", flex: 1 },
      Diff: { type: "leaf", id: "diff", panel: "diff", flex: 1 },
      "Browser Chat": { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "chat", panel: "chat", flex: 50 }, { type: "split", direction: "vertical", flex: 50, children: [{ type: "leaf", id: "docker-browser", panel: "docker-browser", flex: 70 }, { type: "leaf", id: "diff", panel: "diff", flex: 30 }] }] },
      Sessions: { type: "leaf", id: "sessions", panel: "sessions", flex: 1 },
      Swarm: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "agent-0", panel: "agent", flex: 40 }, { type: "split", direction: "vertical", flex: 60, children: [{ type: "leaf", id: "agent-1", panel: "agent", flex: 1 }, { type: "leaf", id: "agent-2", panel: "agent", flex: 1 }] }] },
      Canvas: { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [{ id: "agent-0", panel: "agent", x: 0, y: 0, width: 550, height: 800, zIndex: 0 }, { id: "agent-1", panel: "agent", x: 570, y: 0, width: 500, height: 390, zIndex: 0 }, { id: "agent-2", panel: "agent", x: 570, y: 410, width: 500, height: 390, zIndex: 0 }, { id: "diff", panel: "diff", x: 1090, y: 0, width: 600, height: 400, zIndex: 0 }, { id: "playground", panel: "playground", x: 1090, y: 420, width: 600, height: 380, zIndex: 0 }, { id: "memory", panel: "memory", x: 1710, y: 0, width: 800, height: 800, zIndex: 0 }] },
      Playground: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "chat", panel: "chat", flex: 40 }, { type: "leaf", id: "playground", panel: "playground", flex: 60 }] },
    },
  };
}

/** Load channel layouts, auto-creating default layouts if missing. */
export function ensureDefaultLayouts(channelId: string): ChannelLayouts {
  const all = loadAll();
  let ch = all[channelId];

  if (!ch) {
    ch = createDefaultLayouts();
    all[channelId] = ch;
    saveAll(all);
    return ch;
  }

  // Run versioned migrations.
  let changed = runMigrations(ch);

  // Add any missing default layouts (unless explicitly removed by user).
  const defaults = createDefaultLayouts();
  const removed = ch.removed ?? [];
  for (const name of DEFAULT_LAYOUT_NAMES) {
    if (removed.includes(name)) continue;
    if (!ch.layouts[name] && defaults.layouts[name]) {
      ch.layouts[name] = defaults.layouts[name];
      if (defaults.types[name]) ch.types[name] = defaults.types[name];
      changed = true;
    }
    if (!ch.order.includes(name)) {
      changed = true;
    }
  }

  if (changed) {
    // Ensure order: non-removed defaults first, then custom
    const activeDefaults = DEFAULT_LAYOUT_NAMES.filter((n) => !removed.includes(n));
    const customOrder = ch.order.filter((n) => !(DEFAULT_LAYOUT_NAMES as readonly string[]).includes(n));
    ch.order = [...activeDefaults, ...customOrder];
    saveAll(all);
  }

  return ch;
}

/** Restore any removed default layouts and clear the removed list. */
export function restoreDefaultLayouts(channelId: string): ChannelLayouts {
  const all = loadAll();
  const ch = all[channelId];
  if (!ch) return createDefaultLayouts();
  const defaults = createDefaultLayouts();
  const removed = ch.removed ?? [];
  for (const name of removed) {
    if (defaults.layouts[name] && !ch.layouts[name]) {
      ch.layouts[name] = defaults.layouts[name];
    }
    if (!ch.order.includes(name)) {
      // Insert at position matching DEFAULT_LAYOUT_NAMES order
      const idx = DEFAULT_LAYOUT_NAMES.indexOf(name as typeof DEFAULT_LAYOUT_NAMES[number]);
      const insertAt = ch.order.findIndex((n) => {
        const nIdx = DEFAULT_LAYOUT_NAMES.indexOf(n as typeof DEFAULT_LAYOUT_NAMES[number]);
        return nIdx >= 0 && nIdx > idx;
      });
      if (insertAt >= 0) {
        ch.order.splice(insertAt, 0, name);
      } else {
        // Find the first custom layout position
        const firstCustom = ch.order.findIndex((n) => !(DEFAULT_LAYOUT_NAMES as readonly string[]).includes(n));
        if (firstCustom >= 0) {
          ch.order.splice(firstCustom, 0, name);
        } else {
          ch.order.push(name);
        }
      }
    }
  }
  ch.removed = [];
  saveAll(all);
  return ch;
}

// ---------------------------------------------------------------------------
// Migration helpers
// ---------------------------------------------------------------------------

/** Recursively replace panel:"browser" with "docker-browser" in a layout tree. */
function migrateBrowserPanel(node: any): void {
  if (!node) return;
  if (node.type === "leaf" && node.panel === "browser") {
    node.panel = "docker-browser";
    if (node.id === "browser") node.id = "docker-browser";
  }
  if (node.type === "canvas" && Array.isArray(node.tiles)) {
    for (const tile of node.tiles) {
      if (tile.panel === "browser") {
        tile.panel = "docker-browser";
        if (tile.id === "browser") tile.id = "docker-browser";
      }
    }
  }
  if (Array.isArray(node.children)) {
    for (const child of node.children) {
      migrateBrowserPanel(child);
    }
  }
}
