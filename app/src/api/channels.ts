import type { Channel, Message } from "../types";
import { getApiUrl, getWsUrl } from "./api";

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
  diff_additions?: number;
  diff_deletions?: number;
}

export async function fetchChannels(): Promise<Channel[]> {
  const res = await fetch(`${getApiUrl()}/api/channels?platform=local`);
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
    diff_additions: c.diff_additions ?? 0,
    diff_deletions: c.diff_deletions ?? 0,
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
  const res = await fetch(`${getApiUrl()}/api/threads`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to create thread: ${res.statusText}`);
  const data: { thread_id: string } = await res.json();
  return data.thread_id;
}

export async function deleteThread(threadId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/threads/${threadId}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete thread: ${res.statusText}`);
}

export async function removeWorktree(channelId: string, worktreePath: string, threadId?: string): Promise<void> {
  const body: Record<string, string> = { channel_id: channelId, worktree_path: worktreePath };
  if (threadId) body.thread_id = threadId;
  const res = await fetch(`${getApiUrl()}/api/worktrees`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to remove worktree: ${res.statusText}`);
}

export async function deleteChannel(channelId: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete channel: ${res.statusText}`);
}

export async function ensureChannel(dirPath: string): Promise<Channel> {
  const res = await fetch(`${getApiUrl()}/api/channels`, {
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
    diff_additions: data.diff_additions ?? 0,
    diff_deletions: data.diff_deletions ?? 0,
  };
}

export async function createChannel(name: string, platform = "local"): Promise<string> {
  const res = await fetch(`${getApiUrl()}/api/channels/create`, {
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
  interrupt?: boolean,
): Promise<void> {
  const body: Record<string, string | boolean> = { channel_id: channelId, content };
  if (mode && mode !== "agent") body.mode = mode;
  if (interrupt) body.interrupt = true;
  const res = await fetch(`${getApiUrl()}/api/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to send message: ${res.statusText}`);
}

export async function sendCommand(
  channelId: string,
  command: string,
): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/commands`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, command }),
  });
  if (!res.ok) throw new Error(`Failed to send command: ${res.statusText}`);
}

export async function createWorktreeThread(
  channelId: string,
  branch: string,
  name?: string,
): Promise<{ threadId: string; worktreePath: string }> {
  const body: Record<string, string> = { channel_id: channelId, branch };
  if (name) body.name = name;
  const res = await fetch(`${getApiUrl()}/api/worktrees`, {
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
  const res = await fetch(`${getApiUrl()}/api/worktrees/import`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, worktree_path: worktreePath }),
  });
  if (!res.ok) throw new Error(`Failed to import worktree: ${res.statusText}`);
  const data: { thread_id: string; worktree_path: string } = await res.json();
  return { threadId: data.thread_id, worktreePath: data.worktree_path };
}

interface MessagesResponse {
  messages: Message[];
  next_cursor: number | null;
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
    `${getApiUrl()}/api/channels/${channelId}/messages?${params}`,
  );
  if (!res.ok) throw new Error(`Failed to fetch messages: ${res.statusText}`);
  return res.json();
}

export async function fetchReadme(): Promise<string> {
  const res = await fetch(`${getApiUrl()}/api/readme`);
  if (!res.ok) throw new Error(`Failed to fetch README: ${res.statusText}`);
  return res.text();
}
