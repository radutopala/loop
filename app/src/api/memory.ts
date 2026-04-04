import { getApiUrl } from "./api";

export interface MemoryFileInfo {
  file_path: string;
  dir_path: string;
}

export async function fetchMemoryFiles(channelId: string): Promise<MemoryFileInfo[]> {
  const res = await fetch(`${getApiUrl()}/api/memory/files?channel_id=${encodeURIComponent(channelId)}`);
  if (!res.ok) throw new Error(`Failed to fetch memory files: ${res.statusText}`);
  const data: { files: MemoryFileInfo[] } = await res.json();
  return data.files;
}

export async function fetchMemoryFileContent(channelId: string, filePath: string): Promise<string> {
  const params = new URLSearchParams({ channel_id: channelId, path: filePath });
  const res = await fetch(`${getApiUrl()}/api/memory/file?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch memory file: ${res.statusText}`);
  return res.text();
}

export async function searchMemoryFiles(channelId: string, query: string): Promise<MemoryFileInfo[]> {
  const params = new URLSearchParams({ channel_id: channelId, q: query });
  const res = await fetch(`${getApiUrl()}/api/memory/files/search?${params}`);
  if (!res.ok) throw new Error(`Failed to search memory files: ${res.statusText}`);
  const data: { files: MemoryFileInfo[] } = await res.json();
  return data.files;
}

export async function saveMemoryFileContent(channelId: string, filePath: string, content: string): Promise<void> {
  const params = new URLSearchParams({ channel_id: channelId, path: filePath });
  const res = await fetch(`${getApiUrl()}/api/memory/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save memory file: ${res.statusText}`);
}
