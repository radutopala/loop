import { getApiUrl } from "./api";

export interface PlaygroundItem {
  name: string;
  title?: string;
  description?: string;
  scope: "global" | "project";
}

/** Fetch the current playground content by name. */
export async function fetchPlayground(
  name: string,
  scope?: "global" | "project",
  channelId?: string,
): Promise<{ name: string; title?: string; html: string; description?: string } | null> {
  const params = new URLSearchParams({ name });
  if (scope === "project" && channelId) {
    params.set("scope", "project");
    params.set("channel_id", channelId);
  }
  const res = await fetch(`${getApiUrl()}/api/playground?${params}`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`Failed to fetch playground: ${res.statusText}`);
  return res.json();
}

/** List all playground items with names and titles (global + project-scoped). */
export async function fetchPlaygroundItems(channelId?: string): Promise<PlaygroundItem[]> {
  const params = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
  const res = await fetch(`${getApiUrl()}/api/playground/items${params}`);
  if (!res.ok) throw new Error(`Failed to fetch playground items: ${res.statusText}`);
  const data: { items: PlaygroundItem[] } = await res.json();
  return data.items;
}

export interface PlaygroundShare {
  name: string;
  scope: "global" | "project";
  channel_id: string;
  url: string;
}

function shareParams(name: string, scope?: "global" | "project", channelId?: string): URLSearchParams {
  const params = new URLSearchParams({ name });
  if (scope === "project" && channelId) {
    params.set("scope", "project");
    params.set("channel_id", channelId);
  }
  return params;
}

/** Expose a playground publicly over a cloudflared tunnel; returns the public URL. */
export async function sharePlayground(
  name: string,
  scope?: "global" | "project",
  channelId?: string,
): Promise<{ url: string; token: string }> {
  const res = await fetch(`${getApiUrl()}/api/playground/share?${shareParams(name, scope, channelId)}`, {
    method: "PUT",
  });
  if (!res.ok) throw new Error(`Failed to share playground: ${await res.text()}`);
  return res.json();
}

/** Stop sharing a playground. */
export async function unsharePlayground(
  name: string,
  scope?: "global" | "project",
  channelId?: string,
): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/playground/share?${shareParams(name, scope, channelId)}`, {
    method: "DELETE",
  });
  if (!res.ok && res.status !== 204) throw new Error(`Failed to unshare playground: ${res.statusText}`);
}

/** List all currently-active public shares. */
export async function fetchPlaygroundShares(): Promise<PlaygroundShare[]> {
  const res = await fetch(`${getApiUrl()}/api/playground/share`);
  if (!res.ok) throw new Error(`Failed to fetch playground shares: ${res.statusText}`);
  const data: { shares: PlaygroundShare[] } = await res.json();
  return data.shares;
}

/**
 * Share status for one playground — resolved by dir server-side, so it's
 * identical for every channel/thread that maps to the same playground.
 */
export async function fetchPlaygroundShareStatus(
  name: string,
  scope?: "global" | "project",
  channelId?: string,
): Promise<{ shared: boolean; url: string }> {
  const res = await fetch(`${getApiUrl()}/api/playground/share?${shareParams(name, scope, channelId)}`);
  if (!res.ok) throw new Error(`Failed to fetch playground share status: ${res.statusText}`);
  return res.json();
}
