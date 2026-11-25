// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { browserAction } from "../api/loopApi";
import { useBrowserWs } from "./useBrowserWs";

vi.mock("../api/loopApi", () => ({
  browserAction: vi.fn(),
  getWsUrl: () => "ws://host",
  wsProtocols: () => ["proto"],
}));

// A WebSocket stand-in the test drives by hand: it records what is sent and
// lets the test fire open/message/close.
interface FakeSocket {
  url: string;
  protocols: string[];
  readyState: number;
  binaryType: string;
  sent: unknown[];
  closed: boolean;
  onopen: (() => void) | null;
  onmessage: ((e: { data: unknown }) => void) | null;
  onclose: (() => void) | null;
  send(data: string): void;
  close(): void;
}

function makeSocketClass(sockets: FakeSocket[]) {
  return class implements FakeSocket {
    static OPEN = 1;
    readyState = 0;
    binaryType = "";
    sent: unknown[] = [];
    closed = false;
    onopen: (() => void) | null = null;
    onmessage: ((e: { data: unknown }) => void) | null = null;
    onclose: (() => void) | null = null;
    constructor(
      public url: string,
      public protocols: string[],
    ) {
      sockets.push(this);
    }
    send(data: string) {
      this.sent.push(JSON.parse(data));
    }
    close() {
      this.closed = true;
    }
  };
}

const flush = () => act(async () => {});

