import { apiFetch, getApiUrl } from "./api";

/** How a side chat starts from its channel's session: in a fork of it, or in
 *  a new one. */
export type SideChatMode = "fork" | "fresh";

/** A side chat: an unlisted channel (kind "side") of its own beside its
 *  parent's chat, with its own session and live container. The list gives
 *  the whole channel; these are the fields the app reads. */
export interface SideChat {
  channel_id: string;
  name?: string;
  session_id?: string;
  kind?: string;
  created_at?: string;
}

/** The message of a failed request: its JSON error, else its body, else its
 *  status. */
async function errorText(res: Response): Promise<string> {
  const body = (await res.text().catch(() => "")).trim();
  try {
    const parsed = JSON.parse(body) as { error?: unknown };
    if (typeof parsed.error === "string" && parsed.error) return parsed.error;
  } catch {
    // Not JSON: the body is the message.
  }
  return body || res.statusText;
}

/** createSideChat starts a side chat of the channel in mode, returning its
 *  channel id. */
export async function createSideChat(channelId: string, mode: SideChatMode): Promise<string> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/side-chats`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mode }),
  });
  if (!res.ok) throw new Error(await errorText(res));
  const { channel_id } = (await res.json()) as { channel_id: string };
  return channel_id;
}

/** listSideChats returns the channel's side chats. */
export async function listSideChats(channelId: string): Promise<SideChat[]> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/side-chats`);
  if (!res.ok) throw new Error(`Failed to list side chats: ${await errorText(res)}`);
  return (await res.json()) ?? [];
}

/** deleteSideChat ends a side chat: its live session stops and its channel
 *  goes. One already gone counts as deleted. */
export async function deleteSideChat(sideChannelId: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/side-chats/${encodeURIComponent(sideChannelId)}`, { method: "DELETE" });
  if (!res.ok && res.status !== 404) throw new Error(`Failed to delete the side chat: ${await errorText(res)}`);
}
