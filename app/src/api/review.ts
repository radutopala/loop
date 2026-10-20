import { apiFetch, getApiUrl } from "./api";

export interface ReviewPR {
  number: number;
  url: string;
  base_ref: string;
  head_ref: string;
  state: string;
  title?: string;
  is_draft?: boolean;
}

export type ReviewStatus = "idle" | "loading" | "ready" | "reviewing" | "verifying" | "error";

export type ReviewVerdict = "real" | "false_positive" | "already_fixed";

// How much a real finding, or a GitHub thread still open, weighs on
// approving the PR.
export type ReviewImpact = "blocking" | "nice_to_have";

// The verification step's call on a GitHub thread a person started.
export interface ReviewThreadAssessment {
  status: "addressed" | "open";
  impact?: ReviewImpact;
  reason?: string;
}

export type ReviewOutcomeState = "approve" | "approve_with_nits" | "request_changes" | "unknown";

// Whether the PR can be approved, and the comment ids that decide it.
// GitHub threads are listed by their first comment's id.
export interface ReviewOutcome {
  state: ReviewOutcomeState;
  blocking: string[];
  nits: string[];
  pending: string[];
  /** Why the state is unknown. */
  reason?: string;
  reviewed_sha?: string;
}

export interface ReviewComment {
  id: string;
  path: string;
  line: number;
  side: string;
  body: string;
  /** Finding type slug the review gave, e.g. "correctness". */
  category?: string;
  /** The dedup pass's check of an agent comment against the code. */
  verdict?: ReviewVerdict;
  /** One sentence on what in the code decided the verdict. */
  verdict_reason?: string;
  /** With verdict real: whether it blocks approving the PR. */
  impact?: ReviewImpact;
  pushed: boolean;
  pushed_at?: string;
  source?: "agent" | "github";
  author?: string;
  url?: string;
  created_at?: string;
  outdated?: boolean;
  resolved?: boolean;
  github_id?: number;
  /** The GitHub id of the comment that starts this reply's thread. */
  reply_to?: number;
  /** Claude transcripts of the agent passes (review run, dedup, verification) that reported or changed this agent comment, oldest first. */
  transcripts?: string[];
  /** The history id of the review run that reported this agent comment. */
  run_id?: number;
  /** Set by the panel, not the daemon: the verification step's assessment of the GitHub thread this comment starts. */
  thread?: ReviewThreadAssessment;
}

// Which Claude session a review run starts from. "" (the default) runs
// the review in a fresh session; "current" forks whatever session the
// channel's chat is on when the run starts; "custom" forks fork_session_id.
export type ReviewForkMode = "" | "current" | "custom";

// What a review run reviews. "" (auto) reviews only the commits since the
// last reviewed head when an earlier run of the PR finished and its head is
// still an ancestor, else the whole PR.
export type ReviewScope = "" | "full" | "incremental";

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
  // Model / reasoning effort the next review run uses; unset inherits the
  // config's claude_model / claude_effort.
  model?: string;
  effort?: string;
  scope?: ReviewScope;
  // The scope the latest run resolved to, and for an incremental one the
  // commit its range starts at.
  last_run_scope?: Exclude<ReviewScope, "">;
  last_run_from_sha?: string;
  // The verification step's thread assessments, by the GitHub id of each
  // thread's first comment.
  threads?: Record<string, ReviewThreadAssessment>;
  // The head the last review run or verification step finished on, and the
  // one the PR was last approved at from here.
  reviewed_sha?: string;
  approved_sha?: string;
  // The latest review (a Run, every iteration of its loop included) and the
  // history ids of its runs: the agent comments those reported are new.
  review_id?: string;
  review_runs?: number[];
  // The commit a review-only loop is pinned to; absent when it follows the head.
  review_head?: string;
  updated_at: string;
}

export interface ReviewSessionResponse {
  present: boolean;
  session?: ReviewSession;
  /** Only GET /review sends it. */
  outcome?: ReviewOutcome;
}

export interface PushAllResult {
  pushed: number;
  // Comments marked false_positive or already_fixed, which aren't pushed.
  skipped: number;
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
  const res = await apiFetch(`${getApiUrl()}/api/review/sessions`);
  if (!res.ok) throw new Error(`Failed to list review sessions: ${res.statusText}`);
  const body = (await res.json()) as { sessions?: ReviewSessionSummary[] };
  return Array.isArray(body.sessions) ? body.sessions : [];
}

export async function listReviewPRs(channelId: string): Promise<ReviewPR[]> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/prs`);
  if (!res.ok) throw new Error((await res.text()) || `Failed to list PRs: ${res.statusText}`);
  const body = (await res.json()) as { prs?: ReviewPR[] };
  return Array.isArray(body.prs) ? body.prs : [];
}

export async function getReviewSession(channelId: string, opts: { diff?: boolean } = {}): Promise<ReviewSessionResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review${opts.diff === false ? "?diff=false" : ""}`);
  if (!res.ok) throw new Error(`Failed to fetch review session: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function loadReviewPR(channelId: string, prNumber: number): Promise<ReviewSessionResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/load`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ pr_number: prNumber }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to load PR: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function syncReviewSession(channelId: string): Promise<ReviewSessionResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/sync`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to sync review: ${res.statusText}`);
  return normalizeSession(await res.json());
}

