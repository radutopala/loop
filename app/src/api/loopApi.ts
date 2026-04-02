import type { Channel, ImageStatusResponse, Message } from "../types";

let apiUrl = "http://localhost:8222";

// When running in a browser (not Electron), probe host.docker.internal first
// so the app works from Docker container browsers, then fall back to localhost.
async function probeApiUrl(): Promise<void> {
  if (typeof window === "undefined") return;
  const candidates = ["http://host.docker.internal:8222", "http://localhost:8222"];
  for (const url of candidates) {
    try {
      const res = await fetch(`${url}/api/health`, { signal: AbortSignal.timeout(1000) });
      if (res.ok) { apiUrl = url; return; }
    } catch { /* try next */ }
  }
}

export async function initApiUrl(): Promise<void> {
  if (window.loopAPI) {
    apiUrl = await window.loopAPI.getApiUrl();
  } else {
    await probeApiUrl();
  }
}

export function getApiUrl(): string {
  return apiUrl;
}

export function getWsUrl(): string {
  return apiUrl.replace(/^http/, "ws");
}

/** Call the browser action API for control operations (navigate, tabs, etc). */
export async function browserAction(
  channelId: string,
  action: string,
  params?: Record<string, unknown>,
): Promise<{
  result?: string;
  error?: string;
  tabs?: { target_id: string; url: string; title: string; active?: boolean }[];
  page_info?: { url: string; title: string };
}> {
  const res = await fetch(`${apiUrl}/api/browser/action`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, action, params }),
  });
  return res.json();
}

/** Switch browser mode between docker and host Chrome. */
export async function switchBrowserMode(
  channelId: string,
  mode: "docker" | "host",
): Promise<{ mode: string }> {
  const res = await fetch(`${apiUrl}/api/browser/mode`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, mode }),
  });
  return res.json();
}

/** Open a one-shot WebSocket to send a kill message for a channel's agent container. */
export function killAgentContainer(channelId: string): void {
  const ws = new WebSocket(`${getWsUrl()}/api/ws/terminal`);
  ws.onopen = () => {
    ws.send(JSON.stringify({ type: "kill", channel_id: channelId }));
  };
  ws.onmessage = () => {
    ws.close();
  };
  ws.onerror = () => {
    ws.close();
  };
}

interface ChannelAPIResponse {
  channel_id: string;
  name: string;
  dir_path: string;
  parent_id: string;
  session_id: string;
  active: boolean;
  container_running: boolean;
  agent_running: boolean;
  branch?: string;
  commit?: string;
  worktree?: boolean;
}

export async function fetchChannels(): Promise<Channel[]> {
  const res = await fetch(`${apiUrl}/api/channels?platform=local`);
  if (!res.ok) throw new Error(`Failed to fetch channels: ${res.statusText}`);
  const data: ChannelAPIResponse[] = await res.json();
  return data.map((c) => ({
    id: c.channel_id,
    name: c.name,
    dir_path: c.dir_path,
    parent_id: c.parent_id,
    session_id: c.session_id || "",
    active: c.active,
    container_running: c.container_running,
    agent_running: c.agent_running,
    branch: c.branch || "",
    commit: c.commit || "",
    worktree: c.worktree ?? false,
  }));
}

