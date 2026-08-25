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
  source?: "agent" | "github";
  author?: string;
  url?: string;
  created_at?: string;
  outdated?: boolean;
  resolved?: boolean;
  github_id?: number;
}

// Which Claude session a review run starts from. "" (the default) runs
// the review in a fresh session; "current" forks whatever session the
// channel's chat is on when the run starts; "custom" forks fork_session_id.
export type ReviewForkMode = "" | "current" | "custom";

export interface ReviewSession {
  channel_id: string;
  pr?: ReviewPR;
  head_sha?: string;
  worktree_path?: string;
  raw_diff?: string;
  comments: ReviewComment[];
  status: ReviewStatus;
  error?: string;
  fork_mode?: ReviewForkMode;
  fork_session_id?: string;
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

// Normalize a session payload so callers can always rely on
// `session.comments` being an array. Go marshals a nil slice as JSON
// `null`, and the renderer reads `comments.length` directly — so anything
// that hands a Session to setSession() must go through this.
function normalizeSession(resp: ReviewSessionResponse): ReviewSessionResponse {
  if (resp.session && !Array.isArray(resp.session.comments)) {
    resp.session.comments = [];
  }
  return resp;
}

export interface ReviewSessionSummary {
  channel_id: string;
  status: ReviewStatus;
}

/**
 * Snapshot every live review session's (channel_id, status). Used at
 * app startup to seed the sidebar `rev` pill set — review.status WS
 * events only fire on transitions, and the FE doesn't subscribe to
 * every channel, so without this any session that became ready while
 * the renderer was closed would never re-light its indicator.
 */
export async function listReviewSessions(): Promise<ReviewSessionSummary[]> {
  const res = await fetch(`${getApiUrl()}/api/review/sessions`);
  if (!res.ok) throw new Error(`Failed to list review sessions: ${res.statusText}`);
  const body = (await res.json()) as { sessions?: ReviewSessionSummary[] };
  return Array.isArray(body.sessions) ? body.sessions : [];
}

export async function listReviewPRs(channelId: string): Promise<ReviewPR[]> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/prs`);
  if (!res.ok) throw new Error((await res.text()) || `Failed to list PRs: ${res.statusText}`);
  const body = (await res.json()) as { prs?: ReviewPR[] };
  return Array.isArray(body.prs) ? body.prs : [];
}

export async function getReviewSession(channelId: string): Promise<ReviewSessionResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review`);
  if (!res.ok) throw new Error(`Failed to fetch review session: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function loadReviewPR(channelId: string, prNumber: number): Promise<ReviewSessionResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/load`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ pr_number: prNumber }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to load PR: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function syncReviewSession(channelId: string): Promise<ReviewSessionResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/sync`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to sync review: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function deleteReviewSession(channelId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review`, { method: "DELETE" });
  if (!res.ok && res.status !== 404) throw new Error(`Failed to close review: ${res.statusText}`);
}

export async function runReview(channelId: string): Promise<{ status: string }> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/run`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to start review: ${res.statusText}`);
  return res.json();
}

/**
 * Record which Claude session the next review run should fork from. The
 * choice lives on the review session rather than on the run request
 * because the Run button dispatches a workflow, and the `loop review run`
 * CLI inside it has nowhere to carry per-run options. Returns the updated
 * session so the caller can render the new state without a follow-up GET.
 */
export async function setReviewFork(channelId: string, mode: ReviewForkMode, sessionId?: string): Promise<ReviewSessionResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/fork`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ mode, session_id: sessionId ?? "" }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to set fork mode: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function pushReviewComment(channelId: string, commentId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/comments/${encodeURIComponent(commentId)}/push`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to push comment: ${res.statusText}`);
}

export async function deleteReviewComment(channelId: string, commentId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/comments/${encodeURIComponent(commentId)}`, { method: "DELETE" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to delete comment: ${res.statusText}`);
}

export async function pushAllReviewComments(channelId: string): Promise<PushAllResult> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/review/push-all`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to push comments: ${res.statusText}`);
  return res.json();
}
