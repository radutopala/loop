export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_DEFAULT_WIDTH = 280;

/** The sidebar width to start with, from what storage held: the default for
 *  anything that is not a finite number, never narrower than the minimum. */
export function savedSidebarWidth(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? Math.max(SIDEBAR_MIN_WIDTH, v) : SIDEBAR_DEFAULT_WIDTH;
}
