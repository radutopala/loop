import { act, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message } from "../../types";
import { MessageBubble } from "./bubbles";
import { useCurrentDay } from "./useCurrentDay";

describe("useCurrentDay", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    vi.setSystemTime(new Date(2026, 10, 25, 23, 50));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("changes at local midnight and keeps ticking after", () => {
    const { result } = renderHook(() => useCurrentDay());
    expect(result.current).toBe(new Date(2026, 10, 25).toDateString());
    act(() => vi.advanceTimersByTime(10 * 60 * 1000 - 1));
    expect(result.current).toBe(new Date(2026, 10, 25).toDateString());
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe(new Date(2026, 10, 26).toDateString());
    act(() => vi.advanceTimersByTime(24 * 60 * 60 * 1000));
    expect(result.current).toBe(new Date(2026, 10, 27).toDateString());
  });

  it("shares one timer and stops it when the last subscriber leaves", () => {
    const a = renderHook(() => useCurrentDay());
    const b = renderHook(() => useCurrentDay());
    expect(vi.getTimerCount()).toBe(1);
    a.unmount();
    expect(vi.getTimerCount()).toBe(1);
    b.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("adds the date to a memoized bubble's time once its day has passed", () => {
    const message: Message = {
      id: 1,
      channel_id: "ch-1",
      msg_id: "m1",
      author_id: "u-1",
      author_name: "user",
      content: "late",
      is_bot: false,
      is_processed: true,
      created_at: new Date(2026, 10, 25, 23, 45).toISOString(),
    };
    render(<MessageBubble message={message} />);
    const time = new Date(message.created_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    const date = new Date(message.created_at).toLocaleDateString([], { month: "short", day: "numeric" });
    expect(screen.getByText(time)).toBeTruthy();
    act(() => vi.advanceTimersByTime(10 * 60 * 1000));
    expect(screen.getByText(`${date} ${time}`)).toBeTruthy();
  });
});
