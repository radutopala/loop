import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { prefersReducedMotion, usePresence } from "./usePresence";

describe("usePresence", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("starts mounted and shown when open", () => {
    const { result } = renderHook(() => usePresence(true, 200));
    expect(result.current).toEqual({ mounted: true, shown: true });
  });

  it("starts unmounted and hidden when closed", () => {
    const { result } = renderHook(() => usePresence(false, 200));
    expect(result.current).toEqual({ mounted: false, shown: false });
  });

  it("mounts at once on open and shows two frames later", () => {
    const { result, rerender } = renderHook(({ open }) => usePresence(open, 200), { initialProps: { open: false } });
    act(() => vi.advanceTimersByTime(300));
    rerender({ open: true });
    expect(result.current).toEqual({ mounted: true, shown: false });
    act(() => vi.advanceTimersToNextFrame());
    expect(result.current.shown).toBe(false);
    act(() => vi.advanceTimersToNextFrame());
    expect(result.current).toEqual({ mounted: true, shown: true });
  });

  it("hides at once on close and unmounts after the duration", () => {
    const { result, rerender } = renderHook(({ open }) => usePresence(open, 200), { initialProps: { open: true } });
    rerender({ open: false });
    expect(result.current).toEqual({ mounted: true, shown: false });
    act(() => vi.advanceTimersByTime(199));
    expect(result.current.mounted).toBe(true);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current.mounted).toBe(false);
  });

  it("cancels the pending frames when closed before they run", () => {
    const cancel = vi.spyOn(window, "cancelAnimationFrame");
    const { result, rerender } = renderHook(({ open }) => usePresence(open, 200), { initialProps: { open: false } });
    rerender({ open: true });
    act(() => vi.advanceTimersToNextFrame());
    rerender({ open: false });
    expect(cancel).toHaveBeenCalledTimes(2);
    act(() => vi.advanceTimersByTime(500));
    expect(result.current).toEqual({ mounted: false, shown: false });
  });

  it("keeps the element mounted when reopened before the duration ends", () => {
    const { result, rerender } = renderHook(({ open }) => usePresence(open, 200), { initialProps: { open: true } });
    rerender({ open: false });
    act(() => vi.advanceTimersByTime(100));
    rerender({ open: true });
    act(() => vi.advanceTimersByTime(500));
    expect(result.current).toEqual({ mounted: true, shown: true });
  });
});

describe("prefersReducedMotion", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("is false without matchMedia", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(prefersReducedMotion()).toBe(false);
  });

  it.each([true, false])("follows the media query matching %s", (matches) => {
    const matchMedia = vi.fn().mockReturnValue({ matches });
    vi.stubGlobal("matchMedia", matchMedia);
    expect(prefersReducedMotion()).toBe(matches);
    expect(matchMedia).toHaveBeenCalledWith("(prefers-reduced-motion: reduce)");
  });

  it("is false without a window", () => {
    vi.stubGlobal("window", undefined);
    expect(prefersReducedMotion()).toBe(false);
  });
});
