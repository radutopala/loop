/** The bulk closes a tab's context menu offers, besides closing the tab itself. */
export type BulkClose = "others" | "right" | "saved";

/**
 * tabsToClose lists the tabs a bulk close from the tab at path closes, in
 * strip order. Tabs with unsaved work (an edit, or a file gone from disk
 * that only the tab still holds) are kept, so a bulk close never loses it.
 */
export function tabsToClose(kind: BulkClose, openTabs: readonly string[], path: string, unsaved: (tab: string) => boolean): string[] {
  const at = openTabs.indexOf(path);
  return openTabs.filter((tab, i) => {
    if (unsaved(tab)) return false;
    if (kind === "others") return tab !== path;
    if (kind === "right") return at >= 0 && i > at;
    return true;
  });
}

/**
 * selectionAfterClose picks the tab to show once closing leaves the strip:
 * undefined when the selected tab stays open, null when no tab is left, and
 * otherwise the first remaining tab after the selected one, or the last one
 * before it, as closing a single tab does.
 */
export function selectionAfterClose(openTabs: readonly string[], selected: string | null, closing: readonly string[]): string | null | undefined {
  if (!selected || !closing.includes(selected)) return undefined;
  const at = openTabs.indexOf(selected);
  const left = openTabs.map((tab, i) => ({ tab, i })).filter(({ tab }) => !closing.includes(tab));
  if (left.length === 0) return null;
  return (left.find(({ i }) => i > at) ?? left[left.length - 1]!).tab;
}
