import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  fetchPluginAgent,
  fetchPluginAudit,
  fetchPluginList,
  fetchPlugins,
  inspectPlugin,
  installPlugin,
  PLUGINS_CHANGED,
  pluginXRequest,
  postPluginNotification,
  removePlugin,
  setPluginEnabled,
  setPluginSetting,
  stopPluginContainer,
  withPluginEnabled,
} from "./plugins";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" }, dispatchEvent: vi.fn() });
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

describe("fetchPluginList", () => {
  it("lists for a channel's project", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ plugins: [], project_trusted: false })));
    await expect(fetchPluginList("ch 1")).resolves.toEqual({ off: false, plugins: [], projectTrusted: false });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/plugins?channel_id=ch%201");
  });

  it("answers off when plugins are off", async () => {
    fetchMock.mockResolvedValue(new Response("off", { status: 501 }));
    await expect(fetchPluginList()).resolves.toEqual({ off: true, plugins: [] });
  });
});

describe("plugin management", () => {
  const candidate = { id: "echo", name: "Echo", version: "2", api: "1", image: "echo:2", digest: "sha256:ab", dev: false, capabilities: ["ui.panel"], new_capabilities: [] };

  it.each([
    {
      name: "inspect",
      call: () => inspectPlugin("echo:2", true),
      url: "http://localhost:8222/api/plugins/inspect",
      method: "POST",
      body: { image: "echo:2", dev: true },
    },
    {
      name: "install",
      call: () => installPlugin(candidate, ["ui.panel"]),
      url: "http://localhost:8222/api/plugins",
      method: "POST",
      body: { image: "echo:2", dev: false, digest: "sha256:ab", approve: ["ui.panel"] },
    },
    {
      name: "install without a digest",
      call: () => installPlugin({ image: "echo:2", dev: true }, []),
      url: "http://localhost:8222/api/plugins",
      method: "POST",
      body: { image: "echo:2", dev: true, digest: "", approve: [] },
    },
    { name: "remove", call: () => removePlugin("echo", false), url: "http://localhost:8222/api/plugins/echo", method: "DELETE" },
    { name: "remove and purge", call: () => removePlugin("echo", true), url: "http://localhost:8222/api/plugins/echo?purge=true", method: "DELETE" },
    { name: "stop", call: () => stopPluginContainer("echo", "ch/1"), url: "http://localhost:8222/api/plugins/echo/channels/ch%2F1/stop", method: "POST" },
    {
      name: "a notification",
      call: () => postPluginNotification("echo", "ch", "Hi", "there"),
      url: "http://localhost:8222/api/plugins/echo/notifications",
      method: "POST",
      body: { channel_id: "ch", title: "Hi", body: "there" },
    },
  ])("sends $name", async ({ call, url, method, body }) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(candidate)));
    await call();
    const [gotURL, gotInit] = fetchMock.mock.calls[0]!;
    expect(gotURL).toBe(url);
    expect(gotInit.method).toBe(method);
    if (body) expect(JSON.parse(gotInit.body)).toEqual(body);
  });

  it.each([
    ["inspect", () => inspectPlugin("x"), "Failed to inspect the plugin: Bad Request"],
    ["install", () => installPlugin(candidate, []), "Failed to install the plugin: Bad Request"],
    ["remove", () => removePlugin("echo", false), "Failed to remove the plugin: Bad Request"],
    ["stop", () => stopPluginContainer("echo", "ch"), "Failed to stop the plugin: Bad Request"],
    ["notify", () => postPluginNotification("echo", "ch", "Hi", ""), "Failed to keep the notification: Bad Request"],
  ])("explains a failed %s", async (_name, call, fallback) => {
    fetchMock.mockResolvedValueOnce(new Response("pull failed\n", { status: 400 }));
    await expect(call()).rejects.toThrow(/^pull failed$/);
    fetchMock.mockResolvedValueOnce(new Response("", { status: 400, statusText: "Bad Request" }));
    await expect(call()).rejects.toThrow(fallback);
  });
});

describe("fetchPluginAudit", () => {
  it("lists the gate's records", async () => {
    const entry = { ts: "2026-10-10T09:00:00Z", kind: "exec", target: "/bin/sh", rule_id: "default", decision: "deny" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ entries: [entry] })));
    await expect(fetchPluginAudit("echo", "ch/1", 50)).resolves.toEqual([entry]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/plugins/echo/channels/ch%2F1/audit?limit=50");
  });

  it("explains a failure", async () => {
    fetchMock.mockResolvedValueOnce(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(fetchPluginAudit("echo", "ch")).rejects.toThrow("Failed to read the plugin's gate audit: Internal Server Error");
  });
});

