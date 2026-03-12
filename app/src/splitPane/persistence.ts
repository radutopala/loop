import type { PaneNode } from "./types";

const LAYOUT_KEY = "loop-workspace-layout";

/** Default layout names — these are the "fixed" buttons in the header. */
export const DEFAULT_LAYOUT_NAMES = ["Chat", "Editor", "Memory", "Terminal", "Diff"] as const;

export interface ChannelLayouts {
  active: string;
  layouts: Record<string, PaneNode>;
  /** Ordered layout names for tab display. */
  order: string[];
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
    order: ["Chat", "Editor", "Memory", "Terminal", "Diff"],
    layouts: {
      Chat: { type: "split", direction: "horizontal", flex: 1, children: [{ type: "leaf", id: "chat", panel: "chat", flex: 1 }, { type: "leaf", id: "diff", panel: "diff", flex: 1 }] },
      Editor: { type: "leaf", id: "editor", panel: "editor", flex: 1 },
      Memory: { type: "leaf", id: "memory", panel: "memory", flex: 1 },
      Diff: { type: "leaf", id: "diff", panel: "diff", flex: 1 },
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

  // Add any missing default layouts
  const defaults = createDefaultLayouts();
  let changed = false;
  for (const name of DEFAULT_LAYOUT_NAMES) {
    if (!ch.layouts[name] && defaults.layouts[name]) {
      ch.layouts[name] = defaults.layouts[name];
      changed = true;
    }
    if (!ch.order.includes(name)) {
      changed = true;
    }
  }

  if (changed) {
    // Ensure order: defaults first, then custom
    const customOrder = ch.order.filter((n) => !(DEFAULT_LAYOUT_NAMES as readonly string[]).includes(n));
    ch.order = [...DEFAULT_LAYOUT_NAMES, ...customOrder];
    saveAll(all);
  }

  return ch;
}
