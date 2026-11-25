import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { browserAction, importCookies, listCookieSources, resetBrowserProfile, switchBrowserMode } from "./browser";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("browser", () => {
  it("posts a browser action", async () => {
    fetchMock.mockResolvedValue(json({ result: "ok" }));
    await expect(browserAction("c1", "navigate", { url: "u" })).resolves.toEqual({ result: "ok" });
    expect(sent(fetchMock)).toMatchObject({
      url: `${API}/api/browser/action`,
      method: "POST",
      body: { channel_id: "c1", action: "navigate", params: { url: "u" } },
    });
    expect(sent(fetchMock).headers.get("Content-Type")).toBe("application/json");
  });

  it("resets the profile", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await resetBrowserProfile("c1");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/browser/profile/reset`, method: "POST", body: { channel_id: "c1" } });
  });

  it("switches the mode", async () => {
    fetchMock.mockResolvedValue(json({ mode: "host" }));
    await expect(switchBrowserMode("c1", "host")).resolves.toEqual({ mode: "host" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/browser/mode`, method: "POST", body: { channel_id: "c1", mode: "host" } });
  });

  it("lists cookie sources", async () => {
    fetchMock.mockResolvedValue(json([{ id: "p", browser: "chrome", name: "Default" }]));
    await expect(listCookieSources("c 1")).resolves.toEqual([{ id: "p", browser: "chrome", name: "Default" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/browser/cookies/sources?channel_id=c%201`);
  });

  it("throws the server text or a fallback when listing cookie sources fails", async () => {
    fetchMock.mockResolvedValueOnce(fail(500, "x", "keychain denied"));
    await expect(listCookieSources("c1")).rejects.toThrow("keychain denied");
    fetchMock.mockResolvedValueOnce(fail(500, "x"));
    await expect(listCookieSources("c1")).rejects.toThrow("failed to list cookie sources");
  });

  it("imports cookies", async () => {
    fetchMock.mockResolvedValue(json({ imported: 4, domains: 2 }));
    await expect(importCookies("c1", "p", ["a.test"])).resolves.toEqual({ imported: 4, domains: 2 });
    expect(sent(fetchMock)).toMatchObject({
      url: `${API}/api/browser/cookies/import`,
      method: "POST",
      body: { channel_id: "c1", source: "p", domains: ["a.test"] },
    });
  });

  it("throws the server text or a fallback when the import fails", async () => {
    fetchMock.mockResolvedValueOnce(fail(400, "x", "bad source"));
    await expect(importCookies("c1", "p", [])).rejects.toThrow("bad source");
    fetchMock.mockResolvedValueOnce(fail(400, "x"));
    await expect(importCookies("c1", "p", [])).rejects.toThrow("cookie import failed");
  });
});
