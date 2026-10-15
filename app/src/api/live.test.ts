import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { warmLive } from "./live";

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

describe("warmLive", () => {
  it.each([true, false])("reports started=%s", async (started) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ started })));
    await expect(warmLive("c 1")).resolves.toBe(started);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c%201/live");
    expect(fetchMock.mock.calls[0]![1].method).toBe("POST");
  });

  it("throws on an error", async () => {
    fetchMock.mockResolvedValue(new Response("boom", { status: 500, statusText: "Internal Server Error" }));
    await expect(warmLive("c1")).rejects.toThrow("Internal Server Error");
  });
});
