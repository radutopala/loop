import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchMessages } from "../api/loopApi";
import { deferred, message } from "../test/hooks-b-fixtures";
import type { Message } from "../types";
import { useMessages } from "./useMessages";

vi.mock("../api/loopApi", () => ({ fetchMessages: vi.fn() }));

type Resp = { messages: Message[]; next_cursor: number | null };

const m = (id: number, over: Partial<Message> = {}) => message(`m${id}`, { id, ...over });

describe("useMessages", () => {
  afterEach(() => {
    cleanup();
    vi.mocked(fetchMessages).mockReset();
    vi.restoreAllMocks();
  });

  it("loads the newest page oldest first, then older pages on loadMore", async () => {
    vi.mocked(fetchMessages)
      .mockResolvedValueOnce({ messages: [m(3), m(2)], next_cursor: 2 })
      .mockResolvedValueOnce({ messages: [m(1)], next_cursor: null });
    const { result } = renderHook(() => useMessages("ch"));
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetchMessages).toHaveBeenCalledWith("ch", { limit: 50 });
    expect(result.current.messages.map((x) => x.id)).toEqual([2, 3]);
    expect(result.current.hasMore).toBe(true);

    act(() => result.current.loadMore());
    // A second call while a page loads is ignored.
    act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetchMessages).toHaveBeenLastCalledWith("ch", { limit: 50, cursor: 2 });
    expect(fetchMessages).toHaveBeenCalledTimes(2);
    expect(result.current.messages.map((x) => x.id)).toEqual([1, 2, 3]);
    expect(result.current.hasMore).toBe(false);

    act(() => result.current.loadMore());
    expect(fetchMessages).toHaveBeenCalledTimes(2);
  });

  it("logs a failed older page and stops loading", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(fetchMessages)
      .mockResolvedValueOnce({ messages: [m(2)], next_cursor: 2 })
      .mockRejectedValueOnce(new Error("down"));
    const { result } = renderHook(() => useMessages("ch"));
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    await act(async () => result.current.loadMore());
    expect(result.current.loading).toBe(false);
    expect(warn).toHaveBeenCalledWith("[loop] loading older messages:", expect.any(Error));
    expect(result.current.messages.map((x) => x.id)).toEqual([2]);
  });

  it("does nothing without a channel", async () => {
    const { result } = renderHook(() => useMessages(null));
    act(() => result.current.loadMore());
    expect(fetchMessages).not.toHaveBeenCalled();
    expect(result.current).toMatchObject({ messages: [], loading: false, hasMore: false });
  });

  it("keeps an empty list when the first page fails", async () => {
    vi.mocked(fetchMessages).mockRejectedValue(new Error("down"));
    const { result } = renderHook(() => useMessages("ch"));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.messages).toEqual([]);
  });

  it("opens around a message and fills in the newer ones", async () => {
    vi.mocked(fetchMessages)
      .mockResolvedValueOnce({ messages: [m(4), m(5)], next_cursor: null })
      .mockResolvedValueOnce({ messages: [m(7), m(6), m(5)], next_cursor: 5 })
      .mockResolvedValueOnce({ messages: [m(3)], next_cursor: null });
    const { result } = renderHook(() => useMessages("ch", 5));
    await waitFor(() => expect(result.current.messages.map((x) => x.id)).toEqual([4, 5, 6, 7]));
    expect(fetchMessages).toHaveBeenNthCalledWith(1, "ch", { limit: 50, around: 5 });
    expect(fetchMessages).toHaveBeenNthCalledWith(2, "ch", { limit: 50 });
    expect(result.current.hasMore).toBe(true);

    await act(async () => result.current.loadMore());
    expect(fetchMessages).toHaveBeenLastCalledWith("ch", { limit: 50, cursor: 4 });
    expect(result.current.messages.map((x) => x.id)).toEqual([3, 4, 5, 6, 7]);
  });

  it("keeps the around window when the newest page adds nothing", async () => {
    vi.mocked(fetchMessages)
      .mockResolvedValueOnce({ messages: [m(4), m(5)], next_cursor: null })
      .mockResolvedValueOnce({ messages: [m(5), m(4)], next_cursor: null });
    const { result } = renderHook(() => useMessages("ch", 5));
    await waitFor(() => expect(fetchMessages).toHaveBeenCalledTimes(2));
    await act(async () => {});
    expect(result.current.messages.map((x) => x.id)).toEqual([4, 5]);
  });

  it("skips the fill-in when the around window is empty", async () => {
    vi.mocked(fetchMessages).mockResolvedValueOnce({ messages: [], next_cursor: null });
    const { result } = renderHook(() => useMessages("ch", 5));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetchMessages).toHaveBeenCalledTimes(1);
    expect(result.current.hasMore).toBe(false);
  });

  it("drops pages that land after a channel switch", async () => {
    const first = deferred<Resp>();
    const newest = deferred<Resp>();
    vi.mocked(fetchMessages).mockReturnValueOnce(first.promise);
    const { result, rerender } = renderHook(({ ch }) => useMessages(ch, 5), { initialProps: { ch: "a" as string | null } });
    rerender({ ch: null });
    await act(async () => first.resolve({ messages: [m(1)], next_cursor: null }));
    expect(result.current.messages).toEqual([]);

    // The fill-in page for a window that was left is dropped too.
    const around = deferred<Resp>();
    vi.mocked(fetchMessages).mockReturnValueOnce(around.promise).mockReturnValueOnce(newest.promise);
    rerender({ ch: "b" });
    await act(async () => around.resolve({ messages: [m(4)], next_cursor: null }));
    expect(result.current.messages.map((x) => x.id)).toEqual([4]);
    rerender({ ch: null });
    await act(async () => newest.resolve({ messages: [m(9)], next_cursor: null }));
    expect(result.current.messages).toEqual([]);
  });

  it("adds, marks processed and removes messages by msg_id", async () => {
    vi.mocked(fetchMessages).mockResolvedValue({ messages: [m(1)], next_cursor: null });
    const { result } = renderHook(() => useMessages("ch"));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    const before = result.current.messages;

    act(() => result.current.addMessage(m(1)));
    expect(result.current.messages).toBe(before);
    act(() => result.current.addMessage(m(2)));
    expect(result.current.messages.map((x) => x.msg_id)).toEqual(["m1", "m2"]);

    act(() => result.current.markProcessed(["m2"]));
    expect(result.current.messages.map((x) => x.is_processed)).toEqual([false, true]);
    const marked = result.current.messages;
    act(() => result.current.markProcessed(["m2", "nope"]));
    expect(result.current.messages).toBe(marked);

    act(() => result.current.removeMessage("nope"));
    expect(result.current.messages).toBe(marked);
    act(() => result.current.removeMessage("m1"));
    expect(result.current.messages.map((x) => x.msg_id)).toEqual(["m2"]);
  });
});
