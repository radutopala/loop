// @vitest-environment jsdom
import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { claimTerminalSessions } from "../api/terminal";

vi.mock("../api/terminal", () => ({ claimTerminalSessions: vi.fn() }));

const KEY = "loop.terminalSessions";

function fakeStorage(initial?: string) {
  const data = new Map<string, string>();
  if (initial !== undefined) data.set(KEY, initial);
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

async function load(storage: unknown) {
  vi.resetModules();
  vi.stubGlobal("sessionStorage", storage);
  return import("./useSessionPersistence");
}

describe("stored terminal sessions", () => {
  beforeEach(() => vi.unstubAllGlobals());
  afterEach(() => vi.unstubAllGlobals());

  it("reads the sessions a reload left behind, and saves what it takes", async () => {
    const storage = fakeStorage(JSON.stringify({ ids: [["ch-1:agent:docker-shell-0", "sess-1"]], starts: [["ch-1:agent:docker-shell-0", 5]] }));
    const { heldSessionIds, takeStoredSession } = await load(storage);

    expect(heldSessionIds()).toEqual(["sess-1"]);
    expect(takeStoredSession("ch-1", "agent", "docker-shell-0")).toBe("sess-1");
    expect(heldSessionIds()).toEqual([]);
    expect(JSON.parse(storage.data.get(KEY)!)).toEqual({ ids: [], starts: [] });
    expect(takeStoredSession("ch-1", "agent", "docker-shell-0")).toBeNull();
  });

  it.each([
    ["no storage", undefined],
    ["a corrupt entry", fakeStorage("{not json")],
    ["an entry missing its maps", fakeStorage("{}")],
    [
      "storage that throws",
      {
        getItem: () => {
          throw new Error("blocked");
        },
        setItem: () => {
          throw new Error("blocked");
        },
      },
    ],
  ])("starts empty with %s", async (_name, storage) => {
    const { takeStoredSession } = await load(storage);
    expect(takeStoredSession("ch-1", "host")).toBeNull();
  });
});

function fakeWs() {
  const sent: unknown[] = [];
  const ws = { send: (s: string) => void sent.push(JSON.parse(s)) } as unknown as WebSocket;
  return { ws, sent };
}

describe("useSessionPersistence", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
    vi.mocked(claimTerminalSessions).mockReset();
    vi.mocked(claimTerminalSessions).mockResolvedValue();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("creates a session on open, then attaches to the one the server confirms", async () => {
    const storage = fakeStorage();
    const { useSessionPersistence, heldSessionIds } = await load(storage);
    const sizeRef = { current: () => ({ cols: 80, rows: 24 }) };
    const { result } = renderHook(() => useSessionPersistence("ch", "agent", sizeRef, "leaf-1", "claude-1", true, ["bash"], "fork", 2));
    expect(claimTerminalSessions).not.toHaveBeenCalled();

    const first = fakeWs();
    result.current.handleOpen(first.ws);
    expect(first.sent).toEqual([
      {
        type: "create",
        channel_id: "ch",
        target: "agent",
        agent_id: "leaf-1",
        leaf_id: "leaf-1",
        session_id: "claude-1",
        new_session: true,
        open_mode: "fork",
        root_index: 2,
        cmd: ["bash"],
        cols: 80,
        rows: 24,
      },
    ]);
    expect(result.current.getStartTime()).toBeUndefined();

    vi.spyOn(Date, "now").mockReturnValue(1000);
    result.current.setSessionId("sess-1");
    expect(result.current.sessionIdRef.current).toBe("sess-1");
    expect(result.current.getStartTime()).toBe(1000);
    expect(heldSessionIds()).toEqual(["sess-1"]);
    expect(JSON.parse(storage.data.get(KEY)!)).toEqual({ ids: [["ch:agent:leaf-1", "sess-1"]], starts: [["ch:agent:leaf-1", 1000]] });

    // A confirm of the same session keeps its first start time.
    vi.spyOn(Date, "now").mockReturnValue(2000);
    result.current.setSessionId("sess-1");
    expect(result.current.getStartTime()).toBe(1000);

    const second = fakeWs();
    result.current.handleOpen(second.ws);
    expect(second.sent).toEqual([{ type: "attach", session_id: "sess-1", target: "agent", channel_id: "ch", agent_id: "leaf-1" }]);

    result.current.setSessionId(null);
    expect(result.current.getStartTime()).toBeUndefined();
    expect(heldSessionIds()).toEqual([]);
  });

  it("sends a plain create for a host pane, and nothing once killed", async () => {
    const { useSessionPersistence } = await load(fakeStorage());
    const { result } = renderHook(() => useSessionPersistence("ch", "host", undefined, "pane-1", undefined, false, []));
    const first = fakeWs();
    result.current.handleOpen(first.ws);
    expect(first.sent).toEqual([{ type: "create", channel_id: "ch", target: "host" }]);

    result.current.markKilled();
    expect(result.current.killedRef.current).toBe(true);
    const second = fakeWs();
    result.current.handleOpen(second.ws);
    expect(second.sent).toEqual([]);
  });

  it("creates without a size when the terminal has none yet", async () => {
    const { useSessionPersistence } = await load(fakeStorage());
    const { result } = renderHook(() => useSessionPersistence("ch", "agent", { current: () => null }));
    const { ws, sent } = fakeWs();
    result.current.handleOpen(ws);
    expect(sent).toEqual([{ type: "create", channel_id: "ch", target: "agent" }]);
  });

  it("reattaches to the session a reload left behind and claims it", async () => {
    const { useSessionPersistence } = await load(fakeStorage(JSON.stringify({ ids: [["ch:host", "sess-9"]], starts: [["ch:host", 5]] })));
    const { result } = renderHook(() => useSessionPersistence("ch", "host"));
    expect(claimTerminalSessions).toHaveBeenCalledWith(["sess-9"]);
    expect(result.current.getStartTime()).toBe(5);
    const { ws, sent } = fakeWs();
    result.current.handleOpen(ws);
    expect(sent).toEqual([{ type: "attach", session_id: "sess-9", target: "host", channel_id: "ch" }]);
  });

  it("without a channel tracks the session only in memory and never creates", async () => {
    const storage = fakeStorage();
    const { useSessionPersistence, heldSessionIds } = await load(storage);
    const { result } = renderHook(() => useSessionPersistence(null, "chat"));
    expect(result.current.sessionIdRef.current).toBeNull();
    expect(result.current.getStartTime()).toBeUndefined();

    const first = fakeWs();
    result.current.handleOpen(first.ws);
    expect(first.sent).toEqual([]);

    result.current.setSessionId("sess-x");
    expect(heldSessionIds()).toEqual([]);
    expect(storage.data.has(KEY)).toBe(false);
    const second = fakeWs();
    result.current.handleOpen(second.ws);
    expect(second.sent).toEqual([{ type: "attach", session_id: "sess-x", target: "chat" }]);
  });
});
