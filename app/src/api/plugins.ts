import { apiFetch, getApiUrl } from "./api";

/** A panel a plugin draws in the workspace. */
export interface PluginPanelInfo {
  id: string;
  label: string;
  /** The panel's HTML page, under ui/ of the plugin's bundle. */
  entry: string;
  singleton?: boolean;
}

/** An installed plugin, as GET /api/plugins lists it. */
export interface PluginInfo {
  id: string;
  name?: string;
  version: string;
  capabilities: string[];
  status: string;
  panels?: PluginPanelInfo[];
}

/** The installed plugins; none while plugins are off in the daemon's config. */
export async function fetchPlugins(): Promise<PluginInfo[]> {
  const res = await apiFetch(`${getApiUrl()}/api/plugins`);
  if (res.status === 501) return [];
  if (!res.ok) throw new Error(`Failed to list plugins: ${res.statusText}`);
  const data: { plugins: PluginInfo[] | null } = await res.json();
  return data.plugins ?? [];
}

/** The answer of a plugin's own API, its body decoded when it's JSON. */
export interface PluginXResponse {
  status: number;
  body: unknown;
}

/** Calls the plugin's own API at path (which may carry a query) for the
 *  plugin's container in channelId. A body that isn't a string is sent as
 *  JSON. */
export async function pluginXRequest(pluginId: string, channelId: string, method: string, path: string, body?: unknown): Promise<PluginXResponse> {
  const [rel = "", query = ""] = path.replace(/^\/+/, "").split(/\?(.*)/s);
  const params = new URLSearchParams(query);
  params.set("channel_id", channelId);
  const init: RequestInit = { method: method.toUpperCase() };
  if (body !== undefined && body !== null) {
    init.body = typeof body === "string" ? body : JSON.stringify(body);
    init.headers = { "Content-Type": typeof body === "string" ? "text/plain" : "application/json" };
  }
  const res = await apiFetch(`${getApiUrl()}/api/plugins/${encodeURIComponent(pluginId)}/x/${rel}?${params}`, init);
  const text = await res.text();
  let decoded: unknown = text;
  if (res.headers.get("Content-Type")?.includes("json") && text !== "") {
    try {
      decoded = JSON.parse(text);
    } catch {
      /* keep the text */
    }
  }
  return { status: res.status, body: decoded };
}
