import { type RefObject, useEffect, useState } from "react";

/** pointerLeft reports whether a pointer event's target lies outside row. */
export function pointerLeft(row: Pick<Node, "contains"> | null, target: EventTarget | null): boolean {
  return !row || !target || !row.contains(target as Node);
}

/**
 * useRowHover tracks whether the pointer is on the row in ref. Besides the
 * row's own enter and leave, a hovered row also ends its hover on the first
 * pointer move outside it, or when the pointer leaves the window: a row the
 * sidebar moves or re-renders under a still pointer can miss its mouseleave,
 * which left its hover background and info popup stuck.
 */
export function useRowHover(ref: RefObject<HTMLElement | null>) {
  const [hovered, setHovered] = useState(false);
  useEffect(() => {
    if (!hovered) return;
    const move = (e: MouseEvent) => {
      if (pointerLeft(ref.current, e.target)) setHovered(false);
    };
    const leave = () => setHovered(false);
    window.addEventListener("mousemove", move, true);
    document.addEventListener("mouseleave", leave);
    return () => {
      window.removeEventListener("mousemove", move, true);
      document.removeEventListener("mouseleave", leave);
    };
  }, [hovered, ref]);
  return {
    hovered,
    onMouseEnter: () => setHovered(true),
    onMouseLeave: () => setHovered(false),
  };
}
