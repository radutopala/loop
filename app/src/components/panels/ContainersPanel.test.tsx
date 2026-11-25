import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ContainerInfo } from "../../api/loopApi";
import { fetchContainers } from "../../api/loopApi";
import { darkColors } from "../../theme";
import type { Channel, WSEvent } from "../../types";
import { ContainersPanel, type ContainersPanelHandle } from "./ContainersPanel";

vi.mock("../../api/loopApi", () => ({ fetchContainers: vi.fn() }));

// ChannelHeaderInfo fetches git and PR state of its own; stand in for it.
vi.mock("../layout/ChannelHeaderInfo", () => ({
  ChannelHeaderInfo: ({ channel }: { channel: Channel }) => <span data-testid="channel-info">{channel.id}</span>,
}));

const fetchMock = vi.mocked(fetchContainers);

const container = (over: Partial<ContainerInfo>): ContainerInfo => ({
  container_id: "c0000000000000000",
  channel_id: "ch-1234567890abcdef",
  type: "agent",
  status: "running",
  created_at: "2026-01-01T10:00:00Z",
  updated_at: "2026-01-01T10:00:00Z",
  ...over,
});

const time = (ts: string) => new Date(ts).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });

function cssColor(c: string) {
  const probe = document.createElement("span");
  probe.style.backgroundColor = c;
  return probe.style.backgroundColor;
}

function renderPanel(props: Partial<Parameters<typeof ContainersPanel>[0]> = {}) {
  const ref = createRef<ContainersPanelHandle>();
  const onClose = vi.fn();
  const utils = render(<ContainersPanel ref={ref} onClose={onClose} {...props} />);
  const send = (event: Partial<WSEvent>) => act(() => ref.current!.handleContainerEvent(event as WSEvent));
  return { ...utils, ref, onClose, send };
}

const cards = () => [...document.querySelectorAll("[title='Container ID']")].map((el) => el.textContent);

