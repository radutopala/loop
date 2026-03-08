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

export async function fetchChannels(): Promise<Channel[]> {
  const res = await fetch(`${apiUrl}/api/channels`);
  if (!res.ok) throw new Error(`Failed to fetch channels: ${res.statusText}`);
  return res.json();
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
