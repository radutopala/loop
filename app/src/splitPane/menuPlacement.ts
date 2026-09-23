// Places a dropdown anchored to a button so it stays inside the window.

export interface MenuPlacement {
  top: number;
  left: number;
  /** Set when the menu is taller than the window and has to scroll. */
  maxHeight?: number;
}

const MARGIN = 8;
const GAP = 2;

// placeMenu opens the menu below the anchor when it fits, otherwise above it,
// otherwise pinned to the window's bottom edge (scrolling if it's taller than
// the window). Horizontally it starts at the anchor's left edge and shifts
// left to stay on screen.
export function placeMenu(anchor: DOMRect, menu: { width: number; height: number }, viewport: { width: number; height: number }): MenuPlacement {
  const left = Math.max(MARGIN, Math.min(anchor.left, viewport.width - menu.width - MARGIN));
  const below = anchor.bottom + GAP;
  if (below + menu.height <= viewport.height - MARGIN) return { top: below, left };
  const above = anchor.top - GAP - menu.height;
  if (above >= MARGIN) return { top: above, left };
  const room = viewport.height - 2 * MARGIN;
  if (menu.height > room) return { top: MARGIN, left, maxHeight: room };
  return { top: viewport.height - MARGIN - menu.height, left };
}
