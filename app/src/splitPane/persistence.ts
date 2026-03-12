import type { PaneNode } from "./types";

const LAYOUT_KEY = "loop-workspace-layout";

export function loadLayout(channelId: string): PaneNode | null {
  try {
    const stored = localStorage.getItem(LAYOUT_KEY);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null && all[channelId]) {
        return all[channelId] as PaneNode;
      }
    }
  } catch { /* ignore */ }
  return null;
}

export function saveLayout(channelId: string, tree: PaneNode): void {
  try {
    const stored = localStorage.getItem(LAYOUT_KEY);
    const parsed = stored ? JSON.parse(stored) : null;
    const all: Record<string, PaneNode> = typeof parsed === "object" && parsed !== null ? parsed : {};
    all[channelId] = tree;
    localStorage.setItem(LAYOUT_KEY, JSON.stringify(all));
  } catch { /* ignore */ }
}

export function clearLayout(channelId: string): void {
  try {
    const stored = localStorage.getItem(LAYOUT_KEY);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null) {
        delete all[channelId];
        localStorage.setItem(LAYOUT_KEY, JSON.stringify(all));
      }
    }
  } catch { /* ignore */ }
}
