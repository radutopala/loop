import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { cancelMonitor, fetchMonitors } from "./monitors";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchMonitors", () => {
  it("returns the channel's monitors", async () => {
    const mon = { id: "mon-a", channel_id: "c 1", description: "pr merge", command: "sleep 1", started_at: "2026-01-02T03:04:05Z", timeout_sec: 60, interrupt: false };
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ monitors: [mon] })));
    await expect(fetchMonitors("c 1")).resolves.toEqual([mon]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c%201/monitors");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 501, statusText: "boom" }));
    await expect(fetchMonitors("c1")).rejects.toThrow("Failed to fetch monitors: boom");
  });
});

describe("cancelMonitor", () => {
  it("deletes the monitor", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(cancelMonitor("c1", "mon-a")).resolves.toBeUndefined();
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c1/monitors/mon-a");
    expect(fetchMock.mock.calls[0]![1].method).toBe("DELETE");
  });

  it("throws the server's message", async () => {
    fetchMock.mockResolvedValue(new Response("monitor not found\n", { status: 404 }));
    await expect(cancelMonitor("c1", "mon-a")).rejects.toThrow("monitor not found");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404, statusText: "Not Found" }));
    await expect(cancelMonitor("c1", "mon-a")).rejects.toThrow("Not Found");
  });
});
