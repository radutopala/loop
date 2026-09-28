import { getApiUrl } from "./api";

/**
 * A channel's explain switch. `available` is false for Slack and Discord
 * channels and task threads, which are never explained. `explain` is the
 * channel's own setting ("on", "off", or "" to inherit `default_explain`
 * from config); `enabled` is what applies.
 */
export interface ExplainState {
  available: boolean;
  explain: "" | "on" | "off";
  default_explain: boolean;
  enabled: boolean;
}

export type ExplanationStatus = "queued" | "running" | "done" | "failed";

/**
 * The write-up of one turn in a channel: the turn whose last bot message is
 * `message_id`. `message_row_id`, `prompt` and `reply` describe the turn
 * (the list fills them in; an explain.updated event may not carry them).
 */
export interface Explanation {
  id: number;
  channel_id: string;
  message_id: string;
  explain_channel_id: string;
  status: ExplanationStatus;
  content: string;
  error?: string;
  created_at: string;
  updated_at: string;
  message_row_id?: number;
  prompt?: string;
  reply?: string;
}

async function failure(res: Response, what: string): Promise<Error> {
  const body = (await res.text().catch(() => "")).trim();
  return new Error(body || `Failed to ${what}: ${res.statusText}`);
}

export async function fetchExplainState(channelId: string): Promise<ExplainState> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/explain`);
  if (!res.ok) throw new Error(`Failed to fetch explain state: ${res.statusText}`);
  return res.json();
}

/** Turn explaining each turn on or off; "" goes back to the config default. */
export async function setExplain(channelId: string, explain: ExplainState["explain"]): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/explain`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ explain }),
  });
  if (!res.ok) throw await failure(res, "set explain");
}

/** The channel's explanations, newest first. */
export async function fetchExplanations(channelId: string): Promise<Explanation[]> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/explanations`);
  if (!res.ok) throw new Error(`Failed to fetch explanations: ${res.statusText}`);
  return res.json();
}

/**
 * Explain the turn that ended with messageId: its explanation comes back as
 * it is, or a new one is queued when there's none or force (re-explain) is
 * set.
 */
export async function explainTurn(channelId: string, messageId: string, force = false): Promise<Explanation> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/explanations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ message_id: messageId, force }),
  });
  if (!res.ok) throw await failure(res, "explain");
  return res.json();
}
