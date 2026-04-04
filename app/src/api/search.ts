import { getApiUrl } from "./api";

export interface SearchMessageResult {
  id: number;
  channel_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
  created_at: string;
}

export async function searchMessages(
  query: string,
  limit?: number,
): Promise<SearchMessageResult[]> {
  const params = new URLSearchParams({ q: query });
  if (limit) params.set("limit", String(limit));
  const res = await fetch(`${getApiUrl()}/api/messages/search?${params}`);
  if (!res.ok) throw new Error(`Failed to search messages: ${res.statusText}`);
  return res.json();
}
