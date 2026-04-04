import { getApiUrl } from "./api";

// ── Roots (multi-directory) ──

export interface RootEntry {
  index: number;
  path: string;
  name: string;
}

export async function fetchRoots(channelId: string): Promise<RootEntry[]> {
  const resp = await fetch(`${getApiUrl()}/api/channels/${channelId}/roots`);
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
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/files?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch files: ${res.statusText}`);
  const data: { entries: FileEntry[] } = await res.json();
  return data.entries;
}

export async function fetchFileContent(channelId: string, path: string, root?: number): Promise<{ content: string; binary: boolean }> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch file: ${res.statusText}`);
  if (res.headers.get("X-File-Binary") === "true") {
    return { content: "", binary: true };
  }
  return { content: await res.text(), binary: false };
}

export async function saveFileContent(channelId: string, path: string, content: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save file: ${res.statusText}`);
}

export async function deleteFile(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete: ${res.statusText}`);
}

export async function createDir(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/dir?${params}`, {
    method: "POST",
  });
  if (!res.ok) throw new Error(`Failed to create directory: ${res.statusText}`);
}
