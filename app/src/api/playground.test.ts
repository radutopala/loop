import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { fetchPlayground, fetchPlaygroundItems, fetchPlaygroundShareStatus, fetchPlaygroundShares, fetchPlaygroundState, patchPlaygroundState, sharePlayground, unsharePlayground } from "./playground";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchPlayground", () => {
  it("fetches a global playground by name", async () => {
    fetchMock.mockResolvedValue(json({ name: "p", html: "<p>" }));
    await expect(fetchPlayground("p 1")).resolves.toEqual({ name: "p", html: "<p>" });
    expect(sent(fetchMock).url).toBe(`${API}/api/playground?name=p+1`);
  });

  it("scopes a project playground to the channel", async () => {
    fetchMock.mockResolvedValue(json({ name: "p", html: "" }));
    await fetchPlayground("p", "project", "c1");
    expect(sent(fetchMock).url).toBe(`${API}/api/playground?name=p&scope=project&channel_id=c1`);
  });

  it("ignores the project scope without a channel", async () => {
    fetchMock.mockResolvedValue(json({ name: "p", html: "" }));
    await fetchPlayground("p", "project");
    expect(sent(fetchMock).url).toBe(`${API}/api/playground?name=p`);
  });

  it("returns null when it doesn't exist", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    await expect(fetchPlayground("p")).resolves.toBeNull();
  });

  it("throws on another error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchPlayground("p")).rejects.toThrow("Failed to fetch playground: boom");
  });
});

describe("fetchPlaygroundItems", () => {
  it("lists the items, with and without a channel", async () => {
    fetchMock.mockImplementation(async () => json({ items: [{ name: "a", scope: "global" }] }));
    await expect(fetchPlaygroundItems("c 1")).resolves.toEqual([{ name: "a", scope: "global" }]);
    await fetchPlaygroundItems();
    expect(sent(fetchMock, 0).url).toBe(`${API}/api/playground/items?channel_id=c%201`);
    expect(sent(fetchMock, 1).url).toBe(`${API}/api/playground/items`);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchPlaygroundItems()).rejects.toThrow("Failed to fetch playground items: boom");
  });
});

describe("sharing", () => {
  it("shares a project playground", async () => {
    fetchMock.mockResolvedValue(json({ url: "u", token: "t" }));
    await expect(sharePlayground("p", "project", "c1")).resolves.toEqual({ url: "u", token: "t" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/playground/share?name=p&scope=project&channel_id=c1`, method: "PUT" });
  });

  it("throws the server text when sharing fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom", "no tunnel"));
    await expect(sharePlayground("p")).rejects.toThrow("Failed to share playground: no tunnel");
  });

  it("unshares", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await unsharePlayground("p", "global", "c1");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/playground/share?name=p`, method: "DELETE" });
  });

  it("throws when unsharing fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(unsharePlayground("p")).rejects.toThrow("Failed to unshare playground: boom");
  });

  it("lists the shares", async () => {
    fetchMock.mockResolvedValue(json({ shares: [{ name: "p", scope: "global", channel_id: "", url: "u" }] }));
    await expect(fetchPlaygroundShares()).resolves.toEqual([{ name: "p", scope: "global", channel_id: "", url: "u" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/playground/share`);
  });

  it("throws when listing the shares fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchPlaygroundShares()).rejects.toThrow("Failed to fetch playground shares: boom");
  });

  it("fetches one share's status", async () => {
    fetchMock.mockResolvedValue(json({ shared: true, url: "u" }));
    await expect(fetchPlaygroundShareStatus("p", "project", "c1")).resolves.toEqual({ shared: true, url: "u" });
    expect(sent(fetchMock).url).toBe(`${API}/api/playground/share?name=p&scope=project&channel_id=c1`);
  });

  it("throws when the share status fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchPlaygroundShareStatus("p")).rejects.toThrow("Failed to fetch playground share status: boom");
  });
});

describe("state", () => {
  it("fetches the state", async () => {
    fetchMock.mockResolvedValue(json({ votes: 2 }));
    await expect(fetchPlaygroundState("p", "project", "c1")).resolves.toEqual({ votes: 2 });
    expect(sent(fetchMock).url).toBe(`${API}/api/playground/state?name=p&scope=project&channel_id=c1`);
  });

  it("throws the trimmed server text when fetching fails", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found", "no such playground\n"));
    await expect(fetchPlaygroundState("p")).rejects.toThrow(/^Failed to fetch playground state: no such playground$/);
  });

  it("patches the state", async () => {
    fetchMock.mockResolvedValue(json({ votes: 3 }));
    await expect(patchPlaygroundState("p", { votes: 3 })).resolves.toEqual({ votes: 3 });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/playground/state?name=p`, method: "PATCH", body: { votes: 3 } });
    expect(sent(fetchMock).headers.get("Content-Type")).toBe("application/json");
  });

  it("throws the trimmed server text when patching fails", async () => {
    fetchMock.mockResolvedValue(fail(413, "Too Large", "state too big\n"));
    await expect(patchPlaygroundState("p", {}, "project", "c1")).rejects.toThrow(/^Failed to update playground state: state too big$/);
  });
});
