import { useLayoutEffect, useRef } from "react";

interface ChatSlotProps {
  /** The element the chat is portaled into; it lives wherever the active
   * slot is. */
  host: HTMLElement;
  /** This slot holds the chat now. */
  active: boolean;
  /** Called once the host sits in this slot. */
  onAttach: () => void;
  leafId?: string;
}

/**
 * A place the chat can be shown: the layout's chat pane or the Learn view.
 * The chat itself stays mounted in one portal; the active slot moves its
 * element in, keeping its state (scroll, focus, the draft) instead of
 * mounting it again.
 */
export function ChatSlot({ host, active, onAttach, leafId }: ChatSlotProps) {
  const ref = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const slot = ref.current;
    if (!active || !slot) return;
    if (host.parentElement !== slot) moveInto(slot, host);
    onAttach();
  }, [host, active, onAttach]);

  // A slot unmounting with the chat still in it would take the chat out of
  // the document, losing its state. React runs this before it removes the
  // slot, so the chat is parked, still in the document, until the next slot
  // takes it in the same commit.
  useLayoutEffect(() => {
    const slot = ref.current;
    return () => {
      if (slot && host.parentElement === slot) park(slot, host);
    };
  }, [host]);

  return <div ref={ref} data-learn-chat-leaf={leafId} style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, position: "relative" }} />;
}

// moveBefore keeps the moved element's state (focus, the draft); a plain
// append (the fallback, and the only way for a detached element) keeps less.
// Neither keeps scroll offsets: the chat would jump to its top. So they're
// noted before the move and put back after it, before the next paint. A slot
// of another width wraps the content to another height, so the same offset
// would show something else: one scrolled to (near) its bottom, where the chat
// follows new messages, goes back to its bottom; any other keeps the element
// at its top where it was.
// Parking spots left empty are removed.
function moveInto(slot: HTMLElement, el: HTMLElement) {
  const scrolled: { node: Element; left: number; atBottom: boolean; anchor: Element | null; offset: number; top: number }[] = [];
  for (const node of el.querySelectorAll("*")) {
    if (!node.scrollTop && !node.scrollLeft) continue;
    const anchor = topmostShown(node);
    scrolled.push({
      node,
      left: node.scrollLeft,
      top: node.scrollTop,
      atBottom: node.scrollHeight - node.scrollTop - node.clientHeight < 40,
      anchor,
      offset: anchor ? anchor.getBoundingClientRect().top - node.getBoundingClientRect().top : 0,
    });
  }
  const target = slot as HTMLElement & { moveBefore?: (node: Node, child: Node | null) => void };
  if (target.moveBefore && el.isConnected) target.moveBefore(el, null);
  else slot.appendChild(el);
  for (const { node, left, top, atBottom, anchor, offset } of scrolled) {
    node.scrollLeft = left;
    if (atBottom) node.scrollTop = node.scrollHeight;
    else {
      node.scrollTop = top;
      if (anchor) node.scrollTop += anchor.getBoundingClientRect().top - node.getBoundingClientRect().top - offset;
    }
  }
  for (const spot of document.querySelectorAll("[data-chat-park]")) if (!spot.firstChild) spot.remove();
}

// The innermost element at the top edge of a scroller's view: going down
// from the scroller, the first child reaching into the view, as long as one
// starts above its top edge. Stuck ones (a sticky banner) stay put whatever
// the scroll, so they're passed over.
function topmostShown(scroller: Element): Element | null {
  const edge = scroller.getBoundingClientRect().top;
  const scrolls = (c: Element) => !["sticky", "fixed"].includes(getComputedStyle(c).position);
  let found: Element | null = null;
  let parent: Element = scroller;
  for (;;) {
    const child = Array.from(parent.children).find((c) => c.getBoundingClientRect().bottom > edge && scrolls(c));
    if (!child) return found;
    found = child;
    if (child.getBoundingClientRect().top >= edge) return found;
    parent = child;
  }
}

// Moves el out of slot into an off-screen spot of the same size, so nothing
// about it (its scroll, above all) changes while it waits. It may wait a
// while (its pane minimized, or another one maximized): meanwhile the spot is
// inert and hidden from assistive tech, so nothing focuses or types into a
// chat that can't be seen (a composer filled by Discuss, say).
function park(slot: HTMLElement, el: HTMLElement) {
  const { width, height } = slot.getBoundingClientRect();
  const spot = document.createElement("div");
  spot.setAttribute("data-chat-park", "");
  spot.inert = true;
  spot.setAttribute("aria-hidden", "true");
  spot.style.cssText = `position: fixed; left: -100000px; top: 0; width: ${width}px; height: ${height}px; display: flex; flex-direction: column`;
  document.body.appendChild(spot);
  moveInto(spot, el);
}

/** A fresh element for the chat's portal, filling whichever slot holds it. */
export function createChatHost(): HTMLDivElement {
  const el = document.createElement("div");
  el.style.cssText = "flex: 1; display: flex; flex-direction: column; overflow: hidden; min-height: 0; position: relative";
  return el;
}
