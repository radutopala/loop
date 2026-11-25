import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type AttentionItem, fetchAttention, runAttentionAction } from "../api/attention";
import { useWebSocketConnection } from "../hooks/useWebSocketConnection";
import InboxView, { isInboxWindow } from "./InboxView";

vi.mock("../api/attention", async (orig) => ({
  ...(await orig<typeof import("../api/attention")>()),
  fetchAttention: vi.fn(),
  runAttentionAction: vi.fn(),
}));

vi.mock("../hooks/useWebSocketConnection", () => ({ useWebSocketConnection: vi.fn(() => ({ connected: true })) }));

const fetchMock = vi.mocked(fetchAttention);
const runMock = vi.mocked(runAttentionAction);
const wsMock = vi.mocked(useWebSocketConnection);

const gate: AttentionItem = { kind: "gate", channel_id: "c1", channel_name: "api", title: "Reach example.com", detail: "CONNECT", ref: "r1", blocking: true };
const plan: AttentionItem = { kind: "plan", channel_id: "c2", channel_name: "fix", parent_name: "web", title: "Plan ready", blocking: true };
const run: AttentionItem = { kind: "run", channel_id: "c1", channel_name: "api", title: "Run failed", ref: "4", blocking: false };
const pr: AttentionItem = { kind: "pr", channel_id: "c3", title: "PR open", blocking: false };
const odd: AttentionItem = { kind: "odd", channel_id: "c3", title: "Something", ref: "x", blocking: false };

function loopAPI() {
  const api = {
    onInboxShown: vi.fn((_cb: (shown: boolean) => void) => () => {}),
    inboxReport: vi.fn(),
    setInboxPinned: vi.fn(),
    moveInbox: vi.fn(),
    closeInbox: vi.fn(),
    openChannelFromInbox: vi.fn(),
  };
  vi.stubGlobal("loopAPI", api);
  return api;
}

/** Opens the mocked socket and returns it. */
function openSocket() {
  const ws = { send: vi.fn() };
  act(() => wsMock.mock.lastCall![0].onOpen!(ws as unknown as WebSocket));
  return ws;
}

function message(data: unknown) {
  act(() => wsMock.mock.lastCall![0].onMessage!({ data } as MessageEvent));
}

describe("isInboxWindow", () => {
  it.each([
    ["?window=inbox", true],
    ["?window=main", false],
    ["", false],
  ])("%s", (search, want) => {
    expect(isInboxWindow(search)).toBe(want);
  });

  it("reads the page's own query by default", () => {
    expect(isInboxWindow()).toBe(false);
  });
});

