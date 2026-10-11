import { describe, expect, it, vi } from "vitest";
import { darkColors, fonts, lightColors } from "../theme";
import { BridgeError, type BridgeHost, callBridge, connectBridge, MAX_STATE_BYTES, pluginTheme } from "./bridge";

function host(over: Partial<BridgeHost> = {}): BridgeHost {
  return {
    pluginId: "cogtest",
    panel: "quiz",
    channelId: "ch1",
    channelName: "proj",
    dirPath: "/src/proj",
    capabilities: ["ui.notify", "ui.open_file"],
    request: vi.fn(async () => ({ status: 200, body: { ok: true } })),
    theme: () => pluginTheme(darkColors),
    settings: () => ({ question_count: 5 }),
    notify: vi.fn(),
    openFile: vi.fn(async () => {}),
    loadState: vi.fn(() => undefined),
    saveState: vi.fn(),
    ...over,
  };
}

describe("pluginTheme", () => {
  it.each([
    { name: "dark", colors: darkColors, mode: "dark" },
    { name: "light", colors: lightColors, mode: "light" },
  ])("$name", ({ colors, mode }) => {
    const t = pluginTheme(colors);
    expect(t.mode).toBe(mode);
    expect(t.vars).toEqual({
      "--bg": colors.bg,
      "--surface": colors.surface,
      "--fg": colors.text,
      "--muted": colors.textMuted,
      "--border": colors.border,
      "--accent": colors.active,
      "--success": colors.diffAddText,
      "--danger": colors.error,
      "--font": fonts.sans,
      "--mono": fonts.mono,
    });
  });
});

describe("callBridge", () => {
  it("api.request passes method, path and body", async () => {
    const h = host();
    await expect(callBridge(h, "api.request", { method: "POST", path: "quizzes", body: { n: 1 } })).resolves.toEqual({ status: 200, body: { ok: true } });
    expect(h.request).toHaveBeenCalledWith("POST", "quizzes", { n: 1 });
  });

  it("settings.get", async () => {
    await expect(callBridge(host(), "settings.get", {})).resolves.toEqual({ question_count: 5 });
  });

  it("context.get", async () => {
    await expect(callBridge(host(), "context.get", {})).resolves.toEqual({ pluginId: "cogtest", panel: "quiz", channelId: "ch1", channelName: "proj", dirPath: "/src/proj" });
  });

  it("context.get in a card", async () => {
    const h = host({ panel: "", card: { id: "score", data: { score: 3 } } });
    await expect(callBridge(h, "context.get", {})).resolves.toEqual({
      pluginId: "cogtest",
      panel: "",
      channelId: "ch1",
      channelName: "proj",
      dirPath: "/src/proj",
      card: "score",
      data: { score: 3 },
    });
  });

  it("ui.resize sizes a card", async () => {
    const resize = vi.fn();
    await expect(callBridge(host({ resize }), "ui.resize", { height: 240.5 })).resolves.toBeNull();
    expect(resize).toHaveBeenCalledWith(240.5);
  });

  it.each([
    { name: "a panel", params: { height: 100 }, resize: undefined, code: -32000, msg: "only a card sizes itself; a panel fills its pane" },
    { name: "no height", params: {}, resize: vi.fn(), code: -32602, msg: "height must be a positive number" },
    { name: "zero", params: { height: 0 }, resize: vi.fn(), code: -32602, msg: "height must be a positive number" },
    { name: "a string", params: { height: "9" }, resize: vi.fn(), code: -32602, msg: "height must be a positive number" },
    { name: "infinite", params: { height: Number.POSITIVE_INFINITY }, resize: vi.fn(), code: -32602, msg: "height must be a positive number" },
  ])("ui.resize refuses $name", async ({ params, resize, code, msg }) => {
    const err = await callBridge(host({ resize }), "ui.resize", params).catch((e: unknown) => e);
    expect(err).toMatchObject({ code, message: msg });
    if (resize) expect(resize).not.toHaveBeenCalled();
  });

  it.each([
    { name: "nothing saved", saved: undefined, want: null },
    { name: "saved", saved: { a: 1 }, want: { a: 1 } },
  ])("state.get: $name", async ({ saved, want }) => {
    await expect(callBridge(host({ loadState: () => saved }), "state.get", {})).resolves.toEqual(want);
  });

  it.each([
    { name: "value", params: { value: { a: 1 } }, want: { a: 1 } },
    { name: "no value", params: {}, want: null },
  ])("state.set: $name", async ({ params, want }) => {
    const h = host();
    await expect(callBridge(h, "state.set", params)).resolves.toBeNull();
    expect(h.saveState).toHaveBeenCalledWith(want);
  });

  it("theme.get", async () => {
    await expect(callBridge(host(), "theme.get", {})).resolves.toEqual(pluginTheme(darkColors));
  });

  it("ui.notify", async () => {
    const h = host();
    await expect(callBridge(h, "ui.notify", { title: "Quiz ready" })).resolves.toBeNull();
    expect(h.notify).toHaveBeenCalledWith("Quiz ready", "");
  });

  it.each([
    { name: "line", params: { path: "a.go", line: 3 }, want: ["a.go", 3] },
    { name: "no line", params: { path: "a.go" }, want: ["a.go", undefined] },
  ])("ui.open_file: $name", async ({ params, want }) => {
    const h = host();
    await expect(callBridge(h, "ui.open_file", params)).resolves.toBeNull();
    expect(h.openFile).toHaveBeenCalledWith(...want);
  });

  it.each([
    { name: "unknown method", method: "nope", params: {}, code: -32601, msg: "no method nope" },
    { name: "api.request without path", method: "api.request", params: { method: "GET" }, code: -32602, msg: "path must be a non-empty string" },
    { name: "api.request empty method", method: "api.request", params: { method: "", path: "x" }, code: -32602, msg: "method must be a non-empty string" },
    { name: "state over 64 KB", method: "state.set", params: { value: "x".repeat(MAX_STATE_BYTES) }, code: -32602, msg: "state is over 64 KB" },
    { name: "notify without the cap", method: "ui.notify", params: { title: "t" }, caps: [], code: -32000, msg: "the plugin wasn't approved for ui.notify" },
    { name: "notify body not a string", method: "ui.notify", params: { title: "t", body: 3 }, code: -32602, msg: "body must be a string" },
    { name: "open_file without the cap", method: "ui.open_file", params: { path: "a" }, caps: [], code: -32000, msg: "the plugin wasn't approved for ui.open_file" },
    { name: "open_file line zero", method: "ui.open_file", params: { path: "a", line: 0 }, code: -32602, msg: "line must be a positive integer" },
    { name: "open_file line fractional", method: "ui.open_file", params: { path: "a", line: 1.5 }, code: -32602, msg: "line must be a positive integer" },
    { name: "open_file line a string", method: "ui.open_file", params: { path: "a", line: "2" }, code: -32602, msg: "line must be a positive integer" },
  ])("$name", async ({ method, params, caps, code, msg }) => {
    const err = await callBridge(host(caps ? { capabilities: caps } : {}), method, params).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(BridgeError);
    expect(err).toMatchObject({ code, message: msg });
  });
});

