import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchTimeline } from "../api/loopApi";
import { deferred, message, messageItem } from "../test/hooks-b-fixtures";
import type { TimelineItem, TimelineResponse } from "../types";
import { TimelineCache } from "./timelineCache";
import { useTimeline } from "./useTimeline";

vi.mock("../api/loopApi", () => ({ fetchTimeline: vi.fn() }));

const thinking = (position: number, id: number): TimelineItem => ({ kind: "thinking", position, id, text: `t${id}` });
const page = (items: TimelineItem[], next: { position: number; id: number } | null = null): TimelineResponse => ({ items, next_cursor: next });
const ids = (list: TimelineItem[]) => list.map((it) => it.id);

function setup(channelId: string | null = "ch", cache = new TimelineCache(5, 50)) {
  return renderHook(({ ch }) => useTimeline(ch, cache), { initialProps: { ch: channelId } });
}

async function loaded(first: TimelineResponse, channelId = "ch", cache = new TimelineCache(5, 50)) {
  vi.mocked(fetchTimeline).mockResolvedValueOnce(first);
  const hook = setup(channelId, cache);
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
}

describe("useTimeline", () => {
  afterEach(() => {
    cleanup();
    vi.mocked(fetchTimeline).mockReset();
    vi.restoreAllMocks();
  });

  describe("first page", () => {
    it("loads the newest page oldest first", async () => {
      const { result } = await loaded(page([thinking(3, 3), thinking(2, 2)], { position: 2, id: 2 }));
      expect(fetchTimeline).toHaveBeenCalledWith("ch", { limit: 50 });
      expect(ids(result.current.items)).toEqual([2, 3]);
      expect(result.current.hasMore).toBe(true);
    });

    it("shows the cached page of a channel opened again until its page loads", async () => {
      const cache = new TimelineCache(5, 50);
      cache.set("ch", [thinking(1, 1)]);
      const next = deferred<TimelineResponse>();
      vi.mocked(fetchTimeline).mockReturnValueOnce(next.promise);
      const { result } = setup("ch", cache);
      expect(ids(result.current.items)).toEqual([1]);
      await act(async () => next.resolve(page([thinking(2, 2), thinking(1, 1)])));
      expect(ids(result.current.items)).toEqual([1, 2]);
      expect(ids(cache.get("ch")!)).toEqual([1, 2]);
    });

    it("is empty without a channel, and loadMore does nothing", async () => {
      const { result } = setup(null);
      act(() => result.current.loadMore());
      act(() => result.current.refetchHead());
      expect(fetchTimeline).not.toHaveBeenCalled();
      expect(result.current).toMatchObject({ items: [], liveTail: [], loading: false, hasMore: false });
    });

    it("stays empty when the page fails", async () => {
      vi.mocked(fetchTimeline).mockRejectedValueOnce(new Error("down"));
      const { result } = setup();
      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(result.current.items).toEqual([]);
    });

    it("drops a page that lands after a channel switch", async () => {
      const stale = deferred<TimelineResponse>();
      vi.mocked(fetchTimeline)
        .mockReturnValueOnce(stale.promise)
        .mockReturnValueOnce(new Promise(() => {}));
      const { result, rerender } = setup("a");
      rerender({ ch: "b" });
      await act(async () => stale.resolve(page([thinking(1, 1)])));
      expect(result.current.items).toEqual([]);
      expect(result.current.loading).toBe(true);
    });
  });

  describe("loadMore", () => {
    it("prepends older pages, skipping rows already shown", async () => {
      const { result } = await loaded(page([thinking(5, 5), thinking(4, 4)], { position: 4, id: 4 }));
      vi.mocked(fetchTimeline).mockResolvedValueOnce(page([thinking(4, 4), thinking(3, 3)], null));
      act(() => result.current.loadMore());
      // A second call while the page loads is ignored.
      act(() => result.current.loadMore());
      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(fetchTimeline).toHaveBeenLastCalledWith("ch", { limit: 50, cursorPosition: 4, cursorId: 4 });
      expect(fetchTimeline).toHaveBeenCalledTimes(2);
      expect(ids(result.current.items)).toEqual([3, 4, 5]);
      expect(result.current.hasMore).toBe(false);

      act(() => result.current.loadMore());
      expect(fetchTimeline).toHaveBeenCalledTimes(2);
    });

    it("logs a failed page and stops loading", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const { result } = await loaded(page([thinking(5, 5)], { position: 5, id: 5 }));
      vi.mocked(fetchTimeline).mockRejectedValueOnce(new Error("down"));
      await act(async () => result.current.loadMore());
      expect(result.current.loading).toBe(false);
      expect(warn).toHaveBeenCalledWith("[loop] loading older timeline items:", expect.any(Error));
    });

    it("does nothing once the cursor is gone, even from a stale callback", async () => {
      const { result } = await loaded(page([thinking(5, 5)], { position: 5, id: 5 }));
      const staleLoadMore = result.current.loadMore;
      vi.mocked(fetchTimeline).mockResolvedValueOnce(page([thinking(6, 6), thinking(5, 5)], null));
      await act(async () => result.current.refetchHead());
      await waitFor(() => expect(result.current.hasMore).toBe(false));
      act(() => staleLoadMore());
      expect(fetchTimeline).toHaveBeenCalledTimes(2);
    });
  });

  describe("live tail", () => {
    it("appends live items with decreasing negative ids and trigger attribution", async () => {
      const { result } = await loaded(page([]));
      act(() => {
        result.current.appendLiveThinking("hmm", "u1");
        result.current.appendLiveToolUse("tu1", "Bash", "ls", "u1");
        result.current.appendLiveToolUse(undefined, "Read", "f");
        result.current.appendLiveToolResult("tu1", "out", false, "u1");
        result.current.appendLiveToolResult(undefined, "err", true);
      });
      expect(result.current.liveTail).toEqual([
        { kind: "thinking", position: 0, id: -1, text: "hmm", truncated: false, trigger_msg_id: "u1" },
        { kind: "tool_use", position: 0, id: -2, tool_use_id: "tu1", tool_name: "Bash", tool_input: "ls", trigger_msg_id: "u1" },
        { kind: "tool_use", position: 0, id: -3, tool_use_id: "", tool_name: "Read", tool_input: "f", trigger_msg_id: undefined },
        { kind: "tool_result", position: 0, id: -4, tool_use_id: "tu1", text: "out", is_error: false, trigger_msg_id: "u1" },
        { kind: "tool_result", position: 0, id: -5, tool_use_id: "", text: "err", is_error: true, trigger_msg_id: undefined },
      ]);
    });

    it("dedups live messages by msg_id", async () => {
      const { result } = await loaded(page([]));
      act(() => {
        result.current.appendLiveThinking("x");
        result.current.appendLiveMessage(message("m1", { trigger_msg_id: "u0" }));
        result.current.appendLiveMessage(message("m1"));
        result.current.appendLiveMessage(message("m2"));
      });
      expect(result.current.liveTail.map((it) => it.kind)).toEqual(["thinking", "message", "message"]);
      expect(result.current.liveTail[1]).toMatchObject({ id: -2, trigger_msg_id: "u0" });
    });

    it("coalesces repeated compacting markers", async () => {
      const { result } = await loaded(page([]));
      act(() => {
        result.current.appendLiveCompacting("u1");
        result.current.appendLiveCompacting("u1");
        result.current.appendLiveThinking("x");
        result.current.appendLiveCompacting();
      });
      expect(result.current.liveTail.map((it) => it.kind)).toEqual(["compacting", "thinking", "compacting"]);
      expect(result.current.liveTail[0]).toEqual({ kind: "compacting", position: 0, id: -1, trigger_msg_id: "u1" });
    });
  });

  describe("mutators", () => {
    it("marks messages processed in both lists, leaving unchanged lists as they are", async () => {
      const { result } = await loaded(page([messageItem("m2", 2, 2, { is_processed: true }), messageItem("m1", 1, 1), thinking(0, 0)]));
      act(() => result.current.appendLiveMessage(message("m3")));
      const items = result.current.items;
      const tail = result.current.liveTail;

      act(() => result.current.markProcessed([]));
      act(() => result.current.markProcessed(["m2", "nope"]));
      expect(result.current.items).toBe(items);
      expect(result.current.liveTail).toBe(tail);

      act(() => result.current.markProcessed(["m1", "m3"]));
      expect(result.current.items.map((it) => it.kind === "message" && it.data.is_processed)).toEqual([false, true, true]);
      expect(result.current.liveTail[0]).toMatchObject({ data: { is_processed: true } });
    });

    it("removes a message from both lists", async () => {
      const { result } = await loaded(page([messageItem("m1", 1, 1), thinking(0, 0)]));
      act(() => result.current.appendLiveMessage(message("m2")));
      const items = result.current.items;
      act(() => result.current.removeMessage("m2"));
      expect(result.current.items).toBe(items);
      expect(result.current.liveTail).toEqual([]);
      act(() => result.current.removeMessage("m1"));
      expect(ids(result.current.items)).toEqual([0]);
    });

    it("updates a message's content in both lists", async () => {
      const { result } = await loaded(page([messageItem("m2", 2, 2), messageItem("m1", 1, 1), thinking(0, 0)]));
      act(() => result.current.appendLiveMessage(message("m3")));
      const items = result.current.items;
      act(() => result.current.updateMessageContent("m1", "text of m1"));
      expect(result.current.items).toBe(items);
      act(() => result.current.updateMessageContent("m1", "edited"));
      act(() => result.current.updateMessageContent("m3", "edited live"));
      expect(result.current.items.find((it) => it.id === 1)).toMatchObject({ data: { content: "edited" } });
      expect(result.current.items.find((it) => it.id === 2)).toMatchObject({ data: { content: "text of m2" } });
      expect(result.current.liveTail[0]).toMatchObject({ data: { content: "edited live" } });
    });

    it("moves a taken message from the items to the end of the turn that read it", async () => {
      const { result } = await loaded(page([messageItem("q1", 3, 3), { ...thinking(2, 2), trigger_msg_id: "u1" }, messageItem("u1", 1, 1)]));
      act(() => result.current.appendLiveThinking("later", "u2"));
      act(() => result.current.takeMessage("q1"));
      expect(ids(result.current.items)).toEqual([1, 2]);
      const moved = result.current.liveTail[1]!;
      expect(moved).toMatchObject({ kind: "message", position: 0, trigger_msg_id: "u2", data: { msg_id: "q1", is_processed: true, trigger_msg_id: "u2" } });
      expect(moved.id).toBeLessThan(0);
    });

    it("moves a taken live message to the end, keeping its own trigger when no row links one", async () => {
      const { result } = await loaded(page([]));
      act(() => {
        result.current.appendLiveMessage(message("q1", { trigger_msg_id: "own" }));
        result.current.appendLiveThinking("x");
      });
      const items = result.current.items;
      act(() => result.current.takeMessage("q1"));
      expect(result.current.items).toBe(items);
      expect(result.current.liveTail.map((it) => it.kind)).toEqual(["thinking", "message"]);
      expect(result.current.liveTail[1]).toMatchObject({ trigger_msg_id: "own", data: { is_processed: true } });
    });

    it("ignores taking a message it doesn't have", async () => {
      const { result } = await loaded(page([thinking(1, 1)]));
      const tail = result.current.liveTail;
      act(() => result.current.takeMessage("nope"));
      expect(result.current.liveTail).toBe(tail);
    });
  });

  describe("refetchHead", () => {
    it("replaces the head with the fresh page and keeps unbridged live messages", async () => {
      const { result } = await loaded(page([messageItem("m2", 2, 2), thinking(1, 1)], { position: 1, id: 1 }));
      act(() => {
        result.current.appendLiveThinking("live");
        result.current.appendLiveMessage(message("m3"));
        result.current.appendLiveMessage(message("q1"));
      });
      vi.mocked(fetchTimeline).mockResolvedValueOnce(page([messageItem("m3", 4, 4), thinking(3, 3), messageItem("m2", 2, 2)], { position: 2, id: 2 }));
      await act(async () => result.current.refetchHead());
      expect(fetchTimeline).toHaveBeenLastCalledWith("ch", { limit: 50 });
      expect(ids(result.current.items)).toEqual([1, 2, 3, 4]);
      expect(result.current.liveTail.map((it) => it.kind === "message" && it.data.msg_id)).toEqual(["q1"]);
      // Older pages are already under the fresh ones, so paging keeps its cursor.
      expect(result.current.hasMore).toBe(true);
      vi.mocked(fetchTimeline).mockResolvedValueOnce(page([]));
      await act(async () => result.current.loadMore());
      expect(fetchTimeline).toHaveBeenLastCalledWith("ch", { limit: 50, cursorPosition: 1, cursorId: 1 });
    });

    it("walks back pages until it reaches the previous head", async () => {
      // The head ends in two rows at one position; the second page reaches the newer one by id.
      const { result } = await loaded(page([thinking(5, 7), thinking(5, 9)]));
      vi.mocked(fetchTimeline)
        .mockResolvedValueOnce(page([thinking(8, 12), thinking(7, 11)], { position: 7, id: 11 }))
        .mockResolvedValueOnce(page([thinking(6, 10), thinking(5, 9)], { position: 5, id: 9 }));
      await act(async () => result.current.refetchHead());
      expect(fetchTimeline).toHaveBeenNthCalledWith(3, "ch", { limit: 50, cursorPosition: 7, cursorId: 11 });
      expect(ids(result.current.items)).toEqual([7, 9, 10, 11, 12]);
      // Row 7 is older than the fresh pages, so the first page's cursor stays.
      expect(result.current.hasMore).toBe(false);
    });

    it("stops after the page budget", async () => {
      const { result } = await loaded(page([thinking(0, 0)]));
      for (let p = 0; p < 6; p++) {
        const pos = 100 - p * 2;
        vi.mocked(fetchTimeline).mockResolvedValueOnce(page([thinking(pos, pos), thinking(pos - 1, pos - 1)], { position: pos - 1, id: pos - 1 }));
      }
      await act(async () => result.current.refetchHead());
      expect(fetchTimeline).toHaveBeenCalledTimes(6);
      expect(result.current.items).toHaveLength(11);
    });

    it("updates the paging cursor when nothing older is loaded", async () => {
      const { result } = await loaded(page([thinking(1, 1)]));
      vi.mocked(fetchTimeline).mockResolvedValueOnce(page([thinking(2, 2), thinking(1, 1)], { position: 1, id: 1 }));
      await act(async () => result.current.refetchHead());
      expect(ids(result.current.items)).toEqual([1, 2]);
      expect(result.current.hasMore).toBe(true);
    });

    it("keeps the items and prunes the live tail when the fetch is empty", async () => {
      const { result } = await loaded(page([thinking(1, 1)]));
      act(() => {
        result.current.appendLiveThinking("x");
        result.current.appendLiveMessage(message("q1"));
      });
      vi.mocked(fetchTimeline).mockResolvedValueOnce(page([]));
      await act(async () => result.current.refetchHead());
      expect(ids(result.current.items)).toEqual([1]);
      expect(result.current.liveTail.map((it) => it.kind)).toEqual(["message"]);
    });

    it("logs a failed refetch", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const { result } = await loaded(page([thinking(1, 1)]));
      vi.mocked(fetchTimeline).mockRejectedValueOnce(new Error("down"));
      await act(async () => result.current.refetchHead());
      expect(warn).toHaveBeenCalledWith("[loop] loading timeline:", expect.any(Error));
      expect(ids(result.current.items)).toEqual([1]);
    });
  });
});
