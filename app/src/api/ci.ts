import { apiFetch, getApiUrl } from "./api";

/** What a thread's sidebar pill shows of its PR/MR's CI. */
export type CIAttention = "failed" | "fixing";

/** A head's checks: cancelled or skipped ones count only toward total. */
export interface CIChecks {
  total: number;
  passed: number;
  failed: number;
  pending: number;
}

/** One failed check (GitHub) or job (GitLab). */
export interface CIFailedCheck {
  id: string;
  name: string;
  /** The workflow (GitHub) or stage (GitLab). */
  group?: string;
  url?: string;
  run_id?: string;
  /** A GitLab failure the runner caused, not the code. */
  infra?: boolean;
}

/** The CI of a thread's PR (kind "pr") or MR (kind "mr"): the channel.ci payload. */
export interface CIStatus {
  channel_id: string;
  kind: string;
  number: number;
  url?: string;
  head_sha: string;
  checks: CIChecks;
  failed?: CIFailedCheck[];
  merge_state: string;
  auto_merge: boolean;
  merged?: boolean;
  closed?: boolean;
  /** The method "merge when ready" was armed with. */
  merge_armed?: string;
  merge_methods?: string[];
  /** The method a merge would use now. */
  merge_method?: string;
  ci_attention: CIAttention | "";
  /** The effective auto-fix setting, and the config's. */
  auto_fix: boolean;
  auto_fix_default: boolean;
  attempts: number;
  max_attempts: number;
  /** A new failure the owner is told of. */
  alert?: string;
}

/** The CI of a channel's PR/MR, null when its branch has none. */
export async function getCI(channelId: string): Promise<CIStatus | null> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/ci`);
  if (!res.ok) throw new Error((await res.text()) || `Failed to fetch CI: ${res.statusText}`);
  const data: { present: boolean; ci?: CIStatus } = await res.json();
  return data.present && data.ci ? data.ci : null;
}

async function send(path: string, method: string, what: string, body?: unknown): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}${path}`, {
    method,
    ...(body !== undefined ? { headers: { "content-type": "application/json" }, body: JSON.stringify(body) } : {}),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to ${what}: ${res.statusText}`);
}

/** Sends the current failure to the thread's agent now. */
export function fixCI(channelId: string): Promise<void> {
  return send(`/api/channels/${channelId}/ci/fix`, "POST", "fix CI");
}

/** Overrides ci.auto_fix for the thread; null goes back to the config's. */
export function setCIAutoFix(channelId: string, autoFix: boolean | null): Promise<void> {
  return send(`/api/channels/${channelId}/ci/auto-fix`, "PUT", "set auto-fix", { auto_fix: autoFix });
}

/** Merges the thread's PR/MR now, or arms "merge when ready". */
export function mergePR(channelId: string, whenReady: boolean, method?: string): Promise<void> {
  return send(`/api/channels/${channelId}/pr/merge`, "POST", "merge", { when_ready: whenReady, ...(method ? { method } : {}) });
}

/** Cancels "merge when ready". */
export function cancelMerge(channelId: string): Promise<void> {
  return send(`/api/channels/${channelId}/pr/merge`, "DELETE", "cancel the merge");
}
