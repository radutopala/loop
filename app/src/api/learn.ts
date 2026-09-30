import { apiFetch, getApiUrl } from "./api";

/**
 * A channel's learn switch and its hidden learn thread. `available` is false
 * for Slack and Discord channels, which never learn. `learn` is the channel's
 * own setting ("on", "off", or "" to inherit `default_learn` from config).
 */
export interface LearnState {
  available: boolean;
  learn: "" | "on" | "off";
  default_learn: boolean;
  learn_channel_id: string;
  running: boolean;
}

export async function fetchLearnState(channelId: string): Promise<LearnState> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/learn`);
  if (!res.ok) throw new Error(`Failed to fetch learn state: ${res.statusText}`);
  return res.json();
}

/** Turn the channel's learn pass on or off; "" goes back to the config default. */
export async function setLearn(channelId: string, learn: LearnState["learn"]): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/learn`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ learn }),
  });
  if (!res.ok) {
    const body = (await res.text().catch(() => "")).trim();
    throw new Error(body || `Failed to set learn: ${res.statusText}`);
  }
}

export type LearnProposalKind = "prompt_shortcut" | "bash_shortcut" | "scheduled_task" | "gate_rule" | "mount" | "rename" | "description" | "ticket_url";

type LearnProposalStatus = "pending" | "applying" | "applied" | "dismissed" | "failed" | "withdrawn";

/** One change a learn pass proposed. `payload` is the kind's JSON object.
 * A later pass that found it stale withdraws it, saying why in
 * `withdrawn_reason`. */
export interface LearnProposal {
  id: number;
  channel_id: string;
  learn_channel_id: string;
  kind: LearnProposalKind;
  title: string;
  rationale: string;
  payload: string;
  status: LearnProposalStatus;
  error?: string;
  withdrawn_reason?: string;
  /** The turn whose learn pass filed it: its last bot message's msg_id. */
  message_id?: string;
  created_at: string;
  updated_at: string;
}

/** A learn pass over one chat turn, keyed by the turn's last bot message.
 * "superseded" is only found on passes from before they queued one after
 * another: the next turn's pass replaced it before it started. */
export interface LearnPass {
  id: number;
  channel_id: string;
  message_id: string;
  learn_channel_id: string;
  status: "queued" | "running" | "done" | "failed" | "superseded";
  error?: string;
  created_at: string;
  updated_at: string;
  message_row_id?: number;
}

export async function fetchLearnPasses(channelId: string): Promise<LearnPass[]> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/learn/passes`);
  if (!res.ok) throw new Error(`Failed to fetch learn passes: ${res.statusText}`);
  const body = (await res.json()) as { passes: LearnPass[] };
  return body.passes;
}

/**
 * Learn from the turn that ended with messageId: its pass still queued or
 * running comes back as it is, else a new one is queued.
 */
export async function learnTurn(channelId: string, messageId: string): Promise<LearnPass> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/learn/passes`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ message_id: messageId }),
  });
  if (!res.ok) {
    const body = (await res.text().catch(() => "")).trim();
    throw new Error(body || `Failed to learn from the turn: ${res.statusText}`);
  }
  return res.json();
}

export async function fetchLearnProposals(channelId: string): Promise<LearnProposal[]> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/learn/proposals`);
  if (!res.ok) throw new Error(`Failed to fetch learn proposals: ${res.statusText}`);
  const body = (await res.json()) as { proposals: LearnProposal[] };
  return body.proposals;
}

async function settleProposal(id: number, action: "apply" | "dismiss"): Promise<LearnProposal> {
  const res = await apiFetch(`${getApiUrl()}/api/learn/proposals/${id}/${action}`, { method: "POST" });
  if (!res.ok) {
    const body = (await res.text().catch(() => "")).trim();
    throw new Error(body || `Failed to ${action} proposal: ${res.statusText}`);
  }
  return res.json();
}

/** Apply a proposal. A failed apply comes back with status "failed" and its error, not as a throw. */
export function applyLearnProposal(id: number): Promise<LearnProposal> {
  return settleProposal(id, "apply");
}

export function dismissLearnProposal(id: number): Promise<LearnProposal> {
  return settleProposal(id, "dismiss");
}
