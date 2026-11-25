// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getApiToken, refreshApiToken } from "../api/api";
import { fetchPlaygroundItems } from "../api/playground";
import type { Walkthrough } from "../api/walkthroughs";
import { closeWalkthrough, editorUiState, pauseWalkthrough, playWalkthrough, showWalkthrough } from "../hooks/walkthroughStore";
import { openChannelFile } from "../utils/openChannelFile";
import { DEFAULT_TIMEOUT_MS, runSteps } from "./executor";
import { notifyUiChanged, registerWorkspace } from "./registry";
import type { UiHost, WorkspaceController } from "./types";
import { useUiBridge } from "./useUiBridge";

vi.mock("../api/api", () => ({
  getApiToken: vi.fn(),
  refreshApiToken: vi.fn(),
  getWsUrl: () => "ws://daemon",
  wsProtocols: () => ["loop.v1"],
}));
vi.mock("../api/playground", () => ({ fetchPlaygroundItems: vi.fn() }));
vi.mock("../api/walkthroughs", () => ({ fetchWalkthrough: vi.fn() }));
vi.mock("../hooks/walkthroughStore", () => ({
  closeWalkthrough: vi.fn(),
  editorUiState: vi.fn(),
  pauseWalkthrough: vi.fn(),
  playWalkthrough: vi.fn(),
  showWalkthrough: vi.fn(),
}));
vi.mock("../utils/openChannelFile", () => ({ openChannelFile: vi.fn() }));
vi.mock("./executor", () => ({ DEFAULT_TIMEOUT_MS: 60_000, runSteps: vi.fn() }));

const tokenMock = vi.mocked(getApiToken);
const refreshMock = vi.mocked(refreshApiToken);
const runMock = vi.mocked(runSteps);
const editorMock = vi.mocked(editorUiState);

interface FakeSocket {
  url: string;
  protocols: string[];
  readyState: number;
  sent: unknown[];
  closed: boolean;
  onopen: (() => void) | null;
  onmessage: ((e: { data: string }) => void) | null;
  onerror: (() => void) | null;
  onclose: (() => void) | null;
  send(data: string): void;
  close(): void;
  open(): void;
  receive(data: string): void;
}

// A WebSocket stand-in that records each socket the hook opens.
function fakeWebSocket() {
  const sockets: FakeSocket[] = [];
  class FakeWS implements FakeSocket {
    static OPEN = 1;
    readyState = 0;
    sent: unknown[] = [];
    closed = false;
    onopen: (() => void) | null = null;
    onmessage: ((e: { data: string }) => void) | null = null;
    onerror: (() => void) | null = null;
    onclose: (() => void) | null = null;
    constructor(
      readonly url: string,
      readonly protocols: string[],
    ) {
      sockets.push(this);
    }
    send(data: string) {
      this.sent.push(JSON.parse(data));
    }
    close() {
      this.closed = true;
    }
    open() {
      this.readyState = 1;
      this.onopen?.();
    }
    receive(data: string) {
      this.onmessage?.({ data });
    }
  }
  return { FakeWS, sockets };
}

const flush = async () => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
};

