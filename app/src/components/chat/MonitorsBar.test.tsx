import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cancelMonitor, fetchMonitors, type Monitor } from "../../api/monitors";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { MonitorsBar } from "./MonitorsBar";

vi.mock("../../api/monitors", () => ({ fetchMonitors: vi.fn(), cancelMonitor: vi.fn() }));

const fetchMock = vi.mocked(fetchMonitors);
const cancelMock = vi.mocked(cancelMonitor);

function events() {
  const listeners = new Set<ChatEventListener>();
  const subscribe = (l: ChatEventListener) => {
    listeners.add(l);
    return () => listeners.delete(l);
  };
  const emit = (type: string, data: unknown, channel_id = "ch-1") => act(() => listeners.forEach((l) => l({ type, channel_id, data, timestamp: 0 })));
  return { subscribe, emit, listeners };
}

const mon = (over: Partial<Monitor> = {}): Monitor => ({
  id: "m1",
  channel_id: "ch-1",
  description: "wait for CI",
  command: "gh run watch",
  started_at: new Date().toISOString(),
  timeout_sec: 0,
  interrupt: false,
  ...over,
});

describe("MonitorsBar", () => {
  beforeEach(() => {
    fetchMock.mockReset();
    cancelMock.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders nothing while the channel has no monitors", async () => {
    fetchMock.mockResolvedValue([]);
    const { container } = render(<MonitorsBar channelId="ch-1" subscribeChatEvents={events().subscribe} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("ch-1"));
    expect(container.innerHTML).toBe("");
  });

  it("lists the fetched monitors, then follows started and finished events", async () => {
    fetchMock.mockResolvedValue([mon()]);
    const ev = events();
    render(<MonitorsBar channelId="ch-1" subscribeChatEvents={ev.subscribe} />);
    expect((await screen.findByTestId("monitor-description")).textContent).toBe("wait for CI");
    ev.emit("monitor.started", mon({ id: "m2", description: "tail logs" }));
    expect(screen.getAllByTestId("monitor-row")).toHaveLength(2);
    ev.emit("monitor.started", mon({ id: "m3" }), "other");
    expect(screen.getAllByTestId("monitor-row")).toHaveLength(2);
    ev.emit("monitor.finished", { id: "m1" });
    expect(screen.getAllByTestId("monitor-description").map((e) => e.textContent)).toEqual(["tail logs"]);
    ev.emit("monitor.finished", { id: "m2" });
    expect(screen.queryByTestId("monitors-bar")).toBeNull();
  });

  it("shows the watch script on hover, under a generic heading when undescribed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fetchMock.mockResolvedValue([mon({ description: "" })]);
    render(<MonitorsBar channelId="ch-1" subscribeChatEvents={events().subscribe} />);
    const desc = await screen.findByTestId("monitor-description");
    fireEvent.mouseEnter(desc.parentElement!);
    act(() => vi.advanceTimersByTime(250));
    expect(screen.getByRole("tooltip").textContent).toBe("Monitorgh run watch");
  });

  it("keeps the elapsed time current", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    fetchMock.mockResolvedValue([mon({ started_at: "2026-01-01T00:00:00Z" })]);
    render(<MonitorsBar channelId="ch-1" subscribeChatEvents={events().subscribe} />);
    await act(async () => {});
    const row = screen.getByTestId("monitor-row");
    expect(row.textContent).toContain("0s");
    act(() => vi.advanceTimersByTime(3000));
    expect(row.textContent).toContain("3s");
  });

  it("cancels a monitor, disabled until the cancel settles", async () => {
    let finish!: () => void;
    cancelMock.mockReturnValue(new Promise<void>((r) => (finish = r)));
    fetchMock.mockResolvedValue([mon()]);
    render(<MonitorsBar channelId="ch-1" subscribeChatEvents={events().subscribe} />);
    const btn = (await screen.findByTestId("monitor-cancel")) as HTMLButtonElement;
    await userEvent.click(btn);
    expect(cancelMock).toHaveBeenCalledWith("ch-1", "m1");
    expect(btn.disabled).toBe(true);
    await act(async () => finish());
    expect(btn.disabled).toBe(false);
  });

  it("logs a cancel that fails and lets it be tried again", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    cancelMock.mockRejectedValue(new Error("nope"));
    fetchMock.mockResolvedValue([mon()]);
    render(<MonitorsBar channelId="ch-1" subscribeChatEvents={events().subscribe} />);
    const btn = (await screen.findByTestId("monitor-cancel")) as HTMLButtonElement;
    await userEvent.click(btn);
    await waitFor(() => expect(btn.disabled).toBe(false));
    expect(warn).toHaveBeenCalledWith("[loop] cancelling monitor:", expect.any(Error));
  });

  it("logs a failed fetch", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    render(<MonitorsBar channelId="ch-1" subscribeChatEvents={events().subscribe} />);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching monitors:", expect.any(Error)));
  });

  it("drops a fetch that lands after the channel changed, and fetches again on reconnect", async () => {
    let first!: (m: Monitor[]) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (first = r))).mockResolvedValue([mon({ id: "b", description: "second" })]);
    const ev = events();
    const { rerender } = render(<MonitorsBar channelId="ch-1" subscribeChatEvents={ev.subscribe} />);
    rerender(<MonitorsBar channelId="ch-2" subscribeChatEvents={ev.subscribe} />);
    expect((await screen.findByTestId("monitor-description")).textContent).toBe("second");
    await act(async () => first([mon({ description: "stale" })]));
    expect(screen.getAllByTestId("monitor-description").map((e) => e.textContent)).toEqual(["second"]);
    rerender(<MonitorsBar channelId="ch-2" subscribeChatEvents={ev.subscribe} wsOpens={1} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(fetchMock).toHaveBeenLastCalledWith("ch-2");
  });

  it("unsubscribes from chat events on unmount", async () => {
    fetchMock.mockResolvedValue([]);
    const ev = events();
    const { unmount } = render(<MonitorsBar channelId="ch-1" subscribeChatEvents={ev.subscribe} />);
    expect(ev.listeners.size).toBe(1);
    unmount();
    expect(ev.listeners.size).toBe(0);
  });
});
