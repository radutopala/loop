import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchUnreadNotifications } from "../api/notifications";
import { NotificationsContext, useNotificationsState, useUnreadNotifications } from "./useNotifications";

vi.mock("../api/notifications", () => ({ fetchUnreadNotifications: vi.fn() }));

const fetchMock = vi.mocked(fetchUnreadNotifications);

describe("useNotificationsState", () => {
  it("reads the provided state, or an empty one", () => {
    expect(renderHook(() => useNotificationsState()).result.current).toEqual({ unread: {}, epoch: 0, pluginsEpoch: 0 });
    const value = { unread: { a: 1 }, epoch: 2, pluginsEpoch: 3 };
    const { result } = renderHook(() => useNotificationsState(), {
      wrapper: ({ children }) => <NotificationsContext.Provider value={value}>{children}</NotificationsContext.Provider>,
    });
    expect(result.current).toBe(value);
  });
});

describe("useUnreadNotifications", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("waits until ready, then fetches and refetches on demand", async () => {
    fetchMock.mockResolvedValueOnce({ a: 1 }).mockResolvedValueOnce({ a: 2, b: 1 });
    const { result, rerender } = renderHook(({ ready }) => useUnreadNotifications(ready), { initialProps: { ready: false } });
    expect(fetchMock).not.toHaveBeenCalled();
    rerender({ ready: true });
    await waitFor(() => expect(result.current.unread).toEqual({ a: 1 }));
    expect(result.current.epoch).toBe(0);
    act(() => result.current.refetch());
    expect(result.current.epoch).toBe(1);
    await waitFor(() => expect(result.current.unread).toEqual({ a: 2, b: 1 }));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("refetches every 30 seconds", async () => {
    vi.useFakeTimers();
    fetchMock.mockResolvedValue({});
    const { result } = renderHook(() => useUnreadNotifications(true));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(result.current.epoch).toBe(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("drops a count that lands after unmounting", async () => {
    let resolve!: (v: Record<string, number>) => void;
    fetchMock.mockReturnValue(new Promise((r) => (resolve = r)));
    const { result, unmount } = renderHook(() => useUnreadNotifications(true));
    unmount();
    await act(async () => resolve({ a: 5 }));
    expect(result.current.unread).toEqual({});
  });

  it("logs a failed count", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    renderHook(() => useUnreadNotifications(true));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] counting unread notifications:", expect.any(Error)));
  });
});
