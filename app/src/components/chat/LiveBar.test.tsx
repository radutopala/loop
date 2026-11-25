import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLive, type LiveStatus, warmLive } from "../../api/live";
import { reclaimDockerSpace } from "../../api/loopApi";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { DockerReclaimResult } from "../../types";
import { LiveBar, useLiveStatus, useWarmLive } from "./LiveBar";

vi.mock("../../api/live", () => ({ fetchLive: vi.fn(), warmLive: vi.fn() }));
vi.mock("../../api/loopApi", () => ({ reclaimDockerSpace: vi.fn() }));

const fetchMock = vi.mocked(fetchLive);
const warmMock = vi.mocked(warmLive);
const reclaimMock = vi.mocked(reclaimDockerSpace);

function events() {
  const listeners = new Set<ChatEventListener>();
  const subscribe = (l: ChatEventListener) => {
    listeners.add(l);
    return () => listeners.delete(l);
  };
  const emit = (type: string, data: unknown, channel_id: string) => act(() => listeners.forEach((l) => l({ type, channel_id, data, timestamp: 0 })));
  return { subscribe, emit, listeners };
}

const live = (over: Partial<LiveStatus> = {}): LiveStatus => ({ running: false, busy: false, ...over });
const reclaimed: DockerReclaimResult = { build_cache_reclaimed: 0, images_reclaimed: 0, unused_images_reclaimed: 0, volumes_reclaimed: 0, total_reclaimed: 0, orphan_volumes_removed: 0 };

