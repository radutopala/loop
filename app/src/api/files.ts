import { apiFetch, contentCapBase, encodePathSegments, getApiUrl } from "./api";

// ── Roots (multi-directory) ──

export interface RootEntry {
  index: number;
  path: string;
  /** path with the daemon user's home shown as ~. */
  display_path?: string;
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

/** fetchFileContent's error when the file isn't there (404). */
export class FileNotFoundError extends Error {}

/** `ref` reads the file as it was in that commit instead of from disk. */
export async function fetchFileContent(channelId: string, path: string, root?: number, ref?: string): Promise<{ content: string; binary: boolean }> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  if (ref) params.set("ref", ref);
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`);
  if (res.status === 404) throw new FileNotFoundError(`Failed to fetch file: ${res.statusText}`);
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

/** What a save did with format: the formatted text when the formatter
 * changed it, or why the file couldn't be formatted (it's saved as sent). */
export interface SaveResult {
  content?: string;
  formatError?: string;
}

/** Saves content; with format, the project's formatter for the file runs
 * over it first when format on save is on. */
export async function saveFileContent(channelId: string, path: string, content: string, root?: number, format = false): Promise<SaveResult> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  if (format) params.set("format", "1");
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`, {
    method: "PUT",
    body: content,
  });
  if (!res.ok) throw new Error(`Failed to save file: ${res.statusText}`);
  if (!format) return {};
  const data: { content?: string; format_error?: string } = await res.json().catch(() => ({}));
  return { content: data.content, formatError: data.format_error || undefined };
}

/** Where an editor setting's value comes from. */
export type EditorConfigSource = "default" | "global" | "project";

/** The editor settings in effect for a channel. */
export interface EditorConfig {
  follow_active_file: boolean;
  follow_active_file_from: EditorConfigSource;
  format_on_save: boolean;
  format_on_save_from: EditorConfigSource;
  /** How many formatters are configured. */
  formatters: number;
  /** How wide a tab shows and how many spaces an indent is. */
  tab_size: number;
  /** Indent with spaces rather than a tab. */
  insert_spaces: boolean;
  /** Save the open tabs when the window loses focus. */
  auto_save_on_blur: boolean;
  /** A single click opens a file in a transient tab. */
  preview_tabs: boolean;
}

export const DEFAULT_EDITOR_CONFIG: EditorConfig = {
  follow_active_file: true,
  follow_active_file_from: "default",
  format_on_save: false,
  format_on_save_from: "default",
  formatters: 0,
  tab_size: 4,
  insert_spaces: true,
  auto_save_on_blur: false,
  preview_tabs: true,
};

export async function fetchEditorConfig(channelId: string): Promise<EditorConfig> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/editor-config`);
  if (!res.ok) throw new Error(`Failed to fetch editor config: ${res.statusText}`);
  return { ...DEFAULT_EDITOR_CONFIG, ...((await res.json()) as Partial<EditorConfig>) };
}

export async function deleteFile(channelId: string, path: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error(`Failed to delete: ${res.statusText}`);
}

/** Renames or moves path to to, both relative to root. */
export async function renameFile(channelId: string, path: string, to: string, root?: number): Promise<void> {
  const params = new URLSearchParams({ path, to });
  if (root !== undefined && root > 0) params.set("root", String(root));
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/file/rename?${params}`, { method: "POST" });
  if (res.status === 409) throw new Error(`${to} already exists`);
  if (!res.ok) throw new Error(`Failed to rename: ${res.statusText}`);
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
  /** Set on directory results, which come only with `dirs`. */
  is_dir?: boolean;
}

export async function searchFiles(channelId: string, q: string, limit = 30, dirs = false): Promise<FileSearchResult[]> {
  const params = new URLSearchParams({ q, limit: String(limit) });
  if (dirs) params.set("dirs", "1");
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/files/search?${params}`);
  if (!res.ok) return [];
  const data: { results: FileSearchResult[] } = await res.json();
  return data.results ?? [];
}

// ── Content search ──

export interface GrepMatch {
  line: number;
  /** 1-based, in characters. */
  column: number;
  /** The match's length, in characters. */
  length: number;
  text: string;
}

export interface GrepFile {
  root_index: number;
  rel_path: string;
  matches: GrepMatch[];
}

export interface GrepResult {
  files: GrepFile[];
  /** The caps cut the results short. */
  truncated: boolean;
  /** "ripgrep", or "builtin" when the daemon has no rg. */
  engine: string;
}

export interface GrepOptions {
  regex?: boolean;
  caseSensitive?: boolean;
}

/** Searches the contents of the channel's roots for q. */
export async function grepFiles(channelId: string, q: string, opts: GrepOptions = {}): Promise<GrepResult> {
  const params = new URLSearchParams({ q });
  if (opts.regex) params.set("regex", "1");
  if (opts.caseSensitive) params.set("case", "1");
  const res = await apiFetch(`${getApiUrl()}/api/channels/${channelId}/files/grep?${params}`);
  if (!res.ok) throw new Error((await res.text().catch(() => "")).trim() || `Search failed: ${res.statusText}`);
  const data: GrepResult = await res.json();
  return { files: data.files ?? [], truncated: !!data.truncated, engine: data.engine ?? "" };
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
