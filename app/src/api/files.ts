import { apiFetch, contentCapBase, encodePathSegments, getApiUrl } from "./api";

// ── Roots (multi-directory) ──

export interface RootEntry {
  index: number;
  path: string;
  name: string;
}

export async function fetchRoots(channelId: string): Promise<RootEntry[]> {
  const resp = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/roots`);
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
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/files?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch files: ${res.statusText}`);
  const data: { entries: FileEntry[] } = await res.json();
  return data.entries;
}

/** `ref` reads the file as it was in that commit instead of from disk. */
export async function fetchFileContent(channelId: string, path: string, root?: number, ref?: string): Promise<{ content: string; binary: boolean }> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  if (ref) params.set("ref", ref);
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`);
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

// Video extensions the editor renders inline via <video src=...>. Matches the
// backend's streamedMIMEByExt in internal/api/files_handler.go — keep in sync.
const VIDEO_EXTS = new Set([".mp4", ".webm", ".mov"]);

export function isVideoPath(path: string): boolean {
  const dot = path.lastIndexOf(".");
  if (dot < 0) return false;
  return VIDEO_EXTS.has(path.slice(dot).toLowerCase());
}

// PDF extension the editor renders with its pdf.js viewer. Matches the
// backend's streamedMIMEByExt in internal/api/files_handler.go — keep in sync.
export function isPdfPath(path: string): boolean {
  return path.toLowerCase().endsWith(".pdf");
}

// isMediaPath reports whether the editor shows the file through a URL-backed
// viewer (image, video or PDF) instead of fetching it as text.
export function isMediaPath(path: string): boolean {
  return isImagePath(path) || isVideoPath(path) || isPdfPath(path);
}

// buildFileUrl returns a content-link URL for a workspace file. Used as
// <img>/<video> src and the PDF viewer's URL in the editor, where the browser
// does its own fetch and can't send the API token.
export async function buildFileUrl(channelId: string, path: string, root?: number, cacheBust?: number): Promise<string> {
  const base = await contentCapBase({ kind: "raw", channelId, root: root ?? 0 });
  const query = cacheBust !== undefined ? `?t=${cacheBust}` : "";
  return `${base}${encodePathSegments(path)}${query}`;
}

export async function saveFileContent(channelId: string, path: string, content: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save file: ${res.statusText}`);
}

export async function deleteFile(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete: ${res.statusText}`);
}

export async function createDir(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/dir?${params}`, {
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
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/files/exists`, {
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
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/files/search?${params}`);
  if (!res.ok) return [];
  const data: { results: FileSearchResult[] } = await res.json();
  return data.results ?? [];
}

// buildRawFileBase returns the content-link URL of the directory holding
// `path`, with a trailing slash. The editor's HTML preview uses it as
// <base href> so the page's relative URLs (style.css, img/logo.png) load
// from the workspace.
export async function buildRawFileBase(channelId: string, path: string, root?: number): Promise<string> {
  const dir = path.split("/").slice(0, -1).join("/");
  const base = await contentCapBase({ kind: "raw", channelId, root: root ?? 0 });
  return dir ? `${base}${encodePathSegments(dir)}/` : base;
}
