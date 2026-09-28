import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LearnProposal } from "../api/learn";
import { BulkRuns, fetchWithRetry, reconnected, settleProposal } from "./useLearn";

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
});

describe("reconnected", () => {
  it.each([
    [0, 0, false],
    // The first open: the fetches on mount cover it.
    [0, 1, false],
    [1, 1, false],
    [1, 2, true],
    [3, 4, true],
    // Mounted after the first open, the next one is a reconnect.
    [2, 3, true],
  ])("%i → %i: %j", (prev, next, want) => {
    expect(reconnected(prev, next)).toBe(want);
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
});

describe("BulkRuns", () => {
  it("settles each proposal in scope one by one, skipping those busy or settled meanwhile", async () => {
    let list = [proposal({ id: 4 }), proposal({ id: 3 }), proposal({ id: 2, status: "failed" }), proposal({ id: 1 })];
    const busy = new Set([3]);
    const settled: number[] = [];
    const settle = async (id: number) => {
      settled.push(id);
      // Proposal 1 is dismissed elsewhere while 4 is applying.
      list = list.map((p) => (p.id === id ? { ...p, status: "applied" as const } : p.id === 1 ? { ...p, status: "dismissed" as const } : p));
    };
    const done = await new BulkRuns().run(
      "apply",
      () => list,
      () => busy,
      settle,
    );
    expect(done).toBe(true);
    // 3 is in flight, 2 failed (Apply all leaves it to Retry), 1 was dismissed meanwhile.
    expect(settled).toEqual([4]);
  });

  it("dismiss takes every open proposal, failed ones too", async () => {
    const list = [proposal({ id: 2, status: "failed" }), proposal({ id: 1 }), proposal({ id: 0, status: "applied" })];
    const settled: number[] = [];
    await new BulkRuns().run(
      "dismiss",
      () => list,
      () => new Set(),
      async (id) => {
        settled.push(id);
      },
    );
    expect(settled).toEqual([2, 1]);
  });

  it("stops an older run once a newer one starts", async () => {
    const runs = new BulkRuns();
    const list = [proposal({ id: 3 }), proposal({ id: 2 }), proposal({ id: 1 })];
    const first: number[] = [];
    let release: () => void = () => {};
    const older = runs.run(
      "apply",
      () => list,
      () => new Set(),
      (id) => {
        first.push(id);
        return new Promise<void>((r) => (release = r));
      },
    );
    const newer = runs.run(
      "dismiss",
      () => list,
      () => new Set(),
      async () => {},
    );
    release();
    expect(await older).toBe(false);
    expect(await newer).toBe(true);
    expect(first).toEqual([3]);
  });

  it("stops once stopped (the hook unmounting)", async () => {
    const runs = new BulkRuns();
    const list = [proposal({ id: 2 }), proposal({ id: 1 })];
    const settled: number[] = [];
    const done = runs.run(
      "apply",
      () => list,
      () => new Set(),
      async (id) => {
        settled.push(id);
        runs.stop();
      },
    );
    expect(await done).toBe(false);
    expect(settled).toEqual([2]);
  });
});
