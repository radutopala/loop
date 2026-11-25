import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type FakeWebSocket, fakeWebSocketClass } from "../test/hooks-a-ws";
import { useEventStream } from "./useEventStream";

vi.mock("../api/loopApi", () => ({
  getWsUrl: () => "ws://daemon",
  wsProtocols: () => [],
  clearContentCaps: vi.fn(),
  refreshApiToken: vi.fn(() => Promise.resolve("")),
  retryFailedContentCaps: vi.fn(),
}));

describe("useEventStream", () => {
  let sockets: FakeWebSocket[];

  beforeEach(() => {
    const fake = fakeWebSocketClass();
    sockets = fake.sockets;
    vi.stubGlobal("WebSocket", fake.WebSocket);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("subscribes to the channel's events and passes each one on", () => {
    const onEvent = vi.fn();
    renderHook(() => useEventStream({ channelId: "a b", onEvent }));
    expect(sockets[0]!.url).toBe("ws://daemon/api/ws?channels=a%20b");
    sockets[0]!.receive(JSON.stringify({ type: "message.created", data: { id: 1 } }));
    expect(onEvent).toHaveBeenCalledWith({ type: "message.created", data: { id: 1 } });
  });

  it("ignores malformed messages", () => {
    const onEvent = vi.fn();
    renderHook(() => useEventStream({ channelId: "c", onEvent }));
    sockets[0]!.receive("{not json");
    expect(onEvent).not.toHaveBeenCalled();
  });

  it("doesn't connect without a channel", () => {
    renderHook(() => useEventStream({ channelId: null, onEvent: vi.fn() }));
    expect(sockets).toHaveLength(0);
  });
});
