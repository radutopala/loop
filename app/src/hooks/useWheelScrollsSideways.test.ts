// @vitest-environment jsdom
import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sidewaysWheelDelta, useWheelScrollsSideways } from "./useWheelScrollsSideways";

describe("sidewaysWheelDelta", () => {
  it.each<[string, number, number, number | null]>([
    ["a mouse wheel scrolls the strip", 0, 40, 40],
    ["upwards scrolls back", 0, -40, -40],
    ["a mostly vertical swipe still counts", 5, 40, 40],
    ["a sideways swipe is left to the browser", 30, 10, null],
    ["an even one too", 20, 20, null],
    ["no vertical motion does nothing", 0, 0, null],
  ])("%s", (_name, dx, dy, want) => {
    expect(sidewaysWheelDelta(dx, dy)).toBe(want);
  });
});

describe("useWheelScrollsSideways", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function strip(scrollWidth: number, clientWidth: number) {
    const el = document.createElement("div");
    Object.defineProperty(el, "scrollWidth", { value: scrollWidth });
    Object.defineProperty(el, "clientWidth", { value: clientWidth });
    el.scrollLeft = 0;
    let left = 0;
    Object.defineProperty(el, "scrollLeft", {
      get: () => left,
      set: (v: number) => {
        left = v;
      },
    });
    return el;
  }

  function wheel(el: HTMLElement, deltaX: number, deltaY: number) {
    const e = new WheelEvent("wheel", { deltaX, deltaY, cancelable: true });
    el.dispatchEvent(e);
    return e;
  }

  it("scrolls an overflowing strip sideways on a mouse wheel", () => {
    const el = strip(500, 100);
    renderHook(() => useWheelScrollsSideways({ current: el }));
    const e = wheel(el, 0, 40);
    expect(e.defaultPrevented).toBe(true);
    expect(el.scrollLeft).toBe(40);
  });

  it("leaves a strip that fits to the browser", () => {
    const el = strip(100, 100);
    renderHook(() => useWheelScrollsSideways({ current: el }));
    const e = wheel(el, 0, 40);
    expect(e.defaultPrevented).toBe(false);
    expect(el.scrollLeft).toBe(0);
  });

  it("leaves a sideways swipe to the browser", () => {
    const el = strip(500, 100);
    renderHook(() => useWheelScrollsSideways({ current: el }));
    const e = wheel(el, 30, 10);
    expect(e.defaultPrevented).toBe(false);
    expect(el.scrollLeft).toBe(0);
  });

  it("stops listening on unmount", () => {
    const el = strip(500, 100);
    const { unmount } = renderHook(() => useWheelScrollsSideways({ current: el }));
    unmount();
    wheel(el, 0, 40);
    expect(el.scrollLeft).toBe(0);
  });

  it("does nothing without an element", () => {
    const add = vi.spyOn(HTMLElement.prototype, "addEventListener");
    renderHook(() => useWheelScrollsSideways({ current: null }));
    // React registers its own root listeners; only this hook asks for a non-passive wheel.
    expect(add).not.toHaveBeenCalledWith("wheel", expect.any(Function), { passive: false });
  });
});
