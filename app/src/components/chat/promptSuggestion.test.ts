// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchPromptSuggestion } from "../../api/channels";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { WSEvent } from "../../types";
import { applySuggestionEvent, composerPlaceholder, PROMPT_SUGGESTION_EVENT, suggestionEvent, usePromptSuggestion } from "./promptSuggestion";

vi.mock("../../api/channels", () => ({ fetchPromptSuggestion: vi.fn() }));

function event(type: string, data: unknown, channel_id = "ch1"): WSEvent {
  return { type, channel_id, data, timestamp: 0 };
}

describe("applySuggestionEvent", () => {
  it.each([
    ["a suggestion sets it", null, event(PROMPT_SUGGESTION_EVENT, { suggestion: " run the tests " }), "run the tests"],
    ["a blank one keeps the last", "old", event(PROMPT_SUGGESTION_EVENT, { suggestion: "  " }), "old"],
    ["a run starting drops it", "old", event("agent.status", { status: "running" }), null],
    ["a run ending keeps it", "old", event("agent.status", { status: "completed" }), "old"],
    ["its own thread's run starting drops it", "old", event("agent.status", { status: "running", thread_id: "ch1" }), null],
    ["a thread's run told to the parent keeps it", "old", event("agent.status", { status: "running", thread_id: "th1" }), "old"],
    ["another channel's leaves it", "old", event(PROMPT_SUGGESTION_EVENT, { suggestion: "new" }, "ch2"), "old"],
    ["other events leave it", "old", event("message.created", {}), "old"],
  ])("%s", (_name, current, ev, want) => {
    expect(applySuggestionEvent(current, ev, "ch1")).toBe(want);
  });
});

describe("suggestionEvent", () => {
  it.each([
    ["a suggestion", event(PROMPT_SUGGESTION_EVENT, { suggestion: "x" }), true],
    ["a run starting", event("agent.status", { status: "running" }), true],
    ["a thread's run told to the parent", event("agent.status", { status: "running", thread_id: "th1" }), false],
    ["a run ending", event("agent.status", { status: "completed" }), false],
    ["another channel's suggestion", event(PROMPT_SUGGESTION_EVENT, { suggestion: "x" }, "ch2"), false],
    ["another event", event("message.created", {}), false],
  ])("%s", (_name, ev, want) => {
    expect(suggestionEvent(ev, "ch1")).toBe(want);
  });
});

describe("composerPlaceholder", () => {
  it("shows the suggestion with Tab", () => {
    expect(composerPlaceholder("run the tests", "Ask Loop")).toBe("run the tests  ⇥ Tab");
  });
  it.each([null, undefined, ""])("falls back to the hint for %s", (s) => {
    expect(composerPlaceholder(s, "Ask Loop")).toBe("Ask Loop");
  });
});

// eventBus is a subscribeChatEvents stand-in that lets a test emit events
// and see whether the hook is still listening.
function eventBus() {
  const listeners = new Set<ChatEventListener>();
  const subscribe = vi.fn((listener: ChatEventListener) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  });
  const emit = (ev: WSEvent) => act(() => listeners.forEach((l) => l(ev)));
  return { subscribe, emit, listeners };
}

// deferred returns a promise and the functions that settle it.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("usePromptSuggestion", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches through the channels API by default", async () => {
    vi.mocked(fetchPromptSuggestion).mockResolvedValue("from api");
    const bus = eventBus();
    const { result } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe));
    await waitFor(() => expect(result.current).toBe("from api"));
    expect(fetchPromptSuggestion).toHaveBeenCalledWith("ch1");
  });

  it("has none without a channel or without chat events", () => {
    const fetch = vi.fn(() => Promise.resolve("x"));
    const bus = eventBus();
    const { result: noChannel } = renderHook(() => usePromptSuggestion(null, bus.subscribe, 0, fetch));
    const { result: noEvents } = renderHook(() => usePromptSuggestion("ch1", undefined, 0, fetch));
    expect(noChannel.current).toBeNull();
    expect(noEvents.current).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
    expect(bus.subscribe).not.toHaveBeenCalled();
  });

  it("shows the fetched suggestion, then follows events", async () => {
    const fetch = vi.fn(() => Promise.resolve("kept one"));
    const bus = eventBus();
    const { result } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe, 0, fetch));
    expect(fetch).toHaveBeenCalledWith("ch1");
    await waitFor(() => expect(result.current).toBe("kept one"));
    bus.emit(event(PROMPT_SUGGESTION_EVENT, { suggestion: "newer" }));
    expect(result.current).toBe("newer");
    bus.emit(event("agent.status", { status: "running" }));
    expect(result.current).toBeNull();
  });

  it("keeps an event heard while the fetch is out over the fetched one", async () => {
    const d = deferred<string>();
    const fetch = () => d.promise;
    const bus = eventBus();
    const { result } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe, 0, fetch));
    bus.emit(event(PROMPT_SUGGESTION_EVENT, { suggestion: "from event" }));
    await act(async () => d.resolve("stale"));
    expect(result.current).toBe("from event");
  });

  it("ignores other channels' events while the fetch is out", async () => {
    const d = deferred<string>();
    const fetch = () => d.promise;
    const bus = eventBus();
    const { result } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe, 0, fetch));
    bus.emit(event(PROMPT_SUGGESTION_EVENT, { suggestion: "elsewhere" }, "ch2"));
    await act(async () => d.resolve("fetched"));
    expect(result.current).toBe("fetched");
  });

  it("leaves it empty when the daemon has none", async () => {
    const d = deferred<string>();
    const fetch = () => d.promise;
    const bus = eventBus();
    const { result } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe, 0, fetch));
    await act(async () => d.resolve(""));
    expect(result.current).toBeNull();
  });

  it("drops a fetch that lands after the chat closed and unsubscribes", async () => {
    const d = deferred<string>();
    const fetch = () => d.promise;
    const bus = eventBus();
    const { result, unmount } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe, 0, fetch));
    expect(bus.listeners.size).toBe(1);
    unmount();
    expect(bus.listeners.size).toBe(0);
    await act(async () => d.resolve("late"));
    expect(result.current).toBeNull();
  });

  it("refetches on a WS reconnect and clears on a channel switch", async () => {
    const fetch = vi.fn((id: string) => Promise.resolve(`for ${id}`));
    const bus = eventBus();
    const { result, rerender } = renderHook(({ ch, opens }) => usePromptSuggestion(ch, bus.subscribe, opens, fetch), { initialProps: { ch: "ch1", opens: 0 } });
    await waitFor(() => expect(result.current).toBe("for ch1"));
    rerender({ ch: "ch1", opens: 1 });
    expect(fetch).toHaveBeenCalledTimes(2);
    rerender({ ch: "ch2", opens: 1 });
    expect(result.current).toBeNull();
    await waitFor(() => expect(result.current).toBe("for ch2"));
    expect(fetch).toHaveBeenLastCalledWith("ch2");
  });

  it("logs a failed fetch and shows nothing", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const err = new Error("down");
    const fetch = () => Promise.reject(err);
    const bus = eventBus();
    const { result } = renderHook(() => usePromptSuggestion("ch1", bus.subscribe, 0, fetch));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching the prompt suggestion:", err));
    expect(result.current).toBeNull();
  });
});
