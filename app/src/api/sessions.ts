import { apiFetch, getApiUrl } from "./api";

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
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/sessions`);
  if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.statusText}`);
  return res.json();
}

// setSession switches the channel to one of its project's sessions, so its
// next run resumes that conversation. While a run is in progress the switch
// waits for it to end, and deferred is true.
export async function setSession(channelId: string, sessionId: string): Promise<{ deferred: boolean }> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/session`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ session_id: sessionId }),
  });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return res.json();
}
