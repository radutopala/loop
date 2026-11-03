import { useEffect, useRef, useState } from "react";

/**
 * moveTracker tells a window-level pointer move apart from one the row saw:
 * the row marks each move it gets, and the window's check of the same move
 * then reads and clears the mark.
 */
export function moveTracker() {
  let inside = false;
  return {
    markInside() {
      inside = true;
    },
    /** outside reports whether the move went unseen by the row, and clears the mark. */
    outside() {
      const out = !inside;
      inside = false;
      return out;
    },
  };
}

/**
 * useRowHover tracks whether the pointer is on a row. Besides the row's own
 * enter and leave, a hovered row also ends its hover on the first pointer
 * move outside it, or when the pointer leaves the window: a row the sidebar
 * moves or re-renders under a still pointer can miss its mouseleave, which
 * left its hover background and info popup stuck.
 *
 * "Outside" follows React's tree, not the DOM's: a move over something the
 * row portals elsewhere (the +wt menu) reaches the row's onMouseMove and
 * keeps it hovered. React handles the move at its root before the window's
 * listener sees it, so the row has marked the move as its own by then.
 */
export function useRowHover() {
  const [hovered, setHovered] = useState(false);
  const moves = useRef(moveTracker()).current;
  useEffect(() => {
    if (!hovered) return;
    const move = () => {
      if (moves.outside()) setHovered(false);
    };
    const leave = () => setHovered(false);
    window.addEventListener("mousemove", move);
    document.addEventListener("mouseleave", leave);
    return () => {
      window.removeEventListener("mousemove", move);
      document.removeEventListener("mouseleave", leave);
    };
  }, [hovered, moves]);
  return {
    hovered,
    onMouseEnter: () => setHovered(true),
    onMouseLeave: () => setHovered(false),
    onMouseMove: moves.markInside,
  };
}
