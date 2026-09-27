import { useEffect, useState } from "react";

/**
 * Keeps an element mounted while it animates out. `mounted` turns on as soon
 * as `open` does and off `durationMs` after it turns off; `shown` follows
 * `open`, but only turns on a frame after mounting, so the element first
 * paints in its hidden state and its CSS transition runs.
 */
export function usePresence(open: boolean, durationMs: number): { mounted: boolean; shown: boolean } {
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(open);

  useEffect(() => {
    if (open) {
      setMounted(true);
      // Two frames: the first paints the element hidden, the second starts
      // the transition.
      let inner = 0;
      const outer = requestAnimationFrame(() => {
        inner = requestAnimationFrame(() => setShown(true));
      });
      return () => {
        cancelAnimationFrame(outer);
        cancelAnimationFrame(inner);
      };
    }
    setShown(false);
    const timer = setTimeout(() => setMounted(false), durationMs);
    return () => clearTimeout(timer);
  }, [open, durationMs]);

  return { mounted, shown };
}

/** Whether the user asked the OS for less motion. */
export function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}
