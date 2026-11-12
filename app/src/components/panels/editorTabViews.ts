import { storageGetJSON, storageSetJSON } from "../../utils/storage";

/** Where a tab was left, as document offsets: its selection, and the start
 * of the first line in view. An offset, not pixels: the editor estimates
 * the height of lines it hasn't drawn, so a pixel scroll lands elsewhere. */
export interface TabView {
  anchor: number;
  head: number;
  top: number;
}

const KEY = "loop-editor-tab-views";

const offset = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null);

function parseTabView(v: unknown): TabView | null {
  if (!v || typeof v !== "object") return null;
  const { anchor, head, top } = v as Record<string, unknown>;
  const a = offset(anchor);
  const h = offset(head);
  const t = offset(top);
  return a === null || h === null || t === null ? null : { anchor: a, head: h, top: t };
}

/** The channel's saved tab views, so a tab opens where it was left after
 * switching tabs or channels. */
export function loadTabViews(channelId: string): Map<string, TabView> {
  const saved = storageGetJSON<Record<string, Record<string, unknown>>>(KEY)?.[channelId];
  const views = new Map<string, TabView>();
  if (!saved || typeof saved !== "object") return views;
  for (const [pathKey, v] of Object.entries(saved)) {
    const view = parseTabView(v);
    if (view) views.set(pathKey, view);
  }
  return views;
}

/** Saves the views of the channel's open tabs; a closed tab's view goes. */
export function saveTabViews(channelId: string, views: Map<string, TabView>, openTabs: readonly string[]): void {
  const all = storageGetJSON<Record<string, Record<string, TabView>>>(KEY) ?? {};
  const kept: Record<string, TabView> = {};
  for (const tab of openTabs) {
    const view = views.get(tab);
    if (view) kept[tab] = view;
  }
  if (Object.keys(kept).length > 0) all[channelId] = kept;
  else delete all[channelId];
  storageSetJSON(KEY, all);
}

/** The view to restore in a document of docLength, its offsets clamped
 * into it. */
export function clampTabView(view: TabView, docLength: number): TabView {
  return { anchor: Math.min(view.anchor, docLength), head: Math.min(view.head, docLength), top: Math.min(view.top, docLength) };
}
