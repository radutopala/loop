import { getApiUrl } from "./api";

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
  const res = await fetch(`${getApiUrl()}/api/config/schema`);
  if (!res.ok) throw new Error(`Failed to fetch config schema: ${res.statusText}`);
  return res.json();
}

export async function fetchGlobalConfig(): Promise<ConfigResponse> {
  const res = await fetch(`${getApiUrl()}/api/config`);
  if (!res.ok) throw new Error(`Failed to fetch global config: ${res.statusText}`);
  return res.json();
}

export async function saveGlobalConfig(content: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/config`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
  if (!res.ok) throw new Error(`Failed to save global config: ${res.statusText}`);
}

export async function fetchProjectConfig(channelId: string): Promise<ConfigResponse> {
  const params = new URLSearchParams({ channel_id: channelId });
  const res = await fetch(`${getApiUrl()}/api/config/project?${params}`);
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
  const res = await fetch(`${getApiUrl()}/api/shortcuts${params}`);
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
  const res = await fetch(`${getApiUrl()}/api/bash-shortcuts${params}`);
  if (!res.ok) return [];
  return res.json();
}

export async function saveProjectConfig(channelId: string, content: string): Promise<void> {
  const params = new URLSearchParams({ channel_id: channelId });
  const res = await fetch(`${getApiUrl()}/api/config/project?${params}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
  if (!res.ok) throw new Error(`Failed to save project config: ${res.statusText}`);
}
