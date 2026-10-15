import { apiFetch, getApiUrl } from "./api";

/** The channel's live session: one claude process that takes its chat turns
 *  and stays up between them. Running is false once it ended; reason says why.
 *  Enabled says the channel's chat turns run live at all (the session starts
 *  with the next one); a live.status event leaves it out. */
export interface LiveStatus {
  running: boolean;
  enabled?: boolean;
  channel_id?: string;
  started_at?: string;
  session_id?: string;
  busy: boolean;
  background?: string;
  reason?: string;
}

function liveUrl(channelId: string): string {
  return `${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/live`;
}

/** fetchLive returns the channel's live session. */
export async function fetchLive(channelId: string): Promise<LiveStatus> {
  const res = await apiFetch(liveUrl(channelId));
  if (!res.ok) throw new Error(`Failed to fetch the live session: ${res.statusText}`);
  return res.json();
}

/** warmLive starts the channel's live session ahead of the turn being typed,
 *  and reports whether it started one. */
export async function warmLive(channelId: string): Promise<boolean> {
  const res = await apiFetch(liveUrl(channelId), { method: "POST" });
  if (!res.ok) throw new Error(`Failed to start the live session: ${res.statusText}`);
  const body: { started: boolean } = await res.json();
  return body.started;
}
