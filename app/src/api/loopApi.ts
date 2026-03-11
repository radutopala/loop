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
): Promise<void> {
  const res = await fetch(`${apiUrl}/api/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, content }),
  });
  if (!res.ok) throw new Error(`Failed to send message: ${res.statusText}`);
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