export async function createThread(
  channelId: string,
  name: string,
  sessionId?: string,
): Promise<string> {
  const body: Record<string, string> = {
    channel_id: channelId,
    name,
    author_id: "desktop",
  };
  if (sessionId) body.session_id = sessionId;
  const res = await fetch(`${apiUrl}/api/threads`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to create thread: ${res.statusText}`);
  const data: { thread_id: string } = await res.json();
  return data.thread_id;
}

export interface SessionEntry {
  session_id: string;
  last_modified: string;
  last_message?: string;
}

export interface SessionsResponse {
  current_session_id: string;
  sessions: SessionEntry[];
  imported_session_ids: string[];
}

export async function fetchSessions(channelId: string): Promise<SessionsResponse> {
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/sessions`);
  if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.statusText}`);
  return res.json();
}

export async function deleteThread(threadId: string): Promise<void> {
  const res = await fetch(`${apiUrl}/api/threads/${threadId}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete thread: ${res.statusText}`);
}

export async function deleteChannel(channelId: string): Promise<void> {
  const res = await fetch(`${apiUrl}/api/channels/${channelId}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete channel: ${res.statusText}`);
}

export async function ensureChannel(dirPath: string): Promise<Channel> {
  const res = await fetch(`${apiUrl}/api/channels`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ dir_path: dirPath, platform: "local" }),
  });
  if (!res.ok) throw new Error(await res.text());
  const data: ChannelAPIResponse = await res.json();
  return {
    id: data.channel_id,
    name: data.name,
    dir_path: data.dir_path,
    parent_id: data.parent_id,
    session_id: data.session_id || "",
    active: data.active,
    container_running: data.container_running,
    agent_running: data.agent_running,
    branch: data.branch || "",
    commit: data.commit || "",
    worktree: data.worktree ?? false,
  };
}

export async function createChannel(name: string, platform = "local"): Promise<string> {
  const res = await fetch(`${apiUrl}/api/channels/create`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, platform }),
  });
  if (!res.ok) throw new Error(`Failed to create channel: ${res.statusText}`);
  const data: { channel_id: string } = await res.json();
  return data.channel_id;
}

export async function sendMessage(
  channelId: string,
  content: string,
  mode?: "agent" | "plan",
): Promise<void> {
  const body: Record<string, string> = { channel_id: channelId, content };
  if (mode && mode !== "agent") body.mode = mode;
  const res = await fetch(`${apiUrl}/api/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to send message: ${res.statusText}`);
}

// ── Branch operations ──

export interface WorktreeInfo {
  path: string;
  branch: string;
  thread_id?: string;
}

export interface BranchInfo {
  branches: string[];
  current: string;
  worktrees: WorktreeInfo[];
}

export async function fetchBranches(channelId: string): Promise<BranchInfo> {
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/branches`);
  if (!res.ok) throw new Error(`Failed to fetch branches: ${res.statusText}`);
  return res.json();
}

export async function switchBranch(channelId: string, branch: string): Promise<void> {
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/branches/switch`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ branch }),
  });
  if (!res.ok) throw new Error(await res.text());
}

export async function createWorktreeThread(
  channelId: string,
  branch: string,
  name?: string,
): Promise<{ threadId: string; worktreePath: string }> {
  const body: Record<string, string> = { channel_id: channelId, branch };
  if (name) body.name = name;
  const res = await fetch(`${apiUrl}/api/worktrees`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to create worktree: ${res.statusText}`);
  const data: { thread_id: string; worktree_path: string } = await res.json();
  return { threadId: data.thread_id, worktreePath: data.worktree_path };
}

export async function importWorktree(
  channelId: string,
  worktreePath: string,
): Promise<{ threadId: string; worktreePath: string }> {
  const res = await fetch(`${apiUrl}/api/worktrees/import`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, worktree_path: worktreePath }),
  });
  if (!res.ok) throw new Error(`Failed to import worktree: ${res.statusText}`);
  const data: { thread_id: string; worktree_path: string } = await res.json();
  return { threadId: data.thread_id, worktreePath: data.worktree_path };
}

export async function createBranch(channelId: string, name: string, from?: string): Promise<void> {
  const body: Record<string, string> = { name };
  if (from) body.from = from;
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/branches/create`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to create branch: ${res.statusText}`);
}

interface MessagesResponse {
  messages: Message[];
  next_cursor: number | null;
}

export async function sendCommand(
  channelId: string,
  command: string,
): Promise<void> {
  const res = await fetch(`${apiUrl}/api/commands`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, command }),
  });
  if (!res.ok) throw new Error(`Failed to send command: ${res.statusText}`);
}

export interface DiffFile {
  path: string;
  old_path?: string; // set when file was renamed
  additions: number;
  deletions: number;
  binary: boolean;
}

export interface DiffResponse {
  files: DiffFile[];
  diff: string;
  total_additions: number;
  total_deletions: number;
}

export interface CommitEntry {
  hash: string;
  short: string;
  subject: string;
  author: string;
  date: string;
}