describe("withPluginEnabled", () => {
  it.each([
    { name: "an empty file", raw: "", want: { plugins: { echo: { enabled: true } } } },
    { name: "no plugins", raw: '{"model": "x"}', want: { model: "x", plugins: { echo: { enabled: true } } } },
    { name: "another plugin", raw: '{"plugins": {"quiz": {"enabled": false}}}', want: { plugins: { quiz: { enabled: false }, echo: { enabled: true } } } },
    { name: "the plugin turned off", raw: '{"plugins": {"echo": {"enabled": false}}}', want: { plugins: { echo: { enabled: true } } } },
  ])("sets it in $name", ({ raw, want }) => {
    expect(JSON.parse(withPluginEnabled(raw, "echo", true))).toEqual(want);
  });

  it("keeps comments, trailing commas and the file's indentation", () => {
    const raw = '{\n    // the model\n    "model": "x",\n    "plugins": {\n        "echo": {"enabled": true},\n    },\n}\n';
    const got = withPluginEnabled(raw, "echo", false);
    expect(got).toContain("    // the model\n");
    expect(got).toContain('"enabled": false');
    expect(withPluginEnabled('{\n    "model": "x"\n}', "echo", true)).toContain('\n    "plugins": {\n        "echo": {\n            "enabled": true');
  });

  it("keeps tabs and CRLF line ends", () => {
    const got = withPluginEnabled('{\r\n\t"model": "x"\r\n}', "echo", true);
    expect(got).toContain('\r\n\t"plugins": {\r\n\t\t"echo"');
  });

  it("refuses a file that doesn't parse", () => {
    expect(() => withPluginEnabled("{", "echo", true)).toThrow("the config file doesn't parse; fix it first");
  });
});

describe("setPluginEnabled", () => {
  it.each([
    { name: "a project", channelId: "ch-1", url: "http://localhost:8222/api/config/project?channel_id=ch-1" },
    { name: "the global config", channelId: undefined, url: "http://localhost:8222/api/config" },
  ])("writes $name and tells plugin views", async ({ channelId, url }) => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ raw: "// mine\n{}\n", content: {} })));
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await setPluginEnabled("echo", true, channelId);
    expect(fetchMock.mock.calls[0]![0]).toBe(url);
    const [putURL, putInit] = fetchMock.mock.calls[1]!;
    expect(putURL).toBe(url);
    expect(putInit.method).toBe("PUT");
    const { content } = JSON.parse(putInit.body);
    expect(content).toContain("// mine");
    expect(content).toContain('"enabled": true');
    expect((window.dispatchEvent as ReturnType<typeof vi.fn>).mock.calls[0]![0].type).toBe(PLUGINS_CHANGED);
  });

  it("writes an empty file", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ content: {} })));
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await setPluginEnabled("echo", false);
    expect(JSON.parse(JSON.parse(fetchMock.mock.calls[1]![1].body).content)).toEqual({ plugins: { echo: { enabled: false } } });
  });
});

describe("setPluginSetting", () => {
  it("sets a setting, then removes it", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ raw: '{"plugins": {"echo": {"enabled": true}}}', content: {} })));
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await setPluginSetting("echo", "count", 7, "ch-1");
    const set = JSON.parse(JSON.parse(fetchMock.mock.calls[1]![1].body).content);
    expect(set).toEqual({ plugins: { echo: { enabled: true, settings: { count: 7 } } } });

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ raw: JSON.stringify(set), content: {} })));
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await setPluginSetting("echo", "count", undefined);
    expect(fetchMock.mock.calls[2]![0]).toBe("http://localhost:8222/api/config");
    expect(JSON.parse(JSON.parse(fetchMock.mock.calls[3]![1].body).content)).toEqual({ plugins: { echo: { enabled: true, settings: {} } } });
  });
});

describe("fetchPluginAgent", () => {
  it("looks up the plugin's agent in a channel", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ thread_id: "plugin:echo-abc123", running: true })));
    await expect(fetchPluginAgent("cog test", "ch/1")).resolves.toEqual({ thread_id: "plugin:echo-abc123", running: true });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/plugins/cog%20test/agent?channel_id=ch%2F1");
  });

  it("explains a failure", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404, statusText: "Not Found" }));
    await expect(fetchPluginAgent("echo", "ch")).rejects.toThrow("Failed to look up the plugin's agent: Not Found");
  });
});
