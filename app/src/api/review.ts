import { getApiUrl } from "./api";

export interface ReviewPR {
  number: number;
  url: string;
  base_ref: string;
  head_ref: string;
  state: string;
  title?: string;
  is_draft?: boolean;
}

export type ReviewStatus = "idle" | "loading" | "ready" | "reviewing" | "error";

export interface ReviewComment {
  id: string;
  path: string;
  line: number;
  side: string;
  body: string;
  pushed: boolean;
  pushed_at?: string;
}

export interface ReviewSession {
  channel_id: string;
  pr?: ReviewPR;
  head_sha?: string;
  worktree_path?: string;
  raw_diff?: string;
  comments: ReviewComment[];
  status: ReviewStatus;
  error?: string;
  updated_at: string;
}

export interface ReviewSessionResponse {
  present: boolean;
  session?: ReviewSession;
}

export interface PushAllResult {
  pushed: number;
  failed: number;
  errors?: string[];
}

// Parse a PR number out of either "42", "#42", or
// "https://github.com/owner/repo/pull/42".
export function parsePRInput(input: string): number | null {
  const trimmed = input.trim().replace(/^#/, "");
  if (/^\d+$/.test(trimmed)) return parseInt(trimmed, 10);
  const m = trimmed.match(/\/pull\/(\d+)(?:[/?#]|$)/);
  if (m && m[1]) return parseInt(m[1], 10);
  return null;
}

export async function getReviewSession(channelId: string): Promise<ReviewSessionResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review`);
  if (!res.ok) throw new Error(`Failed to fetch review session: ${res.statusText}`);
  return res.json();
}

export async function loadReviewPR(channelId: string, prNumber: number): Promise<ReviewSessionResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/load`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ pr_number: prNumber }),
  });
  if (!res.ok) throw new Error(await res.text() || `Failed to load PR: ${res.statusText}`);
  return res.json();
}

export async function deleteReviewSession(channelId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review`, { method: "DELETE" });
  if (!res.ok && res.status !== 404) throw new Error(`Failed to close review: ${res.statusText}`);
}

export async function runReview(channelId: string): Promise<{ status: string }> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/run`, { method: "POST" });
  if (!res.ok) throw new Error(await res.text() || `Failed to start review: ${res.statusText}`);
  return res.json();
}

export async function pushReviewComment(channelId: string, commentId: string): Promise<void> {
  const res = await fetch(
    `${getApiUrl()}/api/channels/${channelId}/review/comments/${encodeURIComponent(commentId)}/push`,
    { method: "POST" },
  );
  if (!res.ok) throw new Error(await res.text() || `Failed to push comment: ${res.statusText}`);
}

export async function pushAllReviewComments(channelId: string): Promise<PushAllResult> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/push-all`, { method: "POST" });
  if (!res.ok) throw new Error(await res.text() || `Failed to push comments: ${res.statusText}`);
  return res.json();
}