// Each test uses its own channel ids: the hook remembers the last status
// seen per channel for the rest of the run.
describe("useLiveStatus", () => {
  beforeEach(() => {
    fetchMock.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("follows nothing without a channel or a way to hear its events", () => {
    const { result: none } = renderHook(() => useLiveStatus(null, events().subscribe));
    const { result: readOnly } = renderHook(() => useLiveStatus("ro-1"));
    expect(none.current).toBeNull();
    expect(readOnly.current).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("fetches the status, keeps it up to date from events, and shows it at once next time", async () => {
    fetchMock.mockResolvedValue(live({ running: true, enabled: true }));
    const ev = events();
    const { result, unmount } = renderHook(() => useLiveStatus("a-1", ev.subscribe));
    await waitFor(() => expect(result.current).toEqual(live({ running: true, enabled: true })));
    ev.emit("live.status", live({ running: false, reason: "idle" }), "a-1");
    expect(result.current).toEqual(live({ running: false, reason: "idle", enabled: true }));
    ev.emit("live.status", live({ running: true }), "other");
    expect(result.current?.running).toBe(false);
    unmount();
    expect(ev.listeners.size).toBe(0);

    fetchMock.mockReturnValue(new Promise(() => {}));
    const again = renderHook(() => useLiveStatus("a-1", ev.subscribe));
    expect(again.result.current).toEqual(live({ running: false, reason: "idle", enabled: true }));
  });

  it("fetches again on each reconnect", async () => {
    fetchMock.mockResolvedValue(live());
    const ev = events();
    const { rerender } = renderHook(({ opens }) => useLiveStatus("b-1", ev.subscribe, opens), { initialProps: { opens: 0 } });
    rerender({ opens: 1 });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("drops a fetch that lands after the channel changed", async () => {
    let first!: (s: LiveStatus) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (first = r))).mockResolvedValue(live({ reason: "second" }));
    const ev = events();
    const { result, rerender } = renderHook(({ id }) => useLiveStatus(id, ev.subscribe), { initialProps: { id: "c-1" } });
    rerender({ id: "c-2" });
    await waitFor(() => expect(result.current?.reason).toBe("second"));
    await act(async () => first(live({ reason: "stale" })));
    expect(result.current?.reason).toBe("second");
  });

  it("logs a failed fetch", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    renderHook(() => useLiveStatus("d-1", events().subscribe));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching the live session:", expect.any(Error)));
  });

  it("leaves the status alone for events that don't change it", async () => {
    fetchMock.mockReturnValue(new Promise(() => {}));
    const ev = events();
    const { result } = renderHook(() => useLiveStatus("e-1", ev.subscribe));
    ev.emit("agent.text", {}, "e-1");
    expect(result.current).toBeNull();
  });
});

describe("useWarmLive", () => {
  beforeEach(() => {
    warmMock.mockReset().mockResolvedValue(true);
  });
  afterEach(() => vi.restoreAllMocks());

  it("starts a stopped session once until one runs, then again after it stops", () => {
    const { result, rerender } = renderHook(({ status }) => useWarmLive("w-1", status), { initialProps: { status: live() as LiveStatus | null } });
    result.current();
    result.current();
    expect(warmMock).toHaveBeenCalledOnce();
    expect(warmMock).toHaveBeenCalledWith("w-1");
    rerender({ status: live({ running: true }) });
    result.current();
    expect(warmMock).toHaveBeenCalledOnce();
    rerender({ status: live() });
    result.current();
    expect(warmMock).toHaveBeenCalledTimes(2);
  });

  it("does nothing without a channel or a status", () => {
    renderHook(() => useWarmLive(null, live())).result.current();
    renderHook(() => useWarmLive("w-2", null)).result.current();
    expect(warmMock).not.toHaveBeenCalled();
  });

  it("logs a start that fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    warmMock.mockRejectedValue(new Error("no"));
    renderHook(() => useWarmLive("w-3", live())).result.current();
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] starting the live session:", expect.any(Error)));
  });
});

describe("LiveBar", () => {
  beforeEach(() => {
    reclaimMock.mockReset();
  });

  it.each([
    ["no status", null],
    ["a running session", live({ running: true, reason: "exited" })],
    ["a routine end", live({ reason: "idle" })],
  ])("renders nothing for %s", (_name, status) => {
    const { container } = render(<LiveBar status={status} />);
    expect(container.innerHTML).toBe("");
  });

  it("says why the session ended, without disk actions for other reasons", () => {
    render(<LiveBar status={live({ reason: "exited: boom" })} onReclaimSettings={vi.fn()} />);
    expect(screen.getByTestId("live-bar-reason").textContent).toBe("Session ended: it exited: boom");
    expect(screen.queryByTestId("live-bar-reclaim")).toBeNull();
    expect(screen.queryByTestId("live-bar-reclaim-settings")).toBeNull();
  });

  const full = () => live({ reason: "exited: write /x: no space left on device" });

  it("reclaims Docker space after a full disk, then asks to send again", async () => {
    let finish!: (r: DockerReclaimResult) => void;
    reclaimMock.mockReturnValue(new Promise((r) => (finish = r)));
    render(<LiveBar status={full()} />);
    expect(screen.queryByTestId("live-bar-reclaim-settings")).toBeNull();
    const btn = screen.getByTestId("live-bar-reclaim") as HTMLButtonElement;
    await userEvent.click(btn);
    expect(reclaimMock).toHaveBeenCalledWith(false);
    expect(btn.disabled).toBe(true);
    expect(btn.textContent).toBe("Reclaiming…");
    await act(async () => finish({ ...reclaimed, total_reclaimed: 0 }));
    expect(screen.getByTestId("live-bar-reclaimed").textContent).toBe("Reclaimed 0 B. Send your message again.");
    expect(screen.queryByTestId("live-bar-reclaim")).toBeNull();
  });

  it("shows a reclaim that failed and lets it be tried again", async () => {
    reclaimMock.mockRejectedValueOnce(new Error("daemon busy")).mockRejectedValueOnce({});
    render(<LiveBar status={full()} />);
    await userEvent.click(screen.getByTestId("live-bar-reclaim"));
    expect(await screen.findByText("daemon busy")).toBeTruthy();
    await userEvent.click(screen.getByTestId("live-bar-reclaim"));
    expect(await screen.findByText("Reclaim failed")).toBeTruthy();
    expect(screen.queryByText("daemon busy")).toBeNull();
  });

  it("forgets what was reclaimed when the session ends again", async () => {
    reclaimMock.mockResolvedValue(reclaimed);
    const { rerender } = render(<LiveBar status={full()} />);
    await userEvent.click(screen.getByTestId("live-bar-reclaim"));
    await screen.findByTestId("live-bar-reclaimed");
    rerender(<LiveBar status={full()} />);
    expect(screen.queryByTestId("live-bar-reclaimed")).toBeNull();
    expect(screen.getByTestId("live-bar-reclaim")).toBeTruthy();
  });

  it("opens the reclaim settings", async () => {
    const onReclaimSettings = vi.fn();
    render(<LiveBar status={full()} onReclaimSettings={onReclaimSettings} />);
    await userEvent.click(screen.getByTestId("live-bar-reclaim-settings"));
    expect(onReclaimSettings).toHaveBeenCalledOnce();
  });
});