/** Connects a bridge and returns the panel's end with a call helper. */
function connected(h: BridgeHost) {
  const { port1, port2 } = new MessageChannel();
  const bridge = connectBridge(port1, () => h);
  const received: unknown[] = [];
  const waiters: (() => void)[] = [];
  port2.onmessage = (ev) => {
    received.push(ev.data);
    waiters.shift()?.();
  };
  const next = () => new Promise<unknown>((resolve) => waiters.push(() => resolve(received.at(-1))));
  return { bridge, port2, next, received };
}

describe("connectBridge", () => {
  it("answers calls with results and errors", async () => {
    const { bridge, port2, next } = connected(host());
    port2.postMessage({ jsonrpc: "2.0", id: 1, method: "context.get" });
    expect(await next()).toEqual({ jsonrpc: "2.0", id: 1, result: { pluginId: "cogtest", panel: "quiz", channelId: "ch1", channelName: "proj", dirPath: "/src/proj" } });
    port2.postMessage({ jsonrpc: "2.0", id: "b", method: "nope", params: {} });
    expect(await next()).toEqual({ jsonrpc: "2.0", id: "b", error: { code: -32601, message: "no method nope" } });
    bridge.close();
    port2.close();
  });

  it("reports a host failure as a call error", async () => {
    const { bridge, port2, next } = connected(host({ request: async () => Promise.reject("down") }));
    port2.postMessage({ jsonrpc: "2.0", id: 2, method: "api.request", params: { method: "GET", path: "x" } });
    expect(await next()).toEqual({ jsonrpc: "2.0", id: 2, error: { code: -32000, message: "down" } });
    bridge.close();
    port2.close();
  });

  it("ignores malformed messages and answers no notifications", async () => {
    const h = host();
    const { bridge, port2, next, received } = connected(h);
    port2.postMessage(null);
    port2.postMessage({ id: 3 });
    port2.postMessage({ jsonrpc: "2.0", method: "state.set", params: { value: 1 } });
    port2.postMessage({ jsonrpc: "2.0", id: null, method: "nope" });
    port2.postMessage({ jsonrpc: "2.0", id: 4, method: "state.get", params: "not an object" });
    expect(await next()).toEqual({ jsonrpc: "2.0", id: 4, result: null });
    expect(received).toHaveLength(1);
    expect(h.saveState).toHaveBeenCalledWith(1);
    bridge.close();
    port2.close();
  });

  it("sends notifications to the panel", async () => {
    const { bridge, port2, next } = connected(host());
    bridge.notify("event", { type: "quiz.ready" });
    expect(await next()).toEqual({ jsonrpc: "2.0", method: "event", params: { type: "quiz.ready" } });
    bridge.close();
    port2.close();
  });
});
