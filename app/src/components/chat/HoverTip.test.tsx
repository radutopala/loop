import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HoverTip } from "./HoverTip";

// jsdom has no layout: place the anchor and the tip by their role.
function layout(anchor: Partial<DOMRect>, tipTop: number) {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    const r = this.getAttribute("role") === "tooltip" ? { top: tipTop } : anchor;
    return { left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0, x: 0, y: 0, toJSON: () => ({}), ...r } as DOMRect;
  });
}

function renderTip(props: Partial<Parameters<typeof HoverTip>[0]> = {}) {
  render(
    <HoverTip text={"Heading\nmore detail"} {...props}>
      <button type="button">target</button>
    </HoverTip>,
  );
  return screen.getByRole("button", { name: "target" }).parentElement!;
}

describe("HoverTip", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("shows above its child after a short delay, centred and kept in the window", () => {
    layout({ left: 100, width: 40, top: 300, bottom: 320 }, 200);
    const anchor = renderTip();
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(249));
    expect(screen.queryByRole("tooltip")).toBeNull();
    act(() => vi.advanceTimersByTime(1));
    const tip = screen.getByRole("tooltip");
    expect(tip.style.left).toBe(`${Math.max(8, Math.min(120 - 130, window.innerWidth - 260 - 8))}px`);
    expect(tip.style.bottom).toBe(`${window.innerHeight - 300 + 6}px`);
    expect(tip.style.top).toBe("");
    expect(tip.firstElementChild!.textContent).toBe("Heading");
    expect(tip.textContent).toBe("Headingmore detail");
    expect(tip.style.whiteSpace).toBe("pre-line");
  });

  it("shows below its child when there's no room above", () => {
    layout({ left: 0, width: 10, top: 2, bottom: 22 }, -40);
    const anchor = renderTip({ width: 100 });
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(250));
    const tip = screen.getByRole("tooltip");
    expect(tip.style.top).toBe("28px");
    expect(tip.style.bottom).toBe("");
    expect(tip.style.left).toBe("8px");
    expect(tip.style.width).toBe("100px");
  });

  it("hides on leave and on mouse down, and cancels a pending show", () => {
    layout({ top: 300 }, 200);
    const anchor = renderTip();
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(250));
    fireEvent.mouseLeave(anchor);
    expect(screen.queryByRole("tooltip")).toBeNull();
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(250));
    fireEvent.mouseDown(anchor);
    expect(screen.queryByRole("tooltip")).toBeNull();
    fireEvent.mouseEnter(anchor);
    fireEvent.mouseLeave(anchor);
    act(() => vi.advanceTimersByTime(500));
    expect(screen.queryByRole("tooltip")).toBeNull();
  });

  it("shows nothing for empty text", () => {
    const anchor = renderTip({ text: "" });
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(500));
    expect(screen.queryByRole("tooltip")).toBeNull();
  });

  it("sets the lines below the heading in monospace", () => {
    layout({ top: 300 }, 200);
    const anchor = renderTip({ text: "Run\nmake test", mono: true });
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(250));
    const tip = screen.getByRole("tooltip");
    expect(tip.style.whiteSpace).toBe("pre-wrap");
    expect((tip.lastElementChild as HTMLElement).textContent).toBe("make test");
    expect((tip.firstElementChild as HTMLElement).style.marginBottom).toBe("2px");
    fireEvent.mouseLeave(anchor);
  });

  it("gives a heading alone no gap below it", () => {
    layout({ top: 300 }, 200);
    const anchor = renderTip({ text: "Only" });
    fireEvent.mouseEnter(anchor);
    act(() => vi.advanceTimersByTime(250));
    expect((screen.getByRole("tooltip").firstElementChild as HTMLElement).style.marginBottom).toBe("0px");
  });

  it("lets a shrinking anchor give up width", () => {
    const anchor = renderTip({ shrink: true });
    expect(anchor.style.flexShrink).toBe("1");
    expect(anchor.style.minWidth).toBe("0px");
  });

  it("keeps its width otherwise", () => {
    const anchor = renderTip();
    expect(anchor.style.flexShrink).toBe("0");
    expect(anchor.style.minWidth).toBe("");
  });

  it("clears a pending show on unmount", () => {
    const { unmount } = render(
      <HoverTip text="Tip">
        <button type="button">target</button>
      </HoverTip>,
    );
    fireEvent.mouseEnter(screen.getByRole("button", { name: "target" }).parentElement!);
    expect(vi.getTimerCount()).toBe(1);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
