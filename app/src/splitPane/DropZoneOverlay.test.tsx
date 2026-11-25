import { act, createEvent, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DRAG_MIME, DropZoneOverlay, emitLayoutDragEnd, emitLayoutDragStart } from "./DropZoneOverlay";
import type { DropPosition } from "./types";

/** A drag event carrying a pane drag (or other data) at a point. */
function dragEvent(kind: "dragOver" | "drop", el: Element, { types = [DRAG_MIME], data = "", x = 50, y = 50 }: { types?: string[]; data?: string; x?: number; y?: number } = {}) {
  const dataTransfer = { types, getData: vi.fn((mime: string) => (mime === DRAG_MIME ? data : "")), dropEffect: "none" };
  const event = createEvent[kind](el, { dataTransfer });
  Object.defineProperty(event, "clientX", { value: x });
  Object.defineProperty(event, "clientY", { value: y });
  fireEvent(el, event);
  return { event, dataTransfer };
}

function setup() {
  const onDrop = vi.fn();
  const { container } = render(<DropZoneOverlay leafId="me" headerHeight={22} onDrop={onDrop} />);
  const zone = container.firstElementChild as HTMLElement;
  vi.spyOn(zone, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 100, height: 100 } as DOMRect);
  const highlight = () => zone.firstElementChild as HTMLElement | null;
  return { onDrop, zone, highlight };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("DropZoneOverlay", () => {
  it("sits under the header and takes the mouse only while a pane is dragged", () => {
    const { zone } = setup();
    expect(zone.style.top).toBe("22px");
    expect(zone.style.zIndex).toBe("-1");
    expect(zone.style.pointerEvents).toBe("none");
    act(() => emitLayoutDragStart());
    expect(zone.style.zIndex).toBe("10");
    expect(zone.style.pointerEvents).toBe("auto");
    act(() => emitLayoutDragEnd());
    expect(zone.style.zIndex).toBe("-1");
  });

  it.each<{ name: string; x: number; y: number; want: Partial<CSSStyleDeclaration> }>([
    { name: "top", x: 50, y: 10, want: { top: "0px", height: "50%" } },
    { name: "bottom", x: 50, y: 90, want: { bottom: "0px", height: "50%" } },
    { name: "left", x: 10, y: 50, want: { left: "0px", width: "50%", bottom: "0px" } },
    { name: "right", x: 90, y: 50, want: { right: "0px", width: "50%", bottom: "0px" } },
    { name: "center", x: 50, y: 50, want: { top: "0px", left: "0px", right: "0px", bottom: "0px", width: "", height: "" } },
  ])("highlights the $name zone under a pane drag", ({ x, y, want }) => {
    const { zone, highlight } = setup();
    const { event, dataTransfer } = dragEvent("dragOver", zone, { x, y });
    expect(event.defaultPrevented).toBe(true);
    expect(dataTransfer.dropEffect).toBe("move");
    const style = highlight()!.style;
    for (const [k, v] of Object.entries(want)) expect(style[k as keyof CSSStyleDeclaration]).toBe(v);
  });

  it("ignores a drag that isn't a pane", () => {
    const { zone, highlight } = setup();
    const { event } = dragEvent("dragOver", zone, { types: ["text/plain"] });
    expect(event.defaultPrevented).toBe(false);
    expect(highlight()).toBeNull();
  });

  it("keeps the highlight while moving onto a child and drops it on leaving", () => {
    const { zone, highlight } = setup();
    dragEvent("dragOver", zone);
    const leave = (to: Element | null) => {
      const event = createEvent.dragLeave(zone);
      Object.defineProperty(event, "relatedTarget", { value: to });
      fireEvent(zone, event);
    };
    leave(highlight());
    expect(highlight()).not.toBeNull();
    leave(document.body);
    expect(highlight()).toBeNull();
  });

  it.each<{ name: string; x: number; y: number; want: DropPosition }>([
    { name: "on an edge", x: 95, y: 50, want: "right" },
    { name: "in the middle", x: 50, y: 50, want: "center" },
  ])("drops another pane $name", ({ x, y, want }) => {
    const { zone, onDrop, highlight } = setup();
    const ended = vi.fn();
    document.addEventListener("layout-drag-end", ended);
    dragEvent("dragOver", zone, { x, y });
    const { event } = dragEvent("drop", zone, { data: "other", x, y });
    document.removeEventListener("layout-drag-end", ended);
    expect(event.defaultPrevented).toBe(true);
    expect(onDrop).toHaveBeenCalledWith("other", "me", want);
    expect(highlight()).toBeNull();
    expect(ended).toHaveBeenCalled();
  });

  it.each(["me", ""])("does not drop a pane onto itself or an empty drag (%j)", (data) => {
    const { zone, onDrop } = setup();
    dragEvent("drop", zone, { data });
    expect(onDrop).not.toHaveBeenCalled();
  });

  it.each(["dragend", "drop"])("resets when the document sees %s", (type) => {
    const { zone, highlight } = setup();
    act(() => emitLayoutDragStart());
    dragEvent("dragOver", zone);
    act(() => {
      document.dispatchEvent(new Event(type));
    });
    expect(highlight()).toBeNull();
    expect(zone.style.zIndex).toBe("-1");
  });
});