describe("useUiBridge", () => {
  let sockets: FakeSocket[];
  const selectChannel = vi.fn(async (_id: string) => {});
  const showMessage = vi.fn(async (_ch: string, _id: number) => true);

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    sessionStorage.clear();
    const ws = fakeWebSocket();
    sockets = ws.sockets;
    vi.stubGlobal("WebSocket", ws.FakeWS);
    tokenMock.mockReturnValue("tok");
    refreshMock.mockResolvedValue("tok");
    editorMock.mockReturnValue({});
    runMock.mockResolvedValue([]);
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const mount = async (selected: string | null = "ch-1") => {
    const hook = renderHook(({ id }) => useUiBridge(id, selectChannel, showMessage), { initialProps: { id: selected } });
    await flush();
    return hook;
  };

  // Runs a command and hands back the host the executor got.
  const hostOf = async (sock: FakeSocket): Promise<UiHost> => {
    sock.receive(JSON.stringify({ type: "command", id: "c", steps: [] }));
    await flush();
    return runMock.mock.calls.at(-1)![1];
  };

  it("connects, says hello with a kept client id and reports the state", async () => {
    sessionStorage.setItem("loop-ui-client-id", "saved-id");
    await mount();
    expect(sockets).toHaveLength(1);
    const sock = sockets[0]!;
    expect(sock.url).toBe("ws://daemon/api/ws/ui");
    expect(sock.protocols).toEqual(["loop.v1"]);
    sock.open();
    expect(sock.sent).toEqual([
      { type: "hello", client_id: "saved-id" },
      { type: "state", state: { focused: true, channel_id: "ch-1" } },
    ]);
    expect(refreshMock).not.toHaveBeenCalled();
  });

  it("makes and keeps a client id when there is none", async () => {
    await mount();
    sockets[0]!.open();
    const id = sessionStorage.getItem("loop-ui-client-id");
    expect(id).toMatch(/^[0-9a-f-]{36}$/);
    expect(sockets[0]!.sent[0]).toEqual({ type: "hello", client_id: id });
  });

  it("makes a client id each time when session storage fails", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    await mount();
    sockets[0]!.open();
    expect((sockets[0]!.sent[0] as { client_id: string }).client_id).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("refreshes the token first when there is none, even if that fails", async () => {
    tokenMock.mockReturnValue("");
    refreshMock.mockRejectedValue(new Error("no token"));
    await mount();
    expect(refreshMock).toHaveBeenCalledTimes(1);
    expect(sockets).toHaveLength(1);
  });

  it("doesn't connect when unmounted while the token refreshes", async () => {
    tokenMock.mockReturnValue("");
    let resolve: (v: string) => void = () => {};
    refreshMock.mockReturnValue(new Promise((r) => (resolve = r)));
    const hook = renderHook(() => useUiBridge("ch-1", selectChannel, showMessage));
    hook.unmount();
    resolve("tok");
    await flush();
    expect(sockets).toHaveLength(0);
  });

  it("reports the workspace view and the editor state, debounced and only when it changed", async () => {
    const view = { tab: "Layout 1" };
    const unregister = registerWorkspace("ch-1", { view: () => view } as unknown as WorkspaceController);
    editorMock.mockReturnValue({ selection: { path: "a.ts" } } as ReturnType<typeof editorUiState>);
    await mount();
    const sock = sockets[0]!;
    sock.open();
    expect(sock.sent[1]).toEqual({ type: "state", state: { focused: true, channel_id: "ch-1", tab: "Layout 1", selection: { path: "a.ts" } } });
    expect(editorMock).toHaveBeenCalledWith("ch-1");

    // Nothing changed: nothing is sent.
    act(() => notifyUiChanged());
    act(() => notifyUiChanged());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sock.sent).toHaveLength(2);

    view.tab = "Layout 2";
    act(() => notifyUiChanged());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sock.sent).toHaveLength(3);
    expect(sock.sent[2]).toMatchObject({ state: { tab: "Layout 2" } });
    unregister();
  });

  it("reports no channel, focus and visibility changes", async () => {
    const hook = await mount(null);
    const sock = sockets[0]!;
    sock.open();
    expect(sock.sent[1]).toEqual({ type: "state", state: { focused: true, channel_id: "" } });
    expect(editorMock).not.toHaveBeenCalled();

    vi.mocked(document.hasFocus).mockReturnValue(false);
    window.dispatchEvent(new Event("blur"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sock.sent[2]).toEqual({ type: "state", state: { focused: false, channel_id: "" } });

    vi.mocked(document.hasFocus).mockReturnValue(true);
    window.dispatchEvent(new Event("focus"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sock.sent[3]).toMatchObject({ state: { focused: true } });

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sock.sent[4]).toMatchObject({ state: { focused: false } });

    // A new open channel is reported too.
    hook.rerender({ id: "ch-2" });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sock.sent[5]).toMatchObject({ state: { channel_id: "ch-2" } });
  });

  it("doesn't report state before the socket opens", async () => {
    await mount();
    act(() => notifyUiChanged());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(sockets[0]!.sent).toEqual([]);
  });

  it("runs commands in order and replies with their results", async () => {
    await mount();
    const sock = sockets[0]!;
    sock.open();
    const steps = [{ op: "state" }];
    runMock.mockResolvedValueOnce([{ ok: true }] as never);
    sock.receive(JSON.stringify({ type: "command", id: "c1", steps, timeout_ms: 5000 }));
    sock.receive(JSON.stringify({ type: "command", id: "c2", steps: "nope" }));
    await flush();
    expect(runMock).toHaveBeenNthCalledWith(1, steps, expect.any(Object), 5000);
    expect(runMock).toHaveBeenNthCalledWith(2, [], expect.any(Object), DEFAULT_TIMEOUT_MS);
    expect(sock.sent.slice(2)).toEqual([
      { type: "result", id: "c1", results: [{ ok: true }] },
      { type: "result", id: "c2", results: [] },
    ]);
  });

  it("replies with the error when a command fails", async () => {
    await mount();
    const sock = sockets[0]!;
    sock.open();
    runMock.mockRejectedValueOnce(new Error("timed out")).mockRejectedValueOnce("plain");
    sock.receive(JSON.stringify({ type: "command", id: "c1", steps: [] }));
    sock.receive(JSON.stringify({ type: "command", id: "c2", steps: [] }));
    await flush();
    expect(sock.sent.slice(2)).toEqual([
      { type: "result", id: "c1", results: [], error: "timed out" },
      { type: "result", id: "c2", results: [], error: "plain" },
    ]);
  });

  it("ignores bad JSON and other messages, and drops a reply once the socket closed", async () => {
    await mount();
    const sock = sockets[0]!;
    sock.receive("{bad");
    sock.receive(JSON.stringify({ type: "other" }));
    await flush();
    expect(runMock).not.toHaveBeenCalled();
    sock.receive(JSON.stringify({ type: "command", id: "c", steps: [] }));
    await flush();
    expect(runMock).toHaveBeenCalledTimes(1);
    expect(sock.sent).toEqual([]);
    sock.onerror?.();
  });

  it("reconnects with backoff and stops on unmount", async () => {
    const hook = await mount();
    const first = sockets[0]!;
    first.open();
    act(() => first.onclose?.());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(999);
    });
    expect(sockets).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(sockets).toHaveLength(2);

    // The second one never opens: the delay doubles.
    act(() => sockets[1]!.onclose?.());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1999);
    });
    expect(sockets).toHaveLength(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(sockets).toHaveLength(3);

    // A late close of an old socket leaves the current one in place.
    act(() => first.onclose?.());
    const third = sockets[2]!;
    third.open();
    expect(third.sent[0]).toMatchObject({ type: "hello" });

    // Unmounted with a reconnect and a state report pending.
    act(() => notifyUiChanged());
    hook.unmount();
    expect(third.closed).toBe(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20_000);
    });
    expect(sockets).toHaveLength(3);
    expect(third.sent).toHaveLength(2);
  });

  it("survives strict mode's double mount with one live socket", async () => {
    renderHook(() => useUiBridge("ch-1", selectChannel, showMessage), { reactStrictMode: true });
    await flush();
    expect(sockets).toHaveLength(2);
    expect(sockets[0]!.closed).toBe(true);
    expect(sockets[1]!.closed).toBe(false);
    sockets[1]!.open();
    expect(sockets[1]!.sent[1]).toEqual({ type: "state", state: { focused: true, channel_id: "ch-1" } });
  });

  it("doesn't reconnect when the socket closes after unmount", async () => {
    const hook = await mount();
    const sock = sockets[0]!;
    hook.unmount();
    expect(sock.closed).toBe(true);
    sock.onclose?.();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20_000);
    });
    expect(sockets).toHaveLength(1);
  });

  it("gives the executor a host wired to the app", async () => {
    vi.mocked(fetchPlaygroundItems).mockResolvedValue([{ name: "p1", scope: "channel", extra: 1 }] as never);
    vi.mocked(openChannelFile).mockResolvedValue({ rootIndex: 0, relPath: "a.ts" });
    vi.mocked(showWalkthrough).mockResolvedValue();
    const hook = await mount();
    const sock = sockets[0]!;
    sock.open();
    const host = await hostOf(sock);

    expect(host.selectedChannelId()).toBe("ch-1");
    hook.rerender({ id: "ch-9" });
    expect(host.selectedChannelId()).toBe("ch-9");

    await host.selectChannel("ch-3");
    expect(selectChannel).toHaveBeenCalledWith("ch-3");
    await expect(host.showMessage("ch-3", 7)).resolves.toBe(true);
    expect(showMessage).toHaveBeenCalledWith("ch-3", 7);

    await host.openFile("ch-1", "a.ts", 3);
    expect(openChannelFile).toHaveBeenLastCalledWith("ch-1", "a.ts", 3, true);
    await host.openFile("ch-1", "a.ts", undefined, true);
    expect(openChannelFile).toHaveBeenLastCalledWith("ch-1", "a.ts", undefined, false);

    const w = { id: 4, title: "Tour", channel_id: "ch-1", created_at: "", step_count: 0 } as Walkthrough;
    await host.showWalkthrough("ch-1", w, 0);
    expect(showWalkthrough).toHaveBeenLastCalledWith("ch-1", { id: 4, title: "Tour", steps: [] }, 0, openChannelFile);
    expect(pauseWalkthrough).toHaveBeenCalledWith("ch-1");
    const steps = [{ file: "a.ts", line: 1, text: "x" }] as unknown as Walkthrough["steps"];
    await host.showWalkthrough("ch-1", { ...w, steps }, 1, 5);
    expect(showWalkthrough).toHaveBeenLastCalledWith("ch-1", { id: 4, title: "Tour", steps }, 1, openChannelFile);
    expect(playWalkthrough).toHaveBeenCalledWith("ch-1", openChannelFile, 5);

    host.closeWalkthrough("ch-1");
    expect(closeWalkthrough).toHaveBeenCalledWith("ch-1");

    await expect(host.playgrounds("ch-1")).resolves.toEqual([{ name: "p1", scope: "channel" }]);
    expect(fetchPlaygroundItems).toHaveBeenCalledWith("ch-1");

    expect(host.now()).toBe(Date.now());
    let slept = false;
    void host.sleep(50).then(() => {
      slept = true;
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    expect(slept).toBe(true);
    expect(host.workspace("none")).toBeUndefined();
    expect(host.terminal("none", "p")).toBeUndefined();
    expect(host.chat("none", "p")).toBeUndefined();
    expect(host.side("none")).toBeUndefined();
  });
});
