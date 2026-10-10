import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchPlugins, pluginXRequest } from "./plugins";

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

describe("fetchPlugins", () => {
  it("lists the plugins", async () => {
    const plugin = { id: "echo", version: "1", capabilities: ["ui.panel"], status: "installed", panels: [{ id: "main", label: "Echo", entry: "ui/main/index.html" }] };
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ plugins: [plugin] })));
    await expect(fetchPlugins()).resolves.toEqual([plugin]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/plugins");
  });

  it.each([
    ["plugins off", new Response("off", { status: 501 })],
    ["none installed", new Response(JSON.stringify({ plugins: null }))],
  ])("answers none when %s", async (_name, res) => {
    fetchMock.mockResolvedValue(res);
    await expect(fetchPlugins()).resolves.toEqual([]);
  });

  it("throws on other errors", async () => {
    fetchMock.mockResolvedValue(new Response("down", { status: 500, statusText: "Internal Server Error" }));
    await expect(fetchPlugins()).rejects.toThrow("Failed to list plugins: Internal Server Error");
  });
});

describe("pluginXRequest", () => {
  it.each([
    {
      name: "a GET with a query",
      args: ["GET", "/quizzes?limit=5&x=a?b", undefined] as [string, string, unknown],
      url: "http://localhost:8222/api/plugins/cog%20test/x/quizzes?limit=5&x=a%3Fb&channel_id=ch-1",
      init: { method: "GET" },
    },
    {
      name: "a JSON body",
      args: ["post", "attempts", { question_id: 1 }] as [string, string, unknown],
      url: "http://localhost:8222/api/plugins/cog%20test/x/attempts?channel_id=ch-1",
      init: { method: "POST", body: '{"question_id":1}', headers: { "Content-Type": "application/json" } },
    },
    {
      name: "a text body",
      args: ["PUT", "notes", "hi"] as [string, string, unknown],
      url: "http://localhost:8222/api/plugins/cog%20test/x/notes?channel_id=ch-1",
      init: { method: "PUT", body: "hi", headers: { "Content-Type": "text/plain" } },
    },
  ])("sends $name", async ({ args, url, init }) => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(pluginXRequest("cog test", "ch-1", ...args)).resolves.toEqual({ status: 204, body: "" });
    const [gotURL, gotInit] = fetchMock.mock.calls[0]!;
    expect(gotURL).toBe(url);
    expect(gotInit.method).toBe(init.method);
    expect(gotInit.body).toBe(init.body);
    if (init.headers) expect(new Headers(gotInit.headers).get("Content-Type")).toBe(init.headers["Content-Type"]);
  });

  it.each([
    ["JSON", "application/json; charset=utf-8", '{"a":1}', { a: 1 }],
    ["bad JSON", "application/json", "{", "{"],
    ["text", "text/plain", "hello", "hello"],
    ["no type", null, "raw", "raw"],
  ])("decodes a %s answer", async (_name, type, text, body) => {
    const headers: Record<string, string> = type ? { "Content-Type": type } : {};
    fetchMock.mockResolvedValue(new Response(text, { status: 409, headers }));
    await expect(pluginXRequest("p", "ch", "GET", "x")).resolves.toEqual({ status: 409, body });
  });
});
