import { getApiUrl } from "./api";

export interface AuditFileEntry {
  date: string;
  size: number;
  last_modified: string;
}

export interface AuditFilesResponse {
  files: AuditFileEntry[];
  total: number;
}

export async function fetchAuditFiles(channelId: string, offset: number, limit: number): Promise<AuditFilesResponse> {
  const url = `${getApiUrl()}/api/channels/${channelId}/audit?offset=${offset}&limit=${limit}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`Failed to fetch audit files: ${res.statusText}`);
  return res.json();
}

export async function deleteAuditFile(channelId: string, date: string): Promise<void> {
  const url = `${getApiUrl()}/api/channels/${channelId}/audit/${encodeURIComponent(date)}`;
  const res = await fetch(url, { method: "DELETE" });
  if (!res.ok) throw new Error(`Failed to delete audit file: ${res.statusText}`);
}
