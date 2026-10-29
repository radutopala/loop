import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { deleteWalkthrough, fetchWalkthrough, listWalkthroughs } from "./walkthroughs";

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

const summary = { id: 12, channel_id: "c 1", title: "Tour", created_at: "2026-10-29T10:00:00Z", step_count: 2 };

describe("listWalkthroughs", () => {
  it("returns the channel's walkthroughs", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify([summary])));
    await expect(listWalkthroughs("c 1")).resolves.toEqual([summary]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c%201/walkthroughs");
  });

  it("returns none for a null body", async () => {
    fetchMock.mockResolvedValue(new Response("null"));
    await expect(listWalkthroughs("c1")).resolves.toEqual([]);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(listWalkthroughs("c1")).rejects.toThrow("Failed to list walkthroughs: boom");
  });
});

describe("fetchWalkthrough", () => {
  it("returns the walkthrough with its steps", async () => {
    const full = { ...summary, steps: [{ path: "a.go", start_line: 1, end_line: 2, anchor: "x\ny", body: "**hi**" }] };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(full)));
    await expect(fetchWalkthrough("c 1", 12)).resolves.toEqual(full);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c%201/walkthroughs/12");
  });

  it("returns null for another channel's", async () => {
    fetchMock.mockResolvedValue(new Response("not found", { status: 404 }));
    await expect(fetchWalkthrough("c1", 12)).resolves.toBeNull();
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchWalkthrough("c1", 12)).rejects.toThrow("Failed to load walkthrough 12: boom");
  });
});

describe("deleteWalkthrough", () => {
  it.each([204, 404])("counts a %s as deleted", async (status) => {
    fetchMock.mockResolvedValue(new Response(null, { status }));
    await expect(deleteWalkthrough("c 1", 12)).resolves.toBeUndefined();
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c%201/walkthroughs/12");
    expect(fetchMock.mock.calls[0]![1].method).toBe("DELETE");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(deleteWalkthrough("c1", 12)).rejects.toThrow("Failed to delete walkthrough 12: boom");
  });
});
