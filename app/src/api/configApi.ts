import { apiFetch, getApiUrl } from "./api";

// ── Schema types ──

export interface ConfigSchema {
  type: string;
  properties: Record<string, SchemaProperty>;
}

export interface SchemaProperty {
  type: string;
  title?: string;
  description?: string;
  enum?: any[];
  default?: any;
  items?: SchemaProperty;
  properties?: Record<string, SchemaProperty>;
  additionalProperties?: SchemaProperty;
  "x-section"?: string;
  "x-global-only"?: boolean;
  "x-project-only"?: boolean;
  "x-secret"?: boolean;
  "x-order"?: number;
  "x-step"?: number;
  "x-placeholder"?: string;
  "x-widget"?: string;
  "x-auto-save"?: boolean;
}

export interface ConfigResponse {
  path: string;
  content: Record<string, any> | null;
  raw: string;
}

// ── API functions ──

export async function fetchConfigSchema(): Promise<ConfigSchema> {
  const res = await apiFetch(`${getApiUrl()}/api/config/schema`);
  if (!res.ok) throw new Error(`Failed to fetch config schema: ${res.statusText}`);
  return res.json();
}

export async function fetchGlobalConfig(): Promise<ConfigResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/config`);
  if (!res.ok) throw new Error(`Failed to fetch global config: ${res.statusText}`);
  return res.json();
}

export async function saveGlobalConfig(content: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/config`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
  if (!res.ok) throw new Error(`Failed to save global config: ${res.statusText}`);
}

export async function fetchProjectConfig(channelId: string): Promise<ConfigResponse> {
  const params = new URLSearchParams({ channel_id: channelId });
  const res = await apiFetch(`${getApiUrl()}/api/config/project?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch project config: ${res.statusText}`);
  return res.json();
}

// ── Prompt Shortcuts ──

export interface PromptShortcut {
  name: string;
  description: string;
  prompt: string;
  /** Config file the shortcut is defined in — pass it back to add/update/delete. */
  scope: "global" | "project";
}

export async function fetchShortcuts(channelId?: string): Promise<PromptShortcut[]> {
  const params = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
  const res = await apiFetch(`${getApiUrl()}/api/shortcuts${params}`);
  if (!res.ok) return [];
  return res.json();
}

// ── Bash Shortcuts ──

export interface BashShortcut {
  name: string;
  description: string;
  command: string;
  /** Config file the shortcut is defined in — pass it back to add/update/delete. */
  scope: "global" | "project";
}

export async function fetchBashShortcuts(channelId?: string): Promise<BashShortcut[]> {
  const params = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
  const res = await apiFetch(`${getApiUrl()}/api/bash-shortcuts${params}`);
  if (!res.ok) return [];
  return res.json();
}

// ── Project config trust ──

/** Whether a project config's host-reaching fields (mounts, gates, envs…) apply. */
export interface ProjectTrustStatus {
  /** The fields apply as written. */
  trusted: boolean;
  /** Those fields as the file has them now, as indented JSON. */
  current: string;
  /** The version last trusted, which applies while untrusted; "" if never. */
  approved: string;
  /** `approved` → `current` as a unified diff (from /dev/null if never trusted); "" while trusted. */
  diff: string;
  /** Identifies `current`; pass it back to trust exactly what was shown. */
  hash: string;
}

export async function fetchProjectTrust(channelId: string): Promise<ProjectTrustStatus> {
  const params = new URLSearchParams({ channel_id: channelId });
  const res = await apiFetch(`${getApiUrl()}/api/config/project/trust?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch project trust: ${res.statusText}`);
  return res.json();
}

/** Thrown when the project config changed after its status was fetched. */
export class ProjectTrustChangedError extends Error {}

export async function trustProjectConfig(channelId: string, hash: string): Promise<void> {
  const params = new URLSearchParams({ channel_id: channelId });
  const res = await apiFetch(`${getApiUrl()}/api/config/project/trust?${params}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ hash }),
  });
  if (res.status === 409) throw new ProjectTrustChangedError("The project config changed; review it again");
  if (!res.ok) throw new Error(`Failed to trust project config: ${res.statusText}`);
}

export async function saveProjectConfig(channelId: string, content: string): Promise<void> {
  const params = new URLSearchParams({ channel_id: channelId });
  const res = await apiFetch(`${getApiUrl()}/api/config/project?${params}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
  if (!res.ok) throw new Error(`Failed to save project config: ${res.statusText}`);
}

// ── Config history ──

export interface ConfigRevisionSummary {
  id: number;
  /** What wrote it: settings, learn, shortcuts, workflows, builtins, restore:<id>, external or initial. */
  source: string;
  created_at: string;
  /** Lines changed from the revision before it. */
  added: number;
  removed: number;
}

export interface ConfigHistory {
  path: string;
  /** Newest first; the first one is what the file holds now. */
  revisions: ConfigRevisionSummary[];
}

export interface ConfigRevision {
  id: number;
  path: string;
  content: string;
  hash: string;
  source: string;
  created_at: string;
  /** Unified diff from the revision before it, from /dev/null for the oldest, or from the against revision. */
  diff: string;
}

/** The global config's history, or the project config's when channelId is set. */
export async function fetchConfigHistory(channelId?: string): Promise<ConfigHistory> {
  const url = channelId ? `${getApiUrl()}/api/config/project/history?${new URLSearchParams({ channel_id: channelId })}` : `${getApiUrl()}/api/config/history`;
  const res = await apiFetch(url);
  if (!res.ok) throw new Error(`Failed to fetch config history: ${res.statusText}`);
  return res.json();
}

/** A revision, its diff from the one before it or, when against is set, from that revision of the same file. */
export async function fetchConfigRevision(id: number, against?: number): Promise<ConfigRevision> {
  const query = against === undefined ? "" : `?${new URLSearchParams({ against: String(against) })}`;
  const res = await apiFetch(`${getApiUrl()}/api/config/history/${id}${query}`);
  if (!res.ok) throw new Error(`Failed to fetch config revision: ${res.statusText}`);
  return res.json();
}

/** Writes the revision's content back to its config file. */
export async function restoreConfigRevision(id: number): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/config/history/${id}/restore`, { method: "POST" });
  if (!res.ok) throw new Error(`Failed to restore config revision: ${res.statusText}`);
}

/** How the history list names what wrote a revision. */
export function configRevisionSourceLabel(source: string): string {
  if (source.startsWith("restore:")) return `Restored #${source.slice("restore:".length)}`;
  switch (source) {
    case "settings":
      return "Settings";
    case "learn":
      return "Learn";
    case "shortcuts":
      return "Shortcuts";
    case "workflows":
      return "Workflows";
    case "builtins":
      return "Built-ins";
    case "external":
      return "Edited outside Loop";
    case "initial":
      return "First seen";
    default:
      return source;
  }
}
