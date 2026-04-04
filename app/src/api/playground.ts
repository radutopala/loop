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
