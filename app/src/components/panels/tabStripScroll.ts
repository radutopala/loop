/** Where a horizontally scrolled strip should scroll to bring an item fully
 * into view, or null when it already is. Positions are in the same
 * coordinates (e.g. client rects); an item wider than the strip lines up
 * its start. */
export function scrollLeftToShow(strip: { left: number; width: number; scrollLeft: number }, item: { left: number; width: number }): number | null {
  const start = item.left - strip.left;
  const end = start + item.width;
  if (start < 0 || item.width > strip.width) return strip.scrollLeft + start;
  if (end > strip.width) return strip.scrollLeft + end - strip.width;
  return null;
}
