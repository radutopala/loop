// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type ExplainState, type Explanation, explainTurn, fetchExplainState, fetchExplanations, setExplain } from "../api/explain";
import type { WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { useExplain } from "./useExplain";

vi.mock("../api/explain", () => ({
  explainTurn: vi.fn(),
  fetchExplainState: vi.fn(),
  fetchExplanations: vi.fn(),
  setExplain: vi.fn(),
}));

const STATE: ExplainState = { available: true, explain: "", default_explain: true, enabled: true };

function explanation(over: Partial<Explanation>): Explanation {
  return {
    id: 1,
    channel_id: "c",
    message_id: "m1",
    explain_channel_id: "x",
    status: "done",
    content: "text",
    created_at: "",
    updated_at: "",
    ...over,
  };
}

// A subscribeChatEvents that hands each listener to the test, to emit events.
function eventBus() {
  const listeners = new Set<ChatEventListener>();
  return {
    subscribe: (l: ChatEventListener) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    emit: (event: Partial<WSEvent>) => {
      for (const l of listeners) l(event as WSEvent);
    },
    size: () => listeners.size,
  };
}

describe("useExplain", () => {
  beforeEach(() => {
    vi.mocked(fetchExplainState).mockResolvedValue(STATE);
    vi.mocked(fetchExplanations).mockResolvedValue([explanation({ id: 1, message_id: "m1" })]);
  });
  afterEach(() => {
    vi.resetAllMocks();
    vi.restoreAllMocks();
  });

  it("loads the channel's explain state and explanations", async () => {
    const { result } = renderHook(() => useExplain("c"));
    expect(result.current.loaded).toBe(false);
    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(fetchExplainState).toHaveBeenCalledWith("c");
    expect(fetchExplanations).toHaveBeenCalledWith("c");
    expect(result.current.available).toBe(true);
    expect(result.current.explain).toBe("");
    expect(result.current.defaultExplain).toBe(true);
    await waitFor(() => expect(result.current.explanations).toHaveLength(1));
    expect(result.current.byMessage.get("m1")?.id).toBe(1);
  });

  it("fetches again when wsOpens changes", async () => {
    const { rerender } = renderHook(({ opens }) => useExplain("c", undefined, opens), { initialProps: { opens: 1 } });
    await waitFor(() => expect(fetchExplainState).toHaveBeenCalledTimes(1));
    rerender({ opens: 2 });
    await waitFor(() => expect(fetchExplainState).toHaveBeenCalledTimes(2));
    expect(fetchExplanations).toHaveBeenCalledTimes(2);
  });

  it("follows the channel's channel.explain and explain.updated events, ignoring other channels and types", async () => {
    const bus = eventBus();
    const { result, unmount } = renderHook(() => useExplain("c", bus.subscribe));
    await waitFor(() => expect(result.current.explanations).toHaveLength(1));
    act(() => bus.emit({ type: "channel.explain", channel_id: "c", data: { explain: "on" } }));
    expect(result.current.explain).toBe("on");
    act(() => bus.emit({ type: "channel.explain", channel_id: "other", data: { explain: "off" } }));
    expect(result.current.explain).toBe("on");
    act(() => bus.emit({ type: "explain.updated", channel_id: "c", data: explanation({ id: 2, message_id: "m2" }) }));
    expect(result.current.explanations.map((e) => e.id)).toEqual([2, 1]);
    const before = result.current.explanations;
    act(() => bus.emit({ type: "agent.status", channel_id: "c", data: {} }));
    expect(result.current.explanations).toBe(before);
    unmount();
    expect(bus.size()).toBe(0);
  });

  it("setExplain sets the switch, and puts it back when the request fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(setExplain).mockResolvedValueOnce();
    const { result } = renderHook(() => useExplain("c"));
    await waitFor(() => expect(result.current.loaded).toBe(true));
    await act(() => result.current.setExplain("on"));
    expect(setExplain).toHaveBeenCalledWith("c", "on");
    expect(result.current.explain).toBe("on");
    vi.mocked(setExplain).mockRejectedValueOnce(new Error("nope"));
    await act(() => result.current.setExplain("off"));
    expect(result.current.explain).toBe("on");
    expect(warn).toHaveBeenCalledWith("[loop] setting explain:", expect.any(Error));
  });

  it("show focuses a turn with a new seq each time and calls onShow", () => {
    const onShow = vi.fn();
    const { result } = renderHook(() => useExplain("c", undefined, 0, onShow));
    act(() => result.current.show("m1"));
    expect(result.current.focus).toEqual({ messageId: "m1", seq: 1 });
    act(() => result.current.show("m1"));
    expect(result.current.focus).toEqual({ messageId: "m1", seq: 2 });
    expect(onShow).toHaveBeenCalledTimes(2);
  });

  it("show works without onShow", () => {
    const { result } = renderHook(() => useExplain("c"));
    act(() => result.current.show("m9"));
    expect(result.current.focus).toEqual({ messageId: "m9", seq: 1 });
  });

  it("explainTurn marks the turn busy, merges the result and shows it", async () => {
    let resolve: (e: Explanation) => void = () => {};
    vi.mocked(explainTurn).mockReturnValueOnce(new Promise((r) => (resolve = r)));
    const { result } = renderHook(() => useExplain("c"));
    await waitFor(() => expect(result.current.explanations).toHaveLength(1));
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.explainTurn("m3", true);
    });
    expect(explainTurn).toHaveBeenCalledWith("c", "m3", true);
    expect(result.current.busy.has("m3")).toBe(true);
    expect(result.current.focus?.messageId).toBe("m3");
    await act(async () => {
      resolve(explanation({ id: 3, message_id: "m3" }));
      await done;
    });
    expect(result.current.busy.has("m3")).toBe(false);
    expect(result.current.byMessage.get("m3")?.id).toBe(3);
  });

  it("explainTurn keeps a failed request's error until the next try clears it", async () => {
    vi.mocked(explainTurn)
      .mockRejectedValueOnce(new Error("boom"))
      .mockRejectedValueOnce("plain")
      .mockResolvedValueOnce(explanation({ id: 4, message_id: "m4" }));
    const { result } = renderHook(() => useExplain("c"));
    await act(() => result.current.explainTurn("m4"));
    expect(explainTurn).toHaveBeenCalledWith("c", "m4", false);
    expect(result.current.errors.get("m4")).toBe("boom");
    expect(result.current.busy.size).toBe(0);
    await act(() => result.current.explainTurn("m4"));
    expect(result.current.errors.get("m4")).toBe("plain");
    await act(() => result.current.explainTurn("m4"));
    expect(result.current.errors.has("m4")).toBe(false);
  });
});
