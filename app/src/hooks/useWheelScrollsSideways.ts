import { type RefObject, useEffect } from "react";

/**
 * How far a wheel event should move a horizontal strip, or null to leave it
 * to the browser. A trackpad swipe already scrolls sideways (deltaX leads);
 * a mouse wheel only has deltaY, which a row of tabs would otherwise ignore.
 */
export function sidewaysWheelDelta(deltaX: number, deltaY: number): number | null {
  if (deltaY === 0 || Math.abs(deltaX) >= Math.abs(deltaY)) return null;
  return deltaY;
}

/** Makes a vertical wheel scroll ref's element sideways while it overflows. */
export function useWheelScrollsSideways(ref: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    // By hand: React's onWheel is passive and could not keep the page from
    // scrolling too.
    const onWheel = (e: WheelEvent) => {
      const delta = sidewaysWheelDelta(e.deltaX, e.deltaY);
      if (delta === null || el.scrollWidth <= el.clientWidth) return;
      e.preventDefault();
      el.scrollLeft += delta;
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [ref]);
}
