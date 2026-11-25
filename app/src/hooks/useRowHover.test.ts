// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { moveTracker, useRowHover } from "./useRowHover";

describe("moveTracker", () => {
  it.each([
    { name: "a move the row saw is inside", marks: [true], want: [false] },
    { name: "a move the row didn't see is outside", marks: [false], want: [true] },
    { name: "each mark covers only its own move", marks: [true, false, true], want: [false, true, false] },
  ])("$name", ({ marks, want }) => {
    const moves = moveTracker();
    const got = marks.map((seen) => {
      if (seen) moves.markInside();
      return moves.outside();
    });
    expect(got).toEqual(want);
  });
});

describe("useRowHover", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function hover() {
    const hook = renderHook(() => useRowHover());
    act(() => hook.result.current.onMouseEnter());
    return hook;
  }

  it("is hovered between the row's enter and leave", () => {
    const { result } = renderHook(() => useRowHover());
    expect(result.current.hovered).toBe(false);
    act(() => result.current.onMouseEnter());
    expect(result.current.hovered).toBe(true);
    act(() => result.current.onMouseLeave());
    expect(result.current.hovered).toBe(false);
  });

  it("ends the hover on a pointer move the row didn't see", () => {
    const { result } = hover();
    act(() => {
      window.dispatchEvent(new MouseEvent("mousemove"));
    });
    expect(result.current.hovered).toBe(false);
  });

  it("keeps the hover on a move the row saw", () => {
    const { result } = hover();
    act(() => {
      result.current.onMouseMove();
      window.dispatchEvent(new MouseEvent("mousemove"));
    });
    expect(result.current.hovered).toBe(true);
  });

  it("ends the hover when the pointer leaves the window", () => {
    const { result } = hover();
    act(() => {
      document.dispatchEvent(new MouseEvent("mouseleave"));
    });
    expect(result.current.hovered).toBe(false);
  });

  it("stops listening once the hover ends or the row unmounts", () => {
    const removeWindow = vi.spyOn(window, "removeEventListener");
    const removeDocument = vi.spyOn(document, "removeEventListener");
    const { unmount } = hover();
    unmount();
    expect(removeWindow).toHaveBeenCalledWith("mousemove", expect.any(Function));
    expect(removeDocument).toHaveBeenCalledWith("mouseleave", expect.any(Function));
  });
});
