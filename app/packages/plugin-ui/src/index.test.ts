import { afterEach, describe, expect, it, vi } from "vitest";
// The host's end, so these tests hold the client to the wire Loop speaks.
import { type BridgeHost, connectBridge, type Bridge as HostBridge, pluginTheme } from "../../../src/plugins/bridge";
import { darkColors, lightColors } from "../../../src/theme";
import { ApiError, applyTheme, BRIDGE_INIT, type Bridge, BridgeError, connect, connectPort, createStorage, ErrorCode, MAX_STATE_BYTES, syncTheme, type Theme, type ThemeTarget } from "./index";

// The host's theme fits the package's.
const _theme: Theme = pluginTheme(darkColors);
void _theme;

function fakeHost(over: Partial<BridgeHost> = {}): BridgeHost {
  let state: unknown;
  return {
    pluginId: "echo",
    panel: "main",
    channelId: "ch1",
    channelName: "proj",
    dirPath: "/src/proj",
    capabilities: ["ui.notify", "ui.open_file"],
    request: vi.fn(async (method: string, path: string) => ({ status: 200, body: { method, path } })),
    theme: () => pluginTheme(darkColors),
    settings: () => ({ count: 5 }),
    notify: vi.fn(),
    openFile: vi.fn(async () => {}),
    loadState: vi.fn(() => state),
    saveState: vi.fn((v: unknown) => {
      state = v;
    }),
    ...over,
  };
}

const opened: { close: () => void }[] = [];
afterEach(() => {
  for (const c of opened.splice(0)) c.close();
});

/** A panel bridge wired to the real host bridge over a MessageChannel. */
function pair(over: Partial<BridgeHost> = {}, opts = {}): { bridge: Bridge; host: BridgeHost; hostEnd: HostBridge } {
  const host = fakeHost(over);
  const ch = new MessageChannel();
  const hostEnd = connectBridge(ch.port1, () => host);
  const bridge = connectPort(ch.port2, opts);
  opened.push(hostEnd, bridge);
  return { bridge, host, hostEnd };
}

/** A panel bridge whose host never answers. */
function silent(opts = {}): Bridge {
  const ch = new MessageChannel();
  const bridge = connectPort(ch.port2, opts);
  opened.push(bridge, { close: () => ch.port1.close() });
  return bridge;
}

function nextTick(): Promise<void> {
  return new Promise((r) => setTimeout(r, 0));
}

describe("connect", () => {
  it("resolves with a bridge once the init message brings a port", async () => {
    const target = new EventTarget();
    const ready = connect({ target });
    const host = fakeHost();
    const ch = new MessageChannel();
    opened.push(connectBridge(ch.port1, () => host));
    target.dispatchEvent(new MessageEvent("message", { data: { type: "other" } }));
    target.dispatchEvent(new MessageEvent("message", { data: { type: BRIDGE_INIT } }));
    target.dispatchEvent(new MessageEvent("message", { data: { type: BRIDGE_INIT }, ports: [ch.port2] }));
    const bridge = await ready;
    opened.push(bridge);
    await expect(bridge.context.get()).resolves.toEqual({ pluginId: "echo", panel: "main", channelId: "ch1", channelName: "proj", dirPath: "/src/proj" });
  });
});

