import { getApiUrl } from "./api";

export interface SessionEntry {
  session_id: string;
  last_modified: string;
  last_message?: string;
}

export interface SessionsResponse {
  current_session_id: string;
  sessions: SessionEntry[];
  imported_session_ids: string[];
}

export async function fetchSessions(channelId: string): Promise<SessionsResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/sessions`);
  if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.statusText}`);
  return res.json();
}
