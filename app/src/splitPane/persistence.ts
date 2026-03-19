import type { PaneNode } from "./types";

const LAYOUT_KEY = "loop-workspace-layout";

/** Default layout names — these are the "fixed" buttons in the header. */
export const DEFAULT_LAYOUT_NAMES = ["Chat", "Editor", "Memory", "Terminal", "Diff", "Browser Chat"] as const;

export interface ChannelLayouts {
  active: string;
  layouts: Record<string, PaneNode>;
  /** Ordered layout names for tab display. */
  order: string[];
  /** Default layout names that were explicitly removed by the user. */
  removed?: string[];
}

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

export function loadChannelLayouts(channelId: string): ChannelLayouts | null {
  const all = loadAll();
  return all[channelId] ?? null;
}

export function loadLayout(channelId: string): PaneNode | null {
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
  const ch = all[channelId] ?? { active: layoutName, layouts: {}, order: [] };
  ch.layouts[layoutName] = tree;
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
    order: ["Chat", "Editor", "Memory", "Terminal", "Diff", "Browser Chat"],
    layouts: {
      Chat: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "chat", panel: "chat", flex: 50 }, { type: "leaf", id: "diff", panel: "diff", flex: 50 }] },
      Editor: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "editor", panel: "editor", flex: 65 }, { type: "leaf", id: "diff-1", panel: "diff", flex: 35 }] },
      Memory: { type: "leaf", id: "memory", panel: "memory", flex: 1 },
      Diff: { type: "leaf", id: "diff", panel: "diff", flex: 1 },
      "Browser Chat": { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "chat", panel: "chat", flex: 60 }, { type: "split", direction: "vertical", flex: 40, children: [{ type: "leaf", id: "browser", panel: "browser", flex: 70 }, { type: "leaf", id: "diff", panel: "diff", flex: 30 }] }] },
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

  // Remove stale "Default" layout from old migration
  if (ch.layouts["Default"]) {
    delete ch.layouts["Default"];
    ch.order = ch.order.filter((n) => n !== "Default");
    if (ch.active === "Default") ch.active = "Chat";
  }

  // Add any missing default layouts (unless explicitly removed by user)
  const defaults = createDefaultLayouts();
  let changed = false;

  // Rename "Browser" → "Browser Chat"
  if (ch.layouts["Browser"] || ch.order.includes("Browser")) {
    if (ch.layouts["Browser"]) {
      if (!ch.layouts["Browser Chat"]) {
        ch.layouts["Browser Chat"] = ch.layouts["Browser"];
      }
      delete ch.layouts["Browser"];
    }
    ch.order = ch.order.map((n) => (n === "Browser" ? "Browser Chat" : n));
    if (ch.active === "Browser") ch.active = "Browser Chat";
    changed = true;
  }
  if (ch.removed) {
    const hadBrowser = ch.removed.includes("Browser");
    ch.removed = ch.removed.map((n: string) => (n === "Browser" ? "Browser Chat" : n));
    if (hadBrowser) changed = true;
  }

  const removed = ch.removed ?? [];
  for (const name of DEFAULT_LAYOUT_NAMES) {
    if (removed.includes(name)) continue;
    if (!ch.layouts[name] && defaults.layouts[name]) {
      ch.layouts[name] = defaults.layouts[name];
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