describe("InboxView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    fetchMock.mockResolvedValue([]);
    runMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("subscribes to the global events and lists the items once the socket opens", async () => {
    const api = loopAPI();
    fetchMock.mockResolvedValue([gate, run, plan, pr, odd]);
    render(<InboxView />);
    expect(wsMock.mock.lastCall![0]).toMatchObject({ path: "/api/ws", enabled: true });
    expect(screen.getByText("Nothing needs you.")).toBeTruthy();
    const ws = openSocket();
    expect(ws.send).toHaveBeenCalledWith(JSON.stringify({ type: "subscribe", channels: ["inbox"] }));
    await waitFor(() => expect(screen.getAllByTestId("inbox-item")).toHaveLength(5));
    expect(screen.getByText(/· 5 \(2 blocking\)/)).toBeTruthy();
    expect(screen.getByText("api")).toBeTruthy();
    expect(screen.getByText("web › fix")).toBeTruthy();
    expect(screen.getByText("c3")).toBeTruthy();
    const labels = screen.getAllByTestId("inbox-item").map((el) => el.querySelector("span")!.textContent);
    expect(labels).toEqual(["Approval", "Run", "Plan", "PR", "odd"]);
    expect(screen.getByText("CONNECT")).toBeTruthy();
    const actions = screen.getAllByTestId("inbox-action").map((b) => b.textContent);
    expect(actions).toEqual(["Allow once", "Allow for session", "Deny", "Dismiss", "Approve & Execute", "Discard"]);
    expect(api.inboxReport).toHaveBeenLastCalledWith({ count: 5, flash: true });
  });

  it("shows a count without blocking items", async () => {
    fetchMock.mockResolvedValue([run]);
    render(<InboxView />);
    openSocket();
    await screen.findByText("Run failed");
    expect(screen.getByText(/· 1$/)).toBeTruthy();
  });

  it("stops flashing once the inbox has been shown", async () => {
    const api = loopAPI();
    fetchMock.mockResolvedValue([gate]);
    render(<InboxView />);
    openSocket();
    await screen.findByText("Reach example.com");
    expect(api.inboxReport).toHaveBeenLastCalledWith({ count: 1, flash: true });
    act(() => api.onInboxShown.mock.calls[0]![0](true));
    expect(api.inboxReport).toHaveBeenLastCalledWith({ count: 1, flash: false });
  });

  it("shows a failed listing, as an Error or not", async () => {
    fetchMock.mockRejectedValueOnce(new Error("daemon down")).mockRejectedValueOnce("plain").mockResolvedValueOnce([]);
    render(<InboxView />);
    openSocket();
    expect(await screen.findByText("daemon down")).toBeTruthy();
    expect(screen.queryByText("Nothing needs you.")).toBeNull();
    openSocket();
    expect(await screen.findByText("plain")).toBeTruthy();
    openSocket();
    expect(await screen.findByText("Nothing needs you.")).toBeTruthy();
  });

  it("reads the list again shortly after an event that may change it", async () => {
    vi.useFakeTimers();
    render(<InboxView />);
    for (const type of ["attention.changed", "notifications.changed", "channel.deleted"]) message(JSON.stringify({ type }));
    expect(fetchMock).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("ignores other events, binary frames and junk", async () => {
    vi.useFakeTimers();
    render(<InboxView />);
    message(JSON.stringify({ type: "message.new" }));
    message(new ArrayBuffer(2));
    message("not json");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("polls every 30 seconds", async () => {
    vi.useFakeTimers();
    render(<InboxView />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("toggles and remembers the pin", async () => {
    const api = loopAPI();
    const user = userEvent.setup();
    const { unmount } = render(<InboxView />);
    const pin = screen.getByTestId("inbox-pin");
    expect(pin.textContent).toBe("Pin");
    expect(pin.getAttribute("aria-pressed")).toBe("false");
    expect(api.setInboxPinned).toHaveBeenLastCalledWith(false);
    await user.click(pin);
    expect(pin.textContent).toBe("Pinned");
    expect(pin.title).toBe("Unpin: hide when it loses focus");
    expect(api.setInboxPinned).toHaveBeenLastCalledWith(true);
    expect(localStorage.getItem("loop-inbox-pinned")).toBe("1");
    unmount();
    render(<InboxView />);
    expect(screen.getByTestId("inbox-pin").textContent).toBe("Pinned");
  });

  it("closes, and opens an item's channel on click or Enter", async () => {
    const api = loopAPI();
    fetchMock.mockResolvedValue([run]);
    const user = userEvent.setup();
    render(<InboxView />);
    openSocket();
    const item = await screen.findByTestId("inbox-item");
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(api.closeInbox).toHaveBeenCalledOnce();
    await user.click(item);
    fireEvent.keyDown(item, { key: "Enter" });
    fireEvent.keyDown(item, { key: "a" });
    expect(api.openChannelFromInbox).toHaveBeenCalledTimes(2);
    expect(api.openChannelFromInbox).toHaveBeenCalledWith("c1");
    fireEvent.mouseEnter(item);
    expect(item.style.background).not.toBe("");
    fireEvent.mouseLeave(item);
    expect(item.style.background).toBe("");
  });

  it("works without the desktop bridge", async () => {
    fetchMock.mockResolvedValue([run]);
    render(<InboxView />);
    openSocket();
    const item = await screen.findByTestId("inbox-item");
    fireEvent.click(item);
    fireEvent.keyDown(item, { key: "Enter" });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    const header = screen.getByText("Needs you").parentElement!;
    header.setPointerCapture = vi.fn();
    fireEvent.pointerDown(header, { button: 0, pointerId: 1 });
    fireEvent.pointerMove(header, { screenX: 5, screenY: 5 });
    expect(screen.getByTestId("inbox-item")).toBeTruthy();
  });

  it("takes an action, drops the item and reads the list again", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let finish!: () => void;
    runMock.mockReturnValue(new Promise<void>((r) => (finish = r)));
    fetchMock.mockResolvedValue([run, gate]);
    render(<InboxView />);
    openSocket();
    await screen.findByText("Run failed");
    const dismiss = screen.getByRole("button", { name: "Dismiss" });
    const itemClicks = vi.fn();
    loopAPI().openChannelFromInbox.mockImplementation(itemClicks);
    fireEvent.click(dismiss);
    fireEvent.keyDown(dismiss, { key: "Enter" });
    expect(itemClicks).not.toHaveBeenCalled();
    expect(runMock).toHaveBeenCalledWith(run, "dismiss");
    expect(screen.getByRole("button", { name: "Dismiss" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Dismiss" }).style.opacity).toBe("0.5");
    expect(screen.getByRole("button", { name: "Allow once" })).toHaveProperty("disabled", false);
    fetchMock.mockClear();
    fetchMock.mockResolvedValue([gate]);
    await act(async () => finish());
    expect(screen.queryByText("Run failed")).toBeNull();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("keeps the item and shows the error when an action fails", async () => {
    fetchMock.mockResolvedValue([gate]);
    runMock.mockRejectedValueOnce(new Error("gone wrong")).mockRejectedValueOnce(42);
    render(<InboxView />);
    openSocket();
    await screen.findByText("Reach example.com");
    fireEvent.click(screen.getByRole("button", { name: "Deny" }));
    expect(await screen.findByText("gone wrong")).toBeTruthy();
    await waitFor(() => expect(screen.getByRole("button", { name: "Deny" })).toHaveProperty("disabled", false));
    fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
    expect(await screen.findByText("42")).toBeTruthy();
    expect(screen.getByText("Reach example.com")).toBeTruthy();
  });

  it("moves the window while the header is dragged", () => {
    const api = loopAPI();
    render(<InboxView />);
    const header = screen.getByText("Needs you").parentElement!;
    header.setPointerCapture = vi.fn();
    fireEvent.pointerMove(header, { screenX: 50, screenY: 50 });
    expect(api.moveInbox).not.toHaveBeenCalled();
    // A right button or a press on a header button starts no drag.
    fireEvent.pointerDown(header, { button: 2, pointerId: 1, screenX: 10, screenY: 10 });
    fireEvent.pointerDown(screen.getByTestId("inbox-pin"), { button: 0, pointerId: 1, screenX: 10, screenY: 10 });
    fireEvent.pointerMove(header, { screenX: 50, screenY: 50 });
    expect(api.moveInbox).not.toHaveBeenCalled();
    fireEvent.pointerDown(header, { button: 0, pointerId: 7, screenX: 100, screenY: 200 });
    expect(header.setPointerCapture).toHaveBeenCalledWith(7);
    fireEvent.pointerMove(header, { screenX: 130, screenY: 180 });
    expect(api.moveInbox).toHaveBeenCalledWith(window.screenX + 30, window.screenY - 20);
    fireEvent.pointerUp(header);
    fireEvent.pointerMove(header, { screenX: 1, screenY: 1 });
    expect(api.moveInbox).toHaveBeenCalledOnce();
    fireEvent.pointerDown(header, { button: 0, pointerId: 7, screenX: 0, screenY: 0 });
    fireEvent.pointerCancel(header);
    fireEvent.pointerMove(header, { screenX: 1, screenY: 1 });
    expect(api.moveInbox).toHaveBeenCalledOnce();
  });
});
