import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { fetchComposerSwitches, turnComposerSwitch } from "./composerSwitches";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("composer switches", () => {
  it("lists a chat's switches", async () => {
    fetchMock.mockResolvedValue(json([{ plugin_id: "p", switch_id: "s", on: true }]));
    await expect(fetchComposerSwitches("c 1")).resolves.toEqual([{ plugin_id: "p", switch_id: "s", on: true }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/channels/c%201/composer-switches`);
  });

  it("returns none for a chat without switches", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    await expect(fetchComposerSwitches("c1")).resolves.toEqual([]);
  });

  it("throws the server text, or the status text when the body is empty or unreadable", async () => {
    fetchMock.mockResolvedValueOnce(fail(500, "boom", " broken \n"));
    await expect(fetchComposerSwitches("c1")).rejects.toThrow(/^broken$/);
    fetchMock.mockResolvedValueOnce(fail(500, "boom"));
    await expect(fetchComposerSwitches("c1")).rejects.toThrow("Failed to list the composer switches: boom");
    const unreadable = fail(500, "boom");
    vi.spyOn(unreadable, "clone").mockReturnValue({ ok: false, status: 500, statusText: "boom", text: () => Promise.reject(new Error("x")) } as unknown as Response);
    fetchMock.mockResolvedValueOnce(unreadable);
    await expect(fetchComposerSwitches("c1")).rejects.toThrow("Failed to list the composer switches: boom");
  });

  it("turns a switch and returns the plugin's state", async () => {
    fetchMock.mockResolvedValue(json({ on: false, status: "ok" }));
    await expect(turnComposerSwitch("c1", "p/1", "s 1", false)).resolves.toEqual({ on: false, status: "ok" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/channels/c1/composer-switches/p%2F1/s%201`, method: "PUT", body: { on: false } });
  });

  it("throws when turning fails", async () => {
    fetchMock.mockResolvedValue(fail(502, "Bad Gateway"));
    await expect(turnComposerSwitch("c1", "p", "s", true)).rejects.toThrow("Failed to turn the switch: Bad Gateway");
  });
});
