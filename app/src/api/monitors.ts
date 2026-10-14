import { apiFetch, getApiUrl } from "./api";

/** A watch script an agent left running in its channel; see the monitor MCP tool. */
export interface Monitor {
  id: string;
  channel_id: string;
  description: string;
  command: string;
  started_at: string;
  timeout_sec: number;
  interrupt: boolean;
}

/** The data of a monitor.finished event. */
export interface MonitorFinished {
  id: string;
  channel_id: string;
  status: string;
}

function monitorsUrl(channelId: string): string {
  return `${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/monitors`;
}

/** fetchMonitors returns the channel's running monitors, oldest first. */
export async function fetchMonitors(channelId: string): Promise<Monitor[]> {
  const res = await apiFetch(monitorsUrl(channelId));
  if (!res.ok) throw new Error(`Failed to fetch monitors: ${res.statusText}`);
  const body: { monitors: Monitor[] } = await res.json();
  return body.monitors;
}

/** cancelMonitor stops a monitor without waking the channel's agent. */
export async function cancelMonitor(channelId: string, id: string): Promise<void> {
  const res = await apiFetch(`${monitorsUrl(channelId)}/${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
}
