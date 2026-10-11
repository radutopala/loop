// The bridge between a plugin's panel and the app: JSON-RPC 2.0 over the
// MessagePort the host hands the panel's frame on load. The frame has an
// opaque origin, so this is its only way to reach Loop.

import type { PluginXResponse } from "../api/plugins";
import { type ColorPalette, fonts } from "../theme";

/** Posted to the frame, with the bridge's port, once it has loaded. */
export const BRIDGE_INIT = "loop-plugin:init";

/** The most JSON one panel may keep with state.set. */
export const MAX_STATE_BYTES = 64 << 10;

const METHOD_NOT_FOUND = -32601;
const INVALID_PARAMS = -32602;
const CALL_FAILED = -32000;

export class BridgeError extends Error {
  constructor(
    readonly code: number,
    message: string,
  ) {
    super(message);
  }
}

export interface PluginTheme {
  mode: "dark" | "light";
  vars: Record<string, string>;
}

/** The theme a panel gets, as CSS custom properties. */
export function pluginTheme(colors: ColorPalette): PluginTheme {
  return {
    mode: colors.isDark ? "dark" : "light",
    vars: {
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
    },
  };
}

/** What the bridge calls on: the panel's plugin, channel and the app. */
export interface BridgeHost {
  pluginId: string;
  panel: string;
  channelId: string;
  channelName: string;
  dirPath: string;
  /** The capabilities the owner approved for the plugin. */
  capabilities: string[];
  request: (method: string, path: string, body?: unknown) => Promise<PluginXResponse>;
  theme: () => PluginTheme;
  /** The plugin's settings in the channel's project. */
  settings: () => Record<string, unknown>;
  notify: (title: string, body: string) => void;
  openFile: (path: string, line?: number) => Promise<void>;
  loadState: () => unknown;
  saveState: (value: unknown) => void;
}

type Params = Record<string, unknown>;

function str(params: Params, key: string, required = true): string {
  const v = params[key];
  if (typeof v === "string" && (v !== "" || !required)) return v;
  if (v === undefined && !required) return "";
  throw new BridgeError(INVALID_PARAMS, `${key} must be a ${required ? "non-empty " : ""}string`);
}

function needs(host: BridgeHost, capability: string) {
  if (!host.capabilities.includes(capability)) throw new BridgeError(CALL_FAILED, `the plugin wasn't approved for ${capability}`);
}

/** Runs one bridge call and returns its result. */
export async function callBridge(host: BridgeHost, method: string, params: Params): Promise<unknown> {
  switch (method) {
    case "api.request":
      return host.request(str(params, "method"), str(params, "path"), params.body);
    case "context.get":
      return { pluginId: host.pluginId, panel: host.panel, channelId: host.channelId, channelName: host.channelName, dirPath: host.dirPath };
    case "state.get":
      return host.loadState() ?? null;
    case "state.set": {
      const value = params.value ?? null;
      if (new TextEncoder().encode(JSON.stringify(value)).length > MAX_STATE_BYTES) throw new BridgeError(INVALID_PARAMS, "state is over 64 KB");
      host.saveState(value);
      return null;
    }
    case "theme.get":
      return host.theme();
    case "settings.get":
      return host.settings();
    case "ui.notify":
      needs(host, "ui.notify");
      host.notify(str(params, "title"), str(params, "body", false));
      return null;
    case "ui.open_file": {
      needs(host, "ui.open_file");
      const line = params.line;
      if (line !== undefined && (typeof line !== "number" || !Number.isInteger(line) || line < 1)) throw new BridgeError(INVALID_PARAMS, "line must be a positive integer");
      await host.openFile(str(params, "path"), line);
      return null;
    }
    default:
      throw new BridgeError(METHOD_NOT_FOUND, `no method ${method}`);
  }
}

interface RpcRequest {
  jsonrpc?: string;
  id?: string | number | null;
  method?: unknown;
  params?: unknown;
}

/** The host's end of a connected bridge. */
export interface Bridge {
  /** Sends a notification (no id) to the panel. */
  notify: (method: string, params: unknown) => void;
  close: () => void;
}

/** Answers the panel's calls arriving on port, each with the host getHost
 *  returns when it arrives, and returns the host's end. */
export function connectBridge(port: MessagePort, getHost: () => BridgeHost): Bridge {
  port.onmessage = async (ev: MessageEvent<RpcRequest>) => {
    const msg = ev.data;
    if (!msg || typeof msg !== "object" || typeof msg.method !== "string") return;
    const { id } = msg;
    const params = msg.params && typeof msg.params === "object" ? (msg.params as Params) : {};
    try {
      const result = await callBridge(getHost(), msg.method, params);
      if (id !== undefined && id !== null) port.postMessage({ jsonrpc: "2.0", id, result: result ?? null });
    } catch (err) {
      if (id === undefined || id === null) return;
      const code = err instanceof BridgeError ? err.code : CALL_FAILED;
      port.postMessage({ jsonrpc: "2.0", id, error: { code, message: err instanceof Error ? err.message : String(err) } });
    }
  };
  port.start();
  return {
    notify: (method, params) => port.postMessage({ jsonrpc: "2.0", method, params }),
    close: () => {
      port.onmessage = null;
      port.close();
    },
  };
}
