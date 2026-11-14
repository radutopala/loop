/**
 * moveTab returns the tabs with the tab at from moved next to target: before
 * it, or after it when after is set. The tabs come back unchanged (the same
 * array) when either tab isn't open or the move leaves the order as it was.
 */
export function moveTab(tabs: string[], from: string, target: string, after: boolean): string[] {
  if (from === target || !tabs.includes(from) || !tabs.includes(target)) return tabs;
  const rest = tabs.filter((t) => t !== from);
  const at = rest.indexOf(target) + (after ? 1 : 0);
  const next = [...rest.slice(0, at), from, ...rest.slice(at)];
  return next.every((t, i) => t === tabs[i]) ? tabs : next;
}
