import { apiFetch, getApiUrl } from "./api";

/** Tells the server which terminal sessions this window holds, so it closes
 *  only the sessions no window holds any more. */
export async function claimTerminalSessions(sessionIds: string[]): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/terminal/claims`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ session_ids: sessionIds }),
  });
  if (!res.ok) throw new Error(`Failed to claim terminal sessions: ${res.statusText}`);
}
