import { getApiUrl } from "./api";

export interface WorkflowDef {
  name: string;
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
}

export interface WorkflowNodeRun {
  id: number;
  run_id: string;
  node_id: string;
  status: "pending" | "running" | "success" | "failed" | "skipped" | "paused";
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

export async function fetchWorkflows(): Promise<WorkflowDef[]> {
  const res = await fetch(`${getApiUrl()}/api/workflows`);
  if (!res.ok) throw new Error(`Failed to fetch workflows: ${res.statusText}`);
  return (await res.json()) ?? [];
}

export async function fetchWorkflowRuns(channelId?: string, limit?: number): Promise<WorkflowRun[]> {
  const params = new URLSearchParams();
  if (channelId) params.set("channel_id", channelId);
  if (limit !== undefined) params.set("limit", String(limit));
  const qs = params.toString();
  const res = await fetch(`${getApiUrl()}/api/workflows/runs${qs ? `?${qs}` : ""}`);
  if (!res.ok) throw new Error(`Failed to fetch workflow runs: ${res.statusText}`);
  return (await res.json()) ?? [];
}

export async function fetchWorkflowRun(runId: string): Promise<WorkflowRunDetail> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}`);
  if (!res.ok) throw new Error(`Failed to fetch workflow run: ${res.statusText}`);
  return res.json();
}

export async function startWorkflowRun(data: {
  workflow_name: string;
  channel_id?: string;
  inputs?: Record<string, string>;
}): Promise<{ run_id: string }> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error(`Failed to start workflow: ${res.statusText}`);
  return res.json();
}

export async function resumeWorkflowRun(runId: string, response: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}/resume`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ response }),
  });
  if (!res.ok) throw new Error(`Failed to resume workflow run: ${res.statusText}`);
}

export async function cancelWorkflowRun(runId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}/cancel`, {
    method: "POST",
  });
  if (!res.ok) throw new Error(`Failed to cancel workflow run: ${res.statusText}`);
}

export async function deleteWorkflowRun(runId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete workflow run: ${res.statusText}`);
}

export async function retryWorkflowRun(runId: string): Promise<{ run_id: string }> {
  const res = await fetch(`${getApiUrl()}/api/workflows/runs/${encodeURIComponent(runId)}/retry`, {
    method: "POST",
  });
  if (!res.ok) throw new Error(`Failed to retry workflow run: ${res.statusText}`);
  return res.json();
}
