import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchWithRetry } from "./useLearn";

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
