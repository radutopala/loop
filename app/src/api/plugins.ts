import { applyEdits, modify, type ParseError, parse } from "jsonc-parser";
import { apiFetch, getApiUrl } from "./api";
import { fetchGlobalConfig, fetchProjectConfig, saveGlobalConfig, saveProjectConfig } from "./configApi";

/** A panel a plugin draws in the workspace. */
export interface PluginPanelInfo {
  id: string;
  label: string;
  /** The panel's HTML page, under ui/ of the plugin's bundle. */
  entry: string;
  singleton?: boolean;
}

/** A plugin's container in a channel. */
export interface PluginContainerInfo {
  channel_id: string;
  channel_name?: string;
  status: "running" | "stopped" | string;
  last_error?: string;
  updated_at: string;
}

/** An installed plugin, as GET /api/plugins lists it. */
export interface PluginInfo {
  id: string;
  name?: string;
  version: string;
  capabilities: string[];
  status: string;
  panels?: PluginPanelInfo[];
  image?: string;
  digest?: string;
  api?: string;
  dev?: boolean;
  installed_at?: string;
  updated_at?: string;
  actions?: string[];
  /** The global plugins.<id>.enabled, which projects without their own follow. */
  enabled_by_default?: boolean;
  /** Listed for a channel: whether the plugin runs in its project. */
  enabled?: boolean;
  /** Listed for a channel: the project config's own setting, if any. */
  project_enabled?: boolean;
  containers?: PluginContainerInfo[];
}

/** The installed plugins, and for a channel whether its project config is
 *  trusted. off is set while plugins are off in the daemon's config. */
export interface PluginList {
  off: boolean;
  plugins: PluginInfo[];
  projectTrusted?: boolean;
}

/** Lists the installed plugins, with their enablement in channelId's project
 *  when it's given. */
export async function fetchPluginList(channelId?: string | null): Promise<PluginList> {
  const query = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
  const res = await apiFetch(`${getApiUrl()}/api/plugins${query}`);
  if (res.status === 501) return { off: true, plugins: [] };
  if (!res.ok) throw new Error(`Failed to list plugins: ${res.statusText}`);
  const data: { plugins: PluginInfo[] | null; project_trusted?: boolean } = await res.json();
  return { off: false, plugins: data.plugins ?? [], projectTrusted: data.project_trusted };
}

/** The installed plugins; none while plugins are off in the daemon's config. */
export async function fetchPlugins(channelId?: string | null): Promise<PluginInfo[]> {
  return (await fetchPluginList(channelId)).plugins;
}

/** An image ready to install, as the owner reviews it. */
export interface PluginCandidate {
  id: string;
  name: string;
  version: string;
  api: string;
  image: string;
  digest?: string;
  dev: boolean;
  capabilities: string[];
  /** Capabilities the installed version wasn't approved for. */
  new_capabilities: string[];
  previous_version?: string;
}

async function failure(res: Response, what: string): Promise<Error> {
  const text = (await res.text().catch(() => "")).trim();
  return new Error(text || `Failed to ${what}: ${res.statusText}`);
}

/** Pulls and checks an image, installing nothing. */
export async function inspectPlugin(image: string, dev = false): Promise<PluginCandidate> {
  const res = await apiFetch(`${getApiUrl()}/api/plugins/inspect`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ image, dev }),
  });
  if (!res.ok) throw await failure(res, "inspect the plugin");
  return res.json();
}

/** Installs or upgrades to the image the owner reviewed, approving caps. */
export async function installPlugin(c: Pick<PluginCandidate, "image" | "dev" | "digest">, approve: string[]): Promise<PluginCandidate> {
  const res = await apiFetch(`${getApiUrl()}/api/plugins`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ image: c.image, dev: c.dev, digest: c.digest ?? "", approve }),
  });
  if (!res.ok) throw await failure(res, "install the plugin");
  return res.json();
}

/** Removes a plugin; purge also deletes its data in every channel. */
export async function removePlugin(id: string, purge: boolean): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/plugins/${encodeURIComponent(id)}${purge ? "?purge=true" : ""}`, { method: "DELETE" });
  if (!res.ok) throw await failure(res, "remove the plugin");
}

/** Stops a plugin's container in a channel, keeping its data. */
export async function stopPluginContainer(id: string, channelId: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/plugins/${encodeURIComponent(id)}/channels/${encodeURIComponent(channelId)}/stop`, { method: "POST" });
  if (!res.ok) throw await failure(res, "stop the plugin");
}

/** A plugin's hidden thread in a channel, where its agent runs run. */
export interface PluginAgent {
  /** Unset until the plugin starts its first run there. */
  thread_id?: string;
  running: boolean;
}

/** The thread plugin id's agent runs run in, for channelId. */
export async function fetchPluginAgent(id: string, channelId: string): Promise<PluginAgent> {
  const res = await apiFetch(`${getApiUrl()}/api/plugins/${encodeURIComponent(id)}/agent?channel_id=${encodeURIComponent(channelId)}`);
  if (!res.ok) throw await failure(res, "look up the plugin's agent");
  return res.json();
}

/** The window event telling plugin views their list changed. */
export const PLUGINS_CHANGED = "loop:plugins-changed";

/** raw, a config file's text, with plugins.<id>.enabled set to enabled and
 *  everything else, comments too, left as it was. */
export function withPluginEnabled(raw: string, id: string, enabled: boolean): string {
  const text = raw.trim() === "" ? "{}\n" : raw;
  const errors: ParseError[] = [];
  parse(text, errors, { allowTrailingComma: true });
  if (errors.length > 0) throw new Error("the config file doesn't parse; fix it first");
  const eol = text.includes("\r\n") ? "\r\n" : "\n";
  const indent = /\n(\s+)"/.exec(text)?.[1];
  const formattingOptions = { insertSpaces: !indent?.startsWith("\t"), tabSize: indent && !indent.startsWith("\t") ? indent.length : 2, eol };
  return applyEdits(text, modify(text, ["plugins", id, "enabled"], enabled, { formattingOptions }));
}

/** Sets plugins.<id>.enabled in channelId's project config, or in the
 *  global config without one, then tells plugin views. */
export async function setPluginEnabled(id: string, enabled: boolean, channelId?: string | null): Promise<void> {
  const cfg = channelId ? await fetchProjectConfig(channelId) : await fetchGlobalConfig();
  const text = withPluginEnabled(cfg.raw ?? "", id, enabled);
  if (channelId) await saveProjectConfig(channelId, text);
  else await saveGlobalConfig(text);
  window.dispatchEvent(new Event(PLUGINS_CHANGED));
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
