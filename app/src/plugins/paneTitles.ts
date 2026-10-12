import { useSyncExternalStore } from "react";

// The titles plugin panels gave their panes with ui.set_title, by leaf id.
// Each is owned by the panel that set it, so a panel leaving doesn't drop the
// title of the one that took its leaf.
const titles = new Map<string, { owner: object; title: string }>();
const listeners = new Set<() => void>();

function changed() {
  for (const l of listeners) l();
}

/** Names leafId's pane for owner; "" gives it back its label. */
export function setPaneTitle(leafId: string, owner: object, title: string): void {
  if (title) titles.set(leafId, { owner, title });
  else if (titles.get(leafId)?.owner === owner) titles.delete(leafId);
  else return;
  changed();
}

/** Drops leafId's title if owner set it. */
export function clearPaneTitle(leafId: string, owner: object): void {
  setPaneTitle(leafId, owner, "");
}

/** The title a plugin panel gave leafId's pane, or "". */
export function paneTitle(leafId: string): string {
  return titles.get(leafId)?.title ?? "";
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** paneTitle, kept current. */
export function usePaneTitle(leafId: string): string {
  return useSyncExternalStore(subscribe, () => paneTitle(leafId));
}
