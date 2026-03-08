import type { Channel, Message } from "../types";

let apiUrl = "http://localhost:8080";

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

interface ChannelAPIResponse {
  channel_id: string;
  name: string;
  dir_path: string;
  parent_id: string;
  active: boolean;
}

export async function fetchChannels(): Promise<Channel[]> {
  const res = await fetch(`${apiUrl}/api/channels`);
  if (!res.ok) throw new Error(`Failed to fetch channels: ${res.statusText}`);
  const data: ChannelAPIResponse[] = await res.json();
  return data.map((c) => ({
    id: c.channel_id,
    name: c.name,
    dir_path: c.dir_path,
    parent_id: c.parent_id,
    active: c.active,
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

export async function fetchMessages(
  channelId: string,
  opts?: { limit?: number; before?: string },
): Promise<Message[]> {
  const params = new URLSearchParams();
  if (opts?.limit) params.set("limit", String(opts.limit));
  if (opts?.before) params.set("before", opts.before);
  const res = await fetch(
    `${apiUrl}/api/channels/${channelId}/messages?${params}`,
  );
  if (!res.ok) throw new Error(`Failed to fetch messages: ${res.statusText}`);
  return res.json();
}
