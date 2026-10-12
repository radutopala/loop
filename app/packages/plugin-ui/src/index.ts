// The panel's end of the bridge between a plugin's panel and Loop: JSON-RPC
// 2.0 over the MessagePort Loop hands the panel's frame on load. The frame
// has an opaque origin, so this is its only way to reach Loop.

/** Posted to the frame, with the bridge's port, once it has loaded. */
export const BRIDGE_INIT = "loop-plugin:init";

/** The most JSON one panel may keep with state.set. */
export const MAX_STATE_BYTES = 64 << 10;

/** How long a call waits for its answer. */
export const CALL_TIMEOUT_MS = 10_000;

/** How long api.request waits: longer than Loop waits on the plugin's
 *  container (30 s), so the container's answer or Loop's error comes first. */
export const API_TIMEOUT_MS = 35_000;

/** The codes a BridgeError carries. The first three come from Loop; the
 *  last two are the panel's own. */
export const ErrorCode = {
  METHOD_NOT_FOUND: -32601,
  INVALID_PARAMS: -32602,
  /** The call failed in Loop, or the plugin wasn't approved for it. */
  CALL_FAILED: -32000,
  /** No answer came in time. */
  TIMEOUT: -32001,
  /** The bridge was closed before the answer came. */
  CLOSED: -32002,
} as const;

export class BridgeError extends Error {
  readonly code: number;

  constructor(code: number, message: string) {
    super(message);
    this.name = "BridgeError";
    this.code = code;
  }
}

