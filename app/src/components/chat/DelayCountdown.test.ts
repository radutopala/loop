// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DelayCountdown, formatCountdown } from "./DelayCountdown";

describe("formatCountdown", () => {
  it("renders sub-minute delays as m:ss", () => {
    expect(formatCountdown(5)).toBe("0:05");
    expect(formatCountdown(45)).toBe("0:45");
  });

  it("renders minutes as m:ss with zero-padded seconds", () => {
    expect(formatCountdown(60)).toBe("1:00");
    expect(formatCountdown(90)).toBe("1:30");
    expect(formatCountdown(599)).toBe("9:59");
  });

  it("switches to h:mm:ss once an hour or more remains", () => {
    expect(formatCountdown(3600)).toBe("1:00:00");
    expect(formatCountdown(3661)).toBe("1:01:01");
    expect(formatCountdown(7325)).toBe("2:02:05");
  });

  it("floors fractional seconds", () => {
    expect(formatCountdown(30.9)).toBe("0:30");
  });

  it("clamps negatives to 0:00", () => {
    expect(formatCountdown(-1)).toBe("0:00");
    expect(formatCountdown(-500)).toBe("0:00");
  });
});

describe("DelayCountdown", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  const now = () => Date.now() / 1000;

  it("ticks down once a second and disappears when the delay ends", () => {
    const { container } = render(createElement(DelayCountdown, { notBefore: now() + 2 }));
    expect(screen.getByTitle("Scheduled — runs when the countdown ends").textContent).toBe("⏱ 0:02");
    act(() => vi.advanceTimersByTime(1000));
    expect(container.textContent).toBe("⏱ 0:01");
    act(() => vi.advanceTimersByTime(1000));
    expect(container.innerHTML).toBe("");
  });

  it("renders nothing for a delay already over", () => {
    const { container } = render(createElement(DelayCountdown, { notBefore: now() - 5 }));
    expect(container.innerHTML).toBe("");
  });

  it("restarts the countdown when the time changes, and stops ticking on unmount", () => {
    const { container, rerender, unmount } = render(createElement(DelayCountdown, { notBefore: now() + 5 }));
    rerender(createElement(DelayCountdown, { notBefore: now() + 90 }));
    expect(container.textContent).toBe("⏱ 1:30");
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