export async function fetchCommits(channelId: string, branch?: string, limit?: number, skip?: number): Promise<CommitEntry[]> {
  const params = new URLSearchParams();
  if (branch) params.set("branch", branch);
  if (limit) params.set("limit", String(limit));
  if (skip) params.set("skip", String(skip));
  const qs = params.toString();
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/commits${qs ? `?${qs}` : ""}`);
  if (!res.ok) throw new Error(`Failed to fetch commits: ${res.statusText}`);
  const data: { commits: CommitEntry[] } = await res.json();
  return data.commits;
}

export async function fetchDiff(channelId: string, source?: string, target?: string): Promise<DiffResponse> {
  let url = `${apiUrl}/api/channels/${channelId}/diff`;
  if (source && target) {
    url += `?source=${encodeURIComponent(source)}&target=${encodeURIComponent(target)}`;
  }
  const res = await fetch(url);
  if (!res.ok) throw new Error(`Failed to fetch diff: ${res.statusText}`);
  return res.json();
}

export async function fetchReadme(): Promise<string> {
  const res = await fetch(`${apiUrl}/api/readme`);
  if (!res.ok) throw new Error(`Failed to fetch README: ${res.statusText}`);
  return res.text();
}

export interface SearchMessageResult {
  id: number;
  channel_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
  created_at: string;
}

export async function searchMessages(
  query: string,
  limit?: number,
): Promise<SearchMessageResult[]> {
  const params = new URLSearchParams({ q: query });
  if (limit) params.set("limit", String(limit));
  const res = await fetch(`${apiUrl}/api/messages/search?${params}`);
  if (!res.ok) throw new Error(`Failed to search messages: ${res.statusText}`);
  return res.json();
}

export interface MemoryFileInfo {
  file_path: string;
  dir_path: string;
}

export async function fetchMemoryFiles(channelId: string): Promise<MemoryFileInfo[]> {
  const res = await fetch(`${apiUrl}/api/memory/files?channel_id=${encodeURIComponent(channelId)}`);
  if (!res.ok) throw new Error(`Failed to fetch memory files: ${res.statusText}`);
  const data: { files: MemoryFileInfo[] } = await res.json();
  return data.files;
}

export async function fetchMemoryFileContent(channelId: string, filePath: string): Promise<string> {
  const params = new URLSearchParams({ channel_id: channelId, path: filePath });
  const res = await fetch(`${apiUrl}/api/memory/file?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch memory file: ${res.statusText}`);
  return res.text();
}

export async function searchMemoryFiles(channelId: string, query: string): Promise<MemoryFileInfo[]> {
  const params = new URLSearchParams({ channel_id: channelId, q: query });
  const res = await fetch(`${apiUrl}/api/memory/files/search?${params}`);
  if (!res.ok) throw new Error(`Failed to search memory files: ${res.statusText}`);
  const data: { files: MemoryFileInfo[] } = await res.json();
  return data.files;
}

export async function saveMemoryFileContent(channelId: string, filePath: string, content: string): Promise<void> {
  const params = new URLSearchParams({ channel_id: channelId, path: filePath });
  const res = await fetch(`${apiUrl}/api/memory/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save memory file: ${res.statusText}`);
}

// ── Container operations ──

export interface ContainerInfo {
  container_id: string;
  channel_id: string;
  type: "agent" | "shell" | "chrome";
  status: "running" | "stopped" | "pending-removal";
  container_name?: string;
  created_at: string;
  updated_at: string;
  remove_at?: string;
}

export async function fetchContainers(): Promise<ContainerInfo[]> {
  const res = await fetch(`${apiUrl}/api/containers`);
  if (!res.ok) throw new Error(`Failed to fetch containers: ${res.statusText}`);
  return res.json();
}

// ── Image operations ──

export async function getImageStatus(): Promise<ImageStatusResponse> {
  const resp = await fetch(`${apiUrl}/api/image/status`);
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}

export async function rebuildImage(): Promise<void> {
  const resp = await fetch(`${apiUrl}/api/image/rebuild`, { method: "POST" });
  if (!resp.ok) throw new Error(await resp.text());
}

export async function removeImage(): Promise<void> {
  const resp = await fetch(`${apiUrl}/api/image`, { method: "DELETE" });
  if (!resp.ok) throw new Error(await resp.text());
}

// ── Roots (multi-directory) ──

export interface RootEntry {
  index: number;
  path: string;
  name: string;
}

export async function fetchRoots(channelId: string): Promise<RootEntry[]> {
  const resp = await fetch(`${apiUrl}/api/channels/${channelId}/roots`);
  if (!resp.ok) throw new Error(await resp.text());
  const data: { roots: RootEntry[] } = await resp.json();
  return data.roots;
}

// updateExtraDirs reads the project config via API, updates extra_dirs, and saves it back.
export async function updateExtraDirs(channelId: string, extraDirs: string[]): Promise<void> {
  const { fetchProjectConfig, saveProjectConfig } = await import("./configApi");

  let config: Record<string, unknown> = {};
  try {
    const existing = await fetchProjectConfig(channelId);
    if (existing.content) {
      config = { ...existing.content };
    }
  } catch {
    // No existing config or parse error — start fresh
  }

  if (extraDirs.length > 0) {
    config.extra_dirs = extraDirs;
  } else {
    delete config.extra_dirs;
  }

  const content = JSON.stringify(config, null, 2);
  await saveProjectConfig(channelId, content);
}

// ── File operations ──

export interface FileEntry {
  name: string;
  type: "file" | "dir";
  size?: number;
}

export async function fetchFiles(channelId: string, path: string, root?: number): Promise<FileEntry[]> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/files?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch files: ${res.statusText}`);
  const data: { entries: FileEntry[] } = await res.json();
  return data.entries;
}