/** A non-2xx answer of the plugin's own API, from the api JSON helpers. */
export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(status: number, body: unknown) {
    super(apiErrorMessage(status, body));
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

function apiErrorMessage(status: number, body: unknown): string {
  if (body && typeof body === "object" && "error" in body && typeof body.error === "string" && body.error) return body.error;
  if (typeof body === "string" && body) return body;
  return `request failed (${status})`;
}

/** The answer of the plugin's own API, its body decoded when it's JSON. */
export interface ApiResponse<T = unknown> {
  status: number;
  body: T;
}

/** Where the panel or card is shown. */
export interface PanelContext<T = unknown> {
  pluginId: string;
  /** The panel's id; "" in a card. */
  panel: string;
  channelId: string;
  /** "" in a card about a channel that's gone. */
  channelName: string;
  dirPath: string;
  /** In a card: its id, and the data the plugin posted it with. */
  card?: string;
  data?: T;
}

/** Loop's theme as CSS custom properties: --bg, --surface, --fg, --muted,
 *  --border, --accent, --success, --danger, --font and --mono. */
export interface Theme {
  mode: "dark" | "light";
  vars: Record<string, string>;
}

/** An event the plugin's container sent with POST /events. */
export interface PluginEvent<T = unknown> {
  type: string;
  data: T;
}

export type Unsubscribe = () => void;

export interface Bridge {
  /** The plugin's own API, /x/{path}, in the panel's channel. */
  api: {
    /** Sends a request and returns any answer, whatever its status. A body
     *  that isn't a string is sent as JSON. path may carry a query. */
    request<T = unknown>(method: string, path: string, body?: unknown): Promise<ApiResponse<T>>;
    /** These return the answer's body, or throw an ApiError for a non-2xx
     *  answer. */
    get<T = unknown>(path: string): Promise<T>;
    post<T = unknown>(path: string, body?: unknown): Promise<T>;
    put<T = unknown>(path: string, body?: unknown): Promise<T>;
    patch<T = unknown>(path: string, body?: unknown): Promise<T>;
    delete<T = unknown>(path: string, body?: unknown): Promise<T>;
  };
  context: {
    get<T = unknown>(): Promise<PanelContext<T>>;
  };
  /** The plugin's settings in the channel's project. */
  settings: {
    get<T = Record<string, unknown>>(): Promise<T>;
    /** Called with the new settings when a config write changes them. */
    onChange<T = Record<string, unknown>>(cb: (settings: T) => void): Unsubscribe;
  };
  /** Small JSON UI state (up to MAX_STATE_BYTES), kept by Loop per plugin,
   *  channel and panel. */
  state: {
    get<T = unknown>(): Promise<T | null>;
    set(value: unknown): Promise<void>;
  };
  /** Events of the plugin's container in the panel's channel. */
  events: {
    on<T = unknown>(cb: (event: PluginEvent<T>) => void): Unsubscribe;
  };
  ui: {
    /** Shows a native notification. Needs the ui.notify capability. */
    notify(title: string, body?: string): Promise<void>;
    /** Opens a file of the channel in the editor, at line if given. Needs
     *  the ui.open_file capability. */
    openFile(path: string, line?: number): Promise<void>;
    /** Puts text on the clipboard, at most 1 MB of it. Needs the ui.copy
     *  capability. */
    copy(text: string): Promise<void>;
    /** Sizes a card's frame to height pixels, kept within 80 to 900. A
     *  panel fills its pane and can't. */
    resize(height: number): Promise<void>;
  };
  theme: {
    get(): Promise<Theme>;
    /** Called with the new theme when Loop's theme changes. */
    onChange(cb: (theme: Theme) => void): Unsubscribe;
  };
  /** Calls a bridge method by name, for one this package has no wrapper
   *  for. */
  call<T = unknown>(method: string, params?: Record<string, unknown>, timeoutMs?: number): Promise<T>;
  /** Fails the calls in flight and closes the port. */
  close(): void;
}

export interface BridgeOptions {
  /** How long a call waits, CALL_TIMEOUT_MS by default. */
  timeoutMs?: number;
  /** How long api calls wait, API_TIMEOUT_MS by default. */
  apiTimeoutMs?: number;
}

export interface ConnectOptions extends BridgeOptions {
  /** Where the init message arrives, the frame's window by default. */
  target?: Pick<EventTarget, "addEventListener" | "removeEventListener">;
}

/** Waits for Loop to hand over the bridge's port and returns the bridge.
 *  Loop posts the port as the frame loads, so call this at startup, before
 *  anything awaits, or the message is missed. */
export function connect(opts: ConnectOptions = {}): Promise<Bridge> {
  const target = opts.target ?? globalThis;
  return new Promise((resolve) => {
    const onMessage = (ev: Event) => {
      const { data, ports } = ev as MessageEvent;
      const port = ports?.[0];
      if (!data || typeof data !== "object" || data.type !== BRIDGE_INIT || !port) return;
      target.removeEventListener("message", onMessage);
      resolve(connectPort(port, opts));
    };
    target.addEventListener("message", onMessage);
  });
}

interface RpcMessage {
  jsonrpc?: unknown;
  id?: unknown;
  method?: unknown;
  params?: unknown;
  result?: unknown;
  error?: { code?: unknown; message?: unknown } | null;
}

interface Pending {
  resolve: (v: unknown) => void;
  reject: (err: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

/** Returns the bridge over a port Loop handed over. */
export function connectPort(port: MessagePort, opts: BridgeOptions = {}): Bridge {
  const timeoutMs = opts.timeoutMs ?? CALL_TIMEOUT_MS;
  const apiTimeoutMs = opts.apiTimeoutMs ?? API_TIMEOUT_MS;
  const pending = new Map<number, Pending>();
  const listeners = new Map<string, Set<(params: unknown) => void>>();
  let nextId = 1;
  let closed = false;

  port.onmessage = (ev: MessageEvent) => {
    const msg = ev.data as RpcMessage | null;
    if (!msg || typeof msg !== "object" || msg.jsonrpc !== "2.0") return;
    if (typeof msg.id === "number") {
      const p = pending.get(msg.id);
      if (!p) return;
      pending.delete(msg.id);
      clearTimeout(p.timer);
      if (msg.error) {
        const code = typeof msg.error.code === "number" ? msg.error.code : ErrorCode.CALL_FAILED;
        p.reject(new BridgeError(code, typeof msg.error.message === "string" ? msg.error.message : "bridge call failed"));
      } else p.resolve(msg.result);
      return;
    }
    if (typeof msg.method !== "string") return;
    for (const cb of listeners.get(msg.method) ?? []) {
      // One listener failing doesn't keep the others from the message.
      try {
        cb(msg.params);
      } catch (err) {
        setTimeout(() => {
          throw err;
        });
      }
    }
  };
  port.start();

  const on = (method: string, cb: (params: never) => void): Unsubscribe => {
    let set = listeners.get(method);
    if (!set) listeners.set(method, (set = new Set()));
    const fn = cb as (params: unknown) => void;
    set.add(fn);
    return () => {
      set.delete(fn);
    };
  };

  const call = <T>(method: string, params: Record<string, unknown> = {}, wait = timeoutMs): Promise<T> =>
    new Promise<T>((resolve, reject) => {
      if (closed) {
        reject(new BridgeError(ErrorCode.CLOSED, "the bridge is closed"));
        return;
      }
      const id = nextId++;
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new BridgeError(ErrorCode.TIMEOUT, `${method} timed out`));
      }, wait);
      pending.set(id, { resolve: resolve as (v: unknown) => void, reject, timer });
      port.postMessage({ jsonrpc: "2.0", id, method, params });
    });

  const request = <T>(method: string, path: string, body?: unknown): Promise<ApiResponse<T>> => {
    const params: Record<string, unknown> = { method, path };
    if (body !== undefined) params.body = body;
    return call<ApiResponse<T>>("api.request", params, apiTimeoutMs);
  };
  const json =
    (method: string) =>
    async <T>(path: string, body?: unknown): Promise<T> => {
      const res = await request<T>(method, path, body);
      if (res.status < 200 || res.status > 299) throw new ApiError(res.status, res.body);
      return res.body;
    };

  return {
    api: { request, get: (path) => json("GET")(path), post: json("POST"), put: json("PUT"), patch: json("PATCH"), delete: json("DELETE") },
    context: { get: () => call("context.get") },
    settings: { get: () => call("settings.get"), onChange: (cb) => on("settings", cb) },
    state: {
      get: () => call("state.get"),
      set: async (value) => {
        await call("state.set", { value });
      },
    },
    events: { on: (cb) => on("event", cb) },
    ui: {
      notify: async (title, body) => {
        await call("ui.notify", body === undefined ? { title } : { title, body });
      },
      openFile: async (path, line) => {
        await call("ui.open_file", line === undefined ? { path } : { path, line });
      },
      copy: async (text) => {
        await call("ui.copy", { text });
      },
      resize: async (height) => {
        await call("ui.resize", { height });
      },
    },
    theme: { get: () => call("theme.get"), onChange: (cb) => on("theme", cb) },
    call: (method, params, wait) => call(method, params, wait),
    close: () => {
      if (closed) return;
      closed = true;
      port.onmessage = null;
      port.close();
      listeners.clear();
      for (const p of pending.values()) {
        clearTimeout(p.timer);
        p.reject(new BridgeError(ErrorCode.CLOSED, "the bridge is closed"));
      }
      pending.clear();
    },
  };
}