export async function deleteReviewSession(channelId: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review`, { method: "DELETE" });
  if (!res.ok && res.status !== 404) throw new Error(`Failed to close review: ${res.statusText}`);
}

export async function runReview(channelId: string): Promise<{ status: string }> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/run`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to start review: ${res.statusText}`);
  return res.json();
}

/**
 * Stop the review run in flight (or a dedup pass). The daemon stops the agent
 * and sets the session back to ready, keeping the findings reported so far,
 * once the agent is gone; review.status says when.
 */
export async function stopReview(channelId: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/stop`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to stop review: ${res.statusText}`);
}

/**
 * Record which Claude session the next review run should fork from. The
 * choice lives on the review session rather than on the run request
 * because the Run button dispatches a workflow, and the `loop review:run`
 * CLI inside it has nowhere to carry per-run options. Returns the updated
 * session so the caller can render the new state without a follow-up GET.
 */
export async function setReviewFork(channelId: string, mode: ReviewForkMode, sessionId?: string): Promise<ReviewSessionResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/fork`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ mode, session_id: sessionId ?? "" }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to set fork mode: ${res.statusText}`);
  return normalizeSession(await res.json());
}

/**
 * Record the model and reasoning effort the next review run uses (empty
 * inherits the config). Stored on the review session for the same reason as
 * the fork choice. Returns the updated session.
 */
export async function setReviewAgent(channelId: string, model: string, effort: string): Promise<ReviewSessionResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/agent`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ model, effort }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to set review model: ${res.statusText}`);
  return normalizeSession(await res.json());
}

/**
 * Record what the next review run reviews. Stored on the review session for
 * the same reason as the fork choice. Returns the updated session.
 */
export async function setReviewScope(channelId: string, scope: ReviewScope): Promise<ReviewSessionResponse> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/scope`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ scope }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to set review scope: ${res.statusText}`);
  return normalizeSession(await res.json());
}

// Finding counts: added in all, folded by the dedup pass, deleted by the
// user, pushed; then per verdict, and per outcome of the pushed ones.
export interface ReviewFindingStats {
  added: number;
  folded: number;
  deleted: number;
  pushed: number;
  real: number;
  false_positive: number;
  already_fixed: number;
  unjudged: number;
  // The real ones by impact.
  blocking: number;
  nice_to_have: number;
  resolved: number;
  outdated: number;
  // False positives over the findings with a verdict, 0..1.
  false_positive_rate: number;
}

export interface ReviewStatsGroup extends ReviewFindingStats {
  key: string;
  runs: number;
}

export interface ReviewStats {
  runs: {
    total: number;
    completed: number;
    failed: number;
    cancelled: number;
    running: number;
    full: number;
    incremental: number;
    prs: number;
    median_ms: number;
    p90_ms: number;
  };
  findings: ReviewFindingStats;
  by_model: ReviewStatsGroup[] | null;
  by_effort: ReviewStatsGroup[] | null;
  by_scope: ReviewStatsGroup[] | null;
  by_category: ReviewStatsGroup[] | null;
}

/**
 * Fetch the review stats of the channel's repo over the last `days`, for one
 * PR when `pr` is set. Throws with the daemon's message, which says so when
 * the daemon keeps no review history.
 */
export async function getReviewStats(channelId: string, opts: { pr?: number; days?: number } = {}): Promise<ReviewStats> {
  const q = new URLSearchParams();
  if (opts.days) q.set("days", String(opts.days));
  if (opts.pr) q.set("pr", String(opts.pr));
  const qs = q.toString();
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/stats${qs ? `?${qs}` : ""}`);
  if (!res.ok) throw new Error((await res.text()) || `Failed to load review stats: ${res.statusText}`);
  return res.json();
}

export async function pushReviewComment(channelId: string, commentId: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/comments/${encodeURIComponent(commentId)}/push`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to push comment: ${res.statusText}`);
}

export async function deleteReviewComment(channelId: string, commentId: string): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/comments/${encodeURIComponent(commentId)}`, { method: "DELETE" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to delete comment: ${res.statusText}`);
}

/** Push every unpushed comment, or with `impact` only the findings of that impact. */
export async function pushAllReviewComments(channelId: string, opts: { impact?: ReviewImpact } = {}): Promise<PushAllResult> {
  const qs = opts.impact ? `?impact=${opts.impact}` : "";
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/push-all${qs}`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()) || `Failed to push comments: ${res.statusText}`);
  return res.json();
}

/**
 * Approve the PR on GitHub at the session's head, as the configured gh user.
 * force approves when the outcome isn't approve or approve_with_nits. Throws
 * the daemon's message: the outcome, a head that moved since the review, or
 * GitHub's refusal. Returns the commit approved.
 */
export async function approveReview(channelId: string, opts: { body?: string; force?: boolean } = {}): Promise<string> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/review/approve`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ body: opts.body ?? "", force: !!opts.force }),
  });
  if (!res.ok) throw new Error((await res.text()) || `Failed to approve the PR: ${res.statusText}`);
  const body = (await res.json()) as { approved_sha: string };
  return body.approved_sha;
}
