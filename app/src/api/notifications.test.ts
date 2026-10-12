import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearNotifications, deleteNotification, fetchNotifications, markNotificationsRead } from "./notifications";

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

const url = "http://localhost:8222/api/notifications";

describe("fetchNotifications", () => {
  it("lists them", async () => {
    const list = { notifications: [{ id: 1, kind: "run", read: false, created_at: "" }], unread: 1 };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(list)));
    await expect(fetchNotifications()).resolves.toEqual(list);
    expect(fetchMock.mock.calls[0]![0]).toBe(url);
  });

  it("lists none while they're off", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 501 }));
    await expect(fetchNotifications()).resolves.toEqual({ notifications: [], unread: 0 });
  });

  it("throws with the server's message", async () => {
    fetchMock.mockResolvedValue(new Response("boom\n", { status: 500, statusText: "Internal Server Error" }));
    await expect(fetchNotifications()).rejects.toThrow("Failed to list notifications: boom");
  });
});

describe("markNotificationsRead", () => {
  it("posts up_to", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await markNotificationsRead(7);
    const [u, init] = fetchMock.mock.calls[0]!;
    expect(u).toBe(`${url}/read`);
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ up_to: 7 });
  });

  it("throws with the status without a message", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 400, statusText: "Bad Request" }));
    await expect(markNotificationsRead(7)).rejects.toThrow("Failed to mark notifications read: Bad Request");
  });
});

describe("deleteNotification", () => {
  it("deletes it, and one already gone is fine", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 })).mockResolvedValueOnce(new Response("", { status: 404 }));
    await deleteNotification(3);
    await deleteNotification(3);
    expect(fetchMock.mock.calls[0]![0]).toBe(`${url}/3`);
    expect(fetchMock.mock.calls[0]![1].method).toBe("DELETE");
  });

  it("throws on another error", async () => {
    fetchMock.mockResolvedValue(new Response("boom", { status: 500 }));
    await expect(deleteNotification(3)).rejects.toThrow("Failed to dismiss the notification: boom");
  });
});

describe("clearNotifications", () => {
  it("deletes them all", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await clearNotifications();
    expect(fetchMock.mock.calls[0]![0]).toBe(url);
    expect(fetchMock.mock.calls[0]![1].method).toBe("DELETE");
  });

  it("throws on an error", async () => {
    fetchMock.mockResolvedValue(new Response("boom", { status: 500 }));
    await expect(clearNotifications()).rejects.toThrow("Failed to clear notifications: boom");
  });
});