/** The part of an element applyTheme sets. */
export interface ThemeTarget {
  style: Pick<CSSStyleDeclaration, "setProperty" | "colorScheme">;
  dataset: DOMStringMap;
}

/** Sets the theme's CSS variables on el, its mode as data-theme and the
 *  matching color-scheme. */
export function applyTheme(theme: Theme, el: ThemeTarget = document.documentElement): void {
  for (const [name, value] of Object.entries(theme.vars ?? {})) {
    if (name.startsWith("--") && typeof value === "string") el.style.setProperty(name, value);
  }
  if (theme.mode === "dark" || theme.mode === "light") {
    el.dataset.theme = theme.mode;
    el.style.colorScheme = theme.mode;
  }
}

/** Applies Loop's theme to el now and as it changes. */
export async function syncTheme(bridge: Bridge, el?: ThemeTarget): Promise<Unsubscribe> {
  const target = el ?? document.documentElement;
  const off = bridge.theme.onChange((theme) => applyTheme(theme, target));
  try {
    applyTheme(await bridge.theme.get(), target);
  } catch (err) {
    off();
    throw err;
  }
  return off;
}

/** A Storage-like store kept in the panel's bridge state. */
export interface BridgeStorage {
  readonly length: number;
  key(index: number): string | null;
  getItem(key: string): string | null;
  /** Throws a QuotaExceededError when the store would pass MAX_STATE_BYTES. */
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  clear(): void;
  /** Saves now, not waiting for the debounce. */
  flush(): Promise<void>;
}

export interface StorageOptions {
  /** How long a change waits for more before it's saved, 50 ms by default. */
  debounceMs?: number;
  /** Called when a save fails, console.warn by default. */
  onError?: (err: unknown) => void;
}

function stateBytes(items: Map<string, string>): number {
  return new TextEncoder().encode(JSON.stringify(Object.fromEntries(items))).length;
}

/** Returns a localStorage stand-in for libraries that persist through one
 *  (zustand's persist, query persisters). It reads the panel's state once,
 *  then works in memory and saves changes in the background with state.set.
 *  Like the state, it's kept per plugin, channel and panel, and it owns the
 *  whole state: don't also call bridge.state.set. */
export async function createStorage(bridge: Bridge, opts: StorageOptions = {}): Promise<BridgeStorage> {
  const debounceMs = opts.debounceMs ?? 50;
  const onError = opts.onError ?? ((err: unknown) => console.warn("plugin-ui: saving storage failed", err));
  const items = new Map<string, string>();
  const saved = await bridge.state.get();
  if (saved && typeof saved === "object" && !Array.isArray(saved)) {
    for (const [k, v] of Object.entries(saved)) if (typeof v === "string") items.set(k, v);
  }

  let timer: ReturnType<typeof setTimeout> | undefined;
  let dirty = false;
  const save = (): Promise<void> => {
    clearTimeout(timer);
    timer = undefined;
    if (!dirty) return Promise.resolve();
    dirty = false;
    return bridge.state.set(Object.fromEntries(items)).catch(onError);
  };
  const changed = () => {
    dirty = true;
    clearTimeout(timer);
    timer = setTimeout(save, debounceMs);
  };

  return {
    get length() {
      return items.size;
    },
    key: (index) => [...items.keys()][index] ?? null,
    getItem: (key) => items.get(key) ?? null,
    setItem: (key, value) => {
      const prev = items.get(key);
      items.set(key, String(value));
      if (stateBytes(items) > MAX_STATE_BYTES) {
        if (prev === undefined) items.delete(key);
        else items.set(key, prev);
        throw new DOMException("storage is over 64 KB", "QuotaExceededError");
      }
      changed();
    },
    removeItem: (key) => {
      if (items.delete(key)) changed();
    },
    clear: () => {
      if (items.size === 0) return;
      items.clear();
      changed();
    },
    flush: save,
  };
}
