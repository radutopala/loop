// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type ContainerStatsEntry, fetchContainerStats } from "../api/channels";
import { type ContainerStatsByType, fmtBytes, statsForPanel, useContainerStats } from "./useContainerStats";

vi.mock("../api/channels", () => ({ fetchContainerStats: vi.fn() }));

const fetchMock = vi.mocked(fetchContainerStats);

function entry(id: string): ContainerStatsEntry {
  return { container_id: id, type: id, cpu_percent: 1, mem_usage: 1, mem_limit: 2 };
}

const stats: ContainerStatsByType = {
  agent: entry("agent"),
  shell: entry("shell"),
  chrome: entry("chrome"),
};

describe("statsForPanel", () => {
  it("maps each panel to the container it actually runs in", () => {
    expect(statsForPanel("chat", stats)).toBe(stats.agent);
    expect(statsForPanel("docker-agent", stats)).toBe(stats.shell);
    expect(statsForPanel("docker-browser", stats)).toBe(stats.chrome);
  });

  it("shows nothing for panels with no container of their own", () => {
    // Host mode drives the user's own Chrome, so there is no sidecar to report.
    expect(statsForPanel("host-browser", stats)).toBeUndefined();
    expect(statsForPanel("editor", stats)).toBeUndefined();
  });

  it("tolerates stats that have not arrived yet", () => {
    expect(statsForPanel("docker-browser", undefined)).toBeUndefined();
    expect(statsForPanel("docker-browser", {})).toBeUndefined();
  });
});

describe("fmtBytes", () => {
  it("uses megabytes below a gigabyte and gigabytes above", () => {
    expect(fmtBytes(242638848)).toBe("231M");
    expect(fmtBytes(2 * 1024 * 1024 * 1024)).toBe("2.0G");
  });
});

describe("useContainerStats", () => {
  let hidden: boolean;

  beforeEach(() => {
    vi.useFakeTimers();
    fetchMock.mockReset();
    hidden = false;
    vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  /** Lets the pending fetch settle. */
  async function flush() {
    await act(async () => {
      await Promise.resolve();
    });
  }

  it("keeps the first container of each type", async () => {
    const a1 = { ...entry("a1"), type: "agent" };
    const a2 = { ...entry("a2"), type: "agent" };
    const s1 = { ...entry("s1"), type: "shell" };
    const s2 = { ...entry("s2"), type: "shell" };
    const c1 = { ...entry("c1"), type: "chrome" };
    const c2 = { ...entry("c2"), type: "chrome" };
    const other = { ...entry("o"), type: "other" };
    fetchMock.mockResolvedValue([a1, s1, other, a2, c1, s2, c2]);
    const { result } = renderHook(() => useContainerStats("ch-1"));
    await flush();
    expect(fetchMock).toHaveBeenCalledWith("ch-1");
    expect(result.current).toEqual({ agent: a1, shell: s1, chrome: c1 });
  });

  it("polls again after the interval", async () => {
    fetchMock.mockResolvedValue([]);
    renderHook(() => useContainerStats("ch-1"));
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("skips the fetch while the window is hidden but keeps polling", async () => {
    hidden = true;
    fetchMock.mockResolvedValue([{ ...entry("a"), type: "agent" }]);
    const { result } = renderHook(() => useContainerStats("ch-1"));
    await flush();
    expect(fetchMock).not.toHaveBeenCalled();
    hidden = false;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(result.current.agent?.container_id).toBe("a");
  });

  it("clears the stats when the fetch fails", async () => {
    fetchMock.mockResolvedValueOnce([{ ...entry("a"), type: "agent" }]).mockRejectedValueOnce(new Error("down"));
    const { result } = renderHook(() => useContainerStats("ch-1"));
    await flush();
    expect(result.current.agent).toBeDefined();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(result.current).toEqual({});
  });

  it("polls nothing without a channel", async () => {
    const { result } = renderHook(() => useContainerStats(null));
    await flush();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current).toEqual({});
  });

  it("drops a fetch that settles after unmount and stops polling", async () => {
    let resolve!: (v: ContainerStatsEntry[]) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    const { unmount } = renderHook(() => useContainerStats("ch-1"));
    unmount();
    resolve([{ ...entry("a"), type: "agent" }]);
    await flush();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("drops a failure that settles after unmount", async () => {
    let reject!: (e: Error) => void;
    fetchMock.mockReturnValueOnce(new Promise((_r, j) => (reject = j)));
    const { unmount } = renderHook(() => useContainerStats("ch-1"));
    unmount();
    reject(new Error("down"));
    await flush();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
