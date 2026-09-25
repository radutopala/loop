import { getApiUrl } from "./api";

export interface SearchMessageResult {
  id: number;
  channel_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
  created_at: string;
}

export async function searchMessages(query: string, limit?: number): Promise<SearchMessageResult[]> {
  const params = new URLSearchParams({ q: query });
  if (limit) params.set("limit", String(limit));
  const res = await fetch(`${getApiUrl()}/api/messages/search?${params}`);
  if (!res.ok) throw new Error(`Failed to search messages: ${res.statusText}`);
  return res.json();
}

// searchChannelMessages returns the ids of a channel's messages containing
// query, newest first — what the chat's find bar steps through.
export async function searchChannelMessages(channelId: string, query: string, signal?: AbortSignal): Promise<number[]> {
  const params = new URLSearchParams({ q: query });
  const res = await fetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/messages/search?${params}`, { signal });
  if (!res.ok) throw new Error(`Failed to search messages: ${res.statusText}`);
  const body: { ids: number[] } = await res.json();
  return body.ids;
}
