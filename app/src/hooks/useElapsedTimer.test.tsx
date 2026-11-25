import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useElapsedTimer } from "./useElapsedTimer";

describe("useElapsedTimer", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("counts whole seconds from the start", () => {
    const { result } = renderHook(() => useElapsedTimer());
    expect(result.current.elapsed).toBe(0);
    act(() => result.current.start());
    act(() => vi.advanceTimersByTime(3000));
    expect(result.current.elapsed).toBe(3);
  });

  it("counts from a given start time", () => {
    const { result } = renderHook(() => useElapsedTimer());
    act(() => result.current.start(Date.now() - 42_500));
    expect(result.current.elapsed).toBe(42);
  });

  it("ignores a second start while running", () => {
    const { result } = renderHook(() => useElapsedTimer());
    act(() => result.current.start());
    act(() => vi.advanceTimersByTime(5000));
    act(() => result.current.start(Date.now()));
    expect(result.current.elapsed).toBe(5);
    act(() => vi.advanceTimersByTime(1000));
    expect(result.current.elapsed).toBe(6);
  });

  it("freezes on stop and resumes from the original start", () => {
    const { result } = renderHook(() => useElapsedTimer());
    act(() => result.current.start());
    act(() => vi.advanceTimersByTime(2000));
    act(() => result.current.stop());
    act(() => vi.advanceTimersByTime(5000));
    expect(result.current.elapsed).toBe(2);
    act(() => result.current.resume());
    act(() => vi.advanceTimersByTime(1000));
    expect(result.current.elapsed).toBe(8);
  });

  it("doesn't resume a timer that never started or is already running", () => {
    const { result } = renderHook(() => useElapsedTimer());
    act(() => result.current.resume());
    act(() => vi.advanceTimersByTime(3000));
    expect(result.current.elapsed).toBe(0);
    expect(vi.getTimerCount()).toBe(0);

    act(() => result.current.start());
    act(() => result.current.resume());
    expect(vi.getTimerCount()).toBe(1);
  });

  it("zeroes out on reset and stops ticking", () => {
    const { result } = renderHook(() => useElapsedTimer());
    act(() => result.current.start());
    act(() => vi.advanceTimersByTime(4000));
    act(() => result.current.reset());
    expect(result.current.elapsed).toBe(0);
    act(() => vi.advanceTimersByTime(4000));
    expect(result.current.elapsed).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("treats a zero start time as unset: it doesn't tick and can start again", () => {
    const { result } = renderHook(() => useElapsedTimer());
    act(() => result.current.start(0));
    const shown = result.current.elapsed;
    act(() => vi.advanceTimersByTime(3000));
    expect(result.current.elapsed).toBe(shown);
    act(() => result.current.reset());
    act(() => result.current.start());
    act(() => vi.advanceTimersByTime(2000));
    expect(result.current.elapsed).toBe(2);
  });
});
