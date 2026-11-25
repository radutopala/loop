import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPlaygroundShares, type PlaygroundShare, unsharePlayground } from "../../api/loopApi";
import { useEventStream } from "../../hooks/useEventStream";
import type { Channel, WSEvent } from "../../types";
import { openExternalUrl } from "../../utils/openExternal";
import { GlobalSharesPanel } from "./GlobalSharesPanel";

vi.mock("../../api/loopApi", () => ({
  fetchPlaygroundShares: vi.fn(),
  unsharePlayground: vi.fn(),
}));

vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

vi.mock("../../utils/openExternal", () => ({ openExternalUrl: vi.fn() }));

// The header info fetches PR and commit data; the stub shows which channel it got.
vi.mock("../layout/ChannelHeaderInfo", () => ({
  ChannelHeaderInfo: ({ channel }: { channel: Channel }) => <span>header {channel.name}</span>,
}));

const fetchMock = vi.mocked(fetchPlaygroundShares);
const unshareMock = vi.mocked(unsharePlayground);

const shares: PlaygroundShare[] = [
  { name: "demo", scope: "global", channel_id: "", url: "https://share.example/demo" },
  { name: "site", scope: "project", channel_id: "ch1", url: "https://share.example/site" },
];

function emit(event: WSEvent) {
  const opts = vi.mocked(useEventStream).mock.lastCall?.[0];
  act(() => opts?.onEvent(event));
}

// jsdom has no clipboard; each test that copies installs one and afterEach removes it.
function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
}

describe("GlobalSharesPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchMock.mockResolvedValue(shares);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    Reflect.deleteProperty(navigator, "clipboard");
  });

  it("shows the empty state when nothing is shared", async () => {
    fetchMock.mockResolvedValue([]);
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    expect(await screen.findByText(/No playgrounds are shared publicly/)).toBeTruthy();
    expect(screen.queryByText("(0)")).toBeNull();
    expect(vi.mocked(useEventStream).mock.lastCall?.[0].channelId).toBeNull();
  });

  it("lists shares with a count and opens a URL externally", async () => {
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    expect(await screen.findByText("demo")).toBeTruthy();
    expect(screen.getByText("(2)")).toBeTruthy();
    expect(screen.getByText("project")).toBeTruthy();
    await userEvent.click(screen.getByText("https://share.example/site"));
    expect(openExternalUrl).toHaveBeenCalledWith("https://share.example/site");
  });

  it("logs a failed fetch", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching playground shares:", expect.any(Error)));
  });

  it("renders the palette button and channel header, and closes", async () => {
    const onOpenPalette = vi.fn();
    const onClose = vi.fn();
    const channel = { id: "ch1", name: "alpha" } as Channel;
    render(<GlobalSharesPanel channel={channel} sidebarOpen={false} onOpenPalette={onOpenPalette} onClose={onClose} />);
    await screen.findByText("demo");
    expect(screen.getByText("header alpha")).toBeTruthy();
    expect(vi.mocked(useEventStream).mock.lastCall?.[0].channelId).toBe("ch1");
    await userEvent.click(screen.getByTitle("Search messages (Cmd+K)"));
    expect(onOpenPalette).toHaveBeenCalled();
    await userEvent.click(screen.getByTitle("Close"));
    expect(onClose).toHaveBeenCalled();
  });

  it("shows the Mac shortcut on a Mac", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    render(<GlobalSharesPanel onOpenPalette={vi.fn()} onClose={vi.fn()} />);
    expect(await screen.findByText("⌘K")).toBeTruthy();
  });

  it("shows Ctrl+K elsewhere", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    render(<GlobalSharesPanel onOpenPalette={vi.fn()} onClose={vi.fn()} />);
    expect(await screen.findByText("Ctrl+K")).toBeTruthy();
  });

  it("reloads on a share update and ignores other events", async () => {
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    await screen.findByText("demo");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    emit({ type: "playground.update", data: { kind: "item" } } as WSEvent);
    emit({ type: "playground.update" } as WSEvent);
    emit({ type: "message.new", data: { kind: "share" } } as WSEvent);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    fetchMock.mockResolvedValue([shares[0]!]);
    emit({ type: "playground.update", data: { kind: "share" } } as WSEvent);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(screen.queryByText("site")).toBeNull());
  });

  it("disables a share and drops only that row", async () => {
    let resolve: () => void = () => {};
    unshareMock.mockReturnValue(
      new Promise<void>((r) => {
        resolve = r;
      }),
    );
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    await screen.findByText("site");
    const buttons = screen.getAllByRole("button", { name: "Disable" });
    await userEvent.click(buttons[1]!);
    expect(unshareMock).toHaveBeenCalledWith("site", "project", "ch1");
    const pending = screen.getByRole("button", { name: "…" }) as HTMLButtonElement;
    expect(pending.disabled).toBe(true);
    expect(pending.style.cursor).toBe("default");
    await act(async () => resolve());
    await waitFor(() => expect(screen.queryByText("site")).toBeNull());
    expect(screen.getByText("demo")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Disable" })).toHaveLength(1);
  });

  it("keeps the row and logs when disabling fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    unshareMock.mockRejectedValue(new Error("nope"));
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    await screen.findByText("demo");
    await userEvent.click(screen.getAllByRole("button", { name: "Disable" })[0]!);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] unsharing playground:", expect.any(Error)));
    expect(screen.getByText("demo")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Disable" })).toHaveLength(2);
  });

  it("copies a URL and resets the label after a moment", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard(writeText);
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    await screen.findByText("demo");
    vi.useFakeTimers();
    await act(async () => screen.getAllByTitle("Copy share URL")[0]!.click());
    expect(writeText).toHaveBeenCalledWith("https://share.example/demo");
    expect(screen.getByText("copied!")).toBeTruthy();
    act(() => vi.advanceTimersByTime(1500));
    expect(screen.queryByText("copied!")).toBeNull();
  });

  it("logs a failed copy", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    stubClipboard(writeText);
    render(<GlobalSharesPanel onClose={vi.fn()} />);
    await screen.findByText("demo");
    await act(async () => screen.getAllByTitle("Copy share URL")[0]!.click());
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] copying share url:", expect.any(Error)));
    expect(screen.queryByText("copied!")).toBeNull();
  });
});