describe("calls", () => {
  it("api.request passes method, path and body", async () => {
    const { bridge, host } = pair();
    await expect(bridge.api.request("POST", "items?x=1", { n: 1 })).resolves.toEqual({ status: 200, body: { method: "POST", path: "items?x=1" } });
    expect(host.request).toHaveBeenCalledWith("POST", "items?x=1", { n: 1 });
  });

  it.each([
    { name: "get", run: (b: Bridge) => b.api.get("a"), method: "GET", body: undefined },
    { name: "post", run: (b: Bridge) => b.api.post("a", { n: 1 }), method: "POST", body: { n: 1 } },
    { name: "put", run: (b: Bridge) => b.api.put("a", "text"), method: "PUT", body: "text" },
    { name: "patch", run: (b: Bridge) => b.api.patch("a", { n: 2 }), method: "PATCH", body: { n: 2 } },
    { name: "delete", run: (b: Bridge) => b.api.delete("a"), method: "DELETE", body: undefined },
  ])("api.$name returns the body", async ({ run, method, body }) => {
    const { bridge, host } = pair();
    await expect(run(bridge)).resolves.toEqual({ method, path: "a" });
    expect(host.request).toHaveBeenCalledWith(method, "a", body);
  });

  it.each([
    { name: "error field", body: { error: "no such quiz" }, message: "no such quiz" },
    { name: "text body", body: "bad gateway", message: "bad gateway" },
    { name: "no body", body: null, message: "request failed (404)" },
  ])("api JSON helpers throw an ApiError on a non-2xx answer: $name", async ({ body, message }) => {
    const { bridge } = pair({ request: async () => ({ status: 404, body }) });
    const err = await bridge.api.get("x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 404, body, message });
  });

  it("settings.get", async () => {
    await expect(pair().bridge.settings.get()).resolves.toEqual({ count: 5 });
  });

  it("theme.get", async () => {
    await expect(pair().bridge.theme.get()).resolves.toEqual(pluginTheme(darkColors));
  });

  it("state round-trips", async () => {
    const { bridge, host } = pair();
    await expect(bridge.state.get()).resolves.toBeNull();
    await expect(bridge.state.set({ tab: "history" })).resolves.toBeUndefined();
    expect(host.saveState).toHaveBeenCalledWith({ tab: "history" });
    await expect(bridge.state.get()).resolves.toEqual({ tab: "history" });
  });

  it.each([
    { name: "title only", run: (b: Bridge) => b.ui.notify("Done"), want: ["Done", ""] },
    { name: "title and body", run: (b: Bridge) => b.ui.notify("Done", "3 of 5"), want: ["Done", "3 of 5"] },
  ])("ui.notify: $name", async ({ run, want }) => {
    const { bridge, host } = pair();
    await expect(run(bridge)).resolves.toBeUndefined();
    expect(host.notify).toHaveBeenCalledWith(...want);
  });

  it.each([
    { name: "no line", run: (b: Bridge) => b.ui.openFile("main.go"), want: ["main.go", undefined] },
    { name: "line", run: (b: Bridge) => b.ui.openFile("main.go", 12), want: ["main.go", 12] },
  ])("ui.openFile: $name", async ({ run, want }) => {
    const { bridge, host } = pair();
    await expect(run(bridge)).resolves.toBeUndefined();
    expect(host.openFile).toHaveBeenCalledWith(...want);
  });

  it("context.get in a card", async () => {
    const { bridge } = pair({ panel: "", card: { id: "score", data: { score: 3 } } });
    await expect(bridge.context.get<{ score: number }>()).resolves.toMatchObject({ panel: "", card: "score", data: { score: 3 } });
  });

  it("ui.resize sizes a card", async () => {
    const resize = vi.fn();
    const { bridge } = pair({ card: { id: "score", data: null }, resize });
    await expect(bridge.ui.resize(240)).resolves.toBeUndefined();
    expect(resize).toHaveBeenCalledWith(240);
  });

  it.each([
    { name: "a panel", over: {}, height: 240, code: ErrorCode.CALL_FAILED },
    { name: "no height", over: { resize: vi.fn() }, height: 0, code: ErrorCode.INVALID_PARAMS },
  ])("ui.resize refused: $name", async ({ over, height, code }) => {
    await expect(pair(over).bridge.ui.resize(height)).rejects.toMatchObject({ code });
  });

  it("call reaches any method by name", async () => {
    await expect(pair().bridge.call("context.get")).resolves.toMatchObject({ pluginId: "echo" });
  });
});

describe("errors", () => {
  it.each([
    { name: "unknown method", run: (b: Bridge) => b.call("ui.setTitle", { title: "x" }), code: ErrorCode.METHOD_NOT_FOUND, message: "no method ui.setTitle" },
    { name: "bad params", run: (b: Bridge) => b.ui.openFile("a.go", 0), code: ErrorCode.INVALID_PARAMS, message: "line must be a positive integer" },
    { name: "state over 64 KB", run: (b: Bridge) => b.state.set("x".repeat(MAX_STATE_BYTES)), code: ErrorCode.INVALID_PARAMS, message: "state is over 64 KB" },
    {
      name: "not approved",
      run: (b: Bridge) => b.ui.notify("hi"),
      over: { capabilities: [] },
      code: ErrorCode.CALL_FAILED,
      message: "the plugin wasn't approved for ui.notify",
    },
    {
      name: "host call fails",
      run: (b: Bridge) => b.api.request("GET", "a"),
      over: {
        request: async () => {
          throw new Error("plugin container isn't answering");
        },
      },
      code: ErrorCode.CALL_FAILED,
      message: "plugin container isn't answering",
    },
  ])("$name rejects with a BridgeError", async ({ run, over, code, message }) => {
    const err = await run(pair(over).bridge).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(BridgeError);
    expect(err).toMatchObject({ code, message });
  });

  it("a call times out", async () => {
    const err = await silent({ timeoutMs: 10 })
      .context.get()
      .catch((e: unknown) => e);
    expect(err).toMatchObject({ code: ErrorCode.TIMEOUT, message: "context.get timed out" });
  });

  it("api calls wait for their own timeout", async () => {
    const bridge = silent({ timeoutMs: 10, apiTimeoutMs: 60 });
    const started = Date.now();
    const err = await bridge.api.request("GET", "a").catch((e: unknown) => e);
    expect(err).toMatchObject({ code: ErrorCode.TIMEOUT, message: "api.request timed out" });
    expect(Date.now() - started).toBeGreaterThanOrEqual(50);
  });

  it("close fails the calls in flight and later ones", async () => {
    const bridge = silent();
    const inFlight = bridge.theme.get();
    bridge.close();
    bridge.close();
    await expect(inFlight).rejects.toMatchObject({ code: ErrorCode.CLOSED });
    await expect(bridge.theme.get()).rejects.toMatchObject({ code: ErrorCode.CLOSED });
  });

  it("ignores answers that aren't its own", async () => {
    const ch = new MessageChannel();
    const bridge = connectPort(ch.port2, { timeoutMs: 1000 });
    opened.push(bridge, { close: () => ch.port1.close() });
    ch.port1.onmessage = (ev: MessageEvent<{ id: number }>) => {
      ch.port1.postMessage(null);
      ch.port1.postMessage({ id: ev.data.id, result: "no jsonrpc" });
      ch.port1.postMessage({ jsonrpc: "2.0", id: 999, result: "unknown id" });
      ch.port1.postMessage({ jsonrpc: "2.0", params: {} });
      ch.port1.postMessage({ jsonrpc: "2.0", id: ev.data.id, error: { message: 7 } });
    };
    await expect(bridge.context.get()).rejects.toMatchObject({ code: ErrorCode.CALL_FAILED, message: "bridge call failed" });
  });
});

describe("notifications", () => {
  it("theme, settings and events reach their listeners until unsubscribed", async () => {
    const { bridge, hostEnd } = pair();
    const themes = vi.fn();
    const settings = vi.fn();
    const events = vi.fn();
    const offTheme = bridge.theme.onChange(themes);
    bridge.settings.onChange(settings);
    bridge.events.on(events);
    hostEnd.notify("theme", pluginTheme(lightColors));
    hostEnd.notify("settings", { count: 7 });
    hostEnd.notify("event", { type: "quiz_ready", data: { id: 1 } });
    hostEnd.notify("other", {});
    await nextTick();
    expect(themes).toHaveBeenCalledWith(pluginTheme(lightColors));
    expect(settings).toHaveBeenCalledWith({ count: 7 });
    expect(events).toHaveBeenCalledWith({ type: "quiz_ready", data: { id: 1 } });
    offTheme();
    hostEnd.notify("theme", pluginTheme(darkColors));
    await nextTick();
    expect(themes).toHaveBeenCalledTimes(1);
  });
});

function fakeElement(): ThemeTarget & { vars: Record<string, string> } {
  const vars: Record<string, string> = {};
  return { vars, dataset: {}, style: { colorScheme: "", setProperty: (k: string, v: string | null) => void (vars[k] = v ?? "") } };
}

describe("applyTheme", () => {
  it("sets the variables, data-theme and color-scheme", () => {
    const el = fakeElement();
    applyTheme({ mode: "light", vars: { "--bg": "#fff", "--fg": "#000", bad: "x" } }, el);
    expect(el.vars).toEqual({ "--bg": "#fff", "--fg": "#000" });
    expect(el.dataset.theme).toBe("light");
    expect(el.style.colorScheme).toBe("light");
  });

  it("syncTheme applies now and on change", async () => {
    const { bridge, hostEnd } = pair();
    const el = fakeElement();
    const off = await syncTheme(bridge, el);
    expect(el.dataset.theme).toBe("dark");
    expect(el.vars["--bg"]).toBe(darkColors.bg);
    hostEnd.notify("theme", pluginTheme(lightColors));
    await nextTick();
    expect(el.dataset.theme).toBe("light");
    off();
  });

  it("syncTheme rejects when theme.get fails", async () => {
    await expect(syncTheme(silent({ timeoutMs: 10 }), fakeElement())).rejects.toMatchObject({ code: ErrorCode.TIMEOUT });
  });
});

describe("createStorage", () => {
  it("loads the saved strings and works like localStorage", async () => {
    const { bridge } = pair({ loadState: () => ({ a: "1", b: 2, c: "3" }) });
    const s = await createStorage(bridge);
    expect(s.length).toBe(2);
    expect(s.getItem("a")).toBe("1");
    expect(s.getItem("b")).toBeNull();
    expect(s.key(1)).toBe("c");
    expect(s.key(5)).toBeNull();
    s.setItem("d", "4");
    s.removeItem("a");
    s.removeItem("missing");
    expect([s.key(0), s.key(1)]).toEqual(["c", "d"]);
    s.clear();
    expect(s.length).toBe(0);
    s.clear();
  });

  it.each([
    { name: "nothing", saved: undefined },
    { name: "an array", saved: ["a"] },
    { name: "a string", saved: "a" },
  ])("starts empty when the state is $name", async ({ saved }) => {
    const s = await createStorage(pair({ loadState: () => saved }).bridge);
    expect(s.length).toBe(0);
  });

  it("saves changes once, after the debounce", async () => {
    const { bridge, host } = pair();
    const s = await createStorage(bridge, { debounceMs: 20 });
    s.setItem("a", "1");
    s.setItem("b", "2");
    s.removeItem("a");
    await nextTick();
    expect(host.saveState).not.toHaveBeenCalled();
    await vi.waitFor(() => expect(host.saveState).toHaveBeenCalledWith({ b: "2" }));
    expect(host.saveState).toHaveBeenCalledTimes(1);
    const again = await createStorage(bridge);
    expect(again.getItem("b")).toBe("2");
  });

  it("flush saves now, and only when something changed", async () => {
    const { bridge, host } = pair();
    const s = await createStorage(bridge, { debounceMs: 10_000 });
    await s.flush();
    expect(host.saveState).not.toHaveBeenCalled();
    s.setItem("a", "1");
    await s.flush();
    expect(host.saveState).toHaveBeenCalledWith({ a: "1" });
    s.clear();
    await s.flush();
    expect(host.saveState).toHaveBeenLastCalledWith({});
  });

  it("refuses a change over 64 KB and keeps what it had", async () => {
    const { bridge, host } = pair();
    const s = await createStorage(bridge);
    s.setItem("a", "small");
    const big = "x".repeat(MAX_STATE_BYTES);
    expect(() => s.setItem("a", big)).toThrow(expect.objectContaining({ name: "QuotaExceededError" }));
    expect(() => s.setItem("b", big)).toThrow(expect.objectContaining({ name: "QuotaExceededError" }));
    expect(s.getItem("a")).toBe("small");
    expect(s.getItem("b")).toBeNull();
    await s.flush();
    expect(host.saveState).toHaveBeenCalledWith({ a: "small" });
  });

  it("reports a failed save", async () => {
    const onError = vi.fn();
    const { bridge } = pair({
      saveState: () => {
        throw new Error("disk full");
      },
    });
    const failing = await createStorage(bridge, { onError });
    failing.setItem("a", "1");
    await failing.flush();
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: "disk full" }));
  });
});
