import { getApiUrl } from "./api";

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
  const res = await fetch(`${getApiUrl()}/api/containers`);
  if (!res.ok) throw new Error(`Failed to fetch containers: ${res.statusText}`);
  return res.json();
}
