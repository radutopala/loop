import { getApiUrl } from "./api";

export interface ScheduledTask {
  id: number;
  channel_id: string;
  schedule: string;
  type: "cron" | "interval" | "once";
  prompt: string;
  enabled: boolean;
  next_run_at: string;
  template_name?: string;
  auto_delete_sec: number;
  worktree: boolean;
  origin_branch?: string;
  update_before_run: boolean;
  running: boolean;
  thread_id?: string;
  channel_name?: string;
  dir_path?: string;
  channel_worktree?: boolean;
  workflow_name?: string;
  workflow_inputs?: string;
}

export interface TaskRunLog {
  id: number;
  task_id: number;
  status: "running" | "success" | "failed";
  response_text: string;
  error_text: string;
  started_at: string;
  finished_at: string;
}

export async function fetchTasks(channelId: string): Promise<ScheduledTask[]> {
  const res = await fetch(`${getApiUrl()}/api/tasks?channel_id=${encodeURIComponent(channelId)}`);
  if (!res.ok) throw new Error(`Failed to fetch tasks: ${res.statusText}`);
  return res.json();
}

export async function fetchAllTasks(): Promise<ScheduledTask[]> {
  const res = await fetch(`${getApiUrl()}/api/tasks?platform=local`);
  if (!res.ok) throw new Error(`Failed to fetch tasks: ${res.statusText}`);
  return res.json();
}

export async function createTask(data: {
  channel_id: string;
  schedule: string;
  type: string;
  prompt: string;
  template_name?: string;
  auto_delete_sec?: number;
  worktree?: boolean;
  origin_branch?: string;
  update_before_run?: boolean;
  workflow_name?: string;
  workflow_inputs?: string;
}): Promise<{ id: number }> {
  const res = await fetch(`${getApiUrl()}/api/tasks`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error(`Failed to create task: ${res.statusText}`);
  return res.json();
}

export async function updateTask(
  taskId: number,
  data: {
    enabled?: boolean;
    schedule?: string;
    type?: string;
    prompt?: string;
    auto_delete_sec?: number;
    worktree?: boolean;
    origin_branch?: string;
    update_before_run?: boolean;
    workflow_name?: string;
    workflow_inputs?: string;
  },
): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/tasks/${taskId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error(`Failed to update task: ${res.statusText}`);
}

export async function deleteTask(taskId: number): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/tasks/${taskId}`, { method: "DELETE" });
  if (!res.ok) throw new Error(`Failed to delete task: ${res.statusText}`);
}

export async function fetchTaskRuns(taskId: number): Promise<TaskRunLog[]> {
  const res = await fetch(`${getApiUrl()}/api/tasks/${taskId}/runs`);
  if (!res.ok) throw new Error(`Failed to fetch task runs: ${res.statusText}`);
  return (await res.json()) ?? [];
}

export async function runTaskNow(taskId: number): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/tasks/${taskId}/run`, { method: "POST" });
  if (res.status === 409) throw new Error("Task is already running");
  if (!res.ok) throw new Error(`Failed to run task: ${res.statusText}`);
}