export async function fetchFileContent(channelId: string, path: string, root?: number): Promise<{ content: string; binary: boolean }> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/file?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch file: ${res.statusText}`);
  if (res.headers.get("X-File-Binary") === "true") {
    return { content: "", binary: true };
  }
  return { content: await res.text(), binary: false };
}

export async function saveFileContent(channelId: string, path: string, content: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save file: ${res.statusText}`);
}

export async function deleteFile(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/file?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete: ${res.statusText}`);
}

export async function createDir(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/dir?${params}`, {
    method: "POST",
  });
  if (!res.ok) throw new Error(`Failed to create directory: ${res.statusText}`);
}

export async function fetchMessages(
  channelId: string,
  opts?: { limit?: number; cursor?: number; around?: number },
): Promise<MessagesResponse> {
  const params = new URLSearchParams();
  if (opts?.limit) params.set("limit", String(opts.limit));
  if (opts?.around) params.set("around", String(opts.around));
  else if (opts?.cursor) params.set("cursor", String(opts.cursor));
  const res = await fetch(
    `${apiUrl}/api/channels/${channelId}/messages?${params}`,
  );
  if (!res.ok) throw new Error(`Failed to fetch messages: ${res.statusText}`);
  return res.json();
}

/** Fetch the current playground content by name. */
export async function fetchPlayground(
  name: string,
  scope?: "global" | "project",
  channelId?: string,
): Promise<{ name: string; title?: string; html: string; description?: string } | null> {
  const params = new URLSearchParams({ name });
  if (scope === "project" && channelId) {
    params.set("scope", "project");
    params.set("channel_id", channelId);
  }
  const res = await fetch(`${apiUrl}/api/playground?${params}`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`Failed to fetch playground: ${res.statusText}`);
  return res.json();
}

export interface PlaygroundItem {
  name: string;
  title?: string;
  description?: string;
  scope: "global" | "project";
}

// --- Scheduled Tasks ---

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
  const res = await fetch(`${apiUrl}/api/tasks?channel_id=${encodeURIComponent(channelId)}`);
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
}): Promise<{ id: number }> {
  const res = await fetch(`${apiUrl}/api/tasks`, {
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
  },
): Promise<void> {
  const res = await fetch(`${apiUrl}/api/tasks/${taskId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error(`Failed to update task: ${res.statusText}`);
}

export async function deleteTask(taskId: number): Promise<void> {
  const res = await fetch(`${apiUrl}/api/tasks/${taskId}`, { method: "DELETE" });
  if (!res.ok) throw new Error(`Failed to delete task: ${res.statusText}`);
}

export async function fetchTaskRuns(taskId: number): Promise<TaskRunLog[]> {
  const res = await fetch(`${apiUrl}/api/tasks/${taskId}/runs`);
  if (!res.ok) throw new Error(`Failed to fetch task runs: ${res.statusText}`);
  return (await res.json()) ?? [];
}

/** List all playground items with names and titles (global + project-scoped). */
export async function fetchPlaygroundItems(channelId?: string): Promise<PlaygroundItem[]> {
  const params = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
  const res = await fetch(`${apiUrl}/api/playground/items${params}`);
  if (!res.ok) throw new Error(`Failed to fetch playground items: ${res.statusText}`);
  const data: { items: PlaygroundItem[] } = await res.json();
  return data.items;
}
