import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type FakeWebSocket, fakeWebSocketClass } from "../test/hooks-a-ws";
import type { TerminalTarget } from "../types";
import { useTerminalWs } from "./useTerminalWs";

vi.mock("../api/loopApi", () => ({
  getWsUrl: () => "ws://daemon",
  wsProtocols: () => [],
  clearContentCaps: vi.fn(),
  refreshApiToken: vi.fn(() => Promise.resolve("")),
  retryFailedContentCaps: vi.fn(),
}));
vi.mock("../api/terminal", () => ({ claimTerminalSessions: vi.fn(() => Promise.resolve()) }));

type Options = Parameters<typeof useTerminalWs>[0];

// Session ids outlive a hook (they're kept per pane across remounts), so
// each test gets channels of its own.
let seq = 0;
function channel() {
  seq++;
  return `ch-${seq}`;
}

describe("useTerminalWs", () => {
  let sockets: FakeWebSocket[];

  beforeEach(() => {
    sessionStorage.clear();
    const fake = fakeWebSocketClass();
    sockets = fake.sockets;
    vi.stubGlobal("WebSocket", fake.WebSocket);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function setup(opts: Partial<Options> = {}) {
    const callbacks = { onData: vi.fn(), onStatus: vi.fn(), onError: vi.fn(), onCreated: vi.fn() };
    const hook = renderHook((props: Partial<Options>) => useTerminalWs({ channelId: null, ...callbacks, ...props }), { initialProps: { channelId: channel(), ...opts } });
    const ws = () => sockets.at(-1)!;
    const sent = () => ws().sent.map((m) => JSON.parse(m as string));
    const receive = (msg: object) => act(() => ws().receive(JSON.stringify(msg)));
    return { ...hook, callbacks, ws, sent, receive };
  }

  it("connects to the terminal socket only with a channel", () => {
    setup({ channelId: null });
    expect(sockets).toHaveLength(0);
    setup();
    expect(sockets[0]!.url).toBe("ws://daemon/api/ws/terminal");
  });

  it("creates a session on open and resizes once it's created", () => {
    const { ws, sent, receive, callbacks, result } = setup({ getTerminalSize: () => ({ cols: 80, rows: 24 }) });
    act(() => ws().open());
    expect(result.current.connected).toBe(true);
    expect(sent()[0]).toMatchObject({ type: "create", target: "agent", cols: 80, rows: 24 });
    receive({ type: "created", session_id: "s1" });
    expect(callbacks.onCreated).toHaveBeenCalledOnce();
    expect(callbacks.onStatus).toHaveBeenCalledWith("running");
    expect(sent()[1]).toEqual({ type: "resize", cols: 80, rows: 24 });
    expect(result.current.getStartTime()).toEqual(expect.any(Number));
  });

  it("doesn't resize without a terminal size", () => {
    const { ws, sent, receive } = setup({ getTerminalSize: () => null });
    act(() => ws().open());
    receive({ type: "attached", session_id: "s1" });
    expect(sent()).toHaveLength(1);
  });

  it("passes binary output and errors on", () => {
    const { ws, callbacks, receive } = setup();
    const buf = new ArrayBuffer(2);
    act(() => ws().receive(buf));
    expect(callbacks.onData).toHaveBeenCalledWith(buf);
    receive({ type: "error", message: "bad", error_code: "invalid_input" });
    expect(callbacks.onError).toHaveBeenCalledWith("bad");
  });

  it("sends input as base64 of its UTF-8 bytes", () => {
    const { ws, sent, result } = setup();
    act(() => ws().open());
    result.current.sendInput("a—b");
    const input = sent().at(-1);
    expect(input.type).toBe("input");
    expect(new TextDecoder().decode(Uint8Array.from(atob(input.data), (c) => c.charCodeAt(0)))).toBe("a—b");
  });

  it("sends a resize", () => {
    const { ws, sent, result } = setup();
    act(() => ws().open());
    result.current.sendResize(100, 30);
    expect(sent().at(-1)).toEqual({ type: "resize", cols: 100, rows: 30 });
  });

  it("stops the session on kill and doesn't create another on reconnect", () => {
    vi.useFakeTimers();
    try {
      const { ws, sent, receive, result } = setup();
      act(() => ws().open());
      receive({ type: "created", session_id: "s1" });
      act(() => result.current.sendKill());
      expect(sent().at(-1)).toEqual({ type: "stop", session_id: "s1" });
      // A failed session after the kill isn't retried.
      receive({ type: "error", message: "gone", error_code: "no_session" });
      expect(sent().at(-1)).toEqual({ type: "stop", session_id: "s1" });
      act(() => ws().drop());
      act(() => vi.advanceTimersByTime(3000));
      act(() => ws().open());
      expect(ws().sent).toEqual([]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("sends a stop without a session id when there's none", () => {
    const { ws, sent, result } = setup();
    act(() => ws().open());
    act(() => result.current.sendKill());
    expect(sent().at(-1)).toEqual({ type: "stop" });
  });

  it("closes the session but keeps creating on reconnect", () => {
    const { ws, sent, receive, result } = setup();
    act(() => ws().open());
    receive({ type: "created", session_id: "s1" });
    act(() => result.current.sendClose());
    expect(sent().at(-1)).toEqual({ type: "close", session_id: "s1" });
    act(() => result.current.sendClose());
    expect(sent().at(-1)).toEqual({ type: "close" });
  });

  it("creates a fresh session after a kill", () => {
    const ch = channel();
    const { ws, sent, result } = setup({ channelId: ch, getTerminalSize: () => ({ cols: 10, rows: 5 }) });
    act(() => ws().open());
    act(() => result.current.sendKill());
    act(() => result.current.sendCreate());
    expect(sent().at(-1)).toEqual({ type: "create", channel_id: ch, target: "agent", cols: 10, rows: 5 });
  });

  it("creates nothing without a channel", () => {
    const { result } = setup({ channelId: null });
    result.current.sendCreate();
    expect(sockets).toHaveLength(0);
  });

  it.each<[string, Partial<Options>, object]>([
    ["an agent instance", { target: "agent", instanceId: "i1" }, { agent_id: "i1", leaf_id: "i1" }],
    ["a resumed claude session", { claudeSessionId: "cs" }, { session_id: "cs" }],
    ["a new session", { newSession: true }, { new_session: true }],
    ["an open mode", { openMode: "fork" }, { open_mode: "fork" }],
    ["a workspace root", { target: "shell" as TerminalTarget, rootIndex: 2 }, { root_index: 2, target: "shell" }],
    ["a command", { cmd: ["htop"] }, { cmd: ["htop"] }],
  ])("sends %s in a create", (_name, opts, want) => {
    const ch = channel();
    const { ws, sent, result } = setup({ channelId: ch, ...opts });
    act(() => ws().open());
    act(() => result.current.sendCreate());
    expect(sent().at(-1)).toEqual({ type: "create", channel_id: ch, target: "agent", ...want });
  });

  it.each<[string, Partial<Options>]>([
    ["an instance on a non-agent target", { target: "shell" as TerminalTarget, instanceId: "i1" }],
    ["an empty command", { cmd: [] }],
    ["the primary root", { rootIndex: 0 }],
  ])("leaves out %s", (_name, opts) => {
    const ch = channel();
    const { ws, sent, result } = setup({ channelId: ch, ...opts });
    act(() => ws().open());
    act(() => result.current.sendCreate());
    expect(Object.keys(sent().at(-1)).sort()).toEqual(["channel_id", "target", "type"]);
  });

  it("retries a failed attach once with a create carrying the pane's options", () => {
    const ch = channel();
    const { ws, sent, receive } = setup({
      channelId: ch,
      instanceId: "i1",
      claudeSessionId: "cs",
      newSession: true,
      openMode: "resume",
      rootIndex: 1,
      cmd: ["bash"],
      getTerminalSize: () => ({ cols: 7, rows: 3 }),
    });
    act(() => ws().open());
    const before = sent().length;
    receive({ type: "error", message: "failed", error_code: "session_failed" });
    expect(sent()).toHaveLength(before + 1);
    expect(sent().at(-1)).toEqual({
      type: "create",
      channel_id: ch,
      target: "agent",
      agent_id: "i1",
      leaf_id: "i1",
      session_id: "cs",
      new_session: true,
      open_mode: "resume",
      root_index: 1,
      cmd: ["bash"],
      cols: 7,
      rows: 3,
    });
    // Only once until a session is confirmed.
    receive({ type: "error", message: "failed", error_code: "session_failed" });
    expect(sent()).toHaveLength(before + 1);
    receive({ type: "attached", session_id: "s2" });
    receive({ type: "error", message: "failed", error_code: "session_failed" });
    expect(sent().at(-1)).toMatchObject({ type: "create" });
  });

  it("retries with a bare create when the pane has no options", () => {
    const ch = channel();
    const { ws, sent, receive } = setup({ channelId: ch, target: "shell" as TerminalTarget, instanceId: "i1", cmd: [] });
    act(() => ws().open());
    receive({ type: "error", message: "gone", error_code: "session_gone" });
    expect(sent().at(-1)).toEqual({ type: "create", channel_id: ch, target: "shell" });
  });

  it("doesn't retry once the channel is gone", () => {
    const { ws, rerender } = setup();
    act(() => ws().open());
    const socket = ws();
    rerender({ channelId: null });
    const before = socket.sent.length;
    act(() => socket.receive(JSON.stringify({ type: "error", message: "x", error_code: "no_session" })));
    expect(socket.sent).toHaveLength(before);
  });
});
