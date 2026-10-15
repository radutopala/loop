import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createSideChat, deleteSideChat, listSideChats } from "./sideChats";

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

describe("createSideChat", () => {
  it("starts a side chat in the mode asked for", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ channel_id: "side-1" }), { status: 201 }));
    await expect(createSideChat("c 1", "fresh")).resolves.toBe("side-1");
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c%201/side-chats");
    expect(fetchMock.mock.calls[0]![1].method).toBe("POST");
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ mode: "fresh" });
  });

  it.each([
    { name: "a JSON error", body: JSON.stringify({ error: "the channel has no session to fork" }), want: "the channel has no session to fork" },
    { name: "a plain-text error", body: "channel not found\n", want: "channel not found" },
    { name: "no body", body: "", want: "Conflict" },
  ])("throws $name", async ({ body, want }) => {
    fetchMock.mockResolvedValue(new Response(body, { status: 409, statusText: "Conflict" }));
    await expect(createSideChat("c1", "fork")).rejects.toThrow(want);
  });
});

describe("listSideChats", () => {
  it("returns the channel's side chats", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify([{ channel_id: "side-1", name: "main (fork)", session_id: "s1", kind: "side", created_at: "2026-01-02T03:04:05Z" }])));
    await expect(listSideChats("c1")).resolves.toEqual([{ channel_id: "side-1", name: "main (fork)", session_id: "s1", kind: "side", created_at: "2026-01-02T03:04:05Z" }]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c1/side-chats");
  });

  it("returns none for a null body", async () => {
    fetchMock.mockResolvedValue(new Response("null"));
    await expect(listSideChats("c1")).resolves.toEqual([]);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(listSideChats("c1")).rejects.toThrow("Failed to list side chats: boom");
  });
});

describe("deleteSideChat", () => {
  it.each([204, 404])("counts a %s as deleted", async (status) => {
    fetchMock.mockResolvedValue(new Response(null, { status }));
    await expect(deleteSideChat("side 1")).resolves.toBeUndefined();
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/side-chats/side%201");
    expect(fetchMock.mock.calls[0]![1].method).toBe("DELETE");
  });

  it("throws on another error", async () => {
    fetchMock.mockResolvedValue(new Response("boom", { status: 500 }));
    await expect(deleteSideChat("side-1")).rejects.toThrow("Failed to delete the side chat: boom");
  });
});
