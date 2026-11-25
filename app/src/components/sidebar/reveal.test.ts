// @vitest-environment jsdom
import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Channel } from "../../types";
import { hasDescendant, useExpandToSelected, useRevealSelected } from "./reveal";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

const ch = (id: string) => ({ id }) as Channel;

describe("hasDescendant", () => {
  const byParent = { t1: [ch("t1a")], t1a: [ch("t1a1")] };

  it.each([
    ["a thread", "t2", true],
    ["a sub-thread", "t1a", true],
    ["a deeper one", "t1a1", true],
    ["anything else", "other", false],
  ])("finds %s", (_, id, want) => {
    expect(hasDescendant([ch("t1"), ch("t2")], byParent, id)).toBe(want);
  });

  it("works without sub-threads and ends on a cycle", () => {
    expect(hasDescendant([ch("t1")], undefined, "t1a")).toBe(false);
    expect(hasDescendant([ch("a")], { a: [ch("b")], b: [ch("a")] }, "c")).toBe(false);
  });
});

describe("useExpandToSelected", () => {
  const threads = [ch("t1")];
  const byParent = { t1: [ch("t1a")] };

  it("expands once per selection of a descendant, and again after the selection leaves", () => {
    const setCollapsed = vi.fn();
    const { rerender } = renderHook(({ sel }: { sel: string | null }) => useExpandToSelected(threads, byParent, sel, setCollapsed), { initialProps: { sel: "t1a" as string | null } });
    expect(setCollapsed).toHaveBeenCalledExactlyOnceWith(false);
    // Same selection with a fresh threads map: no second expand.
    rerender({ sel: "t1a" });
    expect(setCollapsed).toHaveBeenCalledOnce();
    rerender({ sel: "elsewhere" });
    rerender({ sel: null });
    expect(setCollapsed).toHaveBeenCalledOnce();
    rerender({ sel: "t1" });
    expect(setCollapsed).toHaveBeenCalledTimes(2);
  });

  it("checks again once a not-yet-loaded selection arrives", () => {
    const setCollapsed = vi.fn();
    const { rerender } = renderHook(({ ts }: { ts: Channel[] }) => useExpandToSelected(ts, undefined, "late", setCollapsed), { initialProps: { ts: [] as Channel[] } });
    expect(setCollapsed).not.toHaveBeenCalled();
    rerender({ ts: [ch("late")] });
    expect(setCollapsed).toHaveBeenCalledExactlyOnceWith(false);
  });
});

describe("useRevealSelected", () => {
  const rect = (top: number, bottom: number) => ({ top, bottom }) as DOMRect;

  function mount(withBar: boolean, inList = true) {
    const list = document.createElement("div");
    if (inList) list.setAttribute("data-sidebar-list", "");
    const bar = document.createElement("div");
    if (withBar) bar.setAttribute("data-sidebar-sticky", "");
    const row = document.createElement("div");
    list.append(bar, row);
    document.body.append(list);
    list.scrollTop = 100;
    const scroll = vi.fn();
    row.scrollIntoView = scroll;
    return { list, bar, row, scroll };
  }

  it("does nothing while unselected or without a row", () => {
    const { row, scroll } = mount(true);
    renderHook(() => useRevealSelected({ current: row }, false));
    renderHook(() => useRevealSelected({ current: null }, true));
    expect(scroll).not.toHaveBeenCalled();
  });

  it("scrolls the row clear of the sticky bar when hidden under it", () => {
    const { list, bar, row, scroll } = mount(true);
    vi.spyOn(bar, "getBoundingClientRect").mockReturnValue(rect(0, 30));
    vi.spyOn(row, "getBoundingClientRect").mockReturnValue(rect(10, 40));
    renderHook(() => useRevealSelected({ current: row }, true));
    expect(scroll).toHaveBeenCalledWith({ block: "nearest" });
    expect(list.scrollTop).toBe(80);
  });

  it("leaves a row already below the bar in place", () => {
    const { list, bar, row } = mount(true);
    vi.spyOn(bar, "getBoundingClientRect").mockReturnValue(rect(0, 30));
    vi.spyOn(row, "getBoundingClientRect").mockReturnValue(rect(50, 80));
    renderHook(() => useRevealSelected({ current: row }, true));
    expect(list.scrollTop).toBe(100);
  });

  it.each([
    ["no sticky bar", false, true],
    ["no list", true, false],
  ])("only scrolls into view with %s", (_, withBar, inList) => {
    const { list, row, scroll } = mount(withBar, inList);
    renderHook(() => useRevealSelected({ current: row }, true));
    expect(scroll).toHaveBeenCalledOnce();
    expect(list.scrollTop).toBe(100);
  });

  it("skips a row without scrollIntoView", () => {
    const row = document.createElement("div");
    Object.defineProperty(row, "scrollIntoView", { value: undefined });
    expect(() => renderHook(() => useRevealSelected({ current: row }, true))).not.toThrow();
  });
});
