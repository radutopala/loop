import { getApiUrl } from "./api";

/**
 * A channel's learn switch and its hidden learn thread. `learn` is the
 * channel's own setting ("on", "off", or "" to inherit `default_learn` from
 * config); `enabled` is what applies.
 */
export interface LearnState {
  learn: "" | "on" | "off";
  default_learn: boolean;
  enabled: boolean;
  learn_channel_id: string;
  running: boolean;
}

export async function fetchLearnState(channelId: string): Promise<LearnState> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/learn`);
  if (!res.ok) throw new Error(`Failed to fetch learn state: ${res.statusText}`);
  return res.json();
}

/** Turn the channel's learn pass on or off; "" goes back to the config default. */
export async function setLearn(channelId: string, learn: LearnState["learn"]): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/learn`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ learn }),
  });
  if (!res.ok) {
    const body = (await res.text().catch(() => "")).trim();
    throw new Error(body || `Failed to set learn: ${res.statusText}`);
  }
}
