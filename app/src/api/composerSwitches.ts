import { apiFetch, getApiUrl } from "./api";

/** How a plugin says its composer switch is doing; "" shows it plain. */
export type ComposerSwitchStatus = "" | "busy" | "ok" | "error";

/** What a composer switch shows in a chat. */
export interface ComposerSwitchState {
  on: boolean;
  status?: ComposerSwitchStatus;
  /** The tooltip; unset keeps the label. */
  title?: string;
}

/** A plugin's on/off pill in the chat composer. */
export interface ComposerSwitch extends ComposerSwitchState {
  plugin_id: string;
  switch_id: string;
  label?: string;
  /** The icon as a data: URL; unset when the plugin's image lacks it. */
  icon?: string;
  /** The manifest's tooltip, for when the plugin set none. */
  default_title?: string;
}

async function failure(res: Response, what: string): Promise<Error> {
  const text = (await res.text().catch(() => "")).trim();
  return new Error(text || `Failed to ${what}: ${res.statusText}`);
}

/** The composer switches of a chat; none for chats without (side chats). */
export async function fetchComposerSwitches(channelId: string): Promise<ComposerSwitch[]> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/composer-switches`);
  if (res.status === 404) return [];
  if (!res.ok) throw await failure(res, "list the composer switches");
  return res.json();
}

/** Asks a switch's plugin to turn it on or off in a chat, and returns the
 *  state the plugin answers. */
export async function turnComposerSwitch(channelId: string, pluginId: string, switchId: string, on: boolean): Promise<ComposerSwitchState> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/composer-switches/${encodeURIComponent(pluginId)}/${encodeURIComponent(switchId)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ on }),
  });
  if (!res.ok) throw await failure(res, "turn the switch");
  return res.json();
}
