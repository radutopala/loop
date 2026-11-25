// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  applyLearnProposal,
  dismissLearnProposal,
  fetchLearnPasses,
  fetchLearnProposals,
  fetchLearnState,
  type LearnPass,
  type LearnProposal,
  type LearnState,
  learnTurn,
  setLearn,
} from "../api/learn";
import type { WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { fetchWithRetry, runBulk, settleProposal, useLearn } from "./useLearn";

vi.mock("../api/learn", () => ({
  applyLearnProposal: vi.fn(),
  dismissLearnProposal: vi.fn(),
  fetchLearnPasses: vi.fn(),
  fetchLearnProposals: vi.fn(),
  fetchLearnState: vi.fn(),
  learnTurn: vi.fn(),
  setLearn: vi.fn(),
}));

function proposal(over: Partial<LearnProposal>): LearnProposal {
  return {
    id: 1,
    channel_id: "c",
    learn_channel_id: "l",
    kind: "rename",
    title: "t",
    rationale: "",
    payload: "{}",
    status: "pending",
    created_at: "",
    updated_at: new Date().toISOString(),
    ...over,
  };
}

describe("fetchWithRetry", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("tries again after each failure, waiting twice as long up to the cap, then hands the result on", async () => {
    const fetch = vi.fn<() => Promise<string>>().mockRejectedValue(new Error("down"));
    const onLoad = vi.fn();
    fetchWithRetry(fetch, "fetching", onLoad);
    await vi.advanceTimersByTimeAsync(0);
    expect(fetch).toHaveBeenCalledTimes(1);
    // 2s, 4s, 8s, 16s, then 30s (capped), 30s…
    for (const [wait, calls] of [
      [2_000, 2],
      [4_000, 3],
      [8_000, 4],
      [16_000, 5],
      [30_000, 6],
    ] as const) {
      await vi.advanceTimersByTimeAsync(wait - 1);
      expect(fetch).toHaveBeenCalledTimes(calls - 1);
      await vi.advanceTimersByTimeAsync(1);
      expect(fetch).toHaveBeenCalledTimes(calls);
    }
    fetch.mockResolvedValue("ok");
    await vi.advanceTimersByTimeAsync(30_000);
    expect(onLoad).toHaveBeenCalledExactlyOnceWith("ok");
    await vi.advanceTimersByTimeAsync(120_000);
    expect(fetch).toHaveBeenCalledTimes(7);
  });

  it("neither retries nor hands on once cancelled", async () => {
    const fetch = vi.fn<() => Promise<string>>().mockRejectedValue(new Error("down"));
    const onLoad = vi.fn();
    const cancel = fetchWithRetry(fetch, "fetching", onLoad);
    await vi.advanceTimersByTimeAsync(0);
    cancel();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(fetch).toHaveBeenCalledTimes(1);

    let resolve: (v: string) => void = () => {};
    const pending = vi.fn(() => new Promise<string>((r) => (resolve = r)));
    fetchWithRetry(pending, "fetching", onLoad)();
    resolve("late");
    await vi.advanceTimersByTimeAsync(0);
    expect(onLoad).not.toHaveBeenCalled();
  });

  it("doesn't schedule a retry for a failure that lands after cancel", async () => {
    let reject: (e: Error) => void = () => {};
    const fetch = vi.fn(() => new Promise<string>((_, r) => (reject = r)));
    fetchWithRetry(fetch, "fetching", vi.fn())();
    reject(new Error("late"));
    await vi.advanceTimersByTimeAsync(120_000);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

describe("settleProposal", () => {
  function harness() {
    let busy: ReadonlySet<number> = new Set([9]);
    const busySeen: number[][] = [];
    const errors = new Map<number, string | undefined>([[1, "old"]]);
    const settled: LearnProposal[] = [];
    const ops = {
      busy: {
        get: () => busy,
        set: (next: ReadonlySet<number>) => {
          busy = next;
          busySeen.push([...next].sort());
        },
      },
      setError: (id: number, error: string | undefined) => errors.set(id, error),
      settled: (p: LearnProposal) => settled.push(p),
    };
    return { ops, busySeen, errors, settled, busy: () => busy };
  }

  it("marks the proposal busy while in flight, clears its old error and hands on the result", async () => {
    const h = harness();
    const done = proposal({ status: "applied" });
    let resolve: (p: LearnProposal) => void = () => {};
    const pending = settleProposal(1, () => new Promise((r) => (resolve = r)), h.ops);
    expect(h.busy()).toEqual(new Set([1, 9]));
    expect(h.errors.get(1)).toBeUndefined();
    resolve(done);
    await pending;
    expect(h.settled).toEqual([done]);
    expect(h.busySeen).toEqual([[1, 9], [9]]);
    expect(h.errors.get(1)).toBeUndefined();
  });

  it("keeps a failed request's error and frees the proposal", async () => {
    const h = harness();
    await settleProposal(1, () => Promise.reject(new Error("unreachable")), h.ops);
    expect(h.errors.get(1)).toBe("unreachable");
    expect(h.settled).toEqual([]);
    expect(h.busy()).toEqual(new Set([9]));
  });

  it("keeps a non-Error rejection as its string", async () => {
    const h = harness();
    await settleProposal(1, () => Promise.reject("refused"), h.ops);
    expect(h.errors.get(1)).toBe("refused");
  });
});

describe("runBulk", () => {
  it("settles each proposal in scope one by one, skipping those busy or settled meanwhile", async () => {
    let list = [proposal({ id: 4 }), proposal({ id: 3 }), proposal({ id: 2, status: "failed" }), proposal({ id: 1 })];
    const busy = new Set([3]);
    const settled: number[] = [];
    const settle = async (id: number) => {
      settled.push(id);
      // Proposal 1 is dismissed elsewhere while 4 is applying.
      list = list.map((p) => (p.id === id ? { ...p, status: "applied" as const } : p.id === 1 ? { ...p, status: "dismissed" as const } : p));
    };
    await runBulk(
      "apply",
      () => list,
      () => busy,
      settle,
      () => false,
    );
    // 3 is in flight, 2 failed (Apply all leaves it to Retry), 1 was dismissed meanwhile.
    expect(settled).toEqual([4]);
  });

  it("dismiss takes every open proposal, failed ones too", async () => {
    const list = [proposal({ id: 2, status: "failed" }), proposal({ id: 1 }), proposal({ id: 0, status: "applied" })];
    const settled: number[] = [];
    await runBulk(
      "dismiss",
      () => list,
      () => new Set(),
      async (id) => {
        settled.push(id);
      },
      () => false,
    );
    expect(settled).toEqual([2, 1]);
  });

  it("stops once stopped (the hook unmounting)", async () => {
    const list = [proposal({ id: 2 }), proposal({ id: 1 })];
    const settled: number[] = [];
    let stopped = false;
    await runBulk(
      "apply",
      () => list,
      () => new Set(),
      async (id) => {
        settled.push(id);
        stopped = true;
      },
      () => stopped,
    );
    expect(settled).toEqual([2]);
  });
});

function pass(over: Partial<LearnPass>): LearnPass {
  return { id: 1, channel_id: "c", message_id: "m1", learn_channel_id: "l", status: "done", created_at: "", updated_at: "", ...over };
}

const STATE: LearnState = { available: true, learn: "", default_learn: true, learn_channel_id: "l", running: false };

// A subscribe function that keeps its listeners, for the test to emit events.
function eventBus() {
  const listeners = new Map<string, Set<ChatEventListener>>();
  const add = (key: string, l: ChatEventListener) => {
    const set = listeners.get(key) ?? new Set();
    set.add(l);
    listeners.set(key, set);
    return () => {
      set.delete(l);
    };
  };
  return {
    chat: (l: ChatEventListener) => add("*", l),
    channel: (channelId: string, l: ChatEventListener) => add(channelId, l),
    emit: (key: string, event: Partial<WSEvent>) => {
      for (const l of listeners.get(key) ?? []) l(event as WSEvent);
    },
    count: (key: string) => listeners.get(key)?.size ?? 0,
  };
}

// A promise with its resolve handed out, for requests left in flight.
function deferred<T>() {
  let resolve: (v: T) => void = () => {};
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe("useLearn", () => {
  beforeEach(() => {
    vi.mocked(fetchLearnState).mockResolvedValue(STATE);
    vi.mocked(fetchLearnProposals).mockResolvedValue([proposal({ id: 1 })]);
    vi.mocked(fetchLearnPasses).mockResolvedValue([pass({ id: 1 })]);
  });
  afterEach(() => {
    vi.resetAllMocks();
    vi.restoreAllMocks();
  });

  it("loads the channel's learn state, proposals and passes", async () => {
    const { result } = renderHook(() => useLearn("c"));
    expect(result.current.loaded).toBe(false);
    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(fetchLearnState).toHaveBeenCalledWith("c");
    expect(fetchLearnProposals).toHaveBeenCalledWith("c");
    expect(fetchLearnPasses).toHaveBeenCalledWith("c");
    expect(result.current).toMatchObject({ available: true, learn: "", defaultLearn: true, learnChannelId: "l", running: false });
    await waitFor(() => expect(result.current.passes).toHaveLength(1));
    await waitFor(() => expect(result.current.proposals).toHaveLength(1));
    expect(result.current.open.map((p) => p.id)).toEqual([1]);
    expect(result.current.passByMessage.get("m1")?.id).toBe(1);
  });

  it("on mount keeps a learn.started that arrived before the state; a shortcut applied earlier bumps nothing", async () => {
    const state = deferred<LearnState>();
    vi.mocked(fetchLearnState).mockReturnValueOnce(state.promise);
    vi.mocked(fetchLearnProposals).mockResolvedValueOnce([proposal({ id: 5, kind: "prompt_shortcut", status: "applied" })]);
    const bus = eventBus();
    const { result } = renderHook(() => useLearn("c", bus.chat));
    act(() => bus.emit("*", { type: "learn.started", channel_id: "c", data: { learn_channel_id: "new" } }));
    await act(async () => state.resolve({ ...STATE, learn_channel_id: "old", running: false }));
    expect(result.current.learnChannelId).toBe("new");
    expect(result.current.running).toBe(true);
    await waitFor(() => expect(result.current.proposals).toHaveLength(1));
    expect(result.current.shortcutsVersion).toBe(0);
  });

  it("on a reconnect the fetched state wins, keeping the learn thread when it has none, and bumps a newly applied shortcut", async () => {
    vi.mocked(fetchLearnProposals).mockResolvedValueOnce([]);
    const { result, rerender } = renderHook(({ opens }) => useLearn("c", undefined, undefined, opens), { initialProps: { opens: 0 } });
    await waitFor(() => expect(result.current.loaded).toBe(true));
    rerender({ opens: 1 });
    await waitFor(() => expect(fetchLearnState).toHaveBeenCalledTimes(2));
    vi.mocked(fetchLearnState).mockResolvedValueOnce({ ...STATE, learn_channel_id: "", running: true });
    vi.mocked(fetchLearnProposals).mockResolvedValueOnce([proposal({ id: 6, kind: "bash_shortcut", status: "applied" })]);
    rerender({ opens: 2 });
    await waitFor(() => expect(result.current.running).toBe(true));
    expect(result.current.learnChannelId).toBe("l");
    await waitFor(() => expect(result.current.shortcutsVersion).toBe(1));
    vi.mocked(fetchLearnState).mockResolvedValueOnce({ ...STATE, learn_channel_id: "l2", running: false });
    rerender({ opens: 3 });
    await waitFor(() => expect(result.current.learnChannelId).toBe("l2"));
    expect(result.current.running).toBe(false);
  });

  it("follows the channel's learn events, ignoring other channels and types", async () => {
    const bus = eventBus();
    const { result, unmount } = renderHook(() => useLearn("c", bus.chat));
    await waitFor(() => expect(result.current.proposals).toHaveLength(1));
    act(() => bus.emit("*", { type: "channel.learn", channel_id: "c", data: { learn: "off" } }));
    expect(result.current.learn).toBe("off");
    act(() => bus.emit("*", { type: "channel.learn", channel_id: "x", data: { learn: "on" } }));
    expect(result.current.learn).toBe("off");
    act(() => bus.emit("*", { type: "learn.proposals", channel_id: "c", data: { proposals: [proposal({ id: 3 })], withdrawn: [proposal({ id: 1, status: "withdrawn" })] } }));
    expect(result.current.proposals.map((p) => [p.id, p.status])).toEqual([
      [3, "pending"],
      [1, "withdrawn"],
    ]);
    act(() => bus.emit("*", { type: "learn.proposal_updated", channel_id: "c", data: proposal({ id: 3, status: "dismissed" }) }));
    expect(result.current.open).toEqual([]);
    act(() => bus.emit("*", { type: "learn.pass", channel_id: "c", data: pass({ id: 2, message_id: "m2", status: "running" }) }));
    expect(result.current.passes.map((p) => p.id)).toEqual([2, 1]);
    const before = result.current;
    act(() => bus.emit("*", { type: "agent.status", channel_id: "c", data: {} }));
    expect(result.current).toBe(before);
    unmount();
    expect(bus.count("*")).toBe(0);
  });

  it("follows the learn thread's run status: a learn pass runs, a reply doesn't, any end stops", async () => {
    const bus = eventBus();
    const { result } = renderHook(() => useLearn("c", bus.chat, bus.channel));
    await waitFor(() => expect(bus.count("l")).toBe(1));
    act(() => bus.emit("l", { type: "agent.status", data: { status: "running", trigger: "learn-reply" } }));
    expect(result.current.running).toBe(false);
    act(() => bus.emit("l", { type: "agent.status", data: { status: "running", trigger: "learn" } }));
    expect(result.current.running).toBe(true);
    act(() => bus.emit("l", { type: "message", data: { status: "completed" } }));
    expect(result.current.running).toBe(true);
    act(() => bus.emit("l", { type: "agent.status", data: { status: "completed" } }));
    expect(result.current.running).toBe(false);
    act(() => bus.emit("*", { type: "learn.started", channel_id: "c", data: { learn_channel_id: "l3" } }));
    expect(bus.count("l")).toBe(0);
    expect(bus.count("l3")).toBe(1);
  });

  it("setLearn sets the switch, and puts it back when the request fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(setLearn).mockResolvedValueOnce();
    const { result } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.loaded).toBe(true));
    await act(() => result.current.setLearn("on"));
    expect(setLearn).toHaveBeenCalledWith("c", "on");
    expect(result.current.learn).toBe("on");
    vi.mocked(setLearn).mockRejectedValueOnce(new Error("nope"));
    await act(() => result.current.setLearn("off"));
    expect(result.current.learn).toBe("on");
    expect(warn).toHaveBeenCalledWith("[loop] setting learn:", expect.any(Error));
  });

  it("apply marks the proposal busy, merges the result and bumps a newly applied shortcut", async () => {
    const req = deferred<LearnProposal>();
    vi.mocked(applyLearnProposal).mockReturnValueOnce(req.promise);
    const { result } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.proposals).toHaveLength(1));
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.apply(1);
    });
    expect(applyLearnProposal).toHaveBeenCalledWith(1);
    expect(result.current.busy.has(1)).toBe(true);
    await act(async () => {
      req.resolve(proposal({ id: 1, kind: "prompt_shortcut", status: "applied" }));
      await done;
    });
    expect(result.current.busy.size).toBe(0);
    expect(result.current.proposals[0]?.status).toBe("applied");
    expect(result.current.shortcutsVersion).toBe(1);
  });

  it("dismiss keeps a failed request's error until the next try clears it", async () => {
    vi.mocked(dismissLearnProposal)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(proposal({ id: 1, status: "dismissed" }));
    const { result } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.proposals).toHaveLength(1));
    await act(() => result.current.dismiss(1));
    expect(dismissLearnProposal).toHaveBeenCalledWith(1);
    expect(result.current.errors.get(1)).toBe("offline");
    await act(() => result.current.dismiss(1));
    expect(result.current.errors.has(1)).toBe(false);
    expect(result.current.proposals[0]?.status).toBe("dismissed");
  });

  it("applyAll applies the pending proposals one by one, with bulk set meanwhile", async () => {
    vi.mocked(fetchLearnProposals).mockResolvedValueOnce([proposal({ id: 2 }), proposal({ id: 1 }), proposal({ id: 0, status: "failed" })]);
    const first = deferred<LearnProposal>();
    vi.mocked(applyLearnProposal)
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(proposal({ id: 1, status: "applied" }));
    const { result } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.proposals).toHaveLength(3));
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.applyAll();
    });
    expect(result.current.bulk).toBe("apply");
    await act(async () => {
      first.resolve(proposal({ id: 2, status: "applied" }));
      await done;
    });
    expect(vi.mocked(applyLearnProposal).mock.calls).toEqual([[2], [1]]);
    expect(result.current.bulk).toBeNull();
  });

  it("dismissAll dismisses every open proposal, failed ones too", async () => {
    vi.mocked(fetchLearnProposals).mockResolvedValueOnce([proposal({ id: 2, status: "failed" }), proposal({ id: 1 })]);
    vi.mocked(dismissLearnProposal).mockImplementation(async (id) => proposal({ id, status: "dismissed" }));
    const { result } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.proposals).toHaveLength(2));
    await act(() => result.current.dismissAll());
    expect(vi.mocked(dismissLearnProposal).mock.calls).toEqual([[2], [1]]);
    expect(result.current.open).toEqual([]);
    expect(result.current.bulk).toBeNull();
  });

  it("a bulk run stops once the hook unmounts", async () => {
    vi.mocked(fetchLearnProposals).mockResolvedValueOnce([proposal({ id: 2 }), proposal({ id: 1 })]);
    const first = deferred<LearnProposal>();
    vi.mocked(applyLearnProposal).mockReturnValueOnce(first.promise);
    const { result, unmount } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.proposals).toHaveLength(2));
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.applyAll();
    });
    unmount();
    first.resolve(proposal({ id: 2, status: "applied" }));
    await done;
    expect(applyLearnProposal).toHaveBeenCalledTimes(1);
  });

  it("show focuses a turn with a new seq each time and calls onShow when given", () => {
    const onShow = vi.fn();
    const { result } = renderHook(() => useLearn("c", undefined, undefined, 0, onShow));
    act(() => result.current.show("m1"));
    act(() => result.current.show("m1"));
    expect(result.current.focus).toEqual({ messageId: "m1", seq: 2 });
    expect(onShow).toHaveBeenCalledTimes(2);
    const bare = renderHook(() => useLearn("c"));
    act(() => bare.result.current.show("m2"));
    expect(bare.result.current.focus).toEqual({ messageId: "m2", seq: 1 });
  });

  it("learnTurn marks the turn busy, shows it and merges its pass", async () => {
    const req = deferred<LearnPass>();
    vi.mocked(learnTurn).mockReturnValueOnce(req.promise);
    const { result } = renderHook(() => useLearn("c"));
    await waitFor(() => expect(result.current.passes).toHaveLength(1));
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.learnTurn("m5");
    });
    expect(learnTurn).toHaveBeenCalledWith("c", "m5");
    expect(result.current.turnBusy.has("m5")).toBe(true);
    expect(result.current.focus?.messageId).toBe("m5");
    await act(async () => {
      req.resolve(pass({ id: 5, message_id: "m5", status: "queued" }));
      await done;
    });
    expect(result.current.turnBusy.size).toBe(0);
    expect(result.current.passByMessage.get("m5")?.status).toBe("queued");
  });

  it("learnTurn keeps a failed request's error until the next try clears it", async () => {
    vi.mocked(learnTurn)
      .mockRejectedValueOnce(new Error("boom"))
      .mockRejectedValueOnce("plain")
      .mockResolvedValueOnce(pass({ id: 6, message_id: "m6" }));
    const { result } = renderHook(() => useLearn("c"));
    await act(() => result.current.learnTurn("m6"));
    expect(result.current.turnErrors.get("m6")).toBe("boom");
    await act(() => result.current.learnTurn("m6"));
    expect(result.current.turnErrors.get("m6")).toBe("plain");
    await act(() => result.current.learnTurn("m6"));
    expect(result.current.turnErrors.has("m6")).toBe(false);
    expect(result.current.turnBusy.size).toBe(0);
  });

  it("a proposal stuck applying opens again once it goes stale", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    try {
      const now = Date.now();
      vi.mocked(fetchLearnProposals).mockResolvedValueOnce([proposal({ id: 7, status: "applying", updated_at: new Date(now - 59_000).toISOString() })]);
      const { result, unmount } = renderHook(() => useLearn("c"));
      await act(() => vi.advanceTimersByTimeAsync(0));
      expect(result.current.proposals).toHaveLength(1);
      expect(result.current.open).toEqual([]);
      await act(() => vi.advanceTimersByTimeAsync(1_002));
      expect(result.current.open.map((p) => p.id)).toEqual([7]);
      unmount();
    } finally {
      vi.useRealTimers();
    }
  });
});
