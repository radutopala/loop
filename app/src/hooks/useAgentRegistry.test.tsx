import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiFetch } from "../api/api";
import { type FakeWebSocket, fakeWebSocketClass } from "../test/hooks-a-ws";
import { logErr } from "../utils/log";
import { useAgentRegistry } from "./useAgentRegistry";

vi.mock("../api/api", () => ({ apiFetch: vi.fn(), getApiUrl: () => "http://daemon" }));
vi.mock("../api/loopApi", () => ({
  getWsUrl: () => "ws://daemon",
  wsProtocols: () => [],
  clearContentCaps: vi.fn(),
  refreshApiToken: vi.fn(() => Promise.resolve("")),
  retryFailedContentCaps: vi.fn(),
}));
vi.mock("../utils/log", () => {
  const log = vi.fn();
  return { logErr: vi.fn(() => log) };
});

const fetchMock = vi.mocked(apiFetch);

function respond(ok: boolean, body: unknown): Response {
  return { ok, json: () => Promise.resolve(body) } as Response;
}

describe("useAgentRegistry", () => {
  let sockets: FakeWebSocket[];

  beforeEach(() => {
    fetchMock.mockReset();
    const fake = fakeWebSocketClass();
    sockets = fake.sockets;
    vi.stubGlobal("WebSocket", fake.WebSocket);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function emit(type: string, data: unknown) {
    act(() => sockets[0]!.receive(JSON.stringify({ type, data })));
  }

  it("seeds from the agents already registered, filling in defaults", async () => {
    fetchMock.mockResolvedValue(
      respond(true, [
        { agent_id: "a1", channel_id: "c/1", name: "Alpha", status: "busy", work_summary: "fixing" },
        { agent_id: "a2", channel_id: "c/1", name: "", status: "", work_summary: "" },
      ]),
    );
    const { result } = renderHook(() => useAgentRegistry("c/1"));
    await waitFor(() => expect(result.current.agents.size).toBe(2));
    expect(fetchMock).toHaveBeenCalledWith("http://daemon/api/agents?channel_id=c%2F1");
    expect(result.current.agents.get("a1")).toEqual({ agent_id: "a1", channel_id: "c/1", name: "Alpha", status: "busy", work_summary: "fixing" });
    expect(result.current.agents.get("a2")).toEqual({ agent_id: "a2", channel_id: "c/1", name: "a2", status: "idle", work_summary: "" });
  });

  it("seeds nothing from a failed response or a body that isn't a list", async () => {
    fetchMock.mockResolvedValueOnce(respond(false, null));
    const { result, rerender } = renderHook(({ ch }) => useAgentRegistry(ch), { initialProps: { ch: "c1" } });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    fetchMock.mockResolvedValueOnce(respond(true, { not: "a list" }));
    rerender({ ch: "c2" });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    await act(async () => {});
    expect(result.current.agents.size).toBe(0);
  });

  it("logs a failed fetch", async () => {
    fetchMock.mockRejectedValue(new Error("down"));
    renderHook(() => useAgentRegistry("c1"));
    const log = vi.mocked(logErr).mock.results.at(-1)!.value as ReturnType<typeof vi.fn>;
    await waitFor(() => expect(log).toHaveBeenCalledWith(new Error("down")));
    expect(logErr).toHaveBeenCalledWith("fetching agent registry");
  });

  it("drops a seed that arrives after the channel changed", async () => {
    let resolve!: (r: Response) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (resolve = r))).mockResolvedValueOnce(respond(true, []));
    const { result, unmount } = renderHook(() => useAgentRegistry("c1"));
    unmount();
    await act(async () => resolve(respond(true, [{ agent_id: "late", channel_id: "c1" }])));
    expect(result.current.agents.size).toBe(0);
  });

  it("fetches nothing and doesn't connect without a channel", () => {
    const { result } = renderHook(() => useAgentRegistry(null));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(sockets).toHaveLength(0);
    expect(result.current.agents.size).toBe(0);
  });

  it("follows registrations, metadata and unregistrations", async () => {
    fetchMock.mockResolvedValue(respond(true, []));
    const { result } = renderHook(() => useAgentRegistry("c1"));
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await act(async () => {});

    emit("agent_instance.registered", { agent_id: "a1", channel_id: "c1" });
    expect(result.current.agents.get("a1")).toEqual({ agent_id: "a1", channel_id: "c1", name: "a1", status: "idle", work_summary: "" });

    emit("agent_instance.registered", { agent_id: "a2", channel_id: "c1", name: "Beta", status: "busy", work_summary: "w" });
    expect(result.current.agents.get("a2")?.name).toBe("Beta");

    emit("agent_instance.metadata", { agent_id: "a1", channel_id: "c1", name: "Alpha", status: "busy", work_summary: "tests" });
    expect(result.current.agents.get("a1")).toEqual({ agent_id: "a1", channel_id: "c1", name: "Alpha", status: "busy", work_summary: "tests" });

    // Fields left out keep what the agent had.
    emit("agent_instance.metadata", { agent_id: "a1", channel_id: "c1" });
    expect(result.current.agents.get("a1")).toEqual({ agent_id: "a1", channel_id: "c1", name: "Alpha", status: "busy", work_summary: "tests" });

    emit("agent_instance.unregistered", { agent_id: "a2", channel_id: "c1" });
    expect(result.current.agents.has("a2")).toBe(false);
    expect(result.current.agents.size).toBe(1);
  });

  it("ignores metadata for an unknown agent, events without an agent and other event types", async () => {
    fetchMock.mockResolvedValue(respond(true, []));
    const { result } = renderHook(() => useAgentRegistry("c1"));
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await act(async () => {});
    const before = result.current.agents;
    emit("agent_instance.metadata", { agent_id: "ghost", channel_id: "c1", name: "x" });
    emit("agent_instance.registered", { channel_id: "c1" });
    emit("agent_instance.registered", null);
    emit("message.created", { agent_id: "a1", channel_id: "c1" });
    expect(result.current.agents).toBe(before);
  });
});