describe("ContainersPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchMock.mockResolvedValue([]);
  });
  afterEach(() => vi.restoreAllMocks());

  it("shows the empty state", async () => {
    renderPanel();
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(screen.getByText("No containers")).toBeTruthy();
  });

  it("logs a failed load and stays empty", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    renderPanel();
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching containers:", expect.any(Error)));
    expect(screen.getByText("No containers")).toBeTruthy();
  });

  it("lists running containers first, newest first, with their details", async () => {
    fetchMock.mockResolvedValue([
      container({ container_id: "stopped0000000aa", status: "stopped", type: "shell", created_at: "2026-01-01T12:00:00Z" }),
      container({ container_id: "oldrun00000000aa", created_at: "2026-01-01T09:00:00Z", container_name: "loop-agent-1" }),
      container({ container_id: "newrun00000000aa", type: "chrome", created_at: "2026-01-01T11:00:00Z" }),
      container({ container_id: "pending0000000aa", status: "pending-removal", type: "plugin", remove_at: "2026-01-01T13:00:00Z", created_at: "2026-01-01T07:00:00Z" }),
      container({ container_id: "custom0000000aaa", type: "gpu" as ContainerInfo["type"], status: "stopped", created_at: "2026-01-01T08:00:00Z" }),
    ]);
    renderPanel();
    await screen.findByText("loop-agent-1");
    expect(cards()).toEqual(["newrun000000", "oldrun000000", "stopped00000", "custom000000", "pending00000"]);
    for (const label of ["Agent", "Shell", "Chrome", "Plugin", "gpu"]) expect(screen.getByText(label)).toBeTruthy();
    expect(screen.getAllByText("ch: ch-123456789")).toHaveLength(5);
    expect(screen.getByText(`pending-removal (removal at ${time("2026-01-01T13:00:00Z")})`)).toBeTruthy();
    expect(screen.getByText(time("2026-01-01T09:00:00Z"))).toBeTruthy();
    expect(screen.getAllByTitle("Created")).toHaveLength(5);

    const dot = (status: string) => screen.getByText(status, { exact: false }).previousElementSibling as HTMLElement;
    expect(dot("pending-removal").style.backgroundColor).toBe(cssColor(darkColors.warning));
    expect((screen.getAllByText("stopped")[0]!.previousElementSibling as HTMLElement).style.backgroundColor).toBe(cssColor(darkColors.textDim));
    expect((screen.getAllByText("running")[0]!.previousElementSibling as HTMLElement).style.backgroundColor).toBe(cssColor(darkColors.active));
  });

  it("leaves out the creation time when there is none", async () => {
    fetchMock.mockResolvedValue([container({ created_at: "" })]);
    renderPanel();
    await waitFor(() => expect(cards()).toHaveLength(1));
    expect(screen.queryByTitle("Created")).toBeNull();
  });

  describe("container events", () => {
    it("adds a registered container, replacing one with the same id", async () => {
      fetchMock.mockResolvedValue([container({ container_id: "aaaa00000000zzzz", status: "stopped" })]);
      const { send } = renderPanel();
      await screen.findByText("stopped");
      send({ type: "container.registered", data: { container_id: "aaaa00000000zzzz", channel_id: "ch-x", type: "shell", status: "", container_name: "sh-1" } });
      expect(cards()).toEqual(["aaaa00000000"]);
      expect(screen.getByText("running")).toBeTruthy();
      expect(screen.getByText("sh-1")).toBeTruthy();
      send({ type: "container.registered", data: { container_id: "bbbb00000000zzzz", channel_id: "ch-y", type: "agent", status: "stopped" } });
      expect(cards()).toHaveLength(2);
    });

    it("removes a container", async () => {
      fetchMock.mockResolvedValue([container({ container_id: "aaaa00000000zzzz" }), container({ container_id: "bbbb00000000zzzz" })]);
      const { send } = renderPanel();
      await waitFor(() => expect(cards()).toHaveLength(2));
      send({ type: "container.removed", data: { container_id: "aaaa00000000zzzz" } });
      expect(cards()).toEqual(["bbbb00000000"]);
    });

    it("updates a container's status and removal time, keeping the status when none is sent", async () => {
      fetchMock.mockResolvedValue([container({ container_id: "aaaa00000000zzzz" }), container({ container_id: "bbbb00000000zzzz", created_at: "2026-01-01T09:00:00Z" })]);
      const { send } = renderPanel();
      await waitFor(() => expect(cards()).toHaveLength(2));
      send({ type: "container.status_changed", data: { container_id: "aaaa00000000zzzz", status: "pending-removal", remove_at: "2026-01-01T13:00:00Z" } });
      expect(screen.getByText(`pending-removal (removal at ${time("2026-01-01T13:00:00Z")})`)).toBeTruthy();
      send({ type: "container.status_changed", data: { container_id: "aaaa00000000zzzz", status: "" } });
      expect(screen.getByText("pending-removal")).toBeTruthy();
      expect(screen.getAllByText("running")).toHaveLength(1);
    });

    it("ignores events without a container id and unknown event types", async () => {
      fetchMock.mockResolvedValue([container({ container_id: "aaaa00000000zzzz" })]);
      const { send } = renderPanel();
      await waitFor(() => expect(cards()).toHaveLength(1));
      send({ type: "container.removed", data: {} });
      send({ type: "container.removed" });
      send({ type: "container.other", data: { container_id: "aaaa00000000zzzz" } } as unknown as WSEvent);
      expect(cards()).toEqual(["aaaa00000000"]);
    });
  });

  it("closes on the close button and on Escape only", async () => {
    const user = userEvent.setup();
    const { onClose, unmount } = renderPanel();
    const close = screen.getByTitle("Close panel");
    fireEvent.mouseEnter(close);
    expect(close.style.backgroundColor).toBe(cssColor(darkColors.hoverBg));
    fireEvent.mouseLeave(close);
    expect(close.style.backgroundColor).toBe("transparent");
    await user.click(close);
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: "Enter" });
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(2);
    unmount();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("shows the search button with the platform's shortcut and the channel info", async () => {
    const onOpenPalette = vi.fn();
    const user = userEvent.setup();
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    const { unmount } = renderPanel({ onOpenPalette, channel: { id: "ch-9" } as Channel, sidebarOpen: false });
    const search = screen.getByTitle("Search messages (Cmd+K)");
    expect(search.textContent).toBe("⌘K");
    expect(screen.getByTestId("channel-info").textContent).toBe("ch-9");
    expect((screen.getByTestId("containers-panel").firstElementChild as HTMLElement).style.paddingLeft).toBe("76px");
    await user.click(search);
    expect(onOpenPalette).toHaveBeenCalledOnce();
    unmount();

    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    renderPanel({ onOpenPalette });
    expect(screen.getByTitle("Search messages (Cmd+K)").textContent).toBe("Ctrl+K");
    expect(screen.queryByTestId("channel-info")).toBeNull();
    expect((screen.getByTestId("containers-panel").firstElementChild as HTMLElement).style.paddingLeft).toBe("4px");
  });

  it("has no search button without a palette handler", () => {
    renderPanel();
    expect(screen.queryByTitle("Search messages (Cmd+K)")).toBeNull();
  });
});
