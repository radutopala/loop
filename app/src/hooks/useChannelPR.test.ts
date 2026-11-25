// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchPR, type PRInfo } from "../api/git";
import { getPRStatus, type PRStatus } from "../api/pr";
import { useChannelPR, useChannelPRStatus } from "./useChannelPR";

vi.mock("../api/git", () => ({ fetchPR: vi.fn() }));
vi.mock("../api/pr", () => ({ getPRStatus: vi.fn() }));

const pr: PRInfo = { number: 7, url: "https://example.test/pr/7", base_ref: "main", head_ref: "feature", state: "OPEN" };

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useChannelPR", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.mocked(fetchPR).mockReset();
  });

  it("looks up at once without a delay, for the open chat's header", async () => {
    vi.mocked(fetchPR).mockResolvedValue({ present: false });
    const { result } = renderHook(() => useChannelPR("open", "feature"));
    await act(async () => {});
    expect(fetchPR).toHaveBeenCalledTimes(1);
    expect(fetchPR).toHaveBeenCalledWith("open");
    expect(result.current).toBeNull();
  });

  it("returns the branch's PR when one is present", async () => {
    vi.mocked(fetchPR).mockResolvedValue({ present: true, pr });
    const { result } = renderHook(() => useChannelPR("ch", "feature"));
    await waitFor(() => expect(result.current).toEqual(pr));
  });

  it("is null when present comes without a PR", async () => {
    vi.mocked(fetchPR).mockResolvedValue({ present: true });
    const { result } = renderHook(() => useChannelPR("ch", "feature"));
    await act(async () => {});
    expect(result.current).toBeNull();
  });

  it("is null when the lookup fails", async () => {
    vi.mocked(fetchPR).mockResolvedValueOnce({ present: true, pr }).mockRejectedValueOnce(new Error("gh down"));
    const { result, rerender } = renderHook(({ branch }) => useChannelPR("ch", branch), { initialProps: { branch: "a" } });
    await waitFor(() => expect(result.current).toEqual(pr));
    rerender({ branch: "b" });
    await waitFor(() => expect(result.current).toBeNull());
    expect(fetchPR).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["disabled", "ch", "feature", false],
    ["no channel", "", "feature", true],
    ["no branch", "ch", undefined, true],
  ])("skips the lookup when %s", async (_name, channelId, branch, enabled) => {
    const { result } = renderHook(() => useChannelPR(channelId, branch, enabled));
    await act(async () => {});
    expect(fetchPR).not.toHaveBeenCalled();
    expect(result.current).toBeNull();
  });

  it("waits the delay before asking, and not at all when disabled meanwhile", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchPR).mockResolvedValue({ present: true, pr });
    const { result, rerender } = renderHook(({ enabled }) => useChannelPR("ch", "feature", enabled, 1000), { initialProps: { enabled: true } });
    act(() => vi.advanceTimersByTime(999));
    expect(fetchPR).not.toHaveBeenCalled();
    rerender({ enabled: false });
    act(() => vi.advanceTimersByTime(5000));
    expect(fetchPR).not.toHaveBeenCalled();

    rerender({ enabled: true });
    await act(async () => vi.advanceTimersByTime(1000));
    expect(fetchPR).toHaveBeenCalledTimes(1);
    expect(result.current).toEqual(pr);
  });

  it("drops an answer that lands after the channel changed", async () => {
    const first = deferred<{ present: boolean; pr?: PRInfo }>();
    const second = deferred<{ present: boolean; pr?: PRInfo }>();
    vi.mocked(fetchPR).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { result, rerender } = renderHook(({ ch }) => useChannelPR(ch, "feature"), { initialProps: { ch: "a" } });
    rerender({ ch: "b" });
    await act(async () => {
      first.resolve({ present: true, pr });
      second.reject(new Error("no"));
    });
    expect(result.current).toBeNull();

    const third = deferred<{ present: boolean; pr?: PRInfo }>();
    vi.mocked(fetchPR)
      .mockReturnValueOnce(third.promise)
      .mockReturnValueOnce(new Promise(() => {}));
    rerender({ ch: "c" });
    rerender({ ch: "d" });
    await act(async () => third.reject(new Error("late")));
    expect(result.current).toBeNull();
  });
});

describe("useChannelPRStatus", () => {
  const status = { channel_id: "ch", kind: "pr", number: 7, head_sha: "abc" } as PRStatus;

  afterEach(() => {
    cleanup();
    vi.mocked(getPRStatus).mockReset();
  });

  it("returns the watcher's status while enabled", async () => {
    vi.mocked(getPRStatus).mockResolvedValue(status);
    const { result } = renderHook(() => useChannelPRStatus("ch", true));
    await waitFor(() => expect(result.current).toEqual(status));
    expect(getPRStatus).toHaveBeenCalledWith("ch");
  });

  it("is null when the lookup fails", async () => {
    vi.mocked(getPRStatus).mockRejectedValue(new Error("down"));
    const { result } = renderHook(() => useChannelPRStatus("ch", true));
    await act(async () => {});
    expect(result.current).toBeNull();
  });

  it.each([
    ["disabled", "ch", false],
    ["no channel", "", true],
  ])("clears and skips the lookup when %s", async (_name, channelId, enabled) => {
    vi.mocked(getPRStatus).mockResolvedValue(status);
    const { result, rerender } = renderHook(({ ch, on }) => useChannelPRStatus(ch, on), { initialProps: { ch: "ch", on: true } });
    await waitFor(() => expect(result.current).toEqual(status));
    rerender({ ch: channelId, on: enabled });
    expect(result.current).toBeNull();
    expect(getPRStatus).toHaveBeenCalledTimes(1);
  });

  it("drops answers that land after it was turned off", async () => {
    const ok = deferred<PRStatus>();
    const bad = deferred<PRStatus>();
    vi.mocked(getPRStatus).mockReturnValueOnce(ok.promise).mockReturnValueOnce(bad.promise);
    const { result, rerender } = renderHook(({ ch }) => useChannelPRStatus(ch, true), { initialProps: { ch: "a" } });
    rerender({ ch: "b" });
    rerender({ ch: "" });
    await act(async () => {
      ok.resolve(status);
      bad.reject(new Error("late"));
    });
    expect(result.current).toBeNull();
  });
});
