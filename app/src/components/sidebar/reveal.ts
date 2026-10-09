import { type RefObject, useEffect, useRef } from "react";
import type { Channel } from "../../types";

/** Whether id is one of threads or, at any depth, their sub-threads. */
export function hasDescendant(threads: Channel[], threadsByParent: Record<string, Channel[]> | undefined, id: string): boolean {
  const seen = new Set<string>();
  const stack = [...threads];
  while (stack.length > 0) {
    const t = stack.pop() as Channel;
    if (t.id === id) return true;
    if (seen.has(t.id)) continue;
    seen.add(t.id);
    stack.push(...(threadsByParent?.[t.id] ?? []));
  }
  return false;
}

/** Expands a collapsed row when the selection moves to one of its threads,
 *  e.g. a channel the UI bridge or a deep link opens. It does so once per
 *  selection, so a collapse the user makes afterwards stays. */
export function useExpandToSelected(threads: Channel[], threadsByParent: Record<string, Channel[]> | undefined, selectedId: string | null, setCollapsed: (collapsed: boolean) => void): void {
  const expandedFor = useRef<string | null>(null);
  useEffect(() => {
    if (selectedId === expandedFor.current) return;
    if (selectedId && hasDescendant(threads, threadsByParent, selectedId)) {
      expandedFor.current = selectedId;
      setCollapsed(false);
    } else {
      // Not here (or not loaded yet): check again when it changes.
      expandedFor.current = null;
    }
  }, [selectedId, threads, threadsByParent, setCollapsed]);
}

/** Scrolls the selected row into the sidebar's view, clear of the list's
 *  sticky bar; a row already in view stays where it is. */
export function useRevealSelected(ref: RefObject<HTMLElement | null>, selected: boolean): void {
  useEffect(() => {
    const row = ref.current;
    if (!selected || !row?.scrollIntoView) return;
    row.scrollIntoView({ block: "nearest" });
    const list = row.closest<HTMLElement>("[data-sidebar-list]");
    const bar = list?.querySelector<HTMLElement>("[data-sidebar-sticky]");
    if (!list || !bar) return;
    const hidden = bar.getBoundingClientRect().bottom - row.getBoundingClientRect().top;
    if (hidden > 0) list.scrollTop -= hidden;
  }, [selected, ref]);
}
