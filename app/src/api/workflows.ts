import { getApiUrl } from "./api";

export interface WorkflowDef {
  name: string;
  /** Config scope this definition comes from: "global" (~/.loop/config.json) or
   *  "project" (a project's .loop/config.json). Tagged by the list endpoint. */
  scope?: "global" | "project";
  description: string;
  timeout?: string;
  inputs?: Record<string, { description?: string; required?: boolean; default?: string }>;
  nodes: WorkflowNodeDef[];
}

export interface WorkflowNodeDef {
  id: string;
  type: "prompt" | "bash" | "loop" | "approval";
  depends_on?: string[];
  when?: string;
  trigger_rule?: "all_success" | "all_done" | "one_success";
  prompt?: string;
  prompt_path?: string;
  system_prompt?: string;
  model?: string;
  script?: string;
  message?: string;
  timeout?: string;
  max_iterations?: number;
  condition?: string;
  retry?: { max_retries: number; backoff_base?: string; backoff_max?: string };
  /** Loop nodes only: children executed sequentially per iteration. Empty
   *  preserves the legacy "self-prompt" behavior used by older workflows. */
  body?: WorkflowNodeDef[];
}

export interface WorkflowRun {
  id: string;
  workflow_name: string;
  channel_id: string;
  dir_path: string;
  worktree_path: string;
  status: "running" | "completed" | "failed" | "paused" | "cancelled";
  inputs: string;
  paused_node_id: string;
  error_text: string;
  workflow_def?: string;
  started_at: string;
  finished_at: string | null;
  channel_name?: string;
  channel_worktree?: boolean;
}

export interface WorkflowNodeRun {
  id: number;
  run_id: string;
  node_id: string;
  /** Zero-based loop iteration this row belongs to. 0 for nodes outside a
   *  loop body. Older rows (pre-iteration column) materialize as 0. */
  iteration: number;
  status: "pending" | "running" | "success" | "failed" | "skipped" | "paused";
  /** Rendered node input: bash script for bash nodes, resolved prompt for
   *  prompt nodes. Empty for loop/approval nodes and pre-column rows. */
  input: string;
  /** Claude session id a prompt node's agent run produced (resumable
   *  transcript). Empty for non-prompt nodes. */
  session_id: string;
  output: string;
  error_text: string;
  attempt: number;
  started_at: string | null;
  finished_at: string | null;
  last_heartbeat_at: string | null;
}

export interface WorkflowRunDetail {
  run: WorkflowRun;
  node_runs: WorkflowNodeRun[];
}

// describeError extracts a meaningful failure description from a non-2xx
// fetch Response. Under HTTP/2 the wire-level reason phrase is removed
// (RFC 7540 §8.1.2.4), so `res.statusText` is empty — relying on it alone
// would surface bare "Failed to ___: " messages to the user. The daemon
// emits JSON error bodies (`{"error":"…"}`), and plaintext bodies in
// the legacy paths. Prefer the body when present; fall back to the
// numeric status code so callers never get a blank suffix.
async function describeError(res: Response): Promise<string> {
  const code = res.status;
  let text: string;
  try {
    text = (await res.text()).trim();
  } catch {
    return res.statusText || String(code);
  }
  if (!text) return res.statusText || String(code);
  try {
    const parsed = JSON.parse(text);
    if (parsed && typeof parsed.error === "string" && parsed.error) {
      return parsed.error;
    }
  } catch {
    // not JSON — fall through and use the raw body.
  }
  return text.length > 400 ? `${text.slice(0, 400)}…` : text;
}

export async function fetchWorkflows(channelId?: string): Promise<WorkflowDef[]> {
  const qs = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
  const res = await fetch(`${getApiUrl()}/api/workflows${qs}`);
  if (!res.ok) throw new Error(`Failed to fetch workflows: ${await describeError(res)}`);
  return (await res.json()) ?? [];
}

export async function fetchWorkflowRuns(channelId?: string, limit?: number, offset?: number): Promise<WorkflowRun[]> {
  const params = new URLSearchParams();
  if (channelId) params.set("channel_id", channelId);
  if (limit !== undefined) params.set("limit", String(limit));
  if (offset !== undefined && offset > 0) params.set("offset", String(offset));
  const qs = params.toString();
  const res = await fetch(`${getApiUrl()}/api/workflows/runs${qs ? `?${qs}` : ""}`);
  if (!res.ok) throw new Error(`Failed to fetch workflow runs: ${await describeError(res)}`);
  return (await res.json()) ?? [];
}

/** Thrown by fetchWorkflowRun for non-2xx responses. Callers can branch on
 *  `status` to distinguish "run is gone" (404) from "daemon hiccup" (5xx). */
export class FetchWorkflowRunError extends Error {
  status: number;
  constructor(status: number, statusText: string) {
    super(`Failed to fetch workflow run: ${statusText || status}`);
    this.status = status;
    this.name = "FetchWorkflowRunError";
  }
}

export async function fetchWorkflowRun(runId: string, opts?: { signal?: AbortSignal }): Promise<WorkflowRunDetail> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}`, { signal: opts?.signal });
  if (!res.ok) throw new FetchWorkflowRunError(res.status, await describeError(res));
  return res.json();
}

export async function startWorkflowRun(data: { workflow_name: string; channel_id?: string; inputs?: Record<string, string> }): Promise<{ run_id: string }> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error(`Failed to start workflow: ${await describeError(res)}`);
  return res.json();
}

export async function saveWorkflowDef(params: { action: "add" | "update" | "delete"; scope?: "global" | "project"; channel_id?: string; name?: string; workflow?: unknown }): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(params),
  });
  if (!res.ok) throw new Error(`Failed to save workflow: ${await describeError(res)}`);
}

export async function resumeWorkflowRun(runId: string, response: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}/resume`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ response }),
  });
  if (!res.ok) throw new Error(`Failed to resume workflow run: ${await describeError(res)}`);
}

export async function cancelWorkflowRun(runId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}/cancel`, {
    method: "POST",
  });
  if (!res.ok) throw new Error(`Failed to cancel workflow run: ${await describeError(res)}`);
}

export async function deleteWorkflowRun(runId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete workflow run: ${await describeError(res)}`);
}

export async function retryWorkflowRun(runId: string): Promise<{ run_id: string }> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}/retry`, {
    method: "POST",
  });
  if (!res.ok) throw new Error(`Failed to retry workflow run: ${await describeError(res)}`);
  return res.json();
}
