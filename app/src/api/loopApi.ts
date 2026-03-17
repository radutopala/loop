import type { Channel, Message } from "../types";

let apiUrl = "http://localhost:8222";

export async function initApiUrl(): Promise<void> {
  if (window.loopAPI) {
    apiUrl = await window.loopAPI.getApiUrl();
  }
}

export function getApiUrl(): string {
  return apiUrl;
}

export function getWsUrl(): string {
  return apiUrl.replace(/^http/, "ws");
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
  active: boolean;
  container_running: boolean;
  agent_running: boolean;
  branch?: string;
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
    active: c.active,
    container_running: c.container_running,
    agent_running: c.agent_running,
    branch: c.branch || "",
    worktree: c.worktree ?? false,
  }));
}

export async function createThread(
  channelId: string,
  name: string,
): Promise<string> {
  const res = await fetch(`${apiUrl}/api/threads`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      channel_id: channelId,
      name,
      author_id: "desktop",
    }),
  });
  if (!res.ok) throw new Error(`Failed to create thread: ${res.statusText}`);
  const data: { thread_id: string } = await res.json();
  return data.thread_id;
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
    active: data.active,
    container_running: data.container_running,
    agent_running: data.agent_running,
    branch: data.branch || "",
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

export async function fetchDiff(channelId: string): Promise<DiffResponse> {
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/diff`);
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

// ── File operations ──

export interface FileEntry {
  name: string;
  type: "file" | "dir";
  size?: number;
}

export async function fetchFiles(channelId: string, path: string): Promise<FileEntry[]> {
  const params = new URLSearchParams({ path });
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/files?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch files: ${res.statusText}`);
  const data: { entries: FileEntry[] } = await res.json();
  return data.entries;
}

export async function fetchFileContent(channelId: string, path: string): Promise<{ content: string; binary: boolean }> {
  const params = new URLSearchParams({ path });
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/file?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch file: ${res.statusText}`);
  if (res.headers.get("X-File-Binary") === "true") {
    return { content: "", binary: true };
  }
  return { content: await res.text(), binary: false };
}

export async function saveFileContent(channelId: string, path: string, content: string): Promise<void> {
  const params = new URLSearchParams({ path });
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save file: ${res.statusText}`);
}

export async function deleteFile(channelId: string, path: string): Promise<void> {
  const params = new URLSearchParams({ path });
  const res = await fetch(`${apiUrl}/api/channels/${channelId}/file?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete file: ${res.statusText}`);
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
