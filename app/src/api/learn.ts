import { getApiUrl } from "./api";

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
  created_at: string;
  updated_at: string;
}

export async function fetchLearnProposals(channelId: string): Promise<LearnProposal[]> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/learn/proposals`);
  if (!res.ok) throw new Error(`Failed to fetch learn proposals: ${res.statusText}`);
  const body = (await res.json()) as { proposals: LearnProposal[] };
  return body.proposals;
}

async function settleProposal(id: number, action: "apply" | "dismiss"): Promise<LearnProposal> {
  const res = await fetch(`${getApiUrl()}/api/learn/proposals/${id}/${action}`, { method: "POST" });
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
