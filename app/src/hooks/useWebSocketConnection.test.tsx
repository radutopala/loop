import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearContentCaps, refreshApiToken, retryFailedContentCaps } from "../api/loopApi";
import { type FakeWebSocket, fakeWebSocketClass } from "../test/hooks-a-ws";
import { useWebSocketConnection } from "./useWebSocketConnection";

vi.mock("../api/loopApi", () => ({
  getWsUrl: () => "ws://daemon",
  wsProtocols: () => ["loop.v1"],
  clearContentCaps: vi.fn(),
  refreshApiToken: vi.fn(),
  retryFailedContentCaps: vi.fn(),
}));

describe("useWebSocketConnection", () => {
  let sockets: FakeWebSocket[];

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    vi.mocked(refreshApiToken).mockResolvedValue("token");
    const fake = fakeWebSocketClass();
    sockets = fake.sockets;
    vi.stubGlobal("WebSocket", fake.WebSocket);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("connects to the path with the API's protocols, as binary", () => {
    renderHook(() => useWebSocketConnection({ path: "/api/ws/x", enabled: true }));
    expect(sockets).toHaveLength(1);
    expect(sockets[0]!.url).toBe("ws://daemon/api/ws/x");
    expect(sockets[0]!.protocols).toEqual(["loop.v1"]);
    expect(sockets[0]!.binaryType).toBe("arraybuffer");
  });

  it("doesn't connect while disabled", () => {
    renderHook(() => useWebSocketConnection({ path: "/p", enabled: false }));
    expect(sockets).toHaveLength(0);
  });

  it("reports the open, retries failed links and hands over the socket", () => {
    const onOpen = vi.fn();
    const { result } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true, onOpen }));
    expect(result.current.connected).toBe(false);
    act(() => sockets[0]!.open());
    expect(result.current.connected).toBe(true);
    expect(retryFailedContentCaps).toHaveBeenCalledOnce();
    expect(onOpen).toHaveBeenCalledWith(sockets[0]);
  });

  it("opens and receives without handlers", () => {
    const { result } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    act(() => {
      sockets[0]!.open();
      sockets[0]!.receive("hi");
    });
    expect(result.current.connected).toBe(true);
  });

  it("passes each message to the latest handler", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ onMessage }) => useWebSocketConnection({ path: "/p", enabled: true, onMessage }), { initialProps: { onMessage: first } });
    rerender({ onMessage: second });
    sockets[0]!.receive("hello");
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledWith(expect.objectContaining({ data: "hello" }));
    expect(sockets).toHaveLength(1);
  });

  it("sends only while open", () => {
    const { result } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    result.current.send("early");
    act(() => sockets[0]!.open());
    result.current.send("ping");
    expect(sockets[0]!.sent).toEqual(["ping"]);
  });

  it("sends nothing once the socket is gone", () => {
    const { result } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    act(() => sockets[0]!.drop());
    result.current.send("x");
    expect(sockets[0]!.sent).toEqual([]);
  });

  it("forgets content links, rereads the token and reconnects after a drop", async () => {
    const { result } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true, reconnectDelay: 500 }));
    act(() => sockets[0]!.open());
    act(() => sockets[0]!.drop());
    expect(result.current.connected).toBe(false);
    expect(clearContentCaps).toHaveBeenCalledOnce();
    expect(refreshApiToken).toHaveBeenCalledOnce();
    act(() => vi.advanceTimersByTime(499));
    expect(sockets).toHaveLength(1);
    act(() => vi.advanceTimersByTime(1));
    expect(sockets).toHaveLength(2);
  });

  it("reconnects even when rereading the token fails", async () => {
    vi.mocked(refreshApiToken).mockRejectedValue(new Error("no token"));
    renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    await act(async () => {
      sockets[0]!.drop();
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(sockets).toHaveLength(2);
  });

  it("closes the socket on an error", () => {
    renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    sockets[0]!.fail();
    expect(sockets[0]!.closed).toBe(true);
  });

  it("closes without reconnecting on unmount", () => {
    const { unmount } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    const ws = sockets[0]!;
    unmount();
    expect(ws.closed).toBe(true);
    expect(ws.onclose).toBeNull();
    act(() => vi.advanceTimersByTime(10_000));
    expect(sockets).toHaveLength(1);
  });

  it("cancels a pending reconnect on unmount", () => {
    const { unmount } = renderHook(() => useWebSocketConnection({ path: "/p", enabled: true }));
    act(() => sockets[0]!.drop());
    unmount();
    act(() => vi.advanceTimersByTime(10_000));
    expect(sockets).toHaveLength(1);
  });
});
