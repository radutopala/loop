import { apiFetch, getApiUrl } from "./api";

/** A step of a walkthrough: a line range of a file and what it says about it. */
export interface WalkthroughStep {
  /** The file as the agent gave it: relative to one of the channel's roots,
   *  or absolute. */
  path: string;
  /** 1-based, inclusive. */
  start_line: number;
  end_line: number;
  /** The text of the lines when the walkthrough was made (maybe cut short),
   *  to find them again once the file changed. */
  anchor?: string;
  title?: string;
  /** Markdown. */
  body?: string;
}

/** A walkthrough over a channel's files. The list leaves its steps out. */
export interface Walkthrough {
  id: number;
  channel_id: string;
  title: string;
  created_at: string;
  step_count: number;
  steps?: WalkthroughStep[];
}

const base = (channelId: string) => `${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/walkthroughs`;

/** listWalkthroughs returns the channel's walkthroughs, newest first, without
 *  their steps. */
export async function listWalkthroughs(channelId: string): Promise<Walkthrough[]> {
  const res = await apiFetch(base(channelId));
  if (!res.ok) throw new Error(`Failed to list walkthroughs: ${res.statusText}`);
  return (await res.json()) ?? [];
}

/** fetchWalkthrough returns a walkthrough of the channel with its steps, or
 *  null when the channel has no such walkthrough. */
export async function fetchWalkthrough(channelId: string, id: number): Promise<Walkthrough | null> {
  const res = await apiFetch(`${base(channelId)}/${id}`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`Failed to load walkthrough ${id}: ${res.statusText}`);
  return res.json();
}

/** deleteWalkthrough deletes a walkthrough of the channel; one already gone
 *  counts as deleted. */
export async function deleteWalkthrough(channelId: string, id: number): Promise<void> {
  const res = await apiFetch(`${base(channelId)}/${id}`, { method: "DELETE" });
  if (!res.ok && res.status !== 404) throw new Error(`Failed to delete walkthrough ${id}: ${res.statusText}`);
}
