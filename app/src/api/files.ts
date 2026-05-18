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

// Image extensions the editor renders inline via <img src=...>. Matches the
// backend's imageMIMEByExt in internal/api/files_handler.go — keep in sync.
const IMAGE_EXTS = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp"]);

export function isImagePath(path: string): boolean {
  const dot = path.lastIndexOf(".");
  if (dot < 0) return false;
  return IMAGE_EXTS.has(path.slice(dot).toLowerCase());
}

// buildFileUrl returns the absolute /api URL for the file-read endpoint, with
// the `path` (and optional `root`) query parameters set. Used as <img src> for
// image tabs in the editor, where the browser does its own fetch instead of
// routing through fetchFileContent.
export function buildFileUrl(channelId: string, path: string, root?: number, cacheBust?: number): string {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  if (cacheBust !== undefined) params.set("t", String(cacheBust));
  return `${getApiUrl()}/api/channels/${channelId}/file?${params}`;
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

// ── File existence (batch validation for chat file links) ──

export interface FileExistsResult {
  path: string;
  exists: boolean;
  root_index?: number;
  rel_path?: string;
}

export async function checkFilesExist(channelId: string, paths: string[]): Promise<FileExistsResult[]> {
  if (paths.length === 0) return [];
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/files/exists`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ paths }),
  });
  if (!res.ok) return paths.map((p) => ({ path: p, exists: false }));
  const data: { results: FileExistsResult[] } = await res.json();
  return data.results ?? [];
}

// ── File search (for @file picker) ──

export interface FileSearchResult {
  root_index: number;
  rel_path: string;
  name: string;
}

export async function searchFiles(channelId: string, q: string, limit = 30): Promise<FileSearchResult[]> {
  const params = new URLSearchParams({ q, limit: String(limit) });
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/files/search?${params}`);
  if (!res.ok) return [];
  const data: { results: FileSearchResult[] } = await res.json();
  return data.results ?? [];
}