describe("useBrowserWs", () => {
  let sockets: FakeSocket[];

  beforeEach(() => {
    sockets = [];
    vi.stubGlobal("WebSocket", makeSocketClass(sockets));
    vi.mocked(browserAction).mockResolvedValue({});
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.mocked(browserAction).mockReset();
  });

  /** The nth socket the hook opened. */
  function socket(n: number): FakeSocket {
    const ws = sockets[n];
    if (!ws) throw new Error(`no socket #${n}`);
    return ws;
  }

  function open(ws: FakeSocket) {
    act(() => {
      ws.readyState = 1;
      ws.onopen?.();
    });
  }

  function recv(ws: FakeSocket, msg: unknown) {
    act(() => ws.onmessage?.({ data: typeof msg === "string" ? msg : JSON.stringify(msg) }));
  }

  it("does nothing without a channel", () => {
    const { result } = renderHook(() => useBrowserWs({ channelId: null }));
    expect(sockets).toHaveLength(0);
    act(() => {
      result.current.startBrowser("docker");
      result.current.stopBrowser();
      result.current.navigate("https://a");
      result.current.reload();
      result.current.goBack();
      result.current.goForward();
      result.current.listDownloads();
      result.current.listTabs();
      result.current.switchTab("t1");
      result.current.newTab();
      result.current.closeTab("t1");
    });
    expect(browserAction).not.toHaveBeenCalled();
    expect(result.current.connected).toBe(false);
    expect(result.current.activeTargetId).toBe("");
  });

  it("connects, sends only while open, and forwards frames", () => {
    const onFrame = vi.fn();
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1", onFrame }));
    const ws = socket(0);
    expect(ws.url).toBe("ws://host/api/ws/browser");
    expect(ws.protocols).toEqual(["proto"]);
    expect(ws.binaryType).toBe("arraybuffer");

    // Not open yet: send is dropped.
    act(() => result.current.startBrowser());
    expect(ws.sent).toEqual([]);

    open(ws);
    expect(result.current.connected).toBe(true);

    act(() => {
      result.current.startBrowser("host");
      result.current.stopBrowser();
      result.current.startStreaming(800, 600);
      result.current.sendInput({ type: "mouse", x: 1, y: 2, button: "left", clickCount: 1, deltaX: 3, deltaY: 4, key: "a", text: "b", modifiers: 2 });
      result.current.chooseSelectOption(3);
      result.current.listDownloads();
      result.current.answerDialog("t1", true, "yes");
      result.current.answerFileChooser("t1", null);
      result.current.answerFileChooser("t1", [{ name: "f", data: "ZA==" } as never]);
    });
    expect(ws.sent).toEqual([
      { type: "start", channel_id: "c1", mode: "host" },
      { type: "stop", channel_id: "c1" },
      { type: "screencast", width: 800, height: 600 },
      { type: "input", input_type: "mouse", x: 1, y: 2, button: "left", click_count: 1, delta_x: 3, delta_y: 4, key: "a", text: "b", modifiers: 2 },
      { type: "input", input_type: "select_choose", index: 3 },
      { type: "list_downloads", channel_id: "c1" },
      { type: "dialog_answer", target_id: "t1", accept: true, text: "yes" },
      { type: "file_chooser_answer", target_id: "t1", files: [] },
      { type: "file_chooser_answer", target_id: "t1", files: [{ name: "f", data: "ZA==" }] },
    ]);

    const buf = new ArrayBuffer(4);
    act(() => ws.onmessage?.({ data: buf }));
    expect(onFrame).toHaveBeenCalledWith(buf);
  });

  it("on started, begins the screencast and loads the tab list", async () => {
    const onStarted = vi.fn();
    vi.mocked(browserAction).mockResolvedValueOnce({
      tabs: [
        { target_id: "a", url: "https://a", title: "A" },
        { target_id: "b", url: "https://b", title: "B", active: true },
      ],
    });
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1", onStarted }));
    const ws = socket(0);
    open(ws);
    recv(ws, { type: "started" });
    await flush();
    expect(result.current.started).toBe(true);
    expect(ws.sent).toEqual([{ type: "screencast", width: 1920, height: 1080 }]);
    expect(browserAction).toHaveBeenCalledWith("c1", "list_tabs");
    expect(onStarted).toHaveBeenCalled();
    expect(result.current.tabs.map((t) => t.target_id)).toEqual(["a", "b"]);
    expect(result.current.activeTargetId).toBe("b");
  });

  it("on started, keeps state when the tab list is empty or has no active tab", async () => {
    vi.mocked(browserAction)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ tabs: [{ target_id: "a", url: "u", title: "t" }] });
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1" }));
    const ws = socket(0);
    recv(ws, { type: "started" });
    await flush();
    expect(result.current.tabs).toEqual([]);
    recv(ws, { type: "started" });
    await flush();
    expect(result.current.tabs).toEqual([{ target_id: "a", url: "u", title: "t" }]);
    expect(result.current.activeTargetId).toBe("");
  });

  it("routes each message type to its callback or state", async () => {
    const cb = {
      onPageInfo: vi.fn(),
      onError: vi.fn(),
      onStopped: vi.fn(),
      onContextMenu: vi.fn(),
      onSelectMenu: vi.fn(),
      onDownload: vi.fn(),
      onDownloadList: vi.fn(),
    };
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1", ...cb }));
    const ws = socket(0);

    recv(ws, {
      type: "tabs",
      tabs: [
        { target_id: "a", url: "ua", title: "A" },
        { target_id: "b", url: "ub", title: "B" },
      ],
      active_target_id: "a",
    });
    expect(result.current.activeTargetId).toBe("a");

    // page_info updates only the active tab, keeping fields the message omits.
    recv(ws, { type: "page_info", url: "ua2", title: "A2" });
    expect(cb.onPageInfo).toHaveBeenLastCalledWith("ua2", "A2");
    recv(ws, { type: "page_info" });
    expect(cb.onPageInfo).toHaveBeenLastCalledWith("", "");
    expect(result.current.tabs).toEqual([
      { target_id: "a", url: "ua2", title: "A2" },
      { target_id: "b", url: "ub", title: "B" },
    ]);

    recv(ws, { type: "clipboard", text: "copied" });
    expect(writeText).toHaveBeenCalledWith("copied");
    recv(ws, { type: "clipboard" });
    expect(writeText).toHaveBeenCalledTimes(1);

    const menu = { x: 1 };
    recv(ws, { type: "context_menu", menu });
    recv(ws, { type: "context_menu" });
    expect(cb.onContextMenu).toHaveBeenCalledTimes(1);
    expect(cb.onContextMenu).toHaveBeenCalledWith(menu);

    const select = { options: [] };
    recv(ws, { type: "select_menu", select });
    recv(ws, { type: "select_menu" });
    expect(cb.onSelectMenu).toHaveBeenCalledTimes(1);
    expect(cb.onSelectMenu).toHaveBeenCalledWith(select);

    const download = { guid: "g" };
    recv(ws, { type: "download", download });
    recv(ws, { type: "download" });
    expect(cb.onDownload).toHaveBeenCalledTimes(1);
    expect(cb.onDownload).toHaveBeenCalledWith(download);

    recv(ws, { type: "downloads", downloads_dir: "/d", files: [{ name: "x" }] });
    expect(cb.onDownloadList).toHaveBeenLastCalledWith({ dir: "/d", files: [{ name: "x" }] });
    recv(ws, { type: "downloads" });
    expect(cb.onDownloadList).toHaveBeenLastCalledWith({ dir: "", files: [] });

    recv(ws, { type: "error", message: "boom" });
    expect(cb.onError).toHaveBeenLastCalledWith("boom");
    recv(ws, { type: "error" });
    expect(cb.onError).toHaveBeenLastCalledWith("Unknown error");

    recv(ws, { type: "stopped" });
    expect(cb.onStopped).toHaveBeenCalled();
    expect(result.current.started).toBe(false);

    // Messages without their payload, and unknown types, change nothing.
    const before = result.current.tabs;
    recv(ws, { type: "tabs" });
    recv(ws, { type: "tab_switched" });
    recv(ws, { type: "tab_created" });
    recv(ws, { type: "tab_updated" });
    recv(ws, { type: "tab_closed" });
    recv(ws, { type: "nope" });
    recv(ws, "not json");
    expect(result.current.tabs).toBe(before);
    expect(result.current.activeTargetId).toBe("a");
  });

  it("swallows a failed clipboard write and a missing clipboard", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    renderHook(() => useBrowserWs({ channelId: "c1" }));
    recv(socket(0), { type: "clipboard", text: "x" });
    await flush();
    expect(writeText).toHaveBeenCalledWith("x");
    vi.stubGlobal("navigator", {});
    expect(() => recv(socket(0), { type: "clipboard", text: "y" })).not.toThrow();
  });

  it("ignores every message when no callbacks are given", () => {
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1" }));
    const ws = socket(0);
    for (const msg of [
      { type: "stopped" },
      { type: "page_info", url: "u", title: "t" },
      { type: "context_menu", menu: {} },
      { type: "select_menu", select: {} },
      { type: "download", download: {} },
      { type: "downloads" },
      { type: "error" },
      { type: "tab_updated", target_id: "" },
    ]) {
      recv(ws, msg);
    }
    act(() => ws.onmessage?.({ data: new ArrayBuffer(1) }));
    expect(result.current.started).toBe(false);
  });

  it("tracks tabs as they are created, switched, updated and closed", () => {
    const onPageInfo = vi.fn();
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1", onPageInfo }));
    const ws = socket(0);

    recv(ws, { type: "tab_created", target_id: "a", url: "ua", title: "A" });
    recv(ws, { type: "tab_created", target_id: "b" });
    // A duplicate is not added twice.
    recv(ws, { type: "tab_created", target_id: "a", url: "other" });
    expect(result.current.tabs).toEqual([
      { target_id: "a", url: "ua", title: "A" },
      { target_id: "b", url: "", title: "" },
    ]);
    expect(result.current.activeTargetId).toBe("a");

    recv(ws, { type: "tab_switched", target_id: "b" });
    expect(result.current.activeTargetId).toBe("b");

    // An update to a background tab does not report page info.
    recv(ws, { type: "tab_updated", target_id: "a", title: "A2" });
    expect(onPageInfo).not.toHaveBeenCalled();
    expect(result.current.tabs[0]).toEqual({ target_id: "a", url: "ua", title: "A2" });

    recv(ws, { type: "tab_updated", target_id: "b", url: "ub", title: "B" });
    expect(onPageInfo).toHaveBeenCalledWith("ub", "B");
    expect(result.current.tabs[1]).toEqual({ target_id: "b", url: "ub", title: "B" });
    // A missing title clears it.
    recv(ws, { type: "tab_updated", target_id: "b", url: "ub" });
    expect(onPageInfo).toHaveBeenLastCalledWith("ub", "");
    expect(result.current.tabs[1]).toEqual({ target_id: "b", url: "ub", title: "" });

    recv(ws, { type: "dialog", dialog: { target_id: "b", type: "confirm", message: "ok?" } });
    recv(ws, { type: "file_chooser", file_chooser: { target_id: "b", multiple: true } });
    expect(result.current.dialog).toEqual({ target_id: "b", type: "confirm", message: "ok?" });
    expect(result.current.fileChooser).toEqual({ target_id: "b", multiple: true });

    recv(ws, { type: "tab_closed", target_id: "b" });
    expect(result.current.tabs.map((t) => t.target_id)).toEqual(["a"]);
    expect(result.current.dialogs).toEqual({});
    expect(result.current.fileChooser).toBeUndefined();
  });

  it("opens and closes dialogs and file choosers per tab", () => {
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1" }));
    const ws = socket(0);
    recv(ws, { type: "tabs", active_target_id: "a" });
    recv(ws, { type: "dialog", dialog: { target_id: "a", type: "prompt", message: "name?" } });
    recv(ws, { type: "dialog" });
    recv(ws, { type: "file_chooser", file_chooser: { target_id: "a" } });
    recv(ws, { type: "file_chooser" });
    expect(result.current.dialog?.message).toBe("name?");
    expect(result.current.fileChooser).toEqual({ target_id: "a" });

    recv(ws, { type: "dialog_closed" });
    recv(ws, { type: "file_chooser_closed" });
    expect(result.current.dialog).toBeDefined();
    recv(ws, { type: "dialog_closed", target_id: "a" });
    recv(ws, { type: "file_chooser_closed", target_id: "a" });
    expect(result.current.dialog).toBeUndefined();
    expect(result.current.fileChooser).toBeUndefined();
  });

  it("reconnects with backoff after a close and clears per-tab state", () => {
    vi.useFakeTimers();
    const { result, unmount } = renderHook(() => useBrowserWs({ channelId: "c1" }));
    const first = socket(0);
    open(first);
    recv(first, { type: "dialog", dialog: { target_id: "", type: "confirm", message: "m" } });
    expect(result.current.dialog).toBeDefined();

    act(() => first.onclose?.());
    expect(result.current.connected).toBe(false);
    expect(result.current.dialogs).toEqual({});
    act(() => vi.advanceTimersByTime(999));
    expect(sockets).toHaveLength(1);
    act(() => vi.advanceTimersByTime(1));
    expect(sockets).toHaveLength(2);

    // The second close waits twice as long.
    act(() => socket(1).onclose?.());
    act(() => vi.advanceTimersByTime(1999));
    expect(sockets).toHaveLength(2);
    act(() => vi.advanceTimersByTime(1));
    expect(sockets).toHaveLength(3);

    // Opening resets the backoff.
    open(socket(2));
    act(() => socket(2).onclose?.());
    act(() => vi.advanceTimersByTime(1000));
    expect(sockets).toHaveLength(4);

    // Unmounting with a reconnect pending cancels it and closes the socket.
    act(() => socket(3).onclose?.());
    unmount();
    act(() => vi.advanceTimersByTime(20000));
    expect(sockets).toHaveLength(4);
  });

  it("closes the socket on unmount and ignores its late close", () => {
    const { result, unmount } = renderHook(() => useBrowserWs({ channelId: "c1" }));
    const ws = socket(0);
    open(ws);
    unmount();
    expect(ws.closed).toBe(true);
    ws.onclose?.();
    expect(result.current.connected).toBe(true);
    expect(sockets).toHaveLength(1);
  });

  it("navigates over HTTP, updates the active tab, then refreshes the tab list", async () => {
    vi.useFakeTimers();
    const onPageInfo = vi.fn();
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1", onPageInfo }));
    const ws = socket(0);
    recv(ws, {
      type: "tabs",
      tabs: [
        { target_id: "a", url: "", title: "" },
        { target_id: "b", url: "", title: "" },
      ],
      active_target_id: "a",
    });

    vi.mocked(browserAction)
      .mockResolvedValueOnce({ page_info: { url: "https://x", title: "X" } })
      .mockResolvedValueOnce({ tabs: [{ target_id: "a", url: "https://x", title: "X final" }] });
    act(() => result.current.navigate("https://x"));
    await flush();
    expect(browserAction).toHaveBeenCalledWith("c1", "navigate", { url: "https://x" });
    expect(onPageInfo).toHaveBeenCalledWith("https://x", "X");
    expect(result.current.tabs).toEqual([
      { target_id: "a", url: "https://x", title: "X" },
      { target_id: "b", url: "", title: "" },
    ]);
    await act(async () => vi.advanceTimersByTime(1000));
    expect(browserAction).toHaveBeenLastCalledWith("c1", "list_tabs");
    expect(result.current.tabs).toEqual([{ target_id: "a", url: "https://x", title: "X final" }]);

    // Without page info or tabs in the answers, nothing changes.
    vi.mocked(browserAction).mockResolvedValueOnce({}).mockResolvedValueOnce({});
    act(() => result.current.navigate("https://y"));
    await flush();
    await act(async () => vi.advanceTimersByTime(1000));
    expect(onPageInfo).toHaveBeenCalledTimes(1);
    expect(result.current.tabs).toEqual([{ target_id: "a", url: "https://x", title: "X final" }]);
  });

  it("calls the HTTP tab and history actions", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useBrowserWs({ channelId: "c1" }));
    act(() => {
      result.current.reload();
      result.current.goBack();
      result.current.goForward();
      result.current.closeTab("t9");
    });
    expect(vi.mocked(browserAction).mock.calls).toEqual([
      ["c1", "reload"],
      ["c1", "go_back"],
      ["c1", "go_forward"],
      ["c1", "close_tab", { target_id: "t9" }],
    ]);

    act(() => result.current.switchTab("t2"));
    expect(result.current.activeTargetId).toBe("t2");
    expect(browserAction).toHaveBeenLastCalledWith("c1", "switch_tab", { target_id: "t2" });

    vi.mocked(browserAction).mockResolvedValueOnce({ tabs: [{ target_id: "x", url: "", title: "", active: true }] });
    act(() => result.current.listTabs());
    await flush();
    expect(result.current.activeTargetId).toBe("x");
    vi.mocked(browserAction).mockResolvedValueOnce({ tabs: [{ target_id: "y", url: "", title: "" }] });
    act(() => result.current.listTabs());
    await flush();
    expect(result.current.tabs.map((t) => t.target_id)).toEqual(["y"]);
    expect(result.current.activeTargetId).toBe("x");
    vi.mocked(browserAction).mockResolvedValueOnce({});
    act(() => result.current.listTabs());
    await flush();
    expect(result.current.tabs.map((t) => t.target_id)).toEqual(["y"]);

    vi.mocked(browserAction)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ tabs: [{ target_id: "n", url: "u", title: "New" }] });
    act(() => result.current.newTab("u"));
    await flush();
    expect(browserAction).toHaveBeenLastCalledWith("c1", "new_tab", { url: "u" });
    await act(async () => vi.advanceTimersByTime(1000));
    expect(browserAction).toHaveBeenLastCalledWith("c1", "list_tabs");
    expect(result.current.tabs).toEqual([{ target_id: "n", url: "u", title: "New" }]);

    vi.mocked(browserAction).mockResolvedValueOnce({}).mockResolvedValueOnce({});
    act(() => result.current.newTab());
    await flush();
    await act(async () => vi.advanceTimersByTime(1000));
    expect(result.current.tabs).toEqual([{ target_id: "n", url: "u", title: "New" }]);
  });
});
