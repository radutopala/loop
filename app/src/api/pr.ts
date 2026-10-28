import { apiFetch, getApiUrl } from "./api";

/** What a thread's sidebar pills show of its PR/MR's CI failure or merge conflict. */
export type PRAttention = "failed" | "fixing" | "conflict";

/** Why the watcher last acted as it did on a failure. */
export type PRReason = "not_agent_commit" | "auto_fix_off" | "max_attempts" | "no_new_commit" | "same_failure" | "infra_rerun" | "infra_again" | "fix_sent" | "fix_asked";

/** A head's checks: cancelled or skipped ones count only toward total. */
export interface PRChecks {
  total: number;
  passed: number;
  failed: number;
  pending: number;
}

/** One failed check (GitHub) or job (GitLab). */
export interface PRFailedCheck {
  id: string;
  name: string;
  /** The workflow (GitHub) or stage (GitLab). */
  group?: string;
  url?: string;
  run_id?: string;
  /** A GitLab failure the runner caused, not the code. */
  infra?: boolean;
}

/** The status of a thread's PR (kind "pr") or MR (kind "mr"): the channel.pr payload. */
export interface PRStatus {
  channel_id: string;
  kind: string;
  number: number;
  url?: string;
  head_sha: string;
  /** The branch the PR/MR merges into. */
  base_ref?: string;
  checks: PRChecks;
  failed?: PRFailedCheck[];
  merge_state: string;
  auto_merge: boolean;
  merged?: boolean;
  closed?: boolean;
  /** The method "merge when ready" was armed with. */
  merge_armed?: string;
  merge_methods?: string[];
  /** The method a merge would use now. */
  merge_method?: string;
  pr_attention: PRAttention | "";
  /** The effective auto-fix setting, and the config's. */
  auto_fix: boolean;
  auto_fix_default: boolean;
  attempts: number;
  max_attempts: number;
  reason?: PRReason;
  /** The commit the thread's checkout is at. */
  checkout_sha?: string;
  /** The PR/MR's unresolved review threads. */
  unresolved_threads?: number;
  /** A new failure or unresolved review thread the owner is told of; a merge conflict shows in the PR/MR tab only. */
  alert?: string;
}

/** The status of a channel's PR/MR, null when its branch has none. */
export async function getPRStatus(channelId: string): Promise<PRStatus | null> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/pr/status`);
  if (!res.ok) throw new Error((await res.text()) || `Failed to fetch the PR/MR status: ${res.statusText}`);
  const data: { present: boolean; pr?: PRStatus } = await res.json();
  return data.present && data.pr ? data.pr : null;
}

async function send(path: string, method: string, what: string, body?: unknown): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}${path}`, {
    method,
    ...(body !== undefined ? { headers: { "content-type": "application/json" }, body: JSON.stringify(body) } : {}),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to ${what}: ${res.statusText}`);
}

/** Sends the current failure to the thread's agent now. */
export function fixPR(channelId: string): Promise<void> {
  return send(`/api/channels/${channelId}/pr/fix`, "POST", "fix it");
}

/** Re-runs the failed checks now. */
export function rerunPRChecks(channelId: string): Promise<void> {
  return send(`/api/channels/${channelId}/pr/rerun`, "POST", "re-run the checks");
}

/** The tail of a failed check's log. */
export async function getPRCheckLog(channelId: string, checkId: string): Promise<string> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/pr/log?check=${encodeURIComponent(checkId)}`);
  if (!res.ok) throw new Error((await res.text()) || `Failed to fetch the log: ${res.statusText}`);
  const data: { log: string } = await res.json();
  return data.log;
}

/** Overrides pr.auto_fix for the thread; null goes back to the config's. */
export function setPRAutoFix(channelId: string, autoFix: boolean | null): Promise<void> {
  return send(`/api/channels/${channelId}/pr/auto-fix`, "PUT", "set auto-fix", { auto_fix: autoFix });
}

/** Merges the thread's PR/MR now, or arms "merge when ready". */
export function mergePR(channelId: string, whenReady: boolean, method?: string): Promise<void> {
  return send(`/api/channels/${channelId}/pr/merge`, "POST", "merge", { when_ready: whenReady, ...(method ? { method } : {}) });
}

/** Cancels "merge when ready". */
export function cancelMerge(channelId: string): Promise<void> {
  return send(`/api/channels/${channelId}/pr/merge`, "DELETE", "cancel the merge");
}
